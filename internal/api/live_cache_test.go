package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

const cacheUpdate = "/xlive/app-blink/v1/streamSource/update"

func cacheSetup(t *testing.T, s *Server) (*store.Session, store.Page, int64) {
	t.Helper()
	// These tests schedule their own jobs. Keep the two fresh demo sources
	// from adding unrelated worker jobs; live_demo_test covers demo startup.
	liveExec(t, s, "UPDATE rooms SET source=?,live_status=0,pending_start=0 WHERE room_id IN (?,?)", `{"type":"push","url":"","loop":true}`, 21000, 21018)
	u := login(t, s, "jumedia", "admin123")
	info := sourceData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", u))
	rid := int64(info["room_id"].(float64))
	pages, e := s.Store.ListPage(context.Background(), "ORDER BY cid LIMIT 3")
	if e != nil || len(pages) < 2 {
		t.Fatal(pages, e)
	}
	for _, p := range pages {
		liveExec(t, s, "UPDATE archives SET mid=?,state=0 WHERE aid=?", u.MID, p.AID)
	}
	liveExec(t, s, "UPDATE rooms SET live_status=0 WHERE room_id=?", rid)
	return u, pages[0], rid
}
func cacheChoose(t *testing.T, s *Server, u *store.Session, p store.Page, auto bool) *liveCache {
	t.Helper()
	v := sourcePost(t, s, u, cacheUpdate, url.Values{"source_type": {"video"}, "aid": {fmt.Sprint(p.AID)}, "cid": {fmt.Sprint(p.CID)}, "auto_start": {fmt.Sprint(auto)}})
	d := sourceData(t, v)
	key := liveField(t, d, "source", "cache", "key").(string)
	x, e := liveCacheByKey(context.Background(), s.Store.DB, key)
	if e != nil || x == nil {
		t.Fatal(x, e)
	}
	return x
}
func cacheReady(t *testing.T, s *Server, x *liveCache) {
	t.Helper()
	if e := os.MkdirAll(x.Dir, 0755); e != nil {
		t.Fatal(e)
	}
	for name, body := range map[string]string{"index.m3u8": "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:6,\nseg00000.ts\n#EXTINF:4,\nseg00001.ts\n#EXT-X-ENDLIST\n", "seg00000.ts": "0123456789", "seg00001.ts": "abcdefghij"} {
		if e := os.WriteFile(filepath.Join(x.Dir, name), []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	liveExec(t, s, "UPDATE live_caches SET status='ready',progress=100 WHERE key=?", x.Key)
}
func TestLiveCacheRoutes(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u, p, rid := cacheSetup(t, s)
		for _, path := range []string{cacheUpdate, "/xlive/app-blink/v1/streamSource/retry", "/room/v1/Room/startLive", "/room/v1/Room/stopLive", "/xlive/app-blink/v1/room/GetInfo", "/x/admin/live/rooms", "/x/admin/live/room/source", "/x/admin/live/caches", "/x/admin/live/cache/retry", "/x/admin/live/cache/delete", "/x/admin/settings/save"} {
			assertCode(t, post(t, s, path, nil), -101)
		}
		liveFailure(t, sourcePost(t, s, u, "/xlive/app-blink/v1/streamSource/retry", nil), -404, "当前直播源没有转码任务")
		x := cacheChoose(t, s, u, p, true)
		if !liveCacheKeyPattern.MatchString(x.Key) || x.Status != "pending" || x.Height != 1080 {
			t.Fatal(x)
		}
		liveWant(t, sourceData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", u)), true, "pending_start")
		for _, tc := range []struct {
			status, message, want string
			progress              int
		}{{"pending", "", "转播视频正在转码缓存（排队中），完成后才能开播", 0}, {"running", "", "转播视频正在转码缓存（37%），完成后才能开播", 37}, {"failed", "坏视频", "转播视频转码失败：坏视频", 0}} {
			liveExec(t, s, "UPDATE live_caches SET status=?,message=?,progress=? WHERE key=?", tc.status, tc.message, tc.progress, x.Key)
			liveFailure(t, sourcePost(t, s, u, "/room/v1/Room/startLive", nil), -400, tc.want)
			if w := sourceHTTP(s, liveCachePath(x.Key)); w.Code != 404 {
				t.Fatal(w.Code)
			}
		}
		d := sourceData(t, sourcePost(t, s, u, "/xlive/app-blink/v1/streamSource/retry", nil))
		liveWant(t, d, "pending", "source", "cache", "status")
		assertCode(t, sourcePost(t, s, u, "/room/v1/Room/stopLive", nil), 0)
		if liveRoom(t, s, rid).PendingStart {
			t.Fatal("stop retained pending")
		}
		y := cacheChoose(t, s, u, p, false)
		if x.Key != y.Key {
			t.Fatal("cache not shared")
		}
		for _, route := range []string{"/x/admin/live/cache/retry", "/x/admin/live/cache/delete"} {
			liveFailure(t, sourcePost(t, s, u, route, url.Values{"key": {"missing"}}), -404, "缓存不存在")
		}
		liveFailure(t, sourcePost(t, s, u, "/x/admin/live/cache/delete", url.Values{"key": {x.Key}}), -400, "仍有直播间在使用该缓存，请先更换它们的直播源")
		d = sourceData(t, sourcePost(t, s, u, "/x/admin/live/room/source", url.Values{"room_id": {fmt.Sprint(rid)}, "source_type": {"video"}, "aid": {fmt.Sprint(p.AID)}, "cid": {fmt.Sprint(p.CID)}, "start": {"true"}}))
		liveWant(t, d, true, "pending_start")
		liveWant(t, d, float64(0), "live_status")
		caches := sourceData(t, get(t, s, "/x/admin/live/caches?pn=1&ps=1", u))
		liveWant(t, caches, float64(24), "keep_hours")
		liveWant(t, caches, x.Dir, "list", 0, "dir")
		liveWant(t, caches, float64(rid), "list", 0, "rooms", 0, "room_id")
		liveWant(t, caches, p.Part, "list", 0, "part")
		rooms := sourceData(t, get(t, s, "/x/admin/live/rooms?ps=100", u))
		found := false
		for _, v := range rooms["list"].([]any) {
			r := v.(map[string]any)
			if r["room_id"] == float64(rid) {
				found = true
				liveWant(t, r, true, "pending_start")
				liveWant(t, r, x.Key, "source", "cache", "key")
			}
		}
		if !found {
			t.Fatal("room missing")
		}
		cacheReady(t, s, x)
		assertCode(t, sourcePost(t, s, u, "/room/v1/Room/startLive", nil), 0)
		if liveRoom(t, s, rid).PendingStart {
			t.Fatal("start retained pending")
		}
		pages, e := s.Store.ListPage(context.Background(), "WHERE aid IN (SELECT aid FROM archives WHERE mid=?) AND cid<>? ORDER BY cid", u.MID, p.CID)
		if e != nil {
			t.Fatal(e)
		}
		liveFailure(t, sourcePost(t, s, u, cacheUpdate, url.Values{"source_type": {"video"}, "aid": {fmt.Sprint(pages[0].AID)}, "cid": {fmt.Sprint(pages[0].CID)}}), -400, "直播中只能切换到已完成转码缓存的视频，请先下播或等待转码完成")
		xs, e := liveCacheList(context.Background(), s.Store.DB)
		if e != nil || len(xs) != 1 {
			t.Fatal("rejected switch queued cache", xs, e)
		}
		if w := sourceHTTP(s, fmt.Sprintf("/live-relay/%d/index.m3u8", rid)); w.Code != 200 || !strings.Contains(w.Body.String(), "/live-cache/"+x.Key+"/seg00000.ts") {
			t.Fatal(w.Code, w.Body.String())
		}
		req := httptest.NewRequest("GET", "/live-cache/"+x.Key+"/seg00000.ts", nil)
		req.Header.Set("Range", "bytes=2-4")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != 206 || w.Body.String() != "234" || w.Header().Get("Cache-Control") != "public, max-age=86400, immutable" {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
		for _, path := range []string{"/live-cache/" + x.Key + "/secret", "/live-cache/invalid/index.m3u8", "/live-cache/" + x.Key + "/seg999999.ts"} {
			if sourceHTTP(s, path).Code != 404 {
				t.Fatal(path)
			}
		}
		for _, address := range []string{liveCachePath(x.Key), fmt.Sprintf("/live-relay/%d/index.m3u8", rid), "http://example.com/live-cache/" + x.Key + "/index.m3u8"} {
			liveFailure(t, get(t, s, "/xlive/app-blink/v1/streamSource/probe?url="+url.QueryEscape(address), u), -400, "请使用「转播视频」选择本站视频")
			liveFailure(t, sourcePost(t, s, u, cacheUpdate, url.Values{"source_type": {"m3u8"}, "source_url": {address}}), -400, "请使用「转播视频」选择本站视频")
		}
		// Missing cache retains the exact start error; stop never requires a cache.
		liveExec(t, s, "DELETE FROM live_caches WHERE key=?", x.Key)
		liveFailure(t, sourcePost(t, s, u, "/room/v1/Room/startLive", nil), -400, "转播视频的转码缓存不存在，请重新选择视频")
		assertCode(t, sourcePost(t, s, u, "/room/v1/Room/stopLive", nil), 0)
	})
}
func TestLiveCacheSettingsDeletionAndGC(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u, p, rid := cacheSetup(t, s)
		x := cacheChoose(t, s, u, p, false)
		cacheReady(t, s, x)
		for _, tc := range []struct{ key, value, message string }{{"liveCacheDir", "relative", "缓存目录必须是绝对路径"}, {"liveTranscodeHeight", "900", "转码分辨率无效"}, {"liveTranscodeHeight", "1080.1", "转码分辨率无效"}} {
			liveFailure(t, sourcePost(t, s, u, "/x/admin/settings/save", url.Values{tc.key: {tc.value}}), -400, tc.message)
		}
		bad := filepath.Join(t.TempDir(), "file")
		if e := os.WriteFile(bad, []byte("x"), 0600); e != nil {
			t.Fatal(e)
		}
		v := sourcePost(t, s, u, "/x/admin/settings/save", url.Values{"liveCacheDir": {bad}})
		assertCode(t, v, -400)
		if !strings.HasPrefix(v["message"].(string), "缓存目录不可写：") {
			t.Fatal(v)
		}
		dir := filepath.Join(t.TempDir(), "next")
		assertCode(t, sourcePost(t, s, u, "/x/admin/settings/save", url.Values{"liveCacheDir": {"  " + dir + "  "}, "liveTranscodeHeight": {"720"}}), 0)
		if sourceHTTP(s, liveCachePath(x.Key)).Code != 200 {
			t.Fatal("old cache disappeared")
		}
		y := cacheChoose(t, s, u, p, false)
		if y.Key == x.Key || y.Dir != filepath.Join(dir, y.Key) || y.Height != 720 {
			t.Fatal(y)
		}
		liveExec(t, s, "UPDATE live_caches SET status='failed',message='oops' WHERE key=?", y.Key)
		liveWant(t, sourceData(t, sourcePost(t, s, u, "/x/admin/live/cache/retry", url.Values{"key": {y.Key}})), "pending", "status")
		liveExec(t, s, "UPDATE rooms SET pending_start=1 WHERE room_id=?", rid)
		assertCode(t, sourcePost(t, s, u, cacheUpdate, url.Values{"source_type": {"push"}}), 0)
		if liveRoom(t, s, rid).PendingStart {
			t.Fatal("push retained pending")
		}
		if e := s.collectLiveCaches(context.Background(), 1000); e != nil {
			t.Fatal(e)
		}
		z, e := liveCacheByKey(context.Background(), s.Store.DB, x.Key)
		if e != nil || z.UnusedSince != 1000 {
			t.Fatal(z, e)
		}
		assertCode(t, sourcePost(t, s, u, "/x/admin/live/cache/delete", url.Values{"key": {y.Key}}), 0)
		if e := s.collectLiveCaches(context.Background(), 1000+86400); e != nil {
			t.Fatal(e)
		}
		if _, e := os.Stat(x.Dir); !os.IsNotExist(e) {
			t.Fatal("GC retained directory", e)
		}
		if _, e := os.Stat(filepath.Dir(x.Dir)); e != nil {
			t.Fatal("GC deleted root", e)
		}
		// Never derive a removal path from the current setting or accept a mismatched basename.
		outside := t.TempDir()
		unsafe := &liveCache{Key: x.Key, Dir: outside}
		if liveCacheRemove(unsafe) == nil {
			t.Fatal("unsafe delete accepted")
		}
		if _, e := os.Stat(outside); e != nil {
			t.Fatal(e)
		}
		y = cacheChoose(t, s, u, p, true)
		cacheReady(t, s, y)
		assertCode(t, sourcePost(t, s, u, "/x/admin/settings/save", url.Values{"reset": {"true"}}), 0)
		xs, e := liveCacheList(context.Background(), s.Store.DB)
		if e != nil || len(xs) != 0 {
			t.Fatal(xs, e)
		}
		if _, e := os.Stat(y.Dir); !os.IsNotExist(e) {
			t.Fatal("reset retained cache", e)
		}
	})
}
func TestLiveCacheMigrationAndRecovery(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		ctx := context.Background()
		u, p, rid := cacheSetup(t, s)
		for i := 0; i < 2; i++ {
			if e := s.Store.Migrate(ctx); e != nil {
				t.Fatal(e)
			}
		}
		x := cacheChoose(t, s, u, p, true)
		liveExec(t, s, "UPDATE live_caches SET status='running',progress=50 WHERE key=?", x.Key)
		if e := os.MkdirAll(x.Dir+".tmp", 0755); e != nil {
			t.Fatal(e)
		}
		if e := s.recoverLiveCaches(ctx); e != nil {
			t.Fatal(e)
		}
		y, e := liveCacheByKey(ctx, s.Store.DB, x.Key)
		if e != nil || y.Status != "pending" {
			t.Fatal(y, e)
		}
		if _, e := os.Stat(x.Dir + ".tmp"); !os.IsNotExist(e) {
			t.Fatal(e)
		}
		liveExec(t, s, "UPDATE live_caches SET status='ready' WHERE key=?", x.Key)
		if e := s.recoverLiveCaches(ctx); e != nil {
			t.Fatal(e)
		}
		y, e = liveCacheByKey(ctx, s.Store.DB, x.Key)
		if e != nil || y.Message != "缓存文件丢失，请重试" || y.Status != "failed" {
			t.Fatal(y, e)
		}
		liveExec(t, s, "UPDATE rooms SET source=?,live_status=1,pending_start=0 WHERE room_id=?", fmt.Sprintf(`{"type":"video","aid":%d,"cid":%d,"url":"old"}`, p.AID, p.CID), rid)
		if e := s.recoverLiveCaches(ctx); e != nil {
			t.Fatal(e)
		}
		r := liveRoom(t, s, rid)
		if r.LiveStatus != 0 || !r.PendingStart || r.Source.Cache != x.Key || r.Source.URL != liveCachePath(x.Key) {
			t.Fatal(r)
		}
		liveExec(t, s, "UPDATE rooms SET banned=1 WHERE room_id=?", rid)
		if e := s.finishLiveCache(ctx, x, 123); e != nil {
			t.Fatal(e)
		}
		if room := liveRoom(t, s, rid); room.LiveStatus != 0 || room.PendingStart {
			t.Fatal("banned pending completion", room)
		}
		liveExec(t, s, "UPDATE rooms SET banned=0,pending_start=0 WHERE room_id=?", rid)
		if e := s.finishLiveCache(ctx, x, 123); e != nil {
			t.Fatal(e)
		}
		if liveRoom(t, s, rid).LiveStatus != 0 {
			t.Fatal("stopped auto-started")
		}
		// Ensure transaction rollback does not leave a new cache.
		before, _ := liveCacheList(ctx, s.Store.DB)
		_ = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
			p.Media = "emby:rollback:src"
			if _, e := s.liveCacheEnsure(ctx, tx, p, false); e != nil {
				t.Fatal(e)
			}
			return fmt.Errorf("rollback")
		})
		after, _ := liveCacheList(ctx, s.Store.DB)
		if len(before) != len(after) {
			t.Fatal("rollback failed")
		}
	})
}
func cacheWait(t *testing.T, s *Server, key, status string) *liveCache {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		x, e := liveCacheByKey(context.Background(), s.Store.DB, key)
		if e != nil {
			t.Fatal(e)
		}
		if x != nil && x.Status == status {
			return x
		}
		if x != nil && x.Status == "failed" && status != "failed" {
			t.Fatal(x.Message)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cache timeout", status)
	return nil
}

func TestLiveCacheReferenceCleanupAndFileBoundary(t *testing.T) {
	s := newTestServer(t)
	u, p, rid := cacheSetup(t, s)
	x := cacheChoose(t, s, u, p, false)
	cacheReady(t, s, x)
	liveExec(t, s, "UPDATE live_caches SET unused_since=? WHERE key=?", 1, x.Key)
	if e := s.collectLiveCaches(context.Background(), 100000); e != nil {
		t.Fatal(e)
	}
	y, e := liveCacheByKey(context.Background(), s.Store.DB, x.Key)
	if e != nil || y.UnusedSince != 0 {
		t.Fatal("referenced GC", y, e)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if e := os.WriteFile(secret, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(x.Dir, "seg00000.ts")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(secret, filepath.Join(x.Dir, "seg00000.ts")); e != nil {
		t.Fatal(e)
	}
	if sourceHTTP(s, "/live-cache/"+x.Key+"/seg00000.ts").Code != 404 {
		t.Fatal("symlink escaped cache root")
	}
	assertCode(t, sourcePost(t, s, u, cacheUpdate, url.Values{"source_type": {"push"}}), 0)
	if e := os.MkdirAll(x.Dir+".tmp", 0755); e != nil {
		t.Fatal(e)
	}
	liveExec(t, s, "UPDATE live_caches SET status='running',unused_since=1 WHERE key=?", x.Key)
	if e := s.collectLiveCaches(context.Background(), 100000); e != nil {
		t.Fatal(e)
	}
	if y, e := liveCacheByKey(context.Background(), s.Store.DB, x.Key); e != nil || y == nil {
		t.Fatal("running GC", y, e)
	}
	assertCode(t, sourcePost(t, s, u, "/x/admin/live/cache/delete", url.Values{"key": {x.Key}}), 0)
	for _, dir := range []string{x.Dir, x.Dir + ".tmp"} {
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatal("directory remains", e)
		}
	}
	if b, e := os.ReadFile(secret); e != nil || string(b) != "private" {
		t.Fatal("delete followed symlink", e)
	}
	if liveRoom(t, s, rid).PendingStart {
		t.Fatal("pending after source change")
	}
}

func TestLiveCacheSourceTransactionRollback(t *testing.T) {
	s := newTestServer(t)
	u, p, rid := cacheSetup(t, s)
	liveExec(t, s, "DROP TABLE logs")
	assertCode(t, sourcePost(t, s, u, "/x/admin/live/room/source", url.Values{"room_id": {fmt.Sprint(rid)}, "source_type": {"video"}, "aid": {fmt.Sprint(p.AID)}, "cid": {fmt.Sprint(p.CID)}, "start": {"true"}}), -500)
	caches, e := liveCacheList(context.Background(), s.Store.DB)
	if e != nil || len(caches) != 0 {
		t.Fatal("cache escaped failed audit transaction", caches, e)
	}
	r := liveRoom(t, s, rid)
	if r.Source != nil && r.Source.Type == "video" || r.PendingStart {
		t.Fatal("room escaped failed transaction", r)
	}
}
