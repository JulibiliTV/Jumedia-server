package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func TestDefaultAvatarRoutes(t *testing.T) {
	s := newTestServer(t)
	s.Config.PublicOrigin = "https://avatars.example.test"
	admin := login(t, s, "jumedia", "admin123")
	ctx := t.Context()
	// Empty faces after startup exercise rendering, independently of backfill.
	for _, q := range []string{"UPDATE users SET face=''", "UPDATE rooms SET live_status=0", "DELETE FROM live_messages"} {
		if _, err := s.Store.DB.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Deliberately create legacy orphan records for deleted-user fallback branches.
	if _, err := s.Store.DB.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	for _, ds := range []store.DataSource{{ID: 501, MID: 1, Name: "owned"}, {ID: 502, MID: 999, Name: "orphan"}} {
		if err := store.SaveDataSource(ctx, s.Store.DB, &ds); err != nil {
			t.Fatal(err)
		}
	}
	price := float64(30)
	for _, m := range []store.LiveMessage{{ID: 900001, RoomID: 21000, UID: 999, Text: "orphan"}, {ID: 900002, RoomID: 21000, UID: 999, Text: "orphan SC", Price: &price}} {
		if err := store.InsertLiveMessage(ctx, s.Store.DB, &m); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.InsertRelation(ctx, s.Store.DB, &store.Relation{MID: 12, FID: 999}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path   string
		mid    int64
		fields []any
	}{
		{"/x/web-interface/nav", 1, []any{"face"}},
		{"/x/web-interface/view?aid=100100", 2, []any{"owner", "face"}},
		{"/x/web-interface/view/detail?aid=100100", 2, []any{"Card", "card", "face"}},
		{"/x/space/wbi/acc/info?mid=2", 2, []any{"face"}},
		{"/x/web-interface/card?mid=2", 2, []any{"card", "face"}},
		{"/x/relation/followings?vmid=1", 2, []any{"list", 0, "face"}},
		{"/x/relation/followings?vmid=12", 999, []any{"list", 0, "face"}},
		{"/x/web-interface/wbi/search/type?search_type=bili_user&keyword=" + url.QueryEscape("流明实验室"), 2, []any{"result", 0, "upic"}},
		{"/x/v3/fav/resource/list?media_id=9010", 1, []any{"info", "upper", "face"}},
		{"/x/polymer/web-dynamic/v1/portal", 1, []any{"my_info", "face"}},
		{"/x/polymer/web-dynamic/v1/portal", 2, []any{"up_list", "items", 0, "face"}},
		{"/live_user/v1/Master/info?uid=2", 2, []any{"info", "face"}},
		{"/live_user/v1/Master/info?uid=999", 999, []any{"info", "face"}},
		{"/xlive/web-room/v1/dM/gethistory?roomid=21000", 999, []any{"room", 0, "user", "base", "face"}},
		{"/av/v1/SuperChat/getMessageList?room_id=21000", 999, []any{"list", 0, "user_info", "face"}},
		{"/x/admin/users?keyword=jumedia", 1, []any{"list", 0, "face"}},
		{"/x/web/datasources", 1, []any{"list", 0, "owner", "face"}},
		{"/x/admin/datasources?keyword=orphan", 999, []any{"list", 0, "owner", "face"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			v := get(t, s, tc.path, admin)
			assertCode(t, v, 0)
			if got := liveField(t, v["data"], tc.fields...); got != s.Config.PublicOrigin+seed.Avatar(tc.mid) {
				t.Fatalf("face = %v", got)
			}
			req := httptest.NewRequest("POST", tc.path, strings.NewReader("x=%zz"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			var bad map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &bad); err != nil {
				t.Fatal(err)
			}
			assertCode(t, bad, -400)
		})
	}
	assertCode(t, get(t, s, "/x/web-interface/nav"), -101)
	// Reads do not rewrite persisted profiles; empty update keeps a custom face.
	u, err := s.Store.UserByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if u.Face != "" {
		t.Fatal("render mutated user")
	}
	if _, err = s.Store.DB.ExecContext(ctx, "UPDATE users SET face=? WHERE mid=?", "/upload/keep.png", 1); err != nil {
		t.Fatal(err)
	}
	assertCode(t, post(t, s, "/x/member/web/update", url.Values{"csrf": {admin.CSRF}, "uname": {u.Uname}, "face": {""}}, admin), 0)
	v := get(t, s, "/x/web-interface/nav", admin)
	assertCode(t, v, 0)
	liveWant(t, v, s.Config.PublicOrigin+"/upload/keep.png", "data", "face")
	s.Config.PublicOrigin = ""
	v = get(t, s, "/x/space/wbi/acc/info?mid=2")
	assertCode(t, v, 0)
	liveWant(t, v, "http://example.com"+seed.Avatar(2), "data", "face")
}

func TestDefaultAvatarHistoryAndMessages(t *testing.T) {
	s := newTestServer(t)
	session := login(t, s, "jumedia", "admin123")
	ctx := t.Context()
	if _, err := s.Store.DB.ExecContext(ctx, "UPDATE users SET face='' WHERE mid=?", 2); err != nil {
		t.Fatal(err)
	}
	eps, err := s.Store.ListEpisode(ctx, "ORDER BY ep_id LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	ep := eps[0]
	if err = store.InsertHistory(ctx, s.Store.DB, &store.History{MID: 1, AID: ep.AID, CID: ep.CID, EpID: ep.EpID, SID: ep.SeasonID, ViewAt: 9999999999}); err != nil {
		t.Fatal(err)
	}
	v := get(t, s, "/x/web-interface/history/cursor", session)
	assertCode(t, v, 0)
	rows := liveField(t, v, "data", "list").([]any)
	pgc, archive := false, false
	for _, raw := range rows {
		row := raw.(map[string]any)
		mid := int64(row["author_mid"].(float64))
		if mid == 0 {
			pgc = true
			if row["author_face"] != "" {
				t.Fatal("PGC acquired user avatar", row)
			}
		}
		if mid == 2 {
			archive = true
			if row["author_face"] != "http://example.com"+seed.Avatar(2) {
				t.Fatal(row)
			}
		}
	}
	if !pgc || !archive {
		t.Fatal("missing history fixtures")
	}
	assertCode(t, get(t, s, "/x/web-interface/history/cursor"), -101)
	user := &store.User{MID: 2, Uname: "empty"}
	packet := liveChatDanmu(&store.LiveMessage{ID: 1, UID: 2}, user, "https://public.test", nil, nil)
	packets, err := decodeLivePackets(packet)
	if err != nil {
		t.Fatal(err)
	}
	body := chatJSON(t, packets[0])
	liveWant(t, body, "https://public.test"+seed.Avatar(2), "info", 0, 15, "user", "base", "face")
}
