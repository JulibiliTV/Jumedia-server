package plex

import (
	"context"
	"errors"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/plex/testdata"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestPlexProtocol(t *testing.T) {
	testdata.Account(t)
	up := testdata.Server(t)
	defer up.Close()
	c, e := New(up.URL+"/base/", "fixture-user", "fixture-password", true)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx := context.Background()
	calls := 0
	c.RequestGate = func(context.Context) (func(), error) { calls++; return func() {}, nil }
	info, e := c.Probe(ctx)
	if e != nil || info.ServerID != "fixture-server" || len(info.Libraries) != 2 {
		t.Fatal(info, e)
	}
	p, e := c.Browse(ctx, "1", "", 0, 20, []string{"20"})
	if e != nil || len(p.Items) != 0 {
		t.Fatal("cross-library import", e)
	}
	p, e = c.Browse(ctx, "1", "电影", 0, 20, nil)
	if e != nil || len(p.Items) != 1 || p.Items[0].RunTimeTicks != 1200000000 {
		t.Fatal(p, e)
	}
	eps, e := c.Episodes(ctx, "20")
	if e != nil || len(eps) != 1 || eps[0].IndexNumber != 2 || *eps[0].ParentIndexNumber != 1 {
		t.Fatal(eps, e)
	}
	for _, method := range []string{"GET", "HEAD"} {
		r, e := c.stream(ctx, method, "10", "100", http.Header{"Range": {"bytes=0-4"}})
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 206 {
			t.Fatal(r.StatusCode)
		}
	}
	link, e := c.StreamURL(ctx, "10", "100", "mp4")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(link)
	if u.Query().Get("X-Plex-Token") != "fixture-token" || !strings.HasPrefix(u.Path, "/base/library/parts/100/") {
		t.Fatal("invalid direct URL")
	}
	if _, e = c.StreamURL(ctx, "10", "210", "mkv"); e == nil {
		t.Fatal("foreign Part accepted")
	}
	if calls < 8 {
		t.Fatal("metadata bypassed request budget")
	}
	denied, _ := New(up.URL, "fixture-user", "fixture-password", false)
	defer denied.Close()
	if _, e = denied.Probe(ctx); !errors.Is(e, emby.ErrPrivate) {
		t.Fatal(e)
	}
	wrong, _ := New(up.URL, "fixture-user", "wrong-fixture", true)
	defer wrong.Close()
	if _, e = wrong.Probe(ctx); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
}
func TestPlexResourcePaths(t *testing.T) {
	for _, p := range []string{"https://foreign.test/library/parts/1/file/a", "//foreign.test/library/parts/1/file/a", "/library/parts/1/../secret", "/library/parts/1/%2e%2e/secret", "/library/parts/1/file/a?token=secret"} {
		if _, e := resource(p, "/library/parts/1/"); e == nil {
			t.Fatal("unsafe path accepted")
		}
	}
}
func TestPlexTruncatedPage(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"size":0,"totalSize":100,"Metadata":[]}}`))
	}))
	defer s.Close()
	c, _ := New(s.URL, "fixture-user", "fixture-password", true)
	c.token = "fixture-token"
	defer c.Close()
	if _, e := c.Browse(context.Background(), "1", "", 0, 20, nil); e == nil {
		t.Fatal("truncated listing accepted")
	}
}

func TestPlexMultipartRejected(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"10","type":"movie","Media":[{"container":"mp4","Part":[{"id":1},{"id":2}]}]}]}}`))
	}))
	defer s.Close()
	c, _ := New(s.URL, "fixture-user", "fixture-password", true)
	c.token = "fixture-token"
	defer c.Close()
	if _, e := c.Hydrate(context.Background(), emby.Item{ID: "10"}); e == nil {
		t.Fatal("multipart movie truncated")
	}
}

func TestPlexPageOffset(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Plex-Container-Start") != "100" || r.URL.Query().Get("X-Plex-Container-Size") != "100" {
			t.Error("wrong page")
		}
		w.Write([]byte(`{"MediaContainer":{"size":1,"totalSize":101,"Metadata":[{"ratingKey":"101","type":"show","title":"Last"}]}}`))
	}))
	defer s.Close()
	c, _ := New(s.URL, "fixture-user", "fixture-password", true)
	c.token = "fixture-token"
	defer c.Close()
	p, e := c.Browse(context.Background(), "1", "", 100, 100, nil)
	if e != nil || len(p.Items) != 1 || p.TotalRecordCount != 101 {
		t.Fatal(p, e)
	}
}

func TestPlexPasswordRenewalConcurrent(t *testing.T) {
	testdata.Account(t)
	up := testdata.Server(t)
	defer up.Close()
	c, err := New(up.URL+"/base", "fixture-user", "fixture-password", true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.token = "expired-fixture"
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Probe(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.token != "fixture-token" {
		t.Fatal("server grant not renewed")
	}
}

func TestPlexCredentialsRequired(t *testing.T) {
	for _, v := range [][2]string{{"", "fixture-password"}, {"fixture-user", ""}} {
		if _, e := New("https://media.example.test", v[0], v[1], false); !errors.Is(e, ErrAuth) {
			t.Fatal("account credentials required")
		}
	}
}
