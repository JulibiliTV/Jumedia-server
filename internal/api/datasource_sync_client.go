package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/fntv"
	"jumedia-server/internal/store"
)

const sourceSyncPage = 100

// A sync owns its budget and session. Playback clients never use this transport.
// Keep this private to the datasource domain until the shared Emby client has a
// request-policy interface (including authentication and response-body reads).
type sourceSyncClient struct {
	fn                       *fntv.Client
	provider                 emby.Provider
	base, username, password string
	http                     *http.Client
	slots, turn, auth        chan struct{}
	next                     time.Time // guarded by turn, which is cancelable unlike a mutex
	interval                 time.Duration
	token, user              string                                     // guarded by auth
	sleep                    func(context.Context, time.Duration) error // retry delay, injectable in tests
}

func sourceSyncSetting(settings map[string]any, key string, fallback, ceiling int) int {
	n, ok := settings[key].(float64)
	if !ok || math.IsNaN(n) || n < 1 || n > float64(ceiling) || math.Trunc(n) != n {
		return fallback
	}
	return int(n)
}

func (s *Server) sourceSyncClient(ctx context.Context, ds *store.DataSource, settings map[string]any) (*sourceSyncClient, error) {
	if err := s.InitDataSources(ctx); err != nil {
		return nil, err
	}
	password, err := s.decryptPassword(ds.PasswordEnc)
	if err != nil {
		return nil, err
	}
	return newSourceSyncClient(emby.Provider(ds.Type), ds.URL, ds.Username, password, ds.System || Truthy(settings["dataSourcePrivateNetwork"]), sourceSyncSetting(settings, "dataSourceSyncRate", 5, 50), sourceSyncSetting(settings, "dataSourceSyncConcurrency", 2, 8))
}

func newSourceSyncClient(provider emby.Provider, address, username, password string, allowPrivate bool, rate, concurrency int) (*sourceSyncClient, error) {
	base, err := provider.NormalizeBase(address)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil || emby.PrivateIP(net.ParseIP(host)) {
				return emby.ErrPrivate
			}
			return nil
		}
	}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second}
	c := &sourceSyncClient{provider: provider, base: base, username: username, password: password,
		http:  &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		slots: make(chan struct{}, concurrency), turn: make(chan struct{}, 1), auth: make(chan struct{}, 1), interval: time.Second / time.Duration(rate), sleep: sourceSyncSleep}
	if provider == emby.FnTV {
		c.fn, err = fntv.New(address, username, password, allowPrivate)
		if err != nil {
			return nil, err
		}
		c.fn.RequestGate = func(ctx context.Context) (func(), error) {
			if err := c.acquire(ctx); err != nil {
				return nil, err
			}
			return func() { <-c.slots }, nil
		}
	}
	return c, nil
}
func (c *sourceSyncClient) close() {
	c.http.CloseIdleConnections()
	if c.fn != nil {
		c.fn.Close()
	}
}
func sourceSyncSleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
func sourceSyncAcquire(ctx context.Context, ch chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- struct{}{}:
		return nil
	}
}
func (c *sourceSyncClient) acquire(ctx context.Context) error {
	if err := sourceSyncAcquire(ctx, c.slots); err != nil {
		return err
	}
	if err := sourceSyncAcquire(ctx, c.turn); err != nil {
		<-c.slots
		return err
	}
	defer func() { <-c.turn }()
	if err := sourceSyncSleep(ctx, time.Until(c.next)); err != nil {
		<-c.slots
		return err
	}
	// Schedule from the actual admission time: delayed requests cannot accumulate
	// tokens and burst when a slow response releases the concurrency slots.
	c.next = time.Now().Add(c.interval)
	return nil
}

type sourceSyncHTTPError struct {
	provider   emby.Provider
	status     int
	retryAfter time.Duration
}

func (e *sourceSyncHTTPError) Error() string {
	if e.status == 401 {
		return e.provider.Name() + " 用户名或密码错误"
	}
	return fmt.Sprintf("%s 返回 HTTP %d", e.provider.Name(), e.status)
}
func sourceSyncRetry(err error, attempt int) (time.Duration, bool) {
	var status *sourceSyncHTTPError
	if errors.As(err, &status) {
		switch status.status {
		case 429, 502, 503, 504:
		default:
			return 0, false
		}
		if status.retryAfter > 0 {
			return min(status.retryAfter, 60*time.Second), true
		}
	} else if !errors.Is(err, emby.ErrNetwork) {
		return 0, false
	}
	return time.Second * time.Duration(1<<attempt), true
}

// attempt covers both headers and body with a per-attempt deadline. Budget and
// backoff waits use the parent context, so Retry-After may legitimately be 60s.
func (c *sourceSyncClient) attempt(ctx context.Context, method, path string, q url.Values, token string, body []byte, out any) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-c.slots }()
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, c.base+path+"?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.provider.AuthorizeHeader(req.Header, token)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, emby.ErrPrivate) {
			return emby.ErrPrivate
		}
		return emby.ErrNetwork
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		retryAfter := time.Duration(0)
		if n, e := strconv.ParseFloat(res.Header.Get("Retry-After"), 64); e == nil && n > 0 {
			retryAfter = time.Duration(min(n, 60) * float64(time.Second))
		}
		return &sourceSyncHTTPError{provider: c.provider, status: res.StatusCode, retryAfter: retryAfter}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (32<<20)+1))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return emby.ErrNetwork
	}
	if len(b) > 32<<20 {
		return errors.New(c.provider.Name() + " JSON 响应超过 32 MB")
	}
	if json.Unmarshal(b, out) != nil {
		return errors.New(c.provider.Name() + " 返回无效 JSON")
	}
	return nil
}
func (c *sourceSyncClient) call(ctx context.Context, method, path string, q url.Values, token string, body []byte, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.attempt(ctx, method, path, q, token, body, out)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay, retry := sourceSyncRetry(err, attempt)
		if !retry || attempt >= 3 {
			return err
		}
		if err = c.sleep(ctx, delay); err != nil {
			return err
		}
	}
}

// Login failures abort the whole run even if they occur during a 401 renewal.
type sourceSyncLoginError struct{ err error }

func (e *sourceSyncLoginError) Error() string { return e.err.Error() }
func (e *sourceSyncLoginError) Unwrap() error { return e.err }
func sourceSyncLoginFailed(err error) bool {
	var login *sourceSyncLoginError
	return errors.As(err, &login) || errors.Is(err, fntv.ErrAuth)
}

func (c *sourceSyncClient) authorize(ctx context.Context, rejected string) (string, string, error) {
	if err := sourceSyncAcquire(ctx, c.auth); err != nil {
		return "", "", &sourceSyncLoginError{err}
	}
	defer func() { <-c.auth }()
	if c.token != "" && (rejected == "" || rejected != c.token) {
		return c.user, c.token, nil
	}
	c.token = ""
	body, _ := json.Marshal(map[string]string{"Username": c.username, "Pw": c.password})
	var result struct {
		AccessToken string
		User        struct{ ID string }
	}
	if err := c.call(ctx, "POST", "/Users/AuthenticateByName", nil, "", body, &result); err != nil {
		return "", "", &sourceSyncLoginError{err}
	}
	if result.AccessToken == "" || result.User.ID == "" {
		return "", "", &sourceSyncLoginError{&sourceSyncHTTPError{provider: c.provider, status: 401}}
	}
	c.token, c.user = result.AccessToken, result.User.ID
	return c.user, c.token, nil
}
func (c *sourceSyncClient) get(ctx context.Context, path func(string) string, q url.Values, out any) error {
	user, token, err := c.authorize(ctx, "")
	if err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		err = c.call(ctx, "GET", path(user), q, token, nil, out)
		var status *sourceSyncHTTPError
		if i == 1 || !errors.As(err, &status) || status.status != 401 {
			return err
		}
		user, token, err = c.authorize(ctx, token)
		if err != nil {
			return err
		}
	}
	return err
}
func (c *sourceSyncClient) Libraries(ctx context.Context) ([]emby.Library, error) {
	if c.fn != nil {
		return c.fn.Libraries(ctx)
	}
	var result struct {
		Items []struct{ ID, Name, CollectionType string }
	}
	q := url.Values{}
	err := c.get(ctx, func(user string) string { return c.provider.UserPath(user, "Views", q) }, q, &result)
	if err != nil {
		return nil, err
	}
	libs := []emby.Library{}
	for _, l := range result.Items {
		switch l.CollectionType {
		case "movies", "tvshows", "homevideos", "musicvideos", "mixed", "":
		default:
			continue
		}
		kind := l.CollectionType
		if kind == "" {
			kind = "mixed"
		}
		libs = append(libs, emby.Library{ID: l.ID, Name: l.Name, Type: kind})
	}
	return libs, nil
}

type sourceSyncItems struct {
	Items            []emby.Item
	TotalRecordCount int
}

func (c *sourceSyncClient) listPage(ctx context.Context, library string, start int) (sourceSyncItems, error) {
	return c.browsePage(ctx, library, "", start, sourceSyncPage, nil)
}
func (c *sourceSyncClient) browsePage(ctx context.Context, library, keyword string, start, limit int, ids []string) (sourceSyncItems, error) {
	if c.fn != nil {
		page, err := c.fn.Browse(ctx, library, keyword, start, limit, ids)
		return sourceSyncItems{Items: page.Items, TotalRecordCount: page.TotalRecordCount}, err
	}
	var page sourceSyncItems
	q := url.Values{"ParentId": {library}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie,Series,Video,MusicVideo"}, "SortBy": {"SortName"}, "SortOrder": {"Ascending"}, "Fields": {c.provider.ItemFields()}, "StartIndex": {strconv.Itoa(start)}, "Limit": {strconv.Itoa(limit)}}
	if keyword != "" {
		q.Set("SearchTerm", keyword)
	}
	if len(ids) > 0 {
		q.Set("Ids", strings.Join(ids, ","))
	}
	err := c.get(ctx, func(user string) string { return c.provider.UserPath(user, "Items", q) }, q, &page)
	return page, err
}
func (c *sourceSyncClient) hasAncestor(ctx context.Context, item, library string) (bool, error) {
	var ancestors []struct{ ID string }
	q := url.Values{}
	err := c.get(ctx, func(user string) string {
		q.Set("UserId", user)
		return "/Items/" + url.PathEscape(item) + "/Ancestors"
	}, q, &ancestors)
	if err != nil {
		return false, err
	}
	for _, parent := range ancestors {
		if parent.ID == library {
			return true, nil
		}
	}
	return false, nil
}

func (c *sourceSyncClient) Episodes(ctx context.Context, id string) ([]emby.Item, error) {
	if c.fn != nil {
		return c.fn.Episodes(ctx, id)
	}
	var result struct{ Items []emby.Item }
	q := url.Values{"Fields": {c.provider.ItemFields()}}
	err := c.get(ctx, func(user string) string { q.Set("UserId", user); return "/Shows/" + url.PathEscape(id) + "/Episodes" }, q, &result)
	return result.Items, err
}

func (c *sourceSyncClient) Hydrate(ctx context.Context, item emby.Item) (emby.Item, error) {
	if c.fn != nil {
		return c.fn.Hydrate(ctx, item)
	}
	return item, nil
}

// Metadata for at most one page is retained; workers write to distinct slots,
// then the transaction consumes them in list order regardless of completion order.
type sourceSyncMirror struct {
	item   string
	mirror *sourceMirror
	err    error
}

func sourceSyncMirrors(ctx context.Context, c *sourceSyncClient, ds *store.DataSource, regions []store.Region, lib store.DataSourceLibrary, items []emby.Item) []sourceSyncMirror {
	out := make([]sourceSyncMirror, len(items))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for i := 0; i < min(cap(c.slots), len(items)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				item := items[index]
				m, err := mirrorSource(ctx, c, ds, regions, lib, item)
				out[index] = sourceSyncMirror{item.ID, m, err}
			}
		}()
	}
	for i := range items {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}
