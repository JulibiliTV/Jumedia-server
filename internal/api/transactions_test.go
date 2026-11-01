package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

// All SQL fragments here are fixed table names, never request values.
func transactionSnapshot(t *testing.T, s *Server) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"users", "sessions", "ids", "archives", "archive_pages", "replies", "reply_reactions", "danmaku", "seasons", "episodes", "history", "banners", "rooms", "regions", "live_areas", "live_area_children", "settings", "fav_folders", "fav_items", "collections", "collection_items"} {
		rows, err := s.Store.DB.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY 1,2")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		values := [][]any{}
		for rows.Next() {
			v := make([]any, len(columns))
			p := make([]any, len(columns))
			for i := range v {
				p[i] = &v[i]
			}
			if err = rows.Scan(p...); err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out[table] = string(b)
	}
	return out
}
func TestAdminLogFailureRollsBack(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		archives, err := s.Store.ListArchive(t.Context(), "ORDER BY aid LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		seasons, err := s.Store.ListSeason(t.Context(), "ORDER BY season_id LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		rooms, err := s.Store.ListLiveRoom(t.Context(), "ORDER BY room_id LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		replies, err := s.Store.ListReply(t.Context(), "ORDER BY rpid LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		communityExec(t, s, "ALTER TABLE logs RENAME TO t10_saved_logs")
		before := transactionSnapshot(t, s)
		cases := []struct {
			path string
			p    url.Values
		}{
			{"user/update", url.Values{"mid": {"2"}, "status": {"1"}}},
			{"archive/audit", url.Values{"aid": {fmt.Sprint(archives[0].AID)}, "state": {"-2"}}},
			{"archive/delete", url.Values{"aid": {fmt.Sprint(archives[0].AID)}}},
			{"season/save", url.Values{"season": {`{"title":"rollback season","episodes":[{}]}`}}},
			{"season/delete", url.Values{"season_id": {fmt.Sprint(seasons[0].SeasonID)}}},
			{"live/room/ban", url.Values{"room_id": {fmt.Sprint(rooms[0].RoomID)}, "banned": {"true"}}},
			{"reply/delete", url.Values{"rpid": {fmt.Sprint(replies[0].RPID)}}},
			{"danmaku/delete", url.Values{"id": {"999999"}}},
			{"region/save", url.Values{"name": {"rollback region"}}},
			{"live/area/save", url.Values{"name": {"rollback area"}}},
			{"banner/save", url.Values{"title": {"rollback banner"}, "img": {"/mock/avatars/default1.png"}}},
			{"banner/delete", url.Values{"id": {"999999"}}},
			{"settings/save", url.Values{"siteName": {"rollback name"}}},
		}
		for _, tc := range cases {
			t.Run(tc.path, func(t *testing.T) {
				v := adminTestPost(t, s, session, tc.path, tc.p)
				assertCode(t, v, -500)
				after := transactionSnapshot(t, s)
				for table, want := range before {
					if after[table] != want {
						t.Errorf("%s changed despite log failure", table)
					}
				}
			})
		}
	})
}
func TestResetLogFailureRollsBack(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		communityExec(t, s, "UPDATE users SET uname=? WHERE mid=?", "重置前昵称", 2)
		communityExec(t, s, "ALTER TABLE logs RENAME TO t10_saved_logs")
		// Seed's initialization log succeeds; only the final reset audit fails.
		communityExec(t, s, `CREATE TABLE logs (id BIGINT PRIMARY KEY,mid BIGINT,action TEXT CHECK(action <> '重置站点数据'),target TEXT,ctime BIGINT)`)
		before := transactionSnapshot(t, s)
		assertCode(t, adminTestPost(t, s, session, "settings/save", url.Values{"reset": {"true"}}), -500)
		if !reflect.DeepEqual(before, transactionSnapshot(t, s)) {
			t.Fatal("reset committed although its audit failed")
		}
		if n := adminTestCount(t, s, "SELECT COUNT(*) FROM logs"); n != 0 {
			t.Fatal("initialization log survived rollback")
		}
	})
}
