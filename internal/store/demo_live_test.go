package store_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestDemoLiveMigration010(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
				t.Fatal(err)
			}
		}
		for _, uid := range []int64{1, 2, 3, 4} {
			if err := store.InsertUser(ctx, s.DB, &store.User{MID: uid, Username: strings.Repeat("u", int(uid))}); err != nil {
				t.Fatal(err)
			}
		}
		exec("INSERT INTO live_areas(id,name) VALUES (?,?)", 1, "测试")
		exec("INSERT INTO live_area_children(id,parent_id,name) VALUES (?,?,?)", 1, 1, "测试")
		title := strings.Repeat("片", 35) + "😀尾巴"
		for _, a := range []store.Archive{
			{AID: 10, MID: 2, State: -1, Title: "未发布"}, {AID: 20, MID: 2, Title: "首P不是Emby"},
			{AID: 30, MID: 2, Title: "无分P"}, {AID: 40, MID: 4, Title: "别人的稿件"},
			{AID: 50, MID: 2, Title: title, Pic: "/upload/cover.jpg"}, {AID: 60, MID: 2, Title: "较晚稿件"},
			{AID: 70, MID: 3, Title: "第二房间"},
		} {
			a.BVID = store.AV2BV(a.AID)
			if err := store.InsertArchive(ctx, s.DB, &a); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range []store.Page{
			{CID: 100, AID: 10, Media: "emby:unpublished:s"}, {CID: 200, AID: 20, Media: "/upload/local.mp4"},
			{CID: 201, AID: 20, Position: 1, Media: "emby:second:s"}, {CID: 400, AID: 40, Media: "emby:other:s"},
			{CID: 502, AID: 50, Position: 7, Media: "emby:first:s"}, {CID: 501, AID: 50, Position: 8, Media: "emby:second:s"},
			{CID: 600, AID: 60, Media: "emby:later:s"}, {CID: 700, AID: 70, Media: "emby:room2:s"},
		} {
			if err := store.InsertPage(ctx, s.DB, &p); err != nil {
				t.Fatal(err)
			}
		}
		old := &store.StreamSource{Type: "m3u8", URL: "/media/emby/old/hls/index.m3u8", Loop: true}
		for _, r := range []store.LiveRoom{
			{RoomID: 21000, UID: 2, Source: old}, {RoomID: 21018, UID: 3, Source: old}, {RoomID: 20000, UID: 1, Source: old}, {RoomID: 40000, UID: 4, Source: old},
			{RoomID: 40001, UID: 4, Source: &store.StreamSource{Type: "m3u8", URL: "https://custom.test/media/emby/a"}},
			{RoomID: 40002, UID: 4, Source: &store.StreamSource{Type: "video", AID: 60, CID: 600}},
			{RoomID: 40003, UID: 4, Source: &store.StreamSource{Type: "push", Loop: true}}, {RoomID: 40004, UID: 4},
		} {
			r.ParentAreaID = 1
			r.AreaID = 1
			r.Title = "自定义标题"
			r.Cover = "/upload/original.jpg"
			r.Description = "保留简介"
			r.LiveStatus = 1
			r.Online = 42
			r.PendingStart = true
			r.LiveTime = 123
			if err := store.InsertLiveRoom(ctx, s.DB, &r); err != nil {
				t.Fatal(err)
			}
		}
		snapshot := func() []store.LiveRoom {
			t.Helper()
			rs, e := s.ListLiveRoom(ctx, "ORDER BY room_id")
			if e != nil {
				t.Fatal(e)
			}
			return rs
		}
		before := snapshot()
		exec("DELETE FROM schema_versions WHERE version=?", 10)
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		got := snapshot()
		for _, r := range got {
			switch r.RoomID {
			case 21000, 21018:
				aid, cid := int64(50), int64(502)
				wantTitle := "【放映厅】" + strings.Repeat("片", 35)
				full := title
				pic := "/upload/cover.jpg"
				if r.RoomID == 21018 {
					aid = 70
					cid = 700
					wantTitle = "【放映厅】第二房间"
					full = "第二房间"
					pic = ""
				}
				want := &store.StreamSource{Type: "video", Loop: true, Format: "ts", Kind: "vod", AID: aid, CID: cid}
				if !reflect.DeepEqual(r.Source, want) || r.LiveStatus != 1 || r.Title != wantTitle || r.Cover != pic || r.Description != "循环转播《"+full+"》，欢迎一起看～" {
					t.Fatalf("relay: %+v source=%+v", r, r.Source)
				}
			case 20000, 40000:
				if !reflect.DeepEqual(r.Source, &store.StreamSource{Type: "push", Loop: true}) || r.LiveStatus != 0 || r.Online != 0 || r.PendingStart {
					t.Fatalf("offline: %+v", r)
				}
				if r.Title != "自定义标题" || r.Description != "保留简介" {
					t.Fatal("offline metadata changed", r)
				}
			default:
				for _, b := range before {
					if b.RoomID == r.RoomID && !reflect.DeepEqual(b, r) {
						t.Fatal("custom source changed", r)
					}
				}
			}
		}
		for i := 0; i < 2; i++ {
			exec("DELETE FROM schema_versions WHERE version=?", 10)
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, snapshot()) {
				t.Fatal("010 replay changed data")
			}
		}
		// A selected room without an eligible archive must stop; changed source on
		// the other selected room must be preserved even during actual replay.
		raw, _ := json.Marshal(old)
		exec("UPDATE rooms SET source=? WHERE room_id=?", string(raw), 21018)
		exec("UPDATE archives SET state=? WHERE mid=?", -6, 3)
		exec("UPDATE rooms SET source=? WHERE room_id=?", `{"type":"m3u8","url":"https://custom.test/live.m3u8","loop":false}`, 21000)
		custom, _ := s.RoomByID(ctx, 21000)
		exec("DELETE FROM schema_versions WHERE version=?", 10)
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		r, e := s.RoomByID(ctx, 21018)
		if e != nil || r.Source.Type != "push" || r.PendingStart || r.LiveStatus != 0 || r.Online != 0 {
			t.Fatal(r, e)
		}
		r, e = s.RoomByID(ctx, 21000)
		if e != nil || !reflect.DeepEqual(r, custom) {
			t.Fatal("selected custom source changed", r, e)
		}
	})
}
