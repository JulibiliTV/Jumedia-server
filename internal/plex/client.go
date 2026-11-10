package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

var ErrAuth = errors.New("Plex 账号认证失败，请检查用户名、密码及账号验证状态")
var ErrResponse = errors.New("Plex 返回无效媒体数据")

type Client struct {
	base, token, username, password string
	mu                              sync.Mutex
	accountHTTP                     *http.Client
	http                            *http.Client
	RequestGate                     func(context.Context) (func(), error)
}

func New(address, username, password string, allowPrivate bool) (*Client, error) {
	base, err := emby.Plex.NormalizeBase(address)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(username) == "" || password == "" {
		return nil, ErrAuth
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		d.Control = func(network, address string, _ syscall.RawConn) error {
			h, _, e := net.SplitHostPort(address)
			if e != nil || emby.PrivateIP(net.ParseIP(h)) {
				return emby.ErrPrivate
			}
			return nil
		}
	}
	return &Client{base: base, username: username, password: password, accountHTTP: &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, http: &http.Client{Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) request(ctx context.Context, method, path string, q url.Values, h http.Header) (*http.Response, error) {
	token, err := c.authorize(ctx, "")
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		res, err := c.requestToken(ctx, method, path, q, h, token)
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
func (c *Client) requestToken(ctx context.Context, method, path string, q url.Values, h http.Header, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, ErrResponse
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	req.Header.Set("X-Plex-Client-Identifier", "jumedia-server")
	req.Header.Set("X-Plex-Product", "Jumedia")
	for _, k := range []string{"Range", "If-Range"} {
		if v := h.Get(k); v != "" {
			req.Header.Set(k, v)
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
func (c *Client) json(ctx context.Context, path string, q url.Values, out any) error {
	for attempt := 0; ; attempt++ {
		err, retry, delay := c.attempt(ctx, path, q, out)
		if err == nil || !retry || attempt >= 3 || c.RequestGate == nil {
			return err
		}
		if delay == 0 {
			delay = time.Second * time.Duration(1<<attempt)
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
func (c *Client) attempt(ctx context.Context, path string, q url.Values, out any) (error, bool, time.Duration) {
	if c.RequestGate != nil {
		release, err := c.RequestGate(ctx)
		if err != nil {
			return err, false, 0
		}
		defer release()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := c.request(ctx, "GET", path, q, nil)
	if err != nil {
		return err, errors.Is(err, emby.ErrNetwork), 0
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return ErrAuth, false, 0
	}
	if res.StatusCode != 200 {
		n, _ := strconv.Atoi(res.Header.Get("Retry-After"))
		return fmt.Errorf("Plex 返回 HTTP %d", res.StatusCode), res.StatusCode == 429 || res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504, time.Duration(max(0, min(60, n))) * time.Second
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (32<<20)+1))
	if err != nil {
		return emby.ErrNetwork, true, 0
	}
	if len(b) > 32<<20 || json.Unmarshal(b, out) != nil {
		return ErrResponse, false, 0
	}
	return nil, false, 0
}

// Plex IDs are represented as strings in metadata and numbers in Media/Part.
type id string

func (i *id) UnmarshalJSON(b []byte) error {
	var s string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	} else {
		s = string(b)
	}
	if !emby.ValidPair(s, s) {
		return ErrResponse
	}
	*i = id(s)
	return nil
}

type part struct {
	ID   id     `json:"id"`
	Key  string `json:"key"`
	File string `json:"file"`
}
type media struct {
	Container  string `json:"container"`
	VideoCodec string `json:"videoCodec"`
	AudioCodec string `json:"audioCodec"`
	Part       []part `json:"Part"`
}
type metadata struct {
	ID          id      `json:"ratingKey"`
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Summary     string  `json:"summary"`
	Thumb       string  `json:"thumb"`
	Art         string  `json:"art"`
	Duration    int64   `json:"duration"`
	Index       int     `json:"index"`
	ParentIndex *int    `json:"parentIndex"`
	Added       int64   `json:"addedAt"`
	Date        string  `json:"originallyAvailableAt"`
	Media       []media `json:"Media"`
	Genre       []struct {
		Tag string `json:"tag"`
	} `json:"Genre"`
}
type container struct {
	Total     *int       `json:"totalSize"`
	Size      int        `json:"size"`
	Metadata  []metadata `json:"Metadata"`
	Directory []struct {
		Key   id     `json:"key"`
		Title string `json:"title"`
		Type  string `json:"type"`
	} `json:"Directory"`
	Name    string `json:"friendlyName"`
	Machine string `json:"machineIdentifier"`
	Version string `json:"version"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (*container, error) {
	var r struct {
		Container *container `json:"MediaContainer"`
	}
	err := c.json(ctx, path, q, &r)
	if err != nil {
		return nil, err
	}
	if r.Container == nil {
		return nil, ErrResponse
	}
	return r.Container, nil
}
func (c *Client) Probe(ctx context.Context) (emby.ServerInfo, error) {
	r, err := c.get(ctx, "/", nil)
	if err != nil {
		return emby.ServerInfo{}, err
	}
	if r.Machine == "" {
		return emby.ServerInfo{}, ErrResponse
	}
	libs, err := c.Libraries(ctx)
	return emby.ServerInfo{ServerName: r.Name, ServerID: r.Machine, Version: r.Version, Libraries: libs}, err
}
func (c *Client) Libraries(ctx context.Context) ([]emby.Library, error) {
	r, err := c.get(ctx, "/library/sections", nil)
	if err != nil {
		return nil, err
	}
	out := []emby.Library{}
	for _, l := range r.Directory {
		kind := ""
		switch l.Type {
		case "movie":
			kind = "movies"
		case "show":
			kind = "tvshows"
		}
		if kind != "" {
			if l.Key == "" {
				return nil, ErrResponse
			}
			out = append(out, emby.Library{ID: string(l.Key), Name: l.Title, Type: kind})
		}
	}
	return out, nil
}
func (m metadata) item() (emby.Item, error) {
	if m.ID == "" {
		return emby.Item{}, ErrResponse
	}
	kind := map[string]string{"movie": "Movie", "show": "Series", "episode": "Episode"}[m.Type]
	out := emby.Item{ID: string(m.ID), Name: m.Title, Type: kind, Overview: m.Summary, RunTimeTicks: m.Duration * 10000, IndexNumber: m.Index, ParentIndexNumber: m.ParentIndex, ImageTags: map[string]string{"Primary": m.Thumb}, BackdropImageTags: []string{m.Art}}
	if m.Added > 0 {
		out.DateCreated = time.Unix(m.Added, 0).UTC().Format(time.RFC3339)
	}
	if t, e := time.Parse("2006-01-02", m.Date); e == nil {
		out.PremiereDate = t.Format(time.RFC3339)
	}
	for _, g := range m.Genre {
		out.Genres = append(out.Genres, g.Tag)
	}
	if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
		v := m.Media[0]
		p := v.Part[0]
		out.MediaSources = []emby.MediaSource{{ID: string(p.ID), Container: v.Container, Path: p.File, MediaStreams: []emby.MediaStream{{Type: "Video", Codec: v.VideoCodec}, {Type: "Audio", Codec: v.AudioCodec}}}}
	}
	return out, nil
}

type Page struct {
	Items            []emby.Item
	TotalRecordCount int
}

func (c *Client) page(ctx context.Context, path string, start, limit int) (Page, error) {
	r, e := c.get(ctx, path, url.Values{"X-Plex-Container-Start": {strconv.Itoa(start)}, "X-Plex-Container-Size": {strconv.Itoa(limit)}})
	if e != nil {
		return Page{}, e
	}
	total := r.Size
	if r.Total != nil {
		total = *r.Total
	} else if start > 0 || len(r.Metadata) >= limit {
		return Page{}, ErrResponse
	}
	if total < 0 || len(r.Metadata) < limit && start+len(r.Metadata) < total {
		return Page{}, ErrResponse
	}
	p := Page{Items: []emby.Item{}, TotalRecordCount: total}
	for _, m := range r.Metadata {
		v, e := m.item()
		if e != nil {
			return Page{}, e
		}
		p.Items = append(p.Items, v)
	}
	return p, nil
}
func (c *Client) Browse(ctx context.Context, library, keyword string, start, limit int, ids []string) (Page, error) {
	if !emby.ValidPair(library, library) || start < 0 || limit < 1 || limit > 100 {
		return Page{}, ErrResponse
	}
	path := "/library/sections/" + library + "/all"
	if keyword == "" && len(ids) == 0 {
		return c.page(ctx, path, start, limit)
	}
	wanted := map[string]bool{}
	for _, v := range ids {
		wanted[v] = true
	}
	items := []emby.Item{}
	for offset := 0; ; offset += 100 {
		p, e := c.page(ctx, path, offset, 100)
		if e != nil {
			return Page{}, e
		}
		for _, v := range p.Items {
			if (len(ids) == 0 || wanted[v.ID]) && strings.Contains(strings.ToLower(v.Name), strings.ToLower(keyword)) {
				items = append(items, v)
				delete(wanted, v.ID)
			}
		}
		if offset+100 >= p.TotalRecordCount || len(ids) > 0 && len(wanted) == 0 {
			break
		}
	}
	n := len(items)
	return Page{items[min(start, n):min(start+limit, n)], n}, nil
}
func (c *Client) detail(ctx context.Context, item string) (metadata, error) {
	if !emby.ValidPair(item, item) {
		return metadata{}, ErrResponse
	}
	r, e := c.get(ctx, "/library/metadata/"+item, nil)
	if e != nil {
		return metadata{}, e
	}
	if len(r.Metadata) != 1 || string(r.Metadata[0].ID) != item {
		return metadata{}, ErrResponse
	}
	return r.Metadata[0], nil
}
func (c *Client) Hydrate(ctx context.Context, item emby.Item) (emby.Item, error) {
	m, e := c.detail(ctx, item.ID)
	if e != nil {
		return item, e
	}
	if len(m.Media) > 0 && len(m.Media[0].Part) > 1 {
		return item, errors.New("Plex 暂不支持多文件分段媒体")
	}
	return m.item()
}
func (c *Client) Episodes(ctx context.Context, item string) ([]emby.Item, error) {
	if !emby.ValidPair(item, item) {
		return nil, ErrResponse
	}
	out := []emby.Item{}
	for start := 0; ; start += 100 {
		p, e := c.page(ctx, "/library/metadata/"+item+"/allLeaves", start, 100)
		if e != nil {
			return nil, e
		}
		for _, v := range p.Items {
			v, e = c.Hydrate(ctx, v)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		if start+100 >= p.TotalRecordCount {
			break
		}
	}
	return out, nil
}
func resource(path, prefix string) (string, error) {
	u, e := url.Parse(path)
	if e != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, prefix) || strings.Contains(u.Path, "\\") {
		return "", ErrResponse
	}
	for _, p := range strings.Split(u.Path, "/") {
		if p == ".." || p == "." {
			return "", ErrResponse
		}
	}
	return u.EscapedPath(), nil
}
func (c *Client) partPath(ctx context.Context, item, source string) (string, error) {
	m, e := c.detail(ctx, item)
	if e != nil {
		return "", e
	}
	for _, v := range m.Media {
		for _, p := range v.Part {
			if string(p.ID) == source {
				return resource(p.Key, "/library/parts/"+source+"/")
			}
		}
	}
	return "", ErrResponse
}
func (c *Client) Image(ctx context.Context, item, kind, tag string) (*http.Response, error) {
	m, e := c.detail(ctx, item)
	if e != nil {
		return nil, e
	}
	path := m.Thumb
	if kind == "Backdrop" && m.Art != "" {
		path = m.Art
	}
	path, e = resource(path, "/library/metadata/")
	if e != nil {
		return nil, e
	}
	return c.request(ctx, "GET", path, nil, nil)
}
func (c *Client) StreamURL(ctx context.Context, item, source, ext string) (string, error) {
	p, e := c.partPath(ctx, item, source)
	if e != nil {
		return "", e
	}
	token, err := c.authorize(ctx, "")
	if err != nil {
		return "", err
	}
	return c.base + p + "?" + url.Values{"X-Plex-Token": {token}}.Encode(), nil
}
func (c *Client) Stream(ctx context.Context, item, source string, h http.Header) (*http.Response, error) {
	return c.RawStream(ctx, item, source, "mp4", h)
}
func (c *Client) RawStream(ctx context.Context, item, source, ext string, h http.Header) (*http.Response, error) {
	return c.stream(ctx, "GET", item, source, h)
}
func (c *Client) HeadStream(ctx context.Context, item, source, ext string, h http.Header) (*http.Response, error) {
	return c.stream(ctx, "HEAD", item, source, h)
}
func (c *Client) stream(ctx context.Context, method, item, source string, h http.Header) (*http.Response, error) {
	p, e := c.partPath(ctx, item, source)
	if e != nil {
		return nil, e
	}
	return c.request(ctx, method, p, nil, h)
}
