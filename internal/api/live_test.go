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
	"testing"
	"time"

	"jumedia-server/internal/store"
)

var liveJSONPaths = []string{
	"/xlive/web-interface/v1/index/getWebAreaList", "/room/v1/area/getRoomList", "/xlive/web-interface/v1/second/getUserRecommend", "/xlive/web-ucenter/user/following", "/room/v1/Room/get_info", "/room/v1/Room/room_init", "/live_user/v1/Master/info", "/xlive/web-room/v2/index/getRoomPlayInfo", "/xlive/web-room/v1/index/getDanmuInfo", "/xlive/web-room/v1/dM/gethistory", "/av/v1/SuperChat/getMessageList", "/msg/send", "/xlive/app-blink/v1/room/GetInfo", "/room/v1/Room/update", "/room/v1/Room/startLive", "/room/v1/Room/stopLive", "/xlive/app-blink/v1/streamSource/probe", "/xlive/app-blink/v1/streamSource/update", "/x/admin/live/room/source",
}

func liveField(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, k := range path {
		switch x := k.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("%v: expected object, got %#v", path, v)
			}
			var exists bool
			v, exists = m[x]
			if !exists {
				t.Fatalf("missing %s in %#v", x, m)
			}
		case int:
			a, ok := v.([]any)
			if !ok || len(a) <= x {
				t.Fatalf("%v: expected array index %d, got %#v", path, x, v)
			}
			v = a[x]
		}
	}
	return v
}
func liveWant(t *testing.T, v any, want any, path ...any) {
	t.Helper()
	got := liveField(t, v, path...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v got %#v, want %#v", path, got, want)
	}
}
func liveData(t *testing.T, v map[string]any) any {
	t.Helper()
	assertCode(t, v, 0)
	liveWant(t, v, "0", "message")
	liveWant(t, v, float64(1), "ttl")
	return liveField(t, v, "data")
}
func liveExec(t *testing.T, s *Server, q string, args ...any) {
	t.Helper()
	if _, e := s.Store.DB.ExecContext(context.Background(), q, args...); e != nil {
		t.Fatal(e)
	}
}
func liveRoom(t *testing.T, s *Server, id int64) *store.LiveRoom {
	t.Helper()
	r, e := s.Store.RoomByID(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func liveFailure(t *testing.T, v map[string]any, code int, message string) {
	t.Helper()
	assertCode(t, v, code)
	liveWant(t, v, message, "message")
	liveWant(t, v, nil, "data")
}

func TestLivePublicContracts(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		// This route contract needs a playable room. T21 seed now waits for
		// a cache URL; the dedicated demo tests cover that startup lifecycle.
		liveExec(t, s, "UPDATE rooms SET source=? WHERE room_id=?", `{"type":"m3u8","url":"https://example.test/live.m3u8","loop":true,"format":"ts"}`, 21000)
		r := liveRoom(t, s, 21000)
		cases := []struct {
			path   string
			checks map[string]any
		}{
			{"/room/v1/Room/get_info?room_id=21000", map[string]any{"room_id": float64(21000), "uid": float64(r.UID), "description": r.Description, "live_time": liveDate(r.LiveTime), "area_id": float64(r.AreaID), "user_cover": r.Cover}},
			{"/room/v1/Room/room_init?id=21000", map[string]any{"room_id": float64(21000), "short_id": float64(0), "live_status": float64(1), "is_locked": false, "live_time": float64(r.LiveTime)}},
			{"/xlive/web-room/v1/index/getDanmuInfo?id=21000", map[string]any{"room_id": float64(21000), "group": "live", "max_delay": float64(5000), "refresh_row_factor": 0.125}},
		}
		for _, tt := range cases {
			t.Run(tt.path, func(t *testing.T) {
				for _, method := range []string{"GET", "POST"} {
					v, _ := request(t, s, method, tt.path, nil, nil)
					data := liveData(t, v)
					for k, w := range tt.checks {
						liveWant(t, data, w, k)
					}
				}
			})
		}
		data := liveData(t, get(t, s, liveJSONPaths[0]))
		liveWant(t, data, "网游", "data", 0, "name")
		liveWant(t, data, "2", "data", 0, "list", 0, "parent_id")
		if _, ok := liveField(t, data, "data", 0, "list", 0, "id").(string); !ok {
			t.Fatal(data)
		}
		data = liveData(t, get(t, s, "/live_user/v1/Master/info?uid=2"))
		liveWant(t, data, float64(2), "info", "uid")
		liveWant(t, data, float64(21000), "room_id")
		liveWant(t, data, float64(-1), "info", "official_verify", "type")
		data = liveData(t, get(t, s, "/live_user/v1/Master/info?uid=999999"))
		liveWant(t, data, float64(0), "info", "uid")
		liveWant(t, data, "", "info", "uname")
		liveWant(t, data, float64(0), "room_id")
		data = liveData(t, get(t, s, "/xlive/web-room/v1/index/getDanmuInfo?id=oops"))
		liveWant(t, data, float64(0), "room_id")
		liveWant(t, data, "example.com", "host_list", 0, "host")
		if liveField(t, data, "token") == "" {
			t.Fatal(data)
		}
		data = liveData(t, get(t, s, "/av/v1/SuperChat/getMessageList?room_id=21000"))
		liveWant(t, data, float64(30), "list", 0, "price")
		liveWant(t, data, "#2A60B2", "list", 0, "background_bottom_color")
		liveWant(t, data, float64(120), "list", 0, "time")
		liveField(t, data, "list", 0, "user_info", "uname")
		data = liveData(t, get(t, s, "/av/v1/SuperChat/getMessageList?room_id=999999"))
		liveWant(t, data, []any{}, "list")
		data = liveData(t, get(t, s, "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=21000"))
		liveWant(t, data, "http_hls", "playurl_info", "playurl", "stream", 0, "protocol_name")
		liveWant(t, data, "ts", "playurl_info", "playurl", "stream", 0, "format", 0, "format_name")
		liveWant(t, data, "/live-relay/21000/index.m3u8", "playurl_info", "playurl", "stream", 0, "format", 0, "codec", 0, "base_url")
		liveExec(t, s, "UPDATE rooms SET live_status=0,live_time=0 WHERE room_id=?", 21000)
		data = liveData(t, get(t, s, "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=21000"))
		liveWant(t, data, nil, "playurl_info")
		data = liveData(t, get(t, s, "/room/v1/Room/get_info?room_id=21000"))
		liveWant(t, data, "0000-00-00 00:00:00", "live_time")
		data = liveData(t, get(t, s, "/xlive/web-room/v1/dM/gethistory?roomid=21000"))
		liveWant(t, data, []any{}, "admin")
		rows := liveField(t, data, "room").([]any)
		if len(rows) != 18 {
			t.Fatal(len(rows))
		}
		liveField(t, data, "room", 0, "check_info", "ts")
		liveField(t, data, "room", 0, "user", "base", "name")
		liveWant(t, data, nil, "room", 0, "medal_info")
		if _, ok := liveField(t, data, "room", 0, "id_str").(string); !ok {
			t.Fatal(data)
		}
		for _, path := range []string{"/room/v1/Room/get_info?room_id=999999", "/room/v1/Room/room_init?id=999999", "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=999999", "/xlive/web-room/v1/dM/gethistory?roomid=999999"} {
			liveFailure(t, get(t, s, path), 19002003, "房间不存在或已被封禁")
		}
		// Public Mock routes with no domain errors still reject malformed form bodies via the shared pipeline.
		for _, path := range liveJSONPaths {
			t.Run("malformed_"+path, func(t *testing.T) {
				req := httptest.NewRequest("POST", path, strings.NewReader("x=%zz"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, req)
				var v map[string]any
				if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
					t.Fatal(e)
				}
				liveFailure(t, v, -400, "请求参数错误")
			})
		}
	})
}

func TestLivePaginationAndFollowing(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		// Explicit fixture for this route contract, independent of demo defaults.
		liveExec(t, s, "UPDATE rooms SET live_status=1")
		admin := login(t, s, "jumedia", "admin123")
		rows, e := s.Store.ListLiveRoom(context.Background(), "WHERE live_status=1 AND banned=0 ORDER BY online DESC, room_id")
		if e != nil {
			t.Fatal(e)
		}
		for _, tt := range []struct {
			pn, ps       string
			start, count int
		}{{"1", "2", 0, 2}, {"2", "2", 2, 2}, {"0", "2", 0, 2}, {"999", "2", len(rows), 0}, {"bad", "bad", 0, min(20, len(rows))}, {"1", "0", 0, 0}, {"1", "-1", 0, len(rows) - 1}} {
			path := "/room/v1/area/getRoomList?page=" + tt.pn + "&page_size=" + tt.ps
			data := liveData(t, get(t, s, path)).([]any)
			if len(data) != tt.count {
				t.Fatalf("%s got %d", path, len(data))
			}
			if len(data) > 0 {
				liveWant(t, data[0], float64(rows[tt.start].RoomID), "roomid")
			}
		}
		data := liveData(t, get(t, s, "/room/v1/area/getRoomList?parent_area_id=999&area_id=999"))
		if len(data.([]any)) != 0 {
			t.Fatal(data)
		}
		data = liveData(t, get(t, s, "/room/v1/area/getRoomList?sort_type=live_time&page_size=1"))
		byTime, e := s.Store.ListLiveRoom(context.Background(), "WHERE live_status=1 AND banned=0 ORDER BY live_time DESC, room_id")
		if e != nil {
			t.Fatal(e)
		}
		liveWant(t, data, float64(byTime[0].RoomID), 0, "roomid")
		data = liveData(t, get(t, s, "/xlive/web-interface/v1/second/getUserRecommend?page_size=1"))
		liveWant(t, data, float64(1), "has_more")
		liveWant(t, data, float64(rows[0].RoomID), "list", 0, "room_id")
		data = liveData(t, get(t, s, "/xlive/web-interface/v1/second/getUserRecommend?page=999"))
		liveWant(t, data, float64(0), "has_more")
		liveWant(t, data, []any{}, "list")
		liveFailure(t, get(t, s, "/xlive/web-ucenter/user/following"), -101, "账号未登录")
		data = liveData(t, get(t, s, "/xlive/web-ucenter/user/following?page_size=1", admin))
		liveWant(t, data, "我的关注", "title")
		liveWant(t, data, float64(1), "pageSize")
		liveField(t, data, "list", 0, "area_name_v2")
		if liveField(t, data, "count").(float64) < 1 {
			t.Fatal(data)
		}
		data = liveData(t, get(t, s, "/xlive/web-ucenter/user/following?page=999", admin))
		liveWant(t, data, []any{}, "list")
		// A request value is never interpolated into the SQL sort clause.
		assertCode(t, get(t, s, "/room/v1/area/getRoomList?sort_type="+url.QueryEscape("online; DROP TABLE rooms")), 0)
		liveRoom(t, s, 20000)
	})
}

func TestLiveMutationsAndAuthorization(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		// Explicit fixture for this route contract, independent of demo defaults.
		liveExec(t, s, "UPDATE rooms SET source=? WHERE room_id=?", `{"type":"m3u8","url":"https://example.test/live.m3u8","loop":true,"format":"ts"}`, 21000)
		admin := login(t, s, "jumedia", "admin123")
		u, e := s.Store.UserByID(context.Background(), 2)
		if e != nil {
			t.Fatal(e)
		}
		owner := login(t, s, u.Username, "demo123")
		authPaths := []string{"/xlive/web-ucenter/user/following", "/msg/send", "/xlive/app-blink/v1/room/GetInfo", "/room/v1/Room/update", "/room/v1/Room/startLive", "/room/v1/Room/stopLive", "/xlive/app-blink/v1/streamSource/probe", "/xlive/app-blink/v1/streamSource/update", "/x/admin/live/room/source"}
		for _, p := range authPaths {
			liveFailure(t, get(t, s, p), -101, "账号未登录")
		}
		for _, p := range []string{"/msg/send", "/room/v1/Room/update", "/room/v1/Room/startLive", "/room/v1/Room/stopLive", "/xlive/app-blink/v1/streamSource/update", "/x/admin/live/room/source"} {
			liveFailure(t, post(t, s, p, nil, admin), -111, "csrf 校验失败")
		}
		liveFailure(t, get(t, s, "/x/admin/live/room/source?room_id=21000", owner), -403, "需要管理员权限")
		liveFailure(t, get(t, s, "/x/admin/live/room/source?room_id=999999", admin), 19002003, "房间不存在或已被封禁")
		data := liveData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", owner))
		liveWant(t, data, float64(21000), "room_id")
		liveWant(t, data, "m3u8", "source", "type")
		liveWant(t, data, true, "source", "loop")
		liveWant(t, data, "rtmp://example.com/live-jumedia/", "rtmp", "addr")
		params := url.Values{"csrf": {owner.CSRF}, "title": {strings.Repeat("播", 45)}, "cover": {"new-cover"}, "area_id": {"215"}, "room_id": {"20000"}}
		liveWant(t, liveData(t, post(t, s, "/room/v1/Room/update", params, owner)), nil)
		r := liveRoom(t, s, 21000)
		if r.Title != strings.Repeat("播", 40) || r.Cover != "new-cover" || r.AreaID != 215 || r.ParentAreaID != 10 {
			t.Fatal(r)
		}
		if liveRoom(t, s, 20000).Cover == "new-cover" {
			t.Fatal("updated another user's room")
		}
		params = url.Values{"biliCSRF": {owner.CSRF}, "area_v2": {"646"}}
		data = liveData(t, post(t, s, "/room/v1/Room/startLive", params, owner))
		liveWant(t, data, "LIVE", "status")
		r = liveRoom(t, s, 21000)
		if r.LiveStatus != 1 || r.Online != 1 || r.AreaID != 646 || time.Now().Unix()-r.LiveTime > 2 {
			t.Fatal(r)
		}
		data = liveData(t, post(t, s, "/room/v1/Room/stopLive", params, owner))
		liveWant(t, data, "PREPARING", "status")
		if liveRoom(t, s, 21000).LiveStatus != 0 {
			t.Fatal("not stopped")
		}
		for i := 0; i < 35; i++ {
			data = liveData(t, post(t, s, "/msg/send", url.Values{"csrf": {owner.CSRF}, "roomid": {"21000"}, "msg": {fmt.Sprintf("  message %d  ", i)}}, owner))
			liveWant(t, data, float64(0), "mode_info", "mode")
		}
		data = liveData(t, get(t, s, "/xlive/web-room/v1/dM/gethistory?roomid=21000"))
		messages := liveField(t, data, "room").([]any)
		if len(messages) != 30 {
			t.Fatal(len(messages))
		}
		liveWant(t, messages[0], "message 5", "text")
		liveWant(t, messages[29], "message 34", "text")
		for _, msg := range []string{" ", strings.Repeat("a", 41), strings.Repeat("😀", 21)} {
			liveFailure(t, post(t, s, "/msg/send", url.Values{"csrf": {owner.CSRF}, "roomid": {"21000"}, "msg": {msg}}, owner), -400, "弹幕需在 1–40 字之间")
		}
		assertCode(t, post(t, s, "/msg/send", url.Values{"csrf": {owner.CSRF}, "roomid": {"21000"}, "msg": {strings.Repeat("😀", 20)}}, owner), 0)
		liveFailure(t, post(t, s, "/msg/send", url.Values{"csrf": {owner.CSRF}, "roomid": {"999999"}, "msg": {"hello"}}, owner), 19002003, "房间不存在或已被封禁")
		liveExec(t, s, "UPDATE rooms SET banned=1 WHERE room_id=?", 21000)
		liveFailure(t, get(t, s, "/room/v1/Room/get_info?room_id=21000"), 19002003, "房间不存在或已被封禁")
		assertCode(t, get(t, s, "/room/v1/Room/get_info?room_id=21000", owner), 0)
		assertCode(t, get(t, s, "/room/v1/Room/get_info?room_id=21000", admin), 0)
		liveFailure(t, post(t, s, "/room/v1/Room/startLive", params, owner), 60009, "直播间已被封禁")
		liveExec(t, s, "UPDATE rooms SET banned=0,source=? WHERE room_id=?", `{"type":"m3u8","url":"","loop":true}`, 21000)
		liveFailure(t, post(t, s, "/room/v1/Room/startLive", params, owner), -400, "暂未接入 RTMP 推流服务，请先配置 m3u8 直播源或选择转播视频")
		liveExec(t, s, "UPDATE users SET status=1 WHERE mid=?", 2)
		liveFailure(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", owner), -403, "账号已被封禁")
	})
}

func TestLiveAutoProvision(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		liveExec(t, s, "DELETE FROM live_messages WHERE room_id=?", 20000)
		liveExec(t, s, "DELETE FROM rooms WHERE uid=?", 1)
		p := url.Values{"csrf": {admin.CSRF}}
		for _, path := range []string{"/room/v1/Room/update", "/room/v1/Room/startLive", "/xlive/app-blink/v1/streamSource/update"} {
			liveFailure(t, post(t, s, path, p, admin), -404, "尚未开通直播间")
		}
		liveWant(t, liveData(t, post(t, s, "/room/v1/Room/stopLive", p, admin)), "PREPARING", "status")
		// Simultaneous GetInfo requests must create only one room.
		var wg sync.WaitGroup
		results := make(chan *httptest.ResponseRecorder, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := httptest.NewRequest("GET", "/xlive/app-blink/v1/room/GetInfo", nil)
				r.AddCookie(&http.Cookie{Name: "SESSDATA", Value: admin.Token})
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				results <- w
			}()
		}
		wg.Wait()
		close(results)
		for w := range results {
			var v map[string]any
			if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
				t.Fatal(e)
			}
			data := liveData(t, v)
			liveWant(t, data, float64(30001), "room_id")
			liveWant(t, data, float64(0), "live_status")
			liveWant(t, data, false, "banned")
			liveWant(t, data, "push", "source", "type")
		}
		r := liveRoom(t, s, 30001)
		if r.AreaID != 646 || r.ParentAreaID != 10 || r.Media != "" || r.Cover != "" || r.Source == nil || r.Source.Type != "push" {
			t.Fatal(r)
		}
		rows, e := s.Store.ListLiveRoom(context.Background(), "WHERE uid=?", 1)
		if e != nil || len(rows) != 1 {
			t.Fatalf("%v %v", rows, e)
		}
	})
}

// T1's foreign keys prevent the Mock's acceptance of arbitrary area numbers.
// Keep this integration limitation visible without changing another task's schema.
func TestLiveUnknownAreaBusinessError(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		// Explicit fixture for this route contract, independent of demo defaults.
		liveExec(t, s, "UPDATE rooms SET source=? WHERE room_id=?", `{"type":"m3u8","url":"https://example.test/live.m3u8","loop":true,"format":"ts"}`, 20000)
		session := login(t, s, "jumedia", "admin123")
		before := liveRoom(t, s, 20000)
		for _, tt := range []struct{ path, param string }{{"/room/v1/Room/update", "area_id"}, {"/room/v1/Room/startLive", "area_v2"}} {
			v := post(t, s, tt.path, url.Values{"csrf": {session.CSRF}, tt.param: {"999999"}}, session)
			liveFailure(t, v, -400, "分区不存在")
			after := liveRoom(t, s, 20000)
			if after.AreaID != before.AreaID || after.LiveTime != before.LiveTime {
				t.Fatal("failed write changed room")
			}
		}
	})
}
