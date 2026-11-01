package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

type sourceFixture struct {
	server   *httptest.Server
	mu       sync.Mutex
	items    map[string][]emby.Item
	episodes map[string][]emby.Item
	views    []map[string]string
	gate     <-chan struct{}
	started  chan struct{}
	logins   atomic.Int32
	expire   atomic.Bool
	pages    atomic.Int32
	badImage atomic.Bool
	stream   http.Handler
}

func sourceMovie(id, name, container string) emby.Item {
	return emby.Item{ID: id, Name: name, Type: "Movie", Overview: "描述", Genres: []string{"Animation"}, PremiereDate: "2020-01-02T00:00:00Z", DateCreated: "2021-03-04T00:00:00Z", RunTimeTicks: 15500000, ImageTags: map[string]string{"Primary": "poster"}, BackdropImageTags: []string{"back"}, MediaSources: []emby.MediaSource{{ID: id + "src", Container: container, MediaStreams: []emby.MediaStream{{Type: "Video", Codec: "h264"}, {Type: "Audio", Codec: "aac"}}}}}
}
func sourceEpisode(id, name string, season, n int) emby.Item {
	x := sourceMovie(id, name, "mp4")
	x.Type = "Episode"
	x.ParentIndexNumber = &season
	x.IndexNumber = n
	return x
}
func newSourceFixture(t *testing.T) *sourceFixture {
	t.Helper()
	f := &sourceFixture{items: map[string][]emby.Item{}, episodes: map[string][]emby.Item{}, started: make(chan struct{}, 20)}
	f.views = []map[string]string{{"Id": "movies", "Name": "电影", "CollectionType": "movies"}, {"Id": "shows", "Name": "剧集", "CollectionType": "tvshows"}, {"Id": "box", "Name": "套装", "CollectionType": "boxsets"}, {"Id": "music", "Name": "音乐", "CollectionType": "music"}, {"Id": "mixed", "Name": "混合"}}
	f.items["movies"] = []emby.Item{sourceMovie("movie", "电影 A", "mp4"), sourceMovie("mkv", "电影 B", "mkv")}
	f.items["shows"] = []emby.Item{{ID: "multi", Name: "多季剧", Type: "Series"}, {ID: "single", Name: "单季剧", Type: "Series"}}
	f.episodes["multi"] = []emby.Item{sourceEpisode("e2", "第二季", 2, 1), sourceEpisode("e1", "1", 1, 1), sourceEpisode("special", "特别篇", 0, 1), sourceEpisode("virtual", "虚拟", 1, 2), sourceEpisode("empty", "无源", 1, 3)}
	f.episodes["multi"][3].LocationType = "Virtual"
	f.episodes["multi"][4].MediaSources = nil
	f.episodes["single"] = []emby.Item{sourceEpisode("single2", "第 2 集", 1, 2), sourceEpisode("single1", "开始", 1, 1)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.URL.Path != "/emby/Users/AuthenticateByName" && !strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
			t.Error("Emby write attempted", r.Method, r.URL.Path)
		}
		if r.URL.Path == "/emby/Users/AuthenticateByName" {
			var credentials map[string]string
			_ = json.NewDecoder(r.Body).Decode(&credentials)
			if credentials["Pw"] == "wrong" {
				w.WriteHeader(401)
				return
			}
			f.logins.Add(1)
			fmt.Fprint(w, `{"AccessToken":"sensitive-token","User":{"Id":"fixture"}}`)
			return
		}
		if r.Header.Get("X-Emby-Token") == "" {
			t.Error("missing token")
		}
		if f.expire.CompareAndSwap(true, false) {
			w.WriteHeader(401)
			return
		}
		if f.stream != nil && strings.Contains(r.URL.Path, "/stream.") {
			f.stream.ServeHTTP(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/emby/System/Info/Public":
			fmt.Fprint(w, `{"ServerName":"假 Emby","Id":"fixture-server","Version":"1.0"}`)
		case r.URL.Path == "/emby/Users/fixture/Views":
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": f.views})
		case r.URL.Path == "/emby/Users/fixture/Items":
			if r.URL.Query().Get("Limit") != "100" || r.URL.Query().Get("SortBy") != "SortName" {
				t.Error("pagination/sort")
			}
			f.pages.Add(1)
			if f.gate != nil {
				gate := f.gate
				f.mu.Unlock()
				select {
				case f.started <- struct{}{}:
				default:
				}
				select {
				case <-gate:
				case <-r.Context().Done():
				}
				f.mu.Lock()
			}
			items := f.items[r.URL.Query().Get("ParentId")]
			start := int(NumberGo(r.URL.Query().Get("StartIndex"), 0))
			end := min(start+100, len(items))
			if start > len(items) {
				start = len(items)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": items[start:end], "TotalRecordCount": len(items)})
		case strings.HasPrefix(r.URL.Path, "/emby/Shows/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": f.episodes[strings.Split(r.URL.Path, "/")[3]]})
		case strings.HasSuffix(r.URL.Path, "/PlaybackInfo"):
			if r.Method != "POST" {
				t.Error("playback method")
			}
			fmt.Fprint(w, `{"PlaySessionId":"session"}`)
		case strings.HasSuffix(r.URL.Path, "/stream.mp4"):
			w.Header().Set("Content-Type", "video/mp4")
			if r.Header.Get("Range") != "" {
				w.Header().Set("Content-Range", "bytes 0-2/6")
				w.WriteHeader(206)
			}
			fmt.Fprint(w, "mp4")
		case strings.HasSuffix(r.URL.Path, "/main.m3u8"):
			if r.URL.Query().Get("MaxWidth") != "1920" {
				t.Error("MaxWidth")
			}
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nhls1/main/0.ts?api_key=secret\n#EXTINF:6,\nhls1/main/1.ts\n#EXT-X-ENDLIST\n")
		case strings.Contains(r.URL.Path, "/hls1/main/"):
			if r.URL.Query().Get("MaxWidth") != "1920" {
				t.Error("segment MaxWidth")
			}
			w.Header().Set("Content-Type", "video/mp2t")
			fmt.Fprint(w, "segment")
		case strings.Contains(r.URL.Path, "/Images/"):
			if r.URL.Query().Get("maxWidth") != "1280" {
				t.Error("image width")
			}
			if f.badImage.Load() {
				w.Header().Set("Content-Type", "text/html")
			} else {
				w.Header().Set("Content-Type", "image/png")
			}
			fmt.Fprint(w, "png")
		default:
			t.Errorf("unexpected upstream %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}
func sourceData(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	assertCode(t, v, 0)
	checkImageURLs(t, v, "")
	return v["data"].(map[string]any)
}
func sourcePost(t *testing.T, s *Server, session *store.Session, path string, p url.Values) map[string]any {
	t.Helper()
	if p == nil {
		p = url.Values{}
	}
	p.Set("csrf", session.CSRF)
	v := post(t, s, path, p, session)
	checkImageURLs(t, v, "")
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "secret-password") || strings.Contains(string(b), "sensitive-token") || strings.Contains(string(b), `"password"`) {
		t.Fatal("credentials leaked", string(b))
	}
	return v
}
func sourceWait(t *testing.T, s *Server, id int64) *store.DataSource {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		ds, e := store.DataSourceByID(context.Background(), s.Store.DB, id)
		if e != nil {
			t.Fatal(e)
		}
		s.sources.mu.Lock()
		_, running := s.sources.running[id]
		s.sources.mu.Unlock()
		if !running && ds.Status != "syncing" {
			if ds.Status != "ok" {
				t.Fatal(ds.Message)
			}
			return ds
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("sync timeout")
	return nil
}
func sourceSave(t *testing.T, s *Server, session *store.Session, f *sourceFixture, admin bool, username string) int64 {
	t.Helper()
	prefix := "/x/web/"
	if admin {
		prefix = "/x/admin/"
	}
	d := sourceData(t, sourcePost(t, s, session, prefix+"datasource/save", url.Values{"url": {f.server.URL}, "username": {username}, "password": {"secret-password"}}))
	if d["status"] != "syncing" || d["has_password"] != true {
		t.Fatal(d)
	}
	id := int64(d["id"].(float64))
	sourceWait(t, s, id)
	return id
}
func sourceArchives(t *testing.T, s *Server, id int64) map[string]*store.Archive {
	t.Helper()
	as, e := s.Store.ListArchive(context.Background(), "WHERE origin_ds=?", id)
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]*store.Archive{}
	for _, a := range as {
		x, e := s.Store.ArchiveByID(context.Background(), a.AID)
		if e != nil {
			t.Fatal(e)
		}
		out[a.OriginItem] = x
	}
	return out
}
func sourceSync(t *testing.T, s *Server, session *store.Session, id int64) {
	t.Helper()
	sourceData(t, sourcePost(t, s, session, "/x/admin/datasource/sync", url.Values{"id": {fmt.Sprint(id)}}))
	sourceWait(t, s, id)
}
func sourceHTTP(s *Server, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestDataSourceSyncOrigins(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		admin := login(t, s, "jumedia", "admin123")
		user := login(t, s, "lumen", "demo123")
		ctx := context.Background()
		if e := s.Store.SetSetting(ctx, "dataSourcePrivateNetwork", true); e != nil {
			t.Fatal(e)
		}
		info := sourceData(t, sourcePost(t, s, user, "/x/web/datasource/test", url.Values{"url": {f.server.URL}, "username": {"fixture"}}))
		if len(info["libraries"].([]any)) != 3 {
			t.Fatal(info)
		}
		id := sourceSave(t, s, user, f, false, "fixture")
		a := sourceArchives(t, s, id)
		if len(a) != 4 {
			t.Fatal(a)
		}
		if a["movie"].State != -1 || a["movie"].MID != user.MID || a["movie"].TID != 1005 || a["movie"].Copyright != 2 || a["movie"].Source != "Emby · 假 Emby" || !strings.HasSuffix(a["movie"].Pages[0].Media, ":direct") || !strings.HasSuffix(a["mkv"].Pages[0].Media, ":raw-mkv") {
			t.Fatal(a["movie"])
		}
		if a["movie"].Ctime != sourceUnix("2021-03-04T00:00:00Z") || a["movie"].Pubdate != sourceUnix("2020-01-02T00:00:00Z") {
			t.Fatal("dates")
		}
		if len(a["multi"].Pages) != 2 || a["multi"].Pages[0].Part != "S01E01" || a["multi"].Pages[1].Part != "S02E01 第二季" || a["single"].Pages[0].Part != "第1集 开始" || a["single"].Pages[1].Part != "第2集" {
			t.Fatal(a["multi"].Pages, a["single"].Pages)
		}
		movie := a["movie"]
		cid := a["multi"].Pages[0].CID
		edit := url.Values{"aid": {fmt.Sprint(movie.AID)}, "title": {"自定义"}, "tid": {"1001"}, "desc": {"自定义描述"}, "cover": {"/mock/custom.jpg"}, "tag": {"锁定"}, "videos": {"invalid"}, "chapters": {"invalid"}, "source": {"ignored"}}
		sourceData(t, sourcePost(t, s, user, "/x/vu/web/edit", edit))
		f.mu.Lock()
		f.items["movies"][0].Name = "源改名"
		f.episodes["multi"][0].Name = "新名称"
		f.episodes["multi"][1].MediaSources[0].ID = "changed-source"
		f.mu.Unlock()
		sourceSync(t, s, admin, id)
		a = sourceArchives(t, s, id)
		if a["movie"].Title != "自定义" || a["movie"].Origin.Meta.Title != "源改名" || len(a["movie"].Origin.Locked) != 5 || a["multi"].Pages[0].CID != cid {
			t.Fatal(a["movie"], a["multi"].Pages)
		}
		row := sourceData(t, get(t, s, fmt.Sprintf("/x/web/archive/view?aid=%d", movie.AID), user))
		origin := row["origin"].(map[string]any)
		if origin["datasource_id"] != float64(id) || origin["library"] != "电影" || !strings.HasPrefix(origin["meta"].(map[string]any)["pic"].(string), "http://") {
			t.Fatal(row)
		}
		sourceData(t, sourcePost(t, s, user, "/x/web/archive/origin/reset", url.Values{"aid": {fmt.Sprint(movie.AID)}, "fields": {"title,invalid"}}))
		a = sourceArchives(t, s, id)
		if a["movie"].Title != "源改名" || len(a["movie"].Origin.Locked) != 4 {
			t.Fatal(a["movie"])
		}
		sourceData(t, sourcePost(t, s, user, "/x/web/archive/origin/reset", url.Values{"aid": {fmt.Sprint(movie.AID)}}))
		if len(sourceArchives(t, s, id)["movie"].Origin.Locked) != 0 {
			t.Fatal("reset")
		}
		ds, _ := store.DataSourceByID(ctx, s.Store.DB, id)
		collection := ds.Libraries[0].CollectionID
		assertCode(t, sourcePost(t, s, user, "/x/web/season/save", url.Values{"season_id": {fmt.Sprint(collection)}, "title": {"库重命名"}, "intro": {"简介"}, "aids": {"100100"}}), 0)
		sourceSync(t, s, admin, id)
		col, e := s.Store.CollectionByID(ctx, collection)
		if e != nil || col.Title != "库重命名" || len(col.AIDs) != 2 {
			t.Fatal(col, e)
		}
		seasons := sourceData(t, get(t, s, "/x/web/seasons", user))["seasons"].([]any)
		if len(seasons) < 3 {
			t.Fatal(seasons)
		}
		assertCode(t, sourcePost(t, s, user, "/x/web/season/delete", url.Values{"season_id": {fmt.Sprint(collection)}}), -400)
		assertCode(t, sourcePost(t, s, user, "/x/web/season/origin/reset", url.Values{"season_id": {fmt.Sprint(collection)}}), 0)
		col, _ = s.Store.CollectionByID(ctx, collection)
		if col.Title != "电影" || len(col.Origin.Locked) != 0 {
			t.Fatal(col)
		}
		// 普通合集不能抢走媒体库稿件，系列可引用。
		for _, kind := range []string{"season", "series"} {
			assertCode(t, sourcePost(t, s, user, "/x/web/season/save", url.Values{"kind": {kind}, "title": {"普通" + kind}, "aids": {fmt.Sprint(movie.AID)}}), 0)
		}
		cols, _ := s.Store.ListCollection(ctx, "WHERE mid=?", user.MID)
		for _, x := range cols {
			if strings.HasPrefix(x.Title, "普通") {
				c, _ := s.Store.CollectionByID(ctx, x.ID)
				if (x.Kind == "season" && len(c.AIDs) != 0) || (x.Kind == "series" && len(c.AIDs) != 1) {
					t.Fatal(c)
				}
			}
		}
		f.mu.Lock()
		saved := f.items["movies"]
		f.items["movies"] = nil
		f.mu.Unlock()
		sourceSync(t, s, admin, id)
		if sourceArchives(t, s, id)["movie"].State != -6 {
			t.Fatal("not missing")
		}
		listing := sourceData(t, get(t, s, "/x/web/archives?origin=datasource&status=missing", user))
		if listing["class"].(map[string]any)["missing"] != float64(2) || len(listing["arc_audits"].([]any)) != 2 {
			t.Fatal(listing)
		}
		f.mu.Lock()
		f.items["movies"] = saved
		f.mu.Unlock()
		sourceSync(t, s, admin, id)
		if sourceArchives(t, s, id)["movie"].State != 0 {
			t.Fatal("not restored")
		}
		assertCode(t, sourcePost(t, s, user, "/x/web/archive/delete", url.Values{"aid": {fmt.Sprint(movie.AID)}}), 0)
		sourceSync(t, s, admin, id)
		if sourceArchives(t, s, id)["movie"].State != -100 {
			t.Fatal("deleted reimported")
		}
		assertCode(t, get(t, s, fmt.Sprintf("/x/web/archive/view?aid=%d", movie.AID), user), -404)
		// Empty password retains the encrypted credential; deselection removes collections.
		sourceData(t, sourcePost(t, s, user, "/x/web/datasource/save", url.Values{"id": {fmt.Sprint(id)}, "url": {f.server.URL}, "username": {"fixture"}, "libraries": {"shows"}}))
		sourceWait(t, s, id)
		if sourceArchives(t, s, id)["mkv"].State != -6 {
			t.Fatal("disabled not hidden")
		}
		var n int
		_ = s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collections WHERE id=?", collection).Scan(&n)
		if n != 0 {
			t.Fatal("disabled collection remains")
		}
		ds, _ = store.DataSourceByID(ctx, s.Store.DB, id)
		if ds.PasswordEnc == "" || strings.Contains(ds.PasswordEnc, "secret-password") {
			t.Fatal("unencrypted")
		}
		pw, e := s.decryptPassword(ds.PasswordEnc)
		if e != nil || pw != "secret-password" {
			t.Fatal("password retention", e)
		}
		if st, e := os.Stat(filepath.Join(s.Config.DataDir, "datasource.key")); e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("key permissions", e)
		}
	})
}

func TestDataSourceAccessLimitsAndLifecycle(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		admin := login(t, s, "jumedia", "admin123")
		user := login(t, s, "lumen", "demo123")
		ctx := context.Background()
		for _, prefix := range []string{"/x/web/", "/x/admin/"} {
			assertCode(t, get(t, s, prefix+"datasources", nil), -101)
			for _, action := range []string{"test", "save", "sync", "delete"} {
				assertCode(t, post(t, s, prefix+"datasource/"+action, nil, nil), -101)
				assertCode(t, post(t, s, prefix+"datasource/"+action, nil, admin), -111)
			}
		}
		for _, p := range []url.Values{{"username": {"fixture"}}, {"url": {f.server.URL}}, {"url": {"not a URL"}, "username": {"fixture"}}, {"url": {"ftp://host"}, "username": {"fixture"}}} {
			assertCode(t, sourcePost(t, s, user, "/x/web/datasource/test", p), -400)
		}
		for _, address := range []string{f.server.URL, strings.Replace(f.server.URL, "127.0.0.1", "localhost", 1)} {
			v := sourcePost(t, s, user, "/x/web/datasource/test", url.Values{"url": {address}, "username": {"fixture"}})
			liveFailure(t, v, -400, "站点不允许使用内网地址的 Emby 服务器")
		}
		assertCode(t, sourcePost(t, s, user, "/x/admin/datasource/test", url.Values{"url": {f.server.URL}, "username": {"fixture"}}), -403)
		sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/test", url.Values{"url": {f.server.URL}, "username": {"fixture"}}))
		liveFailure(t, sourcePost(t, s, admin, "/x/admin/datasource/test", url.Values{"url": {f.server.URL}, "username": {"fixture"}, "password": {"wrong"}}), -400, "Emby 用户名或密码错误")
		assertCode(t, sourcePost(t, s, admin, "/x/admin/settings/save", url.Values{"officialMid": {"999999999"}}), -400)
		assertCode(t, sourcePost(t, s, admin, "/x/admin/settings/save", url.Values{"officialMid": {fmt.Sprint(user.MID)}}), 0)
		system := sourceSave(t, s, admin, f, true, "fixture")
		for _, a := range sourceArchives(t, s, system) {
			if a.MID != user.MID || a.State != 0 {
				t.Fatal("system audit/owner", a)
			}
		}
		assertCode(t, sourcePost(t, s, user, "/x/web/datasource/sync", url.Values{"id": {fmt.Sprint(system)}}), -404)
		list := sourceData(t, get(t, s, "/x/admin/datasources?scope=system&keyword=假&pn=1&ps=1", admin))
		if list["page"].(map[string]any)["total"] != float64(1) || list["official"].(map[string]any)["mid"] != float64(user.MID) {
			t.Fatal(list)
		}
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/save", url.Values{"url": {f.server.URL}, "username": {"fixture"}}), -400)
		assertCode(t, sourcePost(t, s, admin, "/x/admin/settings/save", url.Values{"dataSourcePrivateNetwork": {"true"}}), 0)
		ids := []int64{}
		for i := 0; i < 5; i++ {
			ids = append(ids, sourceSave(t, s, user, f, false, fmt.Sprintf("account%d", i)))
		}
		assertCode(t, sourcePost(t, s, user, "/x/web/datasource/save", url.Values{"url": {f.server.URL}, "username": {"six"}}), -400)
		assertCode(t, sourcePost(t, s, user, "/x/web/datasource/save", url.Values{"id": {fmt.Sprint(ids[1])}, "url": {f.server.URL}, "username": {"account0"}}), -400)
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/save", url.Values{"id": {fmt.Sprint(ids[0])}, "url": {f.server.URL}, "username": {"account0"}}), -404)
		d := sourceData(t, get(t, s, "/x/web/datasources", user))
		if len(d["list"].([]any)) != 5 || d["max"] != float64(5) || d["sync_hours"] != float64(6) {
			t.Fatal(d)
		}
		// A blocked network fetch proves immediate responses and same-source exclusion.
		gate := make(chan struct{})
		f.mu.Lock()
		f.gate = gate
		f.mu.Unlock()
		d = sourceData(t, sourcePost(t, s, user, "/x/web/datasource/sync", url.Values{"id": {fmt.Sprint(ids[0])}}))
		if d["status"] != "syncing" {
			t.Fatal(d)
		}
		select {
		case <-f.started:
		case <-time.After(2 * time.Second):
			t.Fatal("fetch not started")
		}
		liveFailure(t, sourcePost(t, s, user, "/x/web/datasource/sync", url.Values{"id": {fmt.Sprint(ids[0])}}), -400, "正在同步中，请稍后")
		liveFailure(t, sourcePost(t, s, user, "/x/web/datasource/save", url.Values{"id": {fmt.Sprint(ids[0])}, "url": {f.server.URL}, "username": {"account0"}}), -400, "正在同步中，请稍后再修改")
		close(gate)
		f.mu.Lock()
		f.gate = nil
		f.mu.Unlock()
		sourceWait(t, s, ids[0])
		// Clear password, then ordinary administrators bypass audit for personal sources.
		d = sourceData(t, sourcePost(t, s, user, "/x/web/datasource/save", url.Values{"id": {fmt.Sprint(ids[0])}, "url": {f.server.URL}, "username": {"account0"}, "clear_password": {"true"}}))
		if d["has_password"] != false {
			t.Fatal(d)
		}
		sourceWait(t, s, ids[0])
		personalAdmin := sourceSave(t, s, admin, f, false, "administrator")
		for _, a := range sourceArchives(t, s, personalAdmin) {
			if a.State != 0 {
				t.Fatal("admin audit")
			}
		}
		assertCode(t, sourcePost(t, s, admin, "/x/admin/settings/save", url.Values{"userDataSources": {"false"}}), 0)
		for _, action := range []string{"test", "save", "sync"} {
			liveFailure(t, sourcePost(t, s, user, "/x/web/datasource/"+action, url.Values{"id": {fmt.Sprint(ids[0])}}), -403, "站点未开放个人数据源")
		}
		assertCode(t, sourcePost(t, s, user, "/x/web/datasource/delete", url.Values{"id": {fmt.Sprint(ids[0])}}), 0)
		assertCode(t, sourcePost(t, s, user, "/x/web/datasource/delete", url.Values{"id": {fmt.Sprint(ids[0])}}), -404)
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/delete", url.Values{"id": {fmt.Sprint(system)}}), 0)
		for _, action := range []string{"test", "save", "sync", "delete"} {
			assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/"+action, url.Values{"id": {"999999999"}}), -404)
		}
		// Scheduler respects userDataSources=false, and synchronizes due system sources.
		ds, e := store.DataSourceByID(ctx, s.Store.DB, ids[1])
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Store.DB.ExecContext(ctx, "UPDATE data_sources SET last_sync=0 WHERE id=?", ids[1]); e != nil {
			t.Fatal(e)
		}
		s.autoSyncSources(ctx)
		ds, _ = store.DataSourceByID(ctx, s.Store.DB, ids[1])
		if ds.Status == "syncing" || ds.LastSync != 0 {
			t.Fatal("disabled auto sync")
		}
		// Startup interruption recovery runs exactly once per server.
		s.CloseDataSources()
		if _, e = s.Store.DB.ExecContext(ctx, "UPDATE data_sources SET status='syncing' WHERE id=?", ids[1]); e != nil {
			t.Fatal(e)
		}
		restart := New(s.Store, s.Config)
		t.Cleanup(restart.CloseDataSources)
		if e = restart.InitDataSources(ctx); e != nil {
			t.Fatal(e)
		}
		ds, _ = store.DataSourceByID(ctx, s.Store.DB, ids[1])
		if ds.Status != "error" || ds.Message != "同步被中断，请重新同步" {
			t.Fatal(ds)
		}
	})
}

func TestDataSourceMediaAndVideoRelay(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		admin := login(t, s, "jumedia", "admin123")
		id := sourceSave(t, s, admin, f, true, "fixture")
		archives := sourceArchives(t, s, id)
		a := archives["movie"]
		p := a.Pages[0]
		play := sourceData(t, get(t, s, fmt.Sprintf("/x/player/wbi/playurl?aid=%d&cid=%d", a.AID, p.CID), nil))
		encoded, _ := json.Marshal(play)
		if !strings.Contains(string(encoded), fmt.Sprintf("ds=%d", id)) || !strings.Contains(string(encoded), ".mp4?") {
			t.Fatal(play)
		}
		aHLS := archives["mkv"]
		play = sourceData(t, get(t, s, fmt.Sprintf("/x/player/wbi/playurl?aid=%d&cid=%d", aHLS.AID, aHLS.Pages[0].CID), nil))
		encoded, _ = json.Marshal(play)
		if play["format"] != "mkv" || !strings.Contains(string(encoded), "/file.mkv?") {
			t.Fatal(play)
		}
		path := fmt.Sprintf("/media/emby/movie.mp4?source=moviesrc&ds=%d", id)
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Range", "bytes=0-2")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 206 || w.Body.String() != "mp4" {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, kind := range []string{"index.m3u8", "main.m3u8", "hls1/main/0.ts"} {
			w = sourceHTTP(s, fmt.Sprintf("/media/emby/movie/hls/%s?source=moviesrc&ds=%d", kind, id))
			if w.Code != 404 {
				t.Fatal(kind, w.Code, w.Body.String())
			}
		}

		f.expire.Store(true)
		before := f.logins.Load()
		w = sourceHTTP(s, path)
		if w.Code != 200 || f.logins.Load() != before+1 {
			t.Fatal("401 renewal", w.Code, f.logins.Load(), before)
		}
		for _, item := range []string{"movie", "e1", "movies"} {
			w = sourceHTTP(s, sourceImage(id, item, "Primary", "tag"))
			if w.Code != 200 || w.Header().Get("Cache-Control") != "public, max-age=86400" {
				t.Fatal(item, w.Code, w.Body.String())
			}
		}
		f.badImage.Store(true)
		if sourceHTTP(s, sourceImage(id, "movie", "Primary", "invalid-content-tag")).Code != 502 {
			t.Fatal("image content type")
		}
		f.badImage.Store(false)
		for _, bad := range []string{fmt.Sprintf("/media/emby/unknown.mp4?source=x&ds=%d", id), fmt.Sprintf("/media/emby/movie.mp4?source=wrong&ds=%d", id), "/media/emby/movie.mp4?source=moviesrc&ds=999999999", sourceImage(id, "unknown", "Primary", ""), sourceImage(id, "movie", "Logo", "")} {
			if w = sourceHTTP(s, bad); w.Code != 404 {
				t.Fatal("whitelist", bad, w.Code)
			}
		}
		// All newly added list/reset routes have authentication and validation failures.
		for _, route := range []string{"/x/web/archive/origin/reset", "/x/web/season/origin/reset"} {
			assertCode(t, post(t, s, route, nil, nil), -101)
			assertCode(t, sourcePost(t, s, admin, route, url.Values{"aid": {"999999999"}, "season_id": {"999999999"}}), -404)
		}
		assertCode(t, sourcePost(t, s, admin, "/x/web/archive/origin/reset", url.Values{"aid": {"100101"}}), -404)
		assertCode(t, get(t, s, "/xlive/app-blink/v1/streamSource/videos", nil), -101)
		assertCode(t, get(t, s, "/x/admin/live/room/videos?room_id=20000", nil), -101)
		assertCode(t, get(t, s, "/x/admin/live/room/videos?room_id=999999999", admin), 19002003)
		getInfo := sourceData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", admin))
		room := int64(getInfo["room_id"].(float64))
		videos := sourceData(t, get(t, s, "/xlive/app-blink/v1/streamSource/videos?keyword=电影", admin))
		if len(videos["list"].([]any)) != 2 {
			t.Fatal(videos)
		}
		videos = sourceData(t, get(t, s, fmt.Sprintf("/x/admin/live/room/videos?room_id=%d&keyword=电影", room), admin))
		if videos["page"].(map[string]any)["total"] != float64(2) {
			t.Fatal(videos)
		}
		values := url.Values{"source_type": {"video"}, "aid": {fmt.Sprint(a.AID)}, "cid": {fmt.Sprint(p.CID)}, "loop": {"true"}}
		assertCode(t, sourcePost(t, s, admin, "/room/v1/Room/stopLive", nil), 0)
		src := sourceData(t, sourcePost(t, s, admin, "/xlive/app-blink/v1/streamSource/update", values))["source"].(map[string]any)
		if src["video"].(map[string]any)["aid"] != float64(a.AID) || src["format"] != "ts" || src["kind"] != "vod" {
			t.Fatal(src)
		}
		for _, kind := range []string{"index.m3u8", "main.m3u8", "hls1/main/0.ts"} {
			w = sourceHTTP(s, fmt.Sprintf("/media/emby/movie/hls/%s?source=moviesrc&ds=%d", kind, id))
			if w.Code != 404 {
				t.Fatal("removed datasource HLS", kind, w.Code)
			}
		}
		assertCode(t, sourcePost(t, s, admin, "/room/v1/Room/startLive", nil), -400)
		w = sourceHTTP(s, fmt.Sprintf("/live-relay/%d/index.m3u8", room))
		if w.Code != 404 {
			t.Fatal("pending cache relayed", w.Code)
		}

		info := sourceData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", admin))
		if info["source"].(map[string]any)["video"] == nil {
			t.Fatal(info)
		}
		values.Set("room_id", fmt.Sprint(room))
		values.Set("start", "true")
		sourceData(t, sourcePost(t, s, admin, "/x/admin/live/room/source", values))
		rooms := sourceData(t, get(t, s, "/x/admin/live/rooms", admin))
		found := false
		for _, v := range rooms["list"].([]any) {
			r := v.(map[string]any)
			if r["room_id"] == float64(room) {
				found = r["source"].(map[string]any)["video"] != nil
			}
		}
		if !found {
			t.Fatal("admin source JSON")
		}
		for _, route := range []string{"/xlive/app-blink/v1/streamSource/update", "/x/admin/live/room/source"} {
			assertCode(t, sourcePost(t, s, admin, route, url.Values{"room_id": {fmt.Sprint(room)}, "source_type": {"video"}}), -400)
			assertCode(t, sourcePost(t, s, admin, route, url.Values{"room_id": {fmt.Sprint(room)}, "source_type": {"video"}, "aid": {"100100"}, "cid": {"50001"}}), -404)
		}
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/delete", url.Values{"id": {fmt.Sprint(id)}}), 0)
		rroom, e := s.Store.RoomByID(context.Background(), room)
		if e != nil || rroom.Source.Type != "push" || rroom.LiveStatus != 0 {
			t.Fatal(rroom, e)
		}
		for _, a := range sourceArchives(t, s, id) {
			if a.State != -100 {
				t.Fatal("purge", a)
			}
		}
		if sourceHTTP(s, path).Code != 404 {
			t.Fatal("deleted proxy")
		}
	})
}

func TestDataSourcePaginationAndRegions(t *testing.T) {
	s := newTestServer(t)
	f := newSourceFixture(t)
	admin := login(t, s, "jumedia", "admin123")
	f.mu.Lock()
	f.items["movies"] = nil
	for i := 0; i < 401; i++ {
		f.items["movies"] = append(f.items["movies"], sourceMovie(fmt.Sprintf("m%d", i), fmt.Sprintf("电影%d", i), "mp4"))
	}
	f.items["shows"] = nil
	f.mu.Unlock()
	id := sourceSave(t, s, admin, f, true, "large")
	if len(sourceArchives(t, s, id)) != 401 || f.pages.Load() < 5 {
		t.Fatal("server cap/pagination", f.pages.Load())
	}
	regions := []store.Region{{TID: 1001}, {TID: 1003}, {TID: 1005}, {TID: 1002}, {TID: 1010}, {TID: 1018}, {TID: 1020}, {TID: 1030}}
	for _, tt := range []struct {
		lib, kind, genre string
		tid              int64
	}{{"", "", "Food Music", 1020}, {"", "musicvideos", "", 1003}, {"", "", "Documentary", 1010}, {"", "", "Sport", 1018}, {"", "", "Travel", 1030}, {"动漫", "", "", 1005}, {"综艺", "", "", 1002}, {"", "", "Mystery", 1001}} {
		if got := autoRegion(regions, store.DataSourceLibrary{Name: tt.lib, Type: tt.kind}, []string{tt.genre}); got != tt.tid {
			t.Fatal(tt, got)
		}
	}
	if autoRegion([]store.Region{{TID: 7}}, store.DataSourceLibrary{}, nil) != 7 || autoRegion(nil, store.DataSourceLibrary{}, nil) != 0 {
		t.Fatal("region fallback")
	}
}

func TestDataSourceRealSmoke(t *testing.T) {
	if os.Getenv("JUMEDIA_TEST_EMBY") != "1" {
		t.Skip("set JUMEDIA_TEST_EMBY=1 for real Emby smoke")
	}
	s := newTestServer(t)
	admin := login(t, s, "jumedia", "admin123")
	address, username := os.Getenv("JUMEDIA_TEST_EMBY_URL"), os.Getenv("JUMEDIA_TEST_EMBY_USERNAME")
	password, ok := os.LookupEnv("JUMEDIA_TEST_EMBY_PASSWORD")
	if address == "" || username == "" || !ok {
		t.Skip("configure JUMEDIA_TEST_EMBY_URL/USERNAME/PASSWORD")
	}
	p := url.Values{"url": {address}, "username": {username}, "password": {password}}
	info := sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/test", p))
	libs := info["libraries"].([]any)
	if len(libs) == 0 {
		t.Fatal("no video libraries")
	}
	p.Set("libraries", libs[0].(map[string]any)["id"].(string))
	d := sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p))
	id := int64(d["id"].(float64))
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		ds, e := store.DataSourceByID(context.Background(), s.Store.DB, id)
		if e != nil {
			t.Fatal(e)
		}
		if ds.Status == "error" {
			t.Fatal(ds.Message)
		}
		if ds.Status == "ok" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	archives := sourceArchives(t, s, id)
	if len(archives) == 0 {
		t.Fatal("no imports")
	}
	for _, a := range archives {
		if len(a.Pages) > 0 {
			// Data-source originals remain playable without Emby transcoding.
			sourceData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", admin))
			ds, item, src, mode, _ := emby.ParseDSRef(a.Pages[0].Media)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			req := httptest.NewRequestWithContext(ctx, "GET", emby.DSPath(ds, item, src, mode), nil)
			req.Header.Set("Range", "bytes=0-1023")
			w := httptest.NewRecorder()
			w.Body = nil // Do not retain a whole movie if an upstream ignores Range.
			s.ServeHTTP(w, req)
			if w.Code != 206 {
				t.Fatal(w.Code, w.Header())
			}
			return
		}
	}
	t.Fatal("no pages")
}

func TestDataSourceConcurrencyShutdownAndScheduler(t *testing.T) {
	s := newTestServer(t)
	f := newSourceFixture(t)
	admin := login(t, s, "jumedia", "admin123")
	ids := []int64{}
	for i := 0; i < 3; i++ {
		ids = append(ids, sourceSave(t, s, admin, f, true, fmt.Sprintf("worker%d", i)))
	}
	gate := make(chan struct{})
	f.mu.Lock()
	f.gate = gate
	f.mu.Unlock()
	ctx := context.Background()
	if _, e := s.Store.DB.ExecContext(ctx, "UPDATE data_sources SET last_sync=0"); e != nil {
		t.Fatal(e)
	}
	s.autoSyncSources(ctx)
	for i := 0; i < 2; i++ {
		select {
		case <-f.started:
		case <-time.After(2 * time.Second):
			t.Fatal("scheduler did not start workers")
		}
	}
	select {
	case <-f.started:
		t.Fatal("more than two concurrent syncs")
	case <-time.After(50 * time.Millisecond):
	}
	for _, id := range ids {
		ds, e := store.DataSourceByID(ctx, s.Store.DB, id)
		if e != nil || ds.Status != "syncing" {
			t.Fatal(ds, e)
		}
	}
	// A network fetch is blocked; unrelated SQL can still commit.
	if e := s.Store.SetSetting(ctx, "dataSourceSyncHours", 0); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { s.CloseDataSources(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel fetches")
	}
	close(gate)
	for _, id := range ids {
		ds, e := store.DataSourceByID(ctx, s.Store.DB, id)
		if e != nil || ds.Status != "error" || ds.Message != "同步被中断，请重新同步" || ds.LastSync == 0 {
			t.Fatal(ds, e)
		}
	}
}

func TestDataSourceImageRoundTripAndMissingLibraries(t *testing.T) {
	s := newTestServer(t)
	f := newSourceFixture(t)
	admin := login(t, s, "jumedia", "admin123")
	id := sourceSave(t, s, admin, f, true, "fixture")
	a := sourceArchives(t, s, id)["movie"]
	row := sourceData(t, get(t, s, fmt.Sprintf("/x/web/archive/view?aid=%d", a.AID), admin))
	arc := row["Archive"].(map[string]any)
	sourceData(t, sourcePost(t, s, admin, "/x/vu/web/edit", url.Values{"aid": {fmt.Sprint(a.AID)}, "title": {a.Title}, "desc": {a.Desc}, "tid": {fmt.Sprint(a.TID)}, "tag": {a.Tag}, "cover": {arc["cover"].(string)}}))
	if got := sourceArchives(t, s, id)["movie"]; len(got.Origin.Locked) != 0 {
		t.Fatal("unchanged absolute image locked", got.Origin)
	}
	ds, _ := store.DataSourceByID(context.Background(), s.Store.DB, id)
	collection := ds.Libraries[0].CollectionID
	c, e := s.Store.CollectionByID(context.Background(), collection)
	if e != nil {
		t.Fatal(e)
	}
	assertCode(t, sourcePost(t, s, admin, "/x/web/season/save", url.Values{"season_id": {fmt.Sprint(collection)}, "title": {c.Title}, "intro": {c.Intro}, "cover": {"http://example.com" + c.Cover}}), 0)
	c, _ = s.Store.CollectionByID(context.Background(), collection)
	if len(c.Origin.Locked) != 0 {
		t.Fatal("unchanged collection image locked", c.Origin)
	}
	f.mu.Lock()
	f.views = f.views[1:]
	f.mu.Unlock()
	sourceSync(t, s, admin, id)
	a = sourceArchives(t, s, id)["movie"]
	if a.State != -6 {
		t.Fatal("disappeared library not hidden")
	}
	var count int
	_ = s.Store.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM collections WHERE id=?", collection).Scan(&count)
	if count != 0 {
		t.Fatal("disappeared library collection remains")
	}
	// Upload origin reset reports the contract error, as does a normal collection.
	upload := creatorData(t, sourcePost(t, s, admin, "/x/vu/web/add/v3", url.Values{"title": {"本地视频"}, "tid": {"1001"}, "videos": {`[{"filename":"/mock/media/sample.mp4","title":"视频","duration":1}]`}}))
	aid := fmt.Sprint(upload["aid"])
	liveFailure(t, sourcePost(t, s, admin, "/x/web/archive/origin/reset", url.Values{"aid": {aid}}), -400, "该稿件不是数据源中的视频")
	assertCode(t, sourcePost(t, s, admin, "/x/web/season/save", url.Values{"title": {"本地合集"}, "kind": {"season"}}), 0)
	var cid int64
	_ = s.Store.DB.QueryRowContext(context.Background(), "SELECT id FROM collections WHERE title=?", "本地合集").Scan(&cid)
	liveFailure(t, sourcePost(t, s, admin, "/x/web/season/origin/reset", url.Values{"season_id": {fmt.Sprint(cid)}}), -400, "该合集不是数据源媒体库")
	get(t, s, "/xlive/app-blink/v1/room/GetInfo", admin)
	up, _ := s.Store.ArchiveByID(context.Background(), int64(upload["aid"].(float64)))
	liveFailure(t, sourcePost(t, s, admin, "/xlive/app-blink/v1/streamSource/update", url.Values{"source_type": {"video"}, "aid": {aid}, "cid": {fmt.Sprint(up.Pages[0].CID)}}), -400, "只能转播数据源中的视频")
	liveFailure(t, sourcePost(t, s, admin, "/xlive/app-blink/v1/streamSource/update", url.Values{"source_type": {"video"}, "aid": {fmt.Sprint(a.AID)}, "cid": {fmt.Sprint(a.Pages[0].CID)}}), -404, "视频不存在或不属于该直播间的主播")
}

func TestDataSourceKeyAndEncryption(t *testing.T) {
	t.Setenv("JUMEDIA_DATASOURCE_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)))
	s := newTestServer(t)
	if e := s.InitDataSources(context.Background()); e != nil {
		t.Fatal(e)
	}
	one, e := s.encryptPassword("secret-password")
	if e != nil {
		t.Fatal(e)
	}
	two, e := s.encryptPassword("secret-password")
	if e != nil || one == two {
		t.Fatal("nonce reuse", e)
	}
	if p, e := s.decryptPassword(one); e != nil || p != "secret-password" {
		t.Fatal("round trip", e)
	}
	raw, _ := base64.StdEncoding.DecodeString(one)
	raw[len(raw)-1] ^= 1
	if _, e = s.decryptPassword(base64.StdEncoding.EncodeToString(raw)); e == nil {
		t.Fatal("GCM authentication missing")
	}
	if _, e = os.Stat(filepath.Join(s.Config.DataDir, "datasource.key")); !os.IsNotExist(e) {
		t.Fatal("environment key written to file")
	}
	t.Setenv("JUMEDIA_DATASOURCE_KEY", "wrong")
	if _, e = sourceKey(t.TempDir()); e == nil {
		t.Fatal("invalid key accepted")
	}
	t.Setenv("JUMEDIA_DATASOURCE_KEY", "")
	dir := t.TempDir()
	key, e := sourceKey(dir)
	if e != nil {
		t.Fatal(e)
	}
	again, e := sourceKey(dir)
	if e != nil || !bytes.Equal(key, again) {
		t.Fatal("key changed", e)
	}
}

func TestDataSourceLibraryTransactionRollback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		admin := login(t, s, "jumedia", "admin123")
		ctx := context.Background()
		// Force a failure halfway through the first library; no partial archives or
		// collection may become visible from that library's transaction.
		f.mu.Lock()
		f.items["movies"][1].Name = f.items["movies"][0].Name
		f.mu.Unlock()
		if _, e := s.Store.DB.ExecContext(ctx, "CREATE UNIQUE INDEX datasource_test_titles ON archives(title) WHERE origin_ds<>0"); e != nil {
			t.Fatal(e)
		}
		d := sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", url.Values{"url": {f.server.URL}, "username": {"fixture"}}))
		id := int64(d["id"].(float64))
		deadline := time.Now().Add(5 * time.Second)
		for {
			ds, e := store.DataSourceByID(ctx, s.Store.DB, id)
			if e != nil {
				t.Fatal(e)
			}
			if ds.Status == "error" {
				if ds.LastSync == 0 {
					t.Fatal("error last_sync missing")
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("expected failure", ds)
			}
			time.Sleep(5 * time.Millisecond)
		}
		if as := sourceArchives(t, s, id); len(as) != 0 {
			t.Fatal("partial library committed", as)
		}
		var n int
		if e := s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM collections WHERE origin_ds=?", id).Scan(&n); e != nil || n != 0 {
			t.Fatal("partial collection", n, e)
		}
	})
}

func TestDataSourceDeletedPageWhitelistAndStaleLiveWrite(t *testing.T) {
	s := newTestServer(t)
	f := newSourceFixture(t)
	admin := login(t, s, "jumedia", "admin123")
	id := sourceSave(t, s, admin, f, true, "fixture")
	a := sourceArchives(t, s, id)["movie"]
	get(t, s, "/xlive/app-blink/v1/room/GetInfo", admin)
	src := &store.StreamSource{Type: "video", URL: fmt.Sprintf("/media/emby/movie/hls/index.m3u8?source=moviesrc&ds=%d", id), Loop: true, Format: "ts", Kind: "vod", AID: a.AID, CID: a.Pages[0].CID}
	rooms, e := s.Store.ListLiveRoom(context.Background(), "WHERE uid=?", admin.MID)
	if e != nil || len(rooms) != 1 {
		t.Fatal(rooms, e)
	}
	room := rooms[0]
	room.Source = src
	assertCode(t, sourcePost(t, s, admin, "/x/web/archive/delete", url.Values{"aid": {fmt.Sprint(a.AID)}}), 0)
	path := emby.DSPath(id, "movie", "moviesrc", "direct")
	if w := sourceHTTP(s, path); w.Code != 404 {
		t.Fatal("deleted archive media allowed", w.Code)
	}
	c := &Ctx{Server: s, R: httptest.NewRequest("POST", "/", nil)}
	if err := liveSaveSource(c, s.Store.DB, &room); err == nil {
		t.Fatal("stale live write allowed")
	}
	// A stale live source authorizes neither the removed HLS route nor deleted original file.
	b, _ := json.Marshal(src)
	if _, e = s.Store.DB.ExecContext(context.Background(), "UPDATE rooms SET source=? WHERE room_id=?", string(b), room.RoomID); e != nil {
		t.Fatal(e)
	}
	if w := sourceHTTP(s, path); w.Code != 404 {
		t.Fatal("live reference authorized deleted original file", w.Code)
	}
	if w := sourceHTTP(s, src.URL); w.Code != 404 {
		t.Fatal("removed HLS reference allowed", w.Code)
	}
}
