// Package fntv implements the authenticated media-library protocol for 飞牛影视.
package fntv

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"jumedia-server/internal/emby"
)

var ErrAuth = errors.New("飞牛影视 用户名、密码或会话无效")
var ErrResponse = errors.New("飞牛影视 返回无效数据")

type Client struct {
	base, username, password string
	http                     *http.Client
	mu                       sync.Mutex
	token                    string
	// RequestGate is configured before use; release covers headers and body reads.
	RequestGate func(context.Context) (func(), error)
}

func New(address, username, password string, allowPrivate bool) (*Client, error) {
	base, err := emby.FnTV.NormalizeBase(address)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		d.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil || emby.PrivateIP(net.ParseIP(host)) {
				return emby.ErrPrivate
			}
			return nil
		}
	}
	return &Client{base: base, username: username, password: password, http: &http.Client{
		Transport:     &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func signature(path string, body []byte, nonce, timestamp string) string {
	// Public protocol constants, independent of account credentials.
	material := strings.Join([]string{"NDzZTVxnRKP8Z0jXg1VAMonaG8akvh", path, nonce, timestamp, fmt.Sprintf("%x", md5.Sum(body)), "16CCEB3D-AB42-077D-36A1-F355324E4237"}, "_")
	return "nonce=" + nonce + "&timestamp=" + timestamp + "&sign=" + fmt.Sprintf("%x", md5.Sum([]byte(material)))
}

func randomNonce() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(n.Int64()+100000, 10), nil
}

func (c *Client) request(ctx context.Context, method, path, token string, body []byte, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrResponse
	}
	nonce, err := randomNonce()
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	req.Header.Set("Authx", signature("/v"+path, body, nonce, strconv.FormatInt(time.Now().UnixMilli(), 10)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, key := range []string{"Range", "If-Range"} {
		if v := headers.Get(key); v != "" {
			req.Header.Set(key, v)
		}
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, emby.ErrPrivate) {
			return nil, emby.ErrPrivate
		}
		return nil, emby.ErrNetwork
	}
	return res, nil
}

type httpError struct {
	status     int
	retryAfter time.Duration
}

func (e *httpError) Error() string { return fmt.Sprintf("飞牛影视 返回 HTTP %d", e.status) }

func (c *Client) attempt(ctx context.Context, path, token string, body []byte, out any) error {
	if c.RequestGate != nil {
		release, err := c.RequestGate(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	res, err := c.request(ctx, method, path, token, body, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return ErrAuth
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		seconds, _ := strconv.Atoi(res.Header.Get("Retry-After"))
		return &httpError{res.StatusCode, time.Duration(max(0, min(seconds, 60))) * time.Second}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (32<<20)+1))
	if err != nil {
		return emby.ErrNetwork
	}
	if len(b) > 32<<20 {
		return ErrResponse
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &envelope) != nil || envelope.Code == nil {
		return ErrResponse
	}
	if *envelope.Code == 401 || *envelope.Code == 403 {
		return ErrAuth
	}
	if *envelope.Code != 0 {
		return fmt.Errorf("飞牛影视 请求失败（代码 %d）", *envelope.Code)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, out) != nil {
		return ErrResponse
	}
	return nil
}

func (c *Client) call(ctx context.Context, path, token string, body []byte, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.attempt(ctx, path, token, body, out)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var status *httpError
		retry := errors.Is(err, emby.ErrNetwork)
		delay := time.Second * time.Duration(1<<attempt)
		if errors.As(err, &status) {
			retry = status.status == 429 || status.status == 502 || status.status == 503 || status.status == 504
			if status.retryAfter > 0 {
				delay = status.retryAfter
			}
		}
		if !retry || attempt >= 3 || c.RequestGate == nil {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) authorize(ctx context.Context, rejected string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && (rejected == "" || rejected != c.token) {
		return c.token, nil
	}
	c.token = ""
	body, _ := json.Marshal(map[string]string{"username": c.username, "password": fmt.Sprintf("%x", sha256.Sum256([]byte(c.password))), "app_name": "trimemedia-web"})
	var result struct {
		Token string `json:"token"`
	}
	err := c.call(ctx, "/api/v2/user/loginByPassword", "", body, &result)
	var status *httpError
	if errors.As(err, &status) && (status.status == 404 || status.status == 405) {
		nonce, randomErr := randomNonce()
		if randomErr != nil {
			return "", randomErr
		}
		body, _ = json.Marshal(map[string]string{"username": c.username, "password": c.password, "app_name": "trimemedia-web", "nonce": nonce})
		err = c.call(ctx, "/api/v1/login", "", body, &result)
	}
	if err != nil {
		return "", err
	}
	if result.Token == "" || strings.ContainsAny(result.Token, " \t\r\n") {
		return "", ErrAuth
	}
	c.token = result.Token
	return c.token, nil
}

func (c *Client) json(ctx context.Context, path string, body any, out any) error {
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	token, err := c.authorize(ctx, "")
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		err = c.call(ctx, path, token, b, out)
		if !errors.Is(err, ErrAuth) || attempt == 1 {
			return err
		}
		token, err = c.authorize(ctx, token)
		if err != nil {
			return err
		}
	}
	return err
}

func (c *Client) media(ctx context.Context, method, path string, headers http.Header) (*http.Response, error) {
	token, err := c.authorize(ctx, "")
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		res, err := c.request(ctx, method, path, token, nil, headers)
		if err != nil {
			return nil, err
		}
		if (res.StatusCode != 401 && res.StatusCode != 403) || attempt == 1 {
			return res, nil
		}
		res.Body.Close()
		token, err = c.authorize(ctx, token)
		if err != nil {
			return nil, err
		}
	}
	return nil, ErrAuth
}

func (c *Client) Stream(ctx context.Context, item, source string, headers http.Header) (*http.Response, error) {
	return c.RawStream(ctx, item, source, "mp4", headers)
}
func (c *Client) RawStream(ctx context.Context, item, source, container string, headers http.Header) (*http.Response, error) {
	return c.stream(ctx, http.MethodGet, item, source, headers)
}
func (c *Client) HeadStream(ctx context.Context, item, source, container string, headers http.Header) (*http.Response, error) {
	return c.stream(ctx, http.MethodHead, item, source, headers)
}
func (c *Client) stream(ctx context.Context, method, item, source string, headers http.Header) (*http.Response, error) {
	if !emby.ValidPair(item, source) {
		return nil, ErrResponse
	}
	return c.media(ctx, method, "/api/v1/media/range/"+source, headers)
}
func (c *Client) StreamURL(context.Context, string, string, string) (string, error) {
	return "", errors.New("飞牛影视 需要通过 Jumedia 代理播放")
}

func (c *Client) Image(ctx context.Context, item, kind, tag string) (*http.Response, error) {
	if !emby.ValidPair(item, item) {
		return nil, ErrResponse
	}
	if tag == "" {
		var raw entry
		if err := c.json(ctx, "/api/v1/item/"+item, nil, &raw); err != nil {
			return nil, err
		}
		tag = raw.Poster
		if tag == "" {
			tag = string(raw.Posters)
		}
		if kind == "Backdrop" && raw.Backdrops != "" {
			tag = raw.Backdrops
		}
	}
	path := strings.TrimPrefix(tag, "/")
	if path == "" || strings.ContainsAny(path, "?#\\%") {
		return nil, ErrResponse
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, ErrResponse
		}
		parts[i] = url.PathEscape(p)
	}
	return c.media(ctx, http.MethodGet, "/api/v1/sys/img/"+strings.Join(parts, "/"), nil)
}
