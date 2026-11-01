package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const DeviceID = "jumedia-server"
const playlistLimit = 2 << 20

var SegmentPath = regexp.MustCompile(`^hls1/main/[0-9]+\.ts$`)
var ErrUnavailable = errors.New("Emby unavailable")

type Client struct {
	base, username, password string
	userID                   string
	datasource               int64
	http                     *http.Client
	mu                       sync.Mutex
	token                    string
	sessions                 map[string]string
	sessionMu                sync.Mutex
}

func New(base, username, password string) *Client {
	base = strings.TrimRight(base, "/")
	if base != "" && !strings.HasSuffix(base, "/emby") {
		base += "/emby"
	}
	return &Client{base: base, username: username, password: password, sessions: map[string]string{}, http: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// authorize serializes login and replaces only the token that received a 401.
func (c *Client) authorize(ctx context.Context, rejected string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && (rejected == "" || c.token != rejected) {
		return c.token, nil
	}
	if c.base == "" || c.username == "" {
		return "", ErrUnavailable
	}
	loginCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"Username": c.username, "Pw": c.password})
	req, err := http.NewRequestWithContext(loginCtx, "POST", c.base+"/Users/AuthenticateByName", bytes.NewReader(body))
	if err != nil {
		return "", ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="Jumedia", Device="Jumedia Server", DeviceId="`+DeviceID+`", Version="0.1.0"`)
	res, err := c.http.Do(req)
	if err != nil {
		return "", c.networkError(err)
	}
	defer res.Body.Close()
	var data struct {
		AccessToken string
		User        struct {
			ID string `json:"Id"`
		}
	}
	if c.datasource != 0 && res.StatusCode != 200 {
		return "", responseError(res.StatusCode)
	}
	if res.StatusCode != 200 || decodeJSONBody(res.Body, &data) != nil || data.AccessToken == "" {
		c.token = ""
		if c.datasource != 0 {
			return "", responseError(401)
		}
		return "", ErrUnavailable
	}
	c.userID = data.User.ID
	c.token = data.AccessToken
	return c.token, nil
}
func (c *Client) request(ctx context.Context, method, path string, query url.Values, headers http.Header) (*http.Response, error) {
	token, err := c.authorize(ctx, "")
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		var body io.Reader
		if method == "POST" {
			body = strings.NewReader("{}")
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+path+"?"+query.Encode(), body)
		if err != nil {
			return nil, ErrUnavailable
		}
		req.Header.Set("X-Emby-Token", token)
		if method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		for _, h := range []string{"Range", "If-Range"} {
			if v := headers.Get(h); v != "" {
				req.Header.Set(h, v)
			}
		}
		res, err := c.http.Do(req)
		if err != nil {
			return nil, c.networkError(err)
		}
		if res.StatusCode != 401 || attempt == 1 {
			return res, nil
		}
		res.Body.Close()
		token, err = c.authorize(ctx, token)
		if err != nil {
			return nil, err
		}
	}
	return nil, ErrUnavailable
}
func (c *Client) session(ctx context.Context, item, source string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	key := item + ":" + source
	if value := c.sessions[key]; value != "" {
		return value, nil
	}
	res, err := c.request(ctx, "POST", "/Items/"+item+"/PlaybackInfo", url.Values{"MediaSourceId": {source}}, nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var data struct {
		PlaySessionID string `json:"PlaySessionId"`
	}
	if res.StatusCode != 200 || decodeJSONBody(res.Body, &data) != nil || data.PlaySessionID == "" {
		return "", ErrUnavailable
	}
	c.sessions[key] = data.PlaySessionID
	return data.PlaySessionID, nil
}
func (c *Client) Stream(ctx context.Context, item, source string, headers http.Header) (*http.Response, error) {
	return c.RawStream(ctx, item, source, "mp4", headers)
}

// RawStream forwards the original file without requesting a playback session or transcoding.
func (c *Client) RawStream(ctx context.Context, item, source, container string, headers http.Header) (*http.Response, error) {
	if !ValidPair(item, source) || !rawContainer.MatchString(container) {
		return nil, ErrUnavailable
	}
	return c.request(ctx, "GET", "/Videos/"+item+"/stream."+container, url.Values{"Static": {"true"}, "MediaSourceId": {source}, "DeviceId": {DeviceID}}, headers)
}

func (c *Client) hlsQuery(ctx context.Context, item, source string) (url.Values, error) {
	session, err := c.session(ctx, item, source)
	q := url.Values{"MediaSourceId": {source}, "VideoCodec": {"h264"}, "AudioCodec": {"aac"}, "DeviceId": {DeviceID}, "PlaySessionId": {session}}
	if c.datasource != 0 {
		q.Set("MaxWidth", "1920")
	}
	return q, err
}
func (c *Client) Segment(ctx context.Context, item, source, path string) (*http.Response, error) {
	if !ValidPair(item, source) || !SegmentPath.MatchString(path) {
		return nil, ErrUnavailable
	}
	q, err := c.hlsQuery(ctx, item, source)
	if err != nil {
		return nil, err
	}
	return c.request(ctx, "GET", "/Videos/"+item+"/"+path, q, nil)
}
func (c *Client) Playlist(ctx context.Context, item, source, kind, origin string) (string, error) {
	if !ValidPair(item, source) {
		return "", ErrUnavailable
	}
	suffix := ""
	if c.datasource != 0 {
		suffix = fmt.Sprintf("&ds=%d", c.datasource)
	}
	base := strings.TrimRight(origin, "/") + "/media/emby/" + item + "/hls/"
	if kind == "index.m3u8" {
		return "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=5000000,CODECS=\"avc1.640028,mp4a.40.2\"\n" + base + "main.m3u8?source=" + url.QueryEscape(source) + suffix + "\n", nil
	}
	if kind != "main.m3u8" {
		return "", ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	q, err := c.hlsQuery(ctx, item, source)
	if err != nil {
		return "", err
	}
	res, err := c.request(ctx, "GET", "/Videos/"+item+"/main.m3u8", q, nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, playlistLimit+1))
	if err != nil || len(data) > playlistLimit {
		return "", ErrUnavailable
	}
	lines := strings.Split(string(data), "\n")
	if strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return "", ErrUnavailable
	}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") {
			// TS/H.264/AAC playlists require no external URI attributes. Never leak
			// an upstream token through comments, key/map tags or redirect URLs.
			if strings.Contains(t, "URI=") || strings.Contains(strings.ToLower(t), "api_key") || strings.Contains(strings.ToLower(t), "token") {
				return "", fmt.Errorf("unsupported Emby playlist tag")
			}
			continue
		}
		path := strings.SplitN(t, "?", 2)[0]
		if !SegmentPath.MatchString(path) {
			return "", fmt.Errorf("unsupported Emby segment path")
		}
		lines[i] = base + path + "?source=" + url.QueryEscape(source) + suffix
	}
	return strings.Join(lines, "\n"), nil
}
