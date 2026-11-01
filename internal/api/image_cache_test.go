package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

type imageCacheFixture struct {
	calls   atomic.Int32
	status  atomic.Int32
	size    atomic.Int64
	badType atomic.Bool
	delay   atomic.Bool
	started chan struct{}
	unblock chan struct{}
}

func newImageCacheFixture(t *testing.T, s *Server) (*imageCacheFixture, int64) {
	t.Helper()
	f := &imageCacheFixture{started: make(chan struct{}, 100), unblock: make(chan struct{})}
	f.status.Store(200)
	f.size.Store(3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emby/Users/AuthenticateByName" {
			fmt.Fprint(w, `{"AccessToken":"token","User":{"Id":"u"}}`)
			return
		}
		if !strings.Contains(r.URL.Path, "/Images/") {
			t.Error(r.URL.Path)
			http.NotFound(w, r)
			return
		}
		f.calls.Add(1)
		if f.delay.Load() {
			f.started <- struct{}{}
			select {
			case <-f.unblock:
			case <-r.Context().Done():
				return
			}
		}
		if f.badType.Load() {
			w.Header().Set("Content-Type", "text/html")
		} else {
			w.Header().Set("Content-Type", "image/png")
		}
		w.WriteHeader(int(f.status.Load()))
		if f.size.Load() == 3 {
			fmt.Fprint(w, "png")
		} else {
			_, _ = io.CopyN(w, zeroImageReader{}, f.size.Load())
		}
	}))
	t.Cleanup(upstream.Close)
	id, err := s.Store.NextID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ds := &store.DataSource{ID: id, MID: 1, System: true, Type: "emby", Name: "cache", URL: upstream.URL + "/emby", Username: "u", Status: "ok"}
	if err = store.SaveDataSource(t.Context(), s.Store.DB, ds); err != nil {
		t.Fatal(err)
	}
	// Existing seeded collections provide real, inexpensive reference checks.
	liveExec(t, s, "UPDATE collections SET origin_ds=?,origin_library=? WHERE id=(SELECT MIN(id) FROM collections)", id, "picture")
	return f, id
}

type zeroImageReader struct{}

func (zeroImageReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func imageCacheWait(s *Server)                     { s.sources.images.wg.Wait() }
func imageCacheSet(t *testing.T, s *Server, key string, value any) {
	t.Helper()
	if err := s.Store.SetSetting(context.Background(), key, value); err != nil {
		t.Fatal(err)
	}
}
func TestImageCacheProxy(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newImageCacheFixture(t, s)
		path := sourceImage(id, "picture", "Primary", "one")
		first := sourceHTTP(s, path)
		second := sourceHTTP(s, path)
		for i, w := range []*httptest.ResponseRecorder{first, second} {
			want := []string{"MISS", "HIT"}[i]
			if w.Code != 200 || w.Body.String() != "png" || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Length") != "3" || w.Header().Get("X-Cache") != want || w.Header().Get("Cache-Control") != "public, max-age=86400" {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
		}
		if f.calls.Load() != 1 {
			t.Fatal(f.calls.Load())
		}
		for _, header := range []string{"If-None-Match", "If-Modified-Since"} {
			r := httptest.NewRequest("GET", path, nil)
			if header == "If-None-Match" {
				r.Header.Set(header, second.Header().Get("ETag"))
			} else {
				r.Header.Set(header, second.Header().Get("Last-Modified"))
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("X-Cache") != "HIT" {
				t.Fatal(header, w.Code, w.Header())
			}
		}
		if w := sourceHTTP(s, sourceImage(id, "picture", "Primary", "two")); w.Header().Get("X-Cache") != "MISS" || f.calls.Load() != 2 {
			t.Fatal(w.Header(), f.calls.Load())
		}
		for _, kind := range []string{"Backdrop", "Thumb"} {
			if w := sourceHTTP(s, sourceImage(id, "picture", kind, "one")); w.Header().Get("X-Cache") != "MISS" {
				t.Fatal(w.Header())
			}
		}
		before := f.calls.Load()
		liveExec(t, s, "UPDATE collections SET origin_library=? WHERE origin_ds=?", "different", id)
		if w := sourceHTTP(s, path); w.Code != 404 {
			t.Fatal(w.Code)
		}
		liveExec(t, s, "UPDATE collections SET origin_library=? WHERE origin_ds=?", "picture", id)
		liveExec(t, s, "DELETE FROM data_sources WHERE id=?", id)
		if w := sourceHTTP(s, path); w.Code != 404 {
			t.Fatal(w.Code)
		}
		if f.calls.Load() != before {
			t.Fatal("invalid reference reached upstream")
		}
	})
}
func TestImageCacheConcurrentMiss(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newImageCacheFixture(t, s)
		for _, large := range []bool{false, true} {
			tag := "concurrent"
			wantSize := 3
			if large {
				tag = "large"
				wantSize = imageCacheLimit + 1
				f.size.Store(int64(wantSize))
			}
			f.delay.Store(true)
			before := f.calls.Load()
			start := make(chan struct{})
			var wg sync.WaitGroup
			results := make(chan *httptest.ResponseRecorder, 20)
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); <-start; results <- sourceHTTP(s, sourceImage(id, "picture", "Primary", tag)) }()
			}
			close(start)
			select {
			case <-f.started:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream did not start")
			}
			deadline := time.Now().Add(5 * time.Second)
			key := imageCacheKey(id, "picture", "Primary", tag)
			for {
				s.sources.images.mu.Lock()
				flight := s.sources.images.flights[key]
				refs := 0
				if flight != nil {
					refs = flight.refs
				}
				s.sources.images.mu.Unlock()
				if refs == 20 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("only %d requests joined", refs)
				}
				time.Sleep(time.Millisecond)
			}
			close(f.unblock)
			wg.Wait()
			close(results)
			for w := range results {
				if w.Code != 200 || w.Body.Len() != wantSize {
					t.Fatal(w.Code, w.Body.Len())
				}
			}
			if f.calls.Load() != before+1 {
				t.Fatal("multiple upstream requests", f.calls.Load()-before)
			}
			if large {
				dir, _ := imageCacheDefaultDir(s.Config.DataDir)
				if _, err := os.Stat(filepath.Join(dir, key[:2], key)); !os.IsNotExist(err) {
					t.Fatal("oversized response cached", err)
				}
			}
			f.unblock = make(chan struct{})
		}
	})
}
func TestImageCacheExpiryAndDisabled(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newImageCacheFixture(t, s)
		var clock atomic.Int64
		initial := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
		clock.Store(initial.Unix())
		s.sources.images.now = func() time.Time { return time.Unix(clock.Load(), 0) }
		path := sourceImage(id, "picture", "Primary", "")
		if w := sourceHTTP(s, path); w.Code != 200 {
			t.Fatal(w.Code)
		}
		dir, _ := imageCacheDefaultDir(s.Config.DataDir)
		key := imageCacheKey(id, "picture", "Primary", "")
		disk := filepath.Join(dir, key[:2], key)
		st, _ := os.Stat(disk)
		clock.Add(30 * 60)
		if w := sourceHTTP(s, path); w.Header().Get("X-Cache") != "HIT" {
			t.Fatal(w.Header())
		}
		unchanged, _ := os.Stat(disk)
		if !unchanged.ModTime().Equal(st.ModTime()) {
			t.Fatal("touched within one hour")
		}
		clock.Add(90 * 60)
		sourceHTTP(s, path)
		touched, _ := os.Stat(disk)
		if touched.ModTime().Unix() != clock.Load() {
			t.Fatal("access time not updated")
		}
		clock.Store(initial.Add(24*time.Hour + time.Second).Unix())
		if w := sourceHTTP(s, path); w.Header().Get("X-Cache") != "MISS" || f.calls.Load() != 2 {
			t.Fatal(w.Header(), f.calls.Load())
		}
		tagged := sourceImage(id, "picture", "Primary", "forever")
		sourceHTTP(s, tagged)
		clock.Add(365 * 24 * 3600)
		if w := sourceHTTP(s, tagged); w.Header().Get("X-Cache") != "HIT" {
			t.Fatal(w.Header())
		}
		imageCacheWait(s)
		before := s.sources.images.current.counts()
		imageCacheSet(t, s, "imageCacheEnabled", false)
		calls := f.calls.Load()
		sourceHTTP(s, tagged)
		sourceHTTP(s, sourceImage(id, "picture", "Primary", "disabled"))
		imageCacheWait(s)
		after := s.sources.images.current.counts()
		if f.calls.Load() != calls+2 || before != after {
			t.Fatal(f.calls.Load(), before, after)
		}
	})
}
func TestImageCacheRejectsAndWriteFailure(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newImageCacheFixture(t, s)
		for _, tc := range []struct {
			name   string
			status int
			bad    bool
			size   int64
			want   int
		}{
			{"status", 404, false, 3, 502}, {"type", 200, true, 3, 502}, {"oversize", 200, false, imageCacheLimit + 1, 200},
		} {
			f.status.Store(int32(tc.status))
			f.badType.Store(tc.bad)
			f.size.Store(tc.size)
			before := f.calls.Load()
			for i := 0; i < 2; i++ {
				w := sourceHTTP(s, sourceImage(id, "picture", "Primary", tc.name))
				if w.Code != tc.want || w.Header().Get("X-Cache") != "MISS" {
					t.Fatal(tc.name, w.Code, w.Header())
				}
			}
			if f.calls.Load() != before+2 {
				t.Fatal("invalid response cached", tc.name)
			}
		}
		f.status.Store(200)
		f.badType.Store(false)
		f.size.Store(3)
		bad := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(bad, []byte("unchanged"), 0600); err != nil {
			t.Fatal(err)
		}
		imageCacheSet(t, s, "imageCacheDir", bad)
		if w := sourceHTTP(s, sourceImage(id, "picture", "Primary", "cannot-write")); w.Code != 200 || w.Body.String() != "png" {
			t.Fatal(w.Code, w.Body.String())
		}
		b, _ := os.ReadFile(bad)
		if string(b) != "unchanged" {
			t.Fatal("modified non-cache file")
		}
	})
}
func TestImageCacheDirectoryClearAndReset(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		_, id := newImageCacheFixture(t, s)
		admin := login(t, s, "jumedia", "admin123")
		path := sourceImage(id, "picture", "Primary", "one")
		sourceHTTP(s, path)
		imageCacheWait(s)
		old := s.sources.images.current.dir
		key := imageCacheKey(id, "picture", "Primary", "one")
		oldBytes, err := os.ReadFile(filepath.Join(old, key[:2], key))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "new")
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"imageCacheDir": {dir}}), 0)
		if w := sourceHTTP(s, path); w.Header().Get("X-Cache") != "MISS" {
			t.Fatal(w.Header())
		}
		imageCacheWait(s)
		state := adminTestData(t, get(t, s, "/x/admin/imagecache", admin))
		if state["dir"] != dir || state["files"] != float64(1) || state["bytes"].(float64) <= 3 {
			t.Fatal(state)
		}
		b, err := os.ReadFile(filepath.Join(old, key[:2], key))
		if err != nil || !bytes.Equal(b, oldBytes) {
			t.Fatal("old directory changed", err)
		}
		imageCacheSafetyFiles(t, dir, key)
		deleted := adminTestData(t, adminTestPost(t, s, admin, "imagecache/clear", nil))
		if deleted["files"] != float64(1) || deleted["bytes"].(float64) <= 3 {
			t.Fatal(deleted)
		}
		imageCacheAssertSafety(t, dir, key)
		state = adminTestData(t, get(t, s, "/x/admin/imagecache", admin))
		if state["files"] != float64(0) || state["bytes"] != float64(0) {
			t.Fatal(state)
		}
		sourceHTTP(s, path)
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"reset": {"true"}}), 0)
		if _, err := os.Stat(filepath.Join(dir, key[:2], key)); !os.IsNotExist(err) {
			t.Fatal("reset retained cache", err)
		}
		imageCacheAssertSafety(t, dir, key)
		if _, err := os.Stat(filepath.Join(old, key[:2], key)); err != nil {
			t.Fatal("reset cleared previous directory", err)
		}
	})
}
func imageCacheSafetyFiles(t *testing.T, dir, key string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, "ee"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep.txt", filepath.Join(key[:2], "keep.txt"), filepath.Join(key[:2], strings.Repeat("f", 64))} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("safe"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A matching leftover temp and sidecar are ours to delete.
	for _, suffix := range []string{".type", ".tmp-" + strings.Repeat("a", 32)} {
		if err := os.WriteFile(filepath.Join(dir, key[:2], key+suffix), []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(dir, "keep.txt")
	if err := os.Symlink(target, filepath.Join(dir, key[:2], key[:2]+strings.Repeat("b", 62))); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, key[:2]), filepath.Join(dir, "ff")); err != nil {
		t.Fatal(err)
	}
}
func imageCacheAssertSafety(t *testing.T, dir, key string) {
	t.Helper()
	if st, err := os.Stat(filepath.Join(dir, "ee")); err != nil || !st.IsDir() {
		t.Fatal("unrelated empty directory removed", err)
	}
	for _, name := range []string{"keep.txt", filepath.Join(key[:2], "keep.txt"), filepath.Join(key[:2], strings.Repeat("f", 64))} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != "safe" {
			t.Fatal(name, string(b), err)
		}
	}
	for _, name := range []string{"ff", filepath.Join(key[:2], key[:2]+strings.Repeat("b", 62))} {
		st, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			t.Fatal(name, err)
		}
	}
}
func TestImageCacheScanEvictsLRU(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		dir := t.TempDir()
		now := time.Now()
		keys := []string{}
		for i := 0; i < 8; i++ {
			key := imageCacheKey(1, "picture", "Primary", fmt.Sprint(i))
			keys = append(keys, key)
			accessed := now.Add(time.Duration(-8+i) * time.Hour)
			if i == 0 {
				accessed = now
			}
			meta := imageCacheMetadata{Type: "image/png", Created: now, Modified: now}
			if _, err := imageCacheWrite(dir, key, meta, []byte("png"), accessed); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, key[:2], key)
			if err := os.Truncate(path, 9<<20); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, accessed, accessed); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "unrelated"), []byte("safe"), 0600); err != nil {
			t.Fatal(err)
		}
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"imageCacheDir": {dir}, "imageCacheMaxMB": {"128"}}), 0)
		imageCacheWait(s)
		state := adminTestData(t, get(t, s, "/x/admin/imagecache", admin))
		if state["files"] != float64(8) || state["bytes"] != float64(72<<20) {
			t.Fatal(state)
		}
		// Lowering the limit must schedule cleanup without a new image write.
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"imageCacheMaxMB": {"64"}}), 0)
		imageCacheWait(s)
		state = adminTestData(t, get(t, s, "/x/admin/imagecache", admin))
		if state["files"] != float64(6) || state["bytes"].(float64) > float64(64<<20)*.9 {
			t.Fatal(state)
		}
		for i, key := range keys {
			_, err := os.Stat(filepath.Join(dir, key[:2], key))
			if (i == 1 || i == 2) != os.IsNotExist(err) {
				t.Fatal("wrong LRU victim", i, err)
			}
		}
		if b, err := os.ReadFile(filepath.Join(dir, "unrelated")); err != nil || string(b) != "safe" {
			t.Fatal(err)
		}
	})
}

func TestImageCacheClearWaitsForInflightWrite(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newImageCacheFixture(t, s)
		admin := login(t, s, "jumedia", "admin123")
		f.delay.Store(true)
		imageDone := make(chan *httptest.ResponseRecorder, 1)
		go func() { imageDone <- sourceHTTP(s, sourceImage(id, "picture", "Primary", "inflight")) }()
		select {
		case <-f.started:
		case <-time.After(5 * time.Second):
			t.Fatal("no fetch")
		}
		clearDone := make(chan map[string]any, 1)
		go func() { clearDone <- adminTestPost(t, s, admin, "imagecache/clear", nil) }()
		select {
		case v := <-clearDone:
			t.Fatal("clear returned before active write", v)
		case <-time.After(30 * time.Millisecond):
		}
		close(f.unblock)
		if w := <-imageDone; w.Code != 200 {
			t.Fatal(w.Code)
		}
		cleared := adminTestData(t, <-clearDone)
		if cleared["files"] != float64(1) {
			t.Fatal(cleared)
		}
		imageCacheWait(s)
		state := adminTestData(t, get(t, s, "/x/admin/imagecache", admin))
		if state["files"] != float64(0) || state["bytes"] != float64(0) {
			t.Fatal(state)
		}
	})
}
func TestImageCacheDoesNotFollowSymlinks(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		_, id := newImageCacheFixture(t, s)
		imageCacheWait(s)
		dir := t.TempDir()
		outside := t.TempDir()
		imageCacheSet(t, s, "imageCacheDir", dir)
		for _, mode := range []string{"shard", "file", "root"} {
			t.Run(mode, func(t *testing.T) {
				tag := "link-" + mode
				key := imageCacheKey(id, "picture", "Primary", tag)
				target := filepath.Join(outside, key)
				if err := os.WriteFile(target, []byte("never touch"), 0600); err != nil {
					t.Fatal(err)
				}
				var link string
				switch mode {
				case "shard":
					link = filepath.Join(dir, key[:2])
					if err := os.Symlink(outside, link); err != nil {
						t.Fatal(err)
					}
				case "file":
					if err := os.MkdirAll(filepath.Join(dir, key[:2]), 0755); err != nil {
						t.Fatal(err)
					}
					link = filepath.Join(dir, key[:2], key)
					if err := os.Symlink(target, link); err != nil {
						t.Fatal(err)
					}
				case "root":
					link = filepath.Join(t.TempDir(), "root-link")
					if err := os.Symlink(outside, link); err != nil {
						t.Fatal(err)
					}
					imageCacheSet(t, s, "imageCacheDir", link)
				}
				w := sourceHTTP(s, sourceImage(id, "picture", "Primary", tag))
				if w.Code != 200 || w.Body.String() != "png" || w.Header().Get("X-Cache") != "MISS" {
					t.Fatal(w.Code, w.Body.String())
				}
				b, err := os.ReadFile(target)
				if err != nil || string(b) != "never touch" {
					t.Fatal(string(b), err)
				}
				st, err := os.Lstat(link)
				if err != nil || st.Mode()&os.ModeSymlink == 0 {
					t.Fatal(err)
				}
			})
		}
	})
}
func TestImageCacheTruncatedResponseAndSizeBoundary(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newImageCacheFixture(t, s)
		f.size.Store(imageCacheLimit)
		path := sourceImage(id, "picture", "Primary", "exact-limit")
		for i := 0; i < 2; i++ {
			w := sourceHTTP(s, path)
			if w.Code != 200 || w.Body.Len() != imageCacheLimit {
				t.Fatal(w.Code, w.Body.Len())
			}
		}
		if f.calls.Load() != 1 {
			t.Fatal("10 MB response not cached")
		}
		var calls atomic.Int32
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "AuthenticateByName") {
				fmt.Fprint(w, `{"AccessToken":"token","User":{"Id":"u"}}`)
				return
			}
			calls.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", "100")
			fmt.Fprint(w, "short")
		}))
		defer bad.Close()
		liveExec(t, s, "UPDATE data_sources SET url=? WHERE id=?", bad.URL+"/emby", id)
		path = sourceImage(id, "picture", "Primary", "truncated")
		for i := 0; i < 2; i++ {
			if w := sourceHTTP(s, path); w.Code != 502 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
		if calls.Load() != 2 {
			t.Fatal("truncated body cached")
		}
	})
}
