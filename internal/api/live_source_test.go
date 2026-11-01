package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func liveVOD() string {
	s := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n"
	for i := 0; i < 18; i++ {
		s += fmt.Sprintf("#EXTINF:4,\nseg%d.ts\n", i)
	}
	return s + "#EXT-X-ENDLIST\n"
}
func liveOrigin(t *testing.T) *httptest.Server {
	t.Helper()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vod.m3u8", "/nested/vod.m3u8":
			fmt.Fprint(w, liveVOD())
		case "/live.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:123\n#EXT-X-TARGETDURATION:4\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n#EXTINF:4,\nseg.ts\n")
		case "/fmp4.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n#EXTINF:4,\nseg.m4s\n#EXT-X-ENDLIST\n")
		case "/master.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720\nvod.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2000,RESOLUTION=1920x1080\nfmp4.m3u8\n")
		case "/nested-master.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nmaster.m3u8\n")
		case "/redirect.m3u8":
			http.Redirect(w, r, "/nested/vod.m3u8", http.StatusFound)
		case "/bad-redirect.m3u8":
			http.Redirect(w, r, "file:///tmp/playlist.m3u8", http.StatusFound)
		case "/large.m3u8":
			fmt.Fprint(w, strings.Repeat("x", livePlaylistLimit+1))
		case "/large-chunked.m3u8":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			fmt.Fprint(w, strings.Repeat("x", livePlaylistLimit+1))
		case "/invalid.m3u8":
			fmt.Fprint(w, "not a playlist")
		case "/empty.m3u8":
			fmt.Fprint(w, "#EXTM3U\n")
		case "/slow.m3u8":
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(origin.Close)
	return origin
}
func liveSetSource(t *testing.T, s *Server, rid int64, source store.StreamSource, started int64) {
	t.Helper()
	b, e := json.Marshal(source)
	if e != nil {
		t.Fatal(e)
	}
	liveExec(t, s, "UPDATE rooms SET source=?,live_status=1,live_time=?,banned=0 WHERE room_id=?", string(b), started, rid)
}
func liveRelayRequest(t *testing.T, s *Server, path string, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != wantStatus || w.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("%s: status %d headers %v body %s", path, w.Code, w.Header(), w.Body.String())
	}
	return w
}
func TestLiveSourceProbe(t *testing.T) {
	origin := liveOrigin(t)
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		s.Config.PublicOrigin = origin.URL
		cases := []struct {
			path, kind, format string
			master, encrypted  bool
			segments, duration int
		}{
			{"/vod.m3u8", "vod", "ts", false, false, 18, 72},
			{"/live.m3u8", "live", "ts", false, true, 1, 4},
			{"/fmp4.m3u8", "vod", "fmp4", false, true, 1, 4},
			{"/master.m3u8", "vod", "ts", true, false, 18, 72},
			{"/redirect.m3u8", "vod", "ts", false, false, 18, 72},
		}
		for _, tt := range cases {
			t.Run(tt.path, func(t *testing.T) {
				v := post(t, s, "/xlive/app-blink/v1/streamSource/probe", url.Values{"url": {"  " + tt.path + " "}}, admin)
				data := liveData(t, v)
				liveWant(t, data, tt.kind, "kind")
				liveWant(t, data, tt.format, "format")
				liveWant(t, data, tt.master, "master")
				liveWant(t, data, tt.encrypted, "encrypted")
				liveWant(t, data, float64(tt.segments), "segments")
				liveWant(t, data, float64(tt.duration), "duration")
				liveWant(t, data, float64(4), "target_duration")
				path := tt.path
				if path == "/redirect.m3u8" {
					path = "/nested/vod.m3u8"
				}
				liveWant(t, data, origin.URL+path, "url")
				if tt.master {
					liveWant(t, data, float64(1000), "variants", 0, "bandwidth")
					liveWant(t, data, "1280x720", "variants", 0, "resolution")
				} else {
					liveWant(t, data, []any{}, "variants")
				}
			})
		}
		for _, tt := range []struct{ address, msg string }{
			{" ", "请填写 m3u8 地址"}, {"file:///tmp/a.m3u8", "仅支持 http / https 的 m3u8 地址"}, {"ftp://example.com/a.m3u8", "仅支持 http / https 的 m3u8 地址"}, {"http://[bad", "m3u8 地址格式不正确"},
			{"/missing.m3u8", "源站返回 HTTP 404"}, {"/invalid.m3u8", "不是有效的 m3u8 播放列表"}, {"/empty.m3u8", "播放列表中没有分片"}, {"/nested-master.m3u8", "码率播放列表无效"}, {"/bad-redirect.m3u8", "无法访问该地址（网络错误或源站未开放跨域）"}, {"/large.m3u8", "播放列表不能超过 2 MB"}, {"/large-chunked.m3u8", "播放列表不能超过 2 MB"},
		} {
			t.Run("error_"+tt.address, func(t *testing.T) {
				liveFailure(t, post(t, s, "/xlive/app-blink/v1/streamSource/probe", url.Values{"url": {tt.address}}, admin), -400, tt.msg)
			})
		}
	})
}

func TestLiveSourceUpdateAndAdmin(t *testing.T) {
	origin := liveOrigin(t)
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		s.Config.PublicOrigin = origin.URL
		old := time.Now().Unix() - 300
		liveExec(t, s, "UPDATE rooms SET live_time=?,live_status=1 WHERE room_id=?", old, 20000)
		p := url.Values{"csrf": {admin.CSRF}, "source_type": {"m3u8"}, "source_url": {"  /vod.m3u8  "}, "loop": {"0"}}
		data := liveData(t, post(t, s, "/xlive/app-blink/v1/streamSource/update", p, admin))
		liveWant(t, data, "/vod.m3u8", "source", "url")
		liveWant(t, data, true, "source", "loop")
		liveWant(t, data, "vod", "source", "kind")
		r := liveRoom(t, s, 20000)
		if r.LiveTime <= old {
			t.Fatal("source did not restart timeline")
		}
		liveExec(t, s, "UPDATE rooms SET live_time=? WHERE room_id=?", old, 20000)
		assertCode(t, post(t, s, "/xlive/app-blink/v1/streamSource/update", p, admin), 0)
		if liveRoom(t, s, 20000).LiveTime != old {
			t.Fatal("same source restarted timeline")
		}
		p.Set("loop", "false")
		data = liveData(t, post(t, s, "/xlive/app-blink/v1/streamSource/update", p, admin))
		liveWant(t, data, false, "source", "loop")
		if liveRoom(t, s, 20000).LiveTime <= old {
			t.Fatal("changed loop did not restart")
		}
		before := *liveRoom(t, s, 20000)
		p.Set("source_url", "/invalid.m3u8")
		liveFailure(t, post(t, s, "/xlive/app-blink/v1/streamSource/update", p, admin), -400, "不是有效的 m3u8 播放列表")
		after := liveRoom(t, s, 20000)
		if *before.Source != *after.Source || before.LiveTime != after.LiveTime {
			t.Fatal("invalid source changed state")
		}
		p.Set("source_type", "push")
		data = liveData(t, post(t, s, "/xlive/app-blink/v1/streamSource/update", p, admin))
		liveWant(t, data, "push", "source", "type")
		liveWant(t, data, "", "source", "url")
		liveWant(t, data, true, "source", "loop")
		liveExec(t, s, "UPDATE rooms SET live_status=0,online=0 WHERE room_id=?", 21000)
		p = url.Values{"csrf": {admin.CSRF}, "room_id": {"21000"}, "source_type": {"m3u8"}, "source_url": {"/fmp4.m3u8"}, "start": {"1"}}
		data = liveData(t, post(t, s, "/x/admin/live/room/source", p, admin))
		liveWant(t, data, "fmp4", "source", "format")
		liveWant(t, data, float64(1), "live_status")
		r = liveRoom(t, s, 21000)
		if r.LiveStatus != 1 || r.Online != 1 {
			t.Fatal(r)
		}
		logs, e := s.Store.ListLog(context.Background(), "WHERE action=? ORDER BY id DESC", "配置直播源")
		if e != nil || len(logs) != 1 || logs[0].Target != r.Title+" · /fmp4.m3u8" {
			t.Fatalf("%+v %v", logs, e)
		}
		data = liveData(t, get(t, s, "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=21000"))
		liveWant(t, data, "fmp4", "playurl_info", "playurl", "stream", 0, "format", 0, "format_name")
		liveWant(t, data, "/live-relay/21000/index.m3u8", "playurl_info", "playurl", "stream", 0, "format", 0, "codec", 0, "base_url")
		liveWant(t, data, origin.URL, "playurl_info", "playurl", "stream", 0, "format", 0, "codec", 0, "url_info", 0, "host")
		p.Set("source_url", "/invalid.m3u8")
		liveFailure(t, post(t, s, "/x/admin/live/room/source", p, admin), -400, "不是有效的 m3u8 播放列表")
		// GET admin mutations follow Mock's login+role requirement without CSRF.
		data = liveData(t, get(t, s, "/x/admin/live/room/source?room_id=21000&source_type=push", admin))
		liveWant(t, data, "push", "source", "type")
		liveExec(t, s, "UPDATE rooms SET banned=1,live_status=0 WHERE room_id=?", 21000)
		p.Set("source_url", "/vod.m3u8")
		data = liveData(t, post(t, s, "/x/admin/live/room/source", p, admin))
		liveWant(t, data, float64(0), "live_status")
	})
}

func TestLiveRelayRoutes(t *testing.T) {
	origin := liveOrigin(t)
	testServers(t, func(t *testing.T, s *Server) {
		s.Config.PublicOrigin = origin.URL
		source := store.StreamSource{Type: "m3u8", URL: "/master.m3u8", Loop: true, Kind: "vod", Format: "ts"}
		liveSetSource(t, s, 20000, source, time.Now().Unix())
		w := liveRelayRequest(t, s, "/live-relay/20000/index.m3u8", 200)
		if !strings.Contains(w.Body.String(), "/live-relay/20000/v0.m3u8") || !strings.Contains(w.Body.String(), "/live-relay/20000/v1.m3u8") || !strings.Contains(w.Body.String(), origin.URL+"/audio.m3u8") {
			t.Fatal(w.Body.String())
		}
		w = liveRelayRequest(t, s, "/live-relay/20000/v0.m3u8", 200)
		if !strings.Contains(w.Body.String(), "#EXT-X-MEDIA-SEQUENCE:0\n") || !strings.Contains(w.Body.String(), origin.URL+"/seg0.ts") {
			t.Fatal(w.Body.String())
		}
		w = liveRelayRequest(t, s, "/live-relay/20000/v1.m3u8", 200)
		if !strings.Contains(w.Body.String(), "#EXT-X-MAP:URI=\""+origin.URL+"/init.mp4\"") {
			t.Fatal(w.Body.String())
		}
		liveRelayRequest(t, s, "/live-relay/20000/v2.m3u8", 404)
		liveRelayRequest(t, s, "/live-relay/999999/index.m3u8", 404)
		liveRelayRequest(t, s, "/live-relay/999999/v0.m3u8", 404)
		source.URL = "/vod.m3u8"
		liveSetSource(t, s, 20000, source, time.Now().Unix()-52)
		w = liveRelayRequest(t, s, "/live-relay/20000/index.m3u8", 200)
		if !strings.Contains(w.Body.String(), "#EXT-X-MEDIA-SEQUENCE:13\n") || !strings.Contains(w.Body.String(), "#EXT-X-DISCONTINUITY\n") {
			t.Fatal(w.Body.String())
		}
		source.URL = "/live.m3u8"
		source.Kind = "live"
		liveSetSource(t, s, 20000, source, time.Now().Unix())
		w = liveRelayRequest(t, s, "/live-relay/20000/index.m3u8", 200)
		if !strings.Contains(w.Body.String(), "#EXT-X-MEDIA-SEQUENCE:123\n") || strings.Contains(w.Body.String(), "ENDLIST") || !strings.Contains(w.Body.String(), "URI=\""+origin.URL+"/key\"") {
			t.Fatal(w.Body.String())
		}
		source.URL = "/redirect.m3u8"
		source.Kind = "vod"
		liveSetSource(t, s, 20000, source, time.Now().Unix())
		w = liveRelayRequest(t, s, "/live-relay/20000/index.m3u8", 200)
		if !strings.Contains(w.Body.String(), origin.URL+"/nested/seg0.ts") {
			t.Fatal(w.Body.String())
		}
		source.URL = "/vod.m3u8"
		source.Loop = false
		liveSetSource(t, s, 20000, source, time.Now().Unix()-72)
		w = liveRelayRequest(t, s, "/live-relay/20000/index.m3u8", 200)
		if !strings.Contains(w.Body.String(), "#EXT-X-ENDLIST") || liveRoom(t, s, 20000).LiveStatus != 0 {
			t.Fatal(w.Body.String())
		}
		liveRelayRequest(t, s, "/live-relay/20000/index.m3u8", 404)
		liveSetSource(t, s, 20000, source, time.Now().Unix()-72)
		data := liveData(t, get(t, s, "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=20000"))
		liveWant(t, data, float64(0), "live_status")
		liveWant(t, data, nil, "playurl_info")
		// Mock only detects a master VOD's end when a variant is requested.
		source.URL = "/master.m3u8"
		liveSetSource(t, s, 20000, source, time.Now().Unix()-72)
		data = liveData(t, get(t, s, "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=20000"))
		liveWant(t, data, float64(1), "live_status")
		w = liveRelayRequest(t, s, "/live-relay/20000/v0.m3u8", 200)
		if !strings.Contains(w.Body.String(), "ENDLIST") || liveRoom(t, s, 20000).LiveStatus != 0 {
			t.Fatal(w.Body.String())
		}
		liveSetSource(t, s, 20000, source, time.Now().Unix())
		liveExec(t, s, "UPDATE rooms SET banned=1 WHERE room_id=?", 20000)
		liveRelayRequest(t, s, "/live-relay/20000/index.m3u8", 404)
		source.Type = "push"
		liveSetSource(t, s, 20000, source, time.Now().Unix())
		liveRelayRequest(t, s, "/live-relay/20000/v0.m3u8", 404)
		source.Type = "m3u8"
		source.URL = "/invalid.m3u8"
		liveSetSource(t, s, 20000, source, time.Now().Unix())
		liveFailure(t, get(t, s, "/live-relay/20000/index.m3u8"), -400, "不是有效的 m3u8 播放列表")
		liveFailure(t, get(t, s, "/live-relay/20000/v0.m3u8"), -400, "不是有效的 m3u8 播放列表")
		liveFailure(t, get(t, s, "/live-relay/abc/index.m3u8"), -404, "接口不存在")
	})
}

func TestLivePlaylistCacheAndCancellation(t *testing.T) {
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); fmt.Fprint(w, liveVOD()) }))
	defer origin.Close()
	s := newTestServer(t)
	s.Config.PublicOrigin = origin.URL
	clock := time.Unix(100, 0)
	l := &liveAPI{s: s, cache: map[string]livePlaylist{}, now: func() time.Time { return clock }}
	c := &Ctx{Server: s, R: httptest.NewRequest("GET", "http://example.test/", nil)}
	for _, tt := range []struct {
		advance, age time.Duration
		want         int32
	}{{0, time.Minute, 1}, {59 * time.Second, time.Minute, 1}, {time.Second, time.Minute, 2}, {0, time.Second, 2}, {999 * time.Millisecond, time.Second, 2}, {time.Millisecond, time.Second, 3}, {0, 0, 4}} {
		clock = clock.Add(tt.advance)
		p, e := l.loadPlaylist(c, "/vod.m3u8", tt.age)
		if e != nil {
			t.Fatal(e)
		}
		if p.list.Duration != 72 || hits.Load() != tt.want {
			t.Fatalf("%+v hits=%d playlist=%+v", tt, hits.Load(), p)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.R = c.R.WithContext(ctx)
	if _, e := l.loadPlaylist(c, "/uncached.m3u8", 0); e == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestLiveFetchDeadline(t *testing.T) {
	origin := liveOrigin(t)
	s := newTestServer(t)
	session := login(t, s, "jumedia", "admin123")
	before := time.Now()
	v := post(t, s, "/xlive/app-blink/v1/streamSource/probe", url.Values{"url": {origin.URL + "/slow.m3u8"}}, session)
	liveFailure(t, v, -400, "无法访问该地址（网络错误或源站未开放跨域）")
	elapsed := time.Since(before)
	if elapsed < 9*time.Second || elapsed > 12*time.Second {
		t.Fatalf("expected 10 second timeout, took %v", elapsed)
	}
}

func TestLiveEndWithReorderedSourceJSON(t *testing.T) {
	origin := liveOrigin(t)
	testServers(t, func(t *testing.T, s *Server) {
		s.Config.PublicOrigin = origin.URL
		// Source JSON is a persisted contract, not a byte-order contract.
		liveExec(t, s, "UPDATE rooms SET source=?,live_status=1,live_time=? WHERE room_id=?", `{"loop":false,"kind":"vod","format":"ts","url":"/vod.m3u8","type":"m3u8"}`, time.Now().Unix()-72, 20000)
		data := liveData(t, get(t, s, "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=20000"))
		liveWant(t, data, float64(0), "live_status")
		liveWant(t, data, nil, "playurl_info")
	})
}

func TestLiveAdminSourceRollback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		before := liveRoom(t, s, 20000)
		const collisionID = 900000
		if e := store.InsertLog(context.Background(), s.Store.DB, &store.Log{ID: collisionID, MID: 1, Action: "existing", Target: "existing", Ctime: time.Now().Unix()}); e != nil {
			t.Fatal(e)
		}
		liveExec(t, s, "UPDATE ids SET value=? WHERE name=?", collisionID-1, "global")
		v := post(t, s, "/x/admin/live/room/source", url.Values{"csrf": {session.CSRF}, "room_id": {"20000"}, "source_type": {"push"}}, session)
		assertCode(t, v, -500)
		after := liveRoom(t, s, 20000)
		if *after.Source != *before.Source || after.LiveTime != before.LiveTime {
			t.Fatalf("source was committed despite failed log: %+v", after)
		}
		var sequence int64
		if e := s.Store.DB.QueryRowContext(context.Background(), "SELECT value FROM ids WHERE name=?", "global").Scan(&sequence); e != nil {
			t.Fatal(e)
		}
		if sequence != collisionID-1 {
			t.Fatal("sequence was not rolled back")
		}
	})
}
