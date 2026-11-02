package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const DeviceID = "jumedia-server"

var ErrUnavailable = errors.New("Emby unavailable")

type Client struct {
	base, username, password string
	userID                   string
	datasource               int64
	http                     *http.Client
	mu                       sync.Mutex
	token                    string
}

func New(base, username, password string) *Client {
	base = strings.TrimRight(base, "/")
	if base != "" && !strings.HasSuffix(base, "/emby") {
		base += "/emby"
	}
	return &Client{base: base, username: username, password: password, http: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
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
