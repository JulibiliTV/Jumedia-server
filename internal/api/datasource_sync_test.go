package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

type syncFixture struct {
	mu    sync.Mutex
	items map[string][]emby.Item
	views []map[string]string
	hook  func(http.ResponseWriter, *http.Request) bool
}

func newSyncFixture(t *testing.T, s *Server, n int) (*syncFixture, int64) {
	t.Helper()
	f := &syncFixture{items: map[string][]emby.Item{}, views: []map[string]string{{"Id": "movies", "Name": "电影", "CollectionType": "movies"}, {"Id": "other", "Name": "其它", "CollectionType": "movies"}}}
	for i := 0; i < n; i++ {
		f.items["movies"] = append(f.items["movies"], sourceMovie(fmt.Sprintf("m%03d", i), fmt.Sprintf("电影 %03d", i), "mp4"))
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		hook := f.hook
		f.mu.Unlock()
		if hook != nil && hook(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/AuthenticateByName"):
			fmt.Fprint(w, `{"AccessToken":"token","User":{"Id":"user"}}`)
		case strings.HasSuffix(r.URL.Path, "/Views"):
			json.NewEncoder(w).Encode(map[string]any{"Items": f.views})
		case strings.HasSuffix(r.URL.Path, "/Items"):
			if r.URL.Query().Get("Limit") != "100" || r.URL.Query().Get("SortBy") != "SortName" || r.URL.Query().Get("SortOrder") != "Ascending" {
				t.Error(r.URL)
			}
			items := f.items[r.URL.Query().Get("ParentId")]
			start := min(int(NumberGo(r.URL.Query().Get("StartIndex"), 0)), len(items))
			json.NewEncoder(w).Encode(map[string]any{"Items": items[start:min(start+100, len(items))], "TotalRecordCount": len(items)})
		case strings.HasSuffix(r.URL.Path, "/Episodes"):
			json.NewEncoder(w).Encode(map[string]any{"Items": []emby.Item{sourceEpisode(strings.Split(r.URL.Path, "/")[3]+"-ep", "1", 1, 1)}})
		default:
			t.Error("unexpected", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	if err := s.InitDataSources(t.Context()); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{"dataSourceSyncRate": 50, "dataSourceSyncConcurrency": 2} {
		if err := s.Store.SetSetting(t.Context(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	var id int64
	err := s.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error {
		var err error
		id, err = store.NextID(t.Context(), tx)
		if err != nil {
			return err
		}
		return store.SaveDataSource(t.Context(), tx, &store.DataSource{ID: id, MID: 1, System: true, Type: "emby", Name: "fixture", URL: upstream.URL, Username: "fixture", ServerID: fmt.Sprint(id), Libraries: []store.DataSourceLibrary{}, Status: "idle"})
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, id
}
func (f *syncFixture) setHook(h func(http.ResponseWriter, *http.Request) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hook = h
}
func syncStart(t *testing.T, s *Server, id int64) {
	t.Helper()
	if _, err := s.startSourceSync(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}
func syncFinished(t *testing.T, s *Server, id int64) *store.DataSource {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s.sources.mu.Lock()
		_, running := s.sources.running[id]
		s.sources.mu.Unlock()
		if !running {
			ds, err := store.DataSourceByID(t.Context(), s.Store.DB, id)
			if err != nil {
				t.Fatal(err)
			}
			return ds
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("sync timeout")
	return nil
}
func syncAwait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("page never reached")
	}
}
func syncCollection(t *testing.T, s *Server, id int64, library string) *store.Collection {
	t.Helper()
	var cid int64
	if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT id FROM collections WHERE origin_ds=? AND origin_library=?", id, library).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	c, err := s.Store.CollectionByID(t.Context(), cid)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSyncPageCommitAndCancellation(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newSyncFixture(t, s, 250)
		reached, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		f.setHook(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("StartIndex") == "200" {
				once.Do(func() { close(reached) })
				select {
				case <-release:
				case <-r.Context().Done():
					return true
				}
			}
			return false
		})
		syncStart(t, s, id)
		syncAwait(t, reached)
		if as := sourceArchives(t, s, id); len(as) != 200 {
			t.Fatal("not committed per page", len(as))
		}
		c := syncCollection(t, s, id, "movies")
		if len(c.AIDs) != 200 {
			t.Fatal(len(c.AIDs))
		}
		ds, err := store.DataSourceByID(t.Context(), s.Store.DB, id)
		if err != nil || ds.Message != "正在同步「电影」：200 / 250" || ds.Libraries[0].Items != 200 {
			t.Fatal(ds, err)
		}
		// Polling the public route sees committed progress.
		admin := login(t, s, "jumedia", "admin123")
		list := sourceData(t, get(t, s, "/x/admin/datasources", admin))
		encoded, _ := json.Marshal(list)
		if !strings.Contains(string(encoded), "200 / 250") {
			t.Fatal(list)
		}
		close(release)
		ds = syncFinished(t, s, id)
		if ds.Status != "ok" || ds.Message != "已同步 250 个视频" {
			t.Fatal(ds)
		}
		old := sourceArchives(t, s, id)
		oldCID := old["m000"].Pages[0].CID
		// The next run sees 200 changed titles before cancellation; unseen old rows
		// and the collection's stale tail must survive, without a -6 cleanup.
		f.mu.Lock()
		f.items["movies"] = append([]emby.Item{}, f.items["movies"][:249]...)
		f.items["movies"][0].Name = "已更新"
		f.mu.Unlock()
		reached = make(chan struct{})
		once = sync.Once{}
		f.setHook(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("StartIndex") == "200" {
				once.Do(func() { close(reached) })
				<-r.Context().Done()
				return true
			}
			return false
		})
		syncStart(t, s, id)
		syncAwait(t, reached)
		s.sources.mu.Lock()
		s.sources.running[id]()
		s.sources.mu.Unlock()
		ds = syncFinished(t, s, id)
		if ds.Status != "error" || ds.Message != "同步被中断，请重新同步" {
			t.Fatal(ds)
		}
		as := sourceArchives(t, s, id)
		if as["m000"].Title != "已更新" || as["m000"].Pages[0].CID != oldCID {
			t.Fatal(as["m000"])
		}
		for _, a := range as {
			if a.State == -6 {
				t.Fatal("canceled run hid archive", a.OriginItem)
			}
		}
		if len(syncCollection(t, s, id, "movies").AIDs) != 250 {
			t.Fatal("canceled run removed members")
		}
		f.setHook(nil)
		syncStart(t, s, id)
		ds = syncFinished(t, s, id)
		as = sourceArchives(t, s, id)
		if ds.Status != "ok" || as["m249"].State != -6 || len(syncCollection(t, s, id, "movies").AIDs) != 249 {
			t.Fatal(ds, as["m249"])
		}
		f.mu.Lock()
		f.items["movies"] = append(f.items["movies"], sourceMovie("m249", "回归", "mp4"))
		f.mu.Unlock()
		syncStart(t, s, id)
		ds = syncFinished(t, s, id)
		as = sourceArchives(t, s, id)
		if ds.Status != "ok" || as["m249"].State != 0 || as["m249"].Pages[0].CID != old["m249"].Pages[0].CID {
			t.Fatal(ds, as["m249"])
		}
	})
}

func TestSyncPartialFailures(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newSyncFixture(t, s, 120)
		f.mu.Lock()
		f.items["other"] = []emby.Item{sourceMovie("other-old", "旧电影", "mp4"), {ID: "series", Name: "剧集原名", Type: "Series"}}
		f.mu.Unlock()
		syncStart(t, s, id)
		if ds := syncFinished(t, s, id); ds.Status != "ok" {
			t.Fatal(ds)
		}
		before := sourceArchives(t, s, id)
		f.mu.Lock()
		f.items["movies"] = f.items["movies"][:110]
		f.items["other"] = f.items["other"][1:]
		f.items["other"][0].Name = "不能覆盖"
		f.mu.Unlock()
		var pages, episodes atomic.Int32
		f.setHook(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("ParentId") == "movies" && r.URL.Query().Get("StartIndex") == "100" {
				pages.Add(1)
				w.WriteHeader(503)
				return true
			}
			if strings.HasSuffix(r.URL.Path, "/Episodes") {
				episodes.Add(1)
				w.WriteHeader(503)
				return true
			}
			return false
		})
		syncStart(t, s, id)
		ds := syncFinished(t, s, id)
		if ds.Status != "error" || ds.Message != "部分内容同步失败（2 处）：Emby 返回 HTTP 503；已同步 101 个视频" {
			t.Fatal(ds)
		}
		if pages.Load() != 4 || episodes.Load() != 4 {
			t.Fatal(pages.Load(), episodes.Load())
		}
		as := sourceArchives(t, s, id)
		if !reflect.DeepEqual(before["series"], as["series"]) {
			t.Fatal("failed item changed", before["series"], as["series"])
		}
		if as["m119"].State == -6 || as["other-old"].State != -6 {
			t.Fatal("incomplete/complete cleanup")
		}
		if len(syncCollection(t, s, id, "movies").AIDs) != 120 {
			t.Fatal("incomplete collection trimmed")
		}
		c := syncCollection(t, s, id, "other")
		if !reflect.DeepEqual(c.AIDs, []int64{as["series"].AID}) {
			t.Fatal(c)
		}
		// Empty complete library removes all old members; disabled library clears its ID.
		f.setHook(nil)
		f.mu.Lock()
		f.items["other"] = nil
		f.mu.Unlock()
		syncStart(t, s, id)
		ds = syncFinished(t, s, id)
		if ds.Status != "ok" || len(syncCollection(t, s, id, "other").AIDs) != 0 {
			t.Fatal(ds)
		}
		ds.Libraries[0].Enabled = false
		if err := store.SaveDataSource(t.Context(), s.Store.DB, ds); err != nil {
			t.Fatal(err)
		}
		syncStart(t, s, id)
		ds = syncFinished(t, s, id)
		if ds.Libraries[0].CollectionID != 0 {
			t.Fatal(ds)
		}
		var count int
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM collections WHERE origin_ds=? AND origin_library=?", id, "movies").Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
	})
}

func TestSyncEpisodesParallelAndSettingsSnapshot(t *testing.T) {
	s := newTestServer(t)
	f, id := newSyncFixture(t, s, 0)
	f.mu.Lock()
	for i := 0; i < 4; i++ {
		f.items["movies"] = append(f.items["movies"], emby.Item{ID: fmt.Sprint(i), Name: "剧集", Type: "Series"})
	}
	f.mu.Unlock()
	if err := s.Store.SetSetting(t.Context(), "dataSourceSyncRate", 5); err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int32
	var mu sync.Mutex
	var starts []time.Time
	arrived, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.setHook(func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/Views") {
			once.Do(func() { close(arrived) })
			select {
			case <-release:
			case <-r.Context().Done():
				return true
			}
		}
		if strings.HasSuffix(r.URL.Path, "/Episodes") {
			n := active.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(300 * time.Millisecond)
			active.Add(-1)
		}
		return false
	})
	syncStart(t, s, id)
	syncAwait(t, arrived)
	if err := s.Store.SetSetting(t.Context(), "dataSourceSyncConcurrency", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetSetting(t.Context(), "dataSourceSyncRate", 50); err != nil {
		t.Fatal(err)
	}
	close(release)
	if ds := syncFinished(t, s, id); ds.Status != "ok" {
		t.Fatal(ds)
	}
	if peak.Load() != 2 {
		t.Fatal("parallelism/snapshot", peak.Load())
	}
	mu.Lock()
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 180*time.Millisecond {
			t.Error("unthrottled sync request", starts)
		}
	}
	mu.Unlock()
	peak.Store(0)
	syncStart(t, s, id)
	syncFinished(t, s, id)
	if peak.Load() != 1 {
		t.Fatal("new concurrency not applied", peak.Load())
	}
}

func TestSyncDeleteCannotResurrect(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newSyncFixture(t, s, 150)
		reached := make(chan struct{})
		var once sync.Once
		f.setHook(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("StartIndex") == "100" {
				once.Do(func() { close(reached) })
				<-r.Context().Done()
				return true
			}
			return false
		})
		syncStart(t, s, id)
		syncAwait(t, reached)
		ds, err := store.DataSourceByID(t.Context(), s.Store.DB, id)
		if err != nil {
			t.Fatal(err)
		}
		admin := login(t, s, "jumedia", "admin123")
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/delete", url.Values{"id": {fmt.Sprint(id)}}), 0)
		// Even a stale writer with a live context must check source existence under lock.
		err = s.sourceSyncTx(context.Background(), ds, func(tx *db.Tx) error {
			return upsertSourceCollection(context.Background(), tx, ds, &ds.Libraries[0], nil, true, nil)
		})
		if err == nil {
			t.Fatal("deleted source accepted stale write")
		}
		var count int
		if err = s.Store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM collections WHERE origin_ds=?", id).Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
		for _, a := range sourceArchives(t, s, id) {
			if a.State != -100 {
				t.Fatal("resurrected", a)
			}
		}
	})
}

func TestSyncCollectionOrderAndClaims(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f, id := newSyncFixture(t, s, 120)
		syncStart(t, s, id)
		if ds := syncFinished(t, s, id); ds.Status != "ok" {
			t.Fatal(ds)
		}
		original := syncCollection(t, s, id, "movies").AIDs
		var other, series int64
		err := s.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error {
			for _, kind := range []string{"season", "series"} {
				cid, err := store.NextID(t.Context(), tx)
				if err != nil {
					return err
				}
				if kind == "season" {
					other = cid
				} else {
					series = cid
				}
				if err = store.InsertCollection(t.Context(), tx, &store.Collection{ID: cid, MID: 1, Kind: kind}); err != nil {
					return err
				}
				if err = store.InsertCollectionItem(t.Context(), tx, &store.CollectionItem{CollectionID: cid, AID: original[119]}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		for i, j := 0, 119; i < j; i, j = i+1, j-1 {
			f.items["movies"][i], f.items["movies"][j] = f.items["movies"][j], f.items["movies"][i]
		}
		f.mu.Unlock()
		f.setHook(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Query().Get("StartIndex") == "100" {
				w.WriteHeader(400)
				return true
			}
			return false
		})
		syncStart(t, s, id)
		ds := syncFinished(t, s, id)
		if ds.Status != "error" {
			t.Fatal(ds)
		}
		want := []int64{}
		for i := 119; i >= 20; i-- {
			want = append(want, original[i])
		}
		want = append(want, original[:20]...)
		if got := syncCollection(t, s, id, "movies").AIDs; !reflect.DeepEqual(got, want) {
			t.Fatal("partial order", got, want)
		}
		for cid, want := range map[int64]int{other: 0, series: 1} {
			c, err := s.Store.CollectionByID(t.Context(), cid)
			if err != nil || len(c.AIDs) != want {
				t.Fatal(c, err)
			}
		}
		f.setHook(nil)
		syncStart(t, s, id)
		ds = syncFinished(t, s, id)
		if ds.Status != "ok" {
			t.Fatal(ds)
		}
		want = nil
		for i := 119; i >= 0; i-- {
			want = append(want, original[i])
		}
		if got := syncCollection(t, s, id, "movies").AIDs; !reflect.DeepEqual(got, want) {
			t.Fatal("complete order", got, want)
		}
	})
}

func TestSyncReloginFailureIsFatal(t *testing.T) {
	s := newTestServer(t)
	f, id := newSyncFixture(t, s, 120)
	syncStart(t, s, id)
	if ds := syncFinished(t, s, id); ds.Status != "ok" {
		t.Fatal(ds)
	}
	var expired atomic.Bool
	var other atomic.Int32
	f.setHook(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Query().Get("StartIndex") == "100" {
			expired.Store(true)
			w.WriteHeader(401)
			return true
		}
		if expired.Load() && strings.HasSuffix(r.URL.Path, "/AuthenticateByName") {
			w.WriteHeader(401)
			return true
		}
		if r.URL.Query().Get("ParentId") == "other" {
			other.Add(1)
		}
		return false
	})
	syncStart(t, s, id)
	ds := syncFinished(t, s, id)
	if ds.Status != "error" || ds.Message != "Emby 用户名或密码错误" || other.Load() != 0 {
		t.Fatal(ds, other.Load())
	}
	for _, a := range sourceArchives(t, s, id) {
		if a.State == -6 {
			t.Fatal("login failure ran cleanup")
		}
	}
}

func TestSyncQueuedSettingsSnapshot(t *testing.T) {
	s := newTestServer(t)
	f, id := newSyncFixture(t, s, 1)
	if err := s.Store.SetSetting(t.Context(), "dataSourceSyncRate", 5); err != nil {
		t.Fatal(err)
	}
	s.sources.slots <- struct{}{}
	s.sources.slots <- struct{}{}
	held := 2
	defer func() {
		for held > 0 {
			<-s.sources.slots
			held--
		}
	}()
	var mu sync.Mutex
	var starts []time.Time
	f.setHook(func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		starts = append(starts, time.Now())
		return false
	})
	syncStart(t, s, id)
	if err := s.Store.SetSetting(t.Context(), "dataSourceSyncRate", 50); err != nil {
		t.Fatal(err)
	}
	<-s.sources.slots
	held--
	if ds := syncFinished(t, s, id); ds.Status != "ok" {
		t.Fatal(ds)
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 180*time.Millisecond {
			t.Fatal("queued run changed budget", starts)
		}
	}
}
