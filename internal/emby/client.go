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
	provider                 Provider
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
	c.provider.AuthorizeHeader(req.Header, "")
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
		return "", c.provider.responseError(res.StatusCode)
	}
	if res.StatusCode != 200 || c.provider.decodeJSONBody(res.Body, &data) != nil || data.AccessToken == "" {
		c.token = ""
		if c.datasource != 0 {
			return "", c.provider.responseError(401)
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
		c.provider.AuthorizeHeader(req.Header, token)
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

// StreamURL lets the player read the original file directly from Emby.
// Check the cached session through metadata so revoked tokens can be renewed
// by request's existing 401 retry without fetching any video bytes.
func (c *Client) StreamURL(ctx context.Context, item, source, container string) (string, error) {
	if !ValidPair(item, source) || !rawContainer.MatchString(container) {
		return "", ErrUnavailable
	}
	user, err := c.user(ctx)
	if err != nil {
		return "", err
	}
	var profile struct{ ID string }
	path := "/Users/" + url.PathEscape(user)
	if c.provider == Jellyfin {
		path = "/Users/Me"
	}
	if err = c.JSON(ctx, path, nil, &profile); err != nil {
		return "", err
	}
	token, err := c.authorize(ctx, "")
	if err != nil {
		return "", err
	}
	query := url.Values{"Static": {"true"}, "MediaSourceId": {source}, "DeviceId": {DeviceID}}
	if c.provider == Jellyfin {
		query.Set("ApiKey", token)
	} else {
		query.Set("api_key", token)
	}
	return c.base + "/Videos/" + item + "/stream." + container + "?" + query.Encode(), nil
}

// RawStream forwards the original file without requesting a playback session or transcoding.
func (c *Client) RawStream(ctx context.Context, item, source, container string, headers http.Header) (*http.Response, error) {
	return c.rawStream(ctx, http.MethodGet, item, source, container, headers)
}

// HeadStream reads original-file metadata without downloading its body.
func (c *Client) HeadStream(ctx context.Context, item, source, container string, headers http.Header) (*http.Response, error) {
	return c.rawStream(ctx, http.MethodHead, item, source, container, headers)
}

func (c *Client) rawStream(ctx context.Context, method, item, source, container string, headers http.Header) (*http.Response, error) {
	if !ValidPair(item, source) || !rawContainer.MatchString(container) {
		return nil, ErrUnavailable
	}
	return c.request(ctx, method, "/Videos/"+item+"/stream."+container, url.Values{"Static": {"true"}, "MediaSourceId": {source}, "DeviceId": {DeviceID}}, headers)
}
