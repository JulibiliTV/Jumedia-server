package emby

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var ErrPrivate = errors.New("站点不允许使用内网地址的 Emby 服务器")
var ErrNetwork = errors.New("无法连接 Emby 服务器（网络错误或服务器未开放跨域）")

func responseError(status int) error {
	if status == 401 {
		return errors.New("Emby 用户名或密码错误")
	}
	return fmt.Errorf("Emby 返回 HTTP %d", status)
}
func (c *Client) networkError(err error) error {
	if c.datasource == 0 {
		return ErrUnavailable
	}
	if errors.Is(err, ErrPrivate) {
		return ErrPrivate
	}
	return ErrNetwork
}
func NormalizeBase(address string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(address))
	if e != nil || u.Host == "" {
		return "", errors.New("Emby 服务器地址格式不正确")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("仅支持 http / https 的 Emby 地址")
	}
	if u.User != nil {
		return "", errors.New("Emby 服务器地址格式不正确")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(strings.ToLower(path), "/emby") {
		path += "/emby"
	}
	u.Path = path
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
func PrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || cgnat.Contains(ip) || !ip.IsGlobalUnicast()
}

// Control runs after DNS resolution, immediately before connecting. No proxy is
// used: an environment proxy must not bypass the destination IP policy.
func NewDataSource(address, username, password string, id int64, allowPrivate bool) (*Client, error) {
	base, err := NormalizeBase(address)
	if err != nil {
		return nil, err
	}
	c := New("", username, password)
	c.base = base
	c.datasource = id
	if id == 0 {
		c.datasource = -1
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		d.Control = func(network, address string, raw syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil || PrivateIP(net.ParseIP(host)) {
				return ErrPrivate
			}
			return nil
		}
	}
	c.http.Transport = &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second}
	return c, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) JSON(ctx context.Context, path string, q url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := c.request(ctx, "GET", path, q, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return responseError(res.StatusCode)
	}
	return decodeJSONBody(res.Body, out)
}

type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
type ServerInfo struct {
	ServerName string    `json:"server_name"`
	ServerID   string    `json:"server_id"`
	Version    string    `json:"version"`
	Libraries  []Library `json:"libraries"`
}

func (c *Client) Probe(ctx context.Context) (ServerInfo, error) {
	var raw struct{ ServerName, ID, Version string }
	err := c.JSON(ctx, "/System/Info/Public", nil, &raw)
	if err != nil {
		return ServerInfo{}, err
	}
	libs, err := c.Libraries(ctx)
	return ServerInfo{raw.ServerName, raw.ID, raw.Version, libs}, err
}
func (c *Client) user(ctx context.Context) (string, error) {
	_, err := c.authorize(ctx, "")
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.userID == "" {
		return "", errors.New("Emby 用户名或密码错误")
	}
	return c.userID, nil
}
func (c *Client) Libraries(ctx context.Context) ([]Library, error) {
	user, err := c.user(ctx)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Items []struct{ ID, Name, CollectionType string }
	}
	if err = c.JSON(ctx, "/Users/"+url.PathEscape(user)+"/Views", nil, &raw); err != nil {
		return nil, err
	}
	out := []Library{}
	for _, l := range raw.Items {
		switch l.CollectionType {
		case "movies", "tvshows", "homevideos", "musicvideos", "mixed", "":
		default:
			continue
		}
		kind := l.CollectionType
		if kind == "" {
			kind = "mixed"
		}
		out = append(out, Library{l.ID, l.Name, kind})
	}
	return out, nil
}

type MediaStream struct{ Type, Codec string }
type MediaSource struct {
	ID, Container string
	Path          string
	MediaStreams  []MediaStream
}
type Item struct {
	Path                                                              string
	ID, Name, Type, Overview, PremiereDate, DateCreated, LocationType string
	Genres                                                            []string
	RunTimeTicks                                                      int64
	ImageTags                                                         map[string]string
	BackdropImageTags                                                 []string
	MediaSources                                                      []MediaSource
	ParentIndexNumber                                                 *int
	IndexNumber                                                       int
}

func Direct(m MediaSource) bool {
	switch strings.ToLower(m.Container) {
	case "mp4", "m4v", "mov":
	default:
		return false
	}
	video := false
	for _, s := range m.MediaStreams {
		if s.Type == "Video" {
			video = true
			if !strings.EqualFold(s.Codec, "h264") {
				return false
			}
		}
		if s.Type == "Audio" && !strings.EqualFold(s.Codec, "aac") && !strings.EqualFold(s.Codec, "mp3") {
			return false
		}
	}
	return video
}
func DSRef(ds int64, item string, m MediaSource) string {
	container := strings.SplitN(strings.ToLower(m.Container), ",", 2)[0]
	container = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, container)
	if container == "" {
		container = "mkv"
	}
	mode := "raw-" + container
	if Direct(m) {
		mode = "direct"
	}
	return fmt.Sprintf("embyds:%d:%s:%s:%s", ds, item, m.ID, mode)
}

var rawContainer = regexp.MustCompile(`^[a-z0-9]+$`)

func ParseDSRef(ref string) (ds int64, item, source, mode string, ok bool) {
	p := strings.Split(ref, ":")
	if len(p) != 5 || p[0] != "embyds" || !ValidPair(p[2], p[3]) || (p[4] != "direct" && (!strings.HasPrefix(p[4], "raw-") || !rawContainer.MatchString(strings.TrimPrefix(p[4], "raw-")))) {
		return
	}
	ds, e := strconv.ParseInt(p[1], 10, 64)
	return ds, p[2], p[3], p[4], e == nil && ds > 0
}
func DSPath(ds int64, item, source, mode string) string {
	p := MP4Path(item, source)
	if strings.HasPrefix(mode, "raw-") && rawContainer.MatchString(strings.TrimPrefix(mode, "raw-")) {
		p = "/media/emby/" + item + "/file." + strings.TrimPrefix(mode, "raw-") + "?source=" + url.QueryEscape(source)
	}
	return p + fmt.Sprintf("&ds=%d", ds)
}
func (c *Client) Items(ctx context.Context, library string) ([]Item, error) {
	user, err := c.user(ctx)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for start := 0; ; start += 200 {
		var page struct {
			Items            []Item
			TotalRecordCount int
		}
		q := url.Values{"ParentId": {library}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie,Series,Video,MusicVideo"}, "SortBy": {"SortName"}, "SortOrder": {"Ascending"}, "Fields": {"Overview,Genres,PremiereDate,DateCreated,MediaSources,RunTimeTicks"}, "StartIndex": {strconv.Itoa(start)}, "Limit": {"200"}}
		if err = c.JSON(ctx, "/Users/"+url.PathEscape(user)+"/Items", q, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if len(page.Items) == 0 || start+200 >= page.TotalRecordCount {
			break
		}
	}
	return out, nil
}
func (c *Client) Episodes(ctx context.Context, id string) ([]Item, error) {
	user, err := c.user(ctx)
	if err != nil {
		return nil, err
	}
	var page struct{ Items []Item }
	err = c.JSON(ctx, "/Shows/"+url.PathEscape(id)+"/Episodes", url.Values{"UserId": {user}, "Fields": {"Path,Overview,MediaSources,PremiereDate,DateCreated,RunTimeTicks"}}, &page)
	return page.Items, err
}
func (c *Client) Image(ctx context.Context, item, kind, tag string) (*http.Response, error) {
	return c.request(ctx, "GET", "/Items/"+url.PathEscape(item)+"/Images/"+kind, url.Values{"tag": {tag}, "maxWidth": {"1280"}}, nil)
}

func decodeJSONBody(body io.Reader, out any) error {
	b, err := io.ReadAll(io.LimitReader(body, (32<<20)+1))
	if err != nil {
		return ErrNetwork
	}
	if len(b) > 32<<20 {
		return errors.New("Emby JSON 响应超过 32 MB")
	}
	if json.Unmarshal(b, out) != nil {
		return errors.New("Emby 返回无效 JSON")
	}
	return nil
}
