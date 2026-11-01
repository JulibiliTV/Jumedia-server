package api

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func adminTestSeasonSave(t *testing.T, s *Server, session *store.Session, input any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return adminTestPost(t, s, session, "season/save", url.Values{"season": {string(raw)}})
}
func TestAdminSeasonsWorkflow(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		adminTestError(t, adminTestSeasonSave(t, s, session, map[string]any{}), -400, "请填写标题")
		adminTestError(t, adminTestPost(t, s, session, "season/save", url.Values{"season": {"{"}}), -500, "服务器内部错误")
		adminTestError(t, adminTestSeasonSave(t, s, session, map[string]any{"title": "invalid", "episodes": "invalid"}), -500, "服务器内部错误")
		adminTestError(t, adminTestPost(t, s, session, "season/save", url.Values{"season": {""}}), -500, "服务器内部错误")
		now := time.Now().Unix()
		data := adminTestData(t, adminTestSeasonSave(t, s, session, map[string]any{"title": "T9 番剧", "episodes": []any{map[string]any{}, map[string]any{"title": "OVA", "duration": "120", "long_title": "番外", "badge": "会员"}}}))
		id := int64(data["season_id"].(float64))
		season, err := s.Store.SeasonByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if season.Title != "T9 番剧" || season.MediaID == 0 || season.SeasonType != 1 || season.Weekday != 1 || season.PubTime != "20:00" || season.Score != 0 || season.State != 0 || season.Ctime < now || len(season.Episodes) != 2 || season.Views != 0 {
			t.Fatalf("season defaults: %+v", season)
		}
		if season.Cover != "" || season.Horizontal != "" {
			t.Fatal("default covers")
		}
		ep := season.Episodes[0]
		if ep.Title != "1" || ep.Duration != 1440 || ep.Media != "" || ep.PubTime < now || ep.LongTitle != "" || ep.Badge != "" {
			t.Fatalf("episode defaults: %+v", ep)
		}
		if ep.EpID == ep.AID || ep.AID == ep.CID || ep.CID >= id {
			t.Fatal("episode IDs should be allocated before season IDs")
		}
		if season.Episodes[1].Title != "OVA" || season.Episodes[1].Duration != 120 || season.Episodes[1].Badge != "会员" {
			t.Fatal("episode input fields")
		}
		adminTestLog(t, s, "新建番剧", "T9 番剧")
		list := adminTestList(t, get(t, s, "/x/admin/seasons?keyword=T9&season_type=1", session))
		if len(list) != 1 {
			t.Fatal("season filter")
		}
		row := list[0].(map[string]any)
		if len(row["episodes"].([]any)) != 2 || row["season_id"] != float64(id) {
			t.Fatal("episode projection")
		}
		if row["areas"] == nil || row["styles"] == nil || row["is_finish"] != false {
			t.Fatal("season field types")
		}

		if _, err = s.Store.DB.ExecContext(t.Context(), "UPDATE seasons SET views=123,follow=45,danmakus=6 WHERE season_id=?", id); err != nil {
			t.Fatal(err)
		}
		h := store.History{MID: session.MID, AID: ep.AID, CID: ep.CID, EpID: ep.EpID, SID: id, Progress: 30, ViewAt: now}
		if err = store.InsertHistory(t.Context(), s.Store.DB, &h); err != nil {
			t.Fatal(err)
		}
		oldSecond := season.Episodes[1]
		input := map[string]any{"season_id": id, "title": "T9 编辑", "season_type": "4", "areas": []string{"中国"}, "styles": []string{"日常"}, "is_finish": true, "score": "8.7", "weekday": 5, "pub_time": "19:30", "cover": "/cover.png", "horizontal": "/horizontal.png", "state": -1, "episodes": []any{map[string]any{"ep_id": ep.EpID, "aid": ep.AID, "cid": ep.CID, "title": "重命名", "long_title": "修改", "media": "/edit.mp4", "duration": 300}, map[string]any{"title": 3}}}
		data = adminTestData(t, adminTestSeasonSave(t, s, session, input))
		if data["season_id"] != float64(id) {
			t.Fatal("edit must preserve ID")
		}
		edited, err := s.Store.SeasonByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if edited.MediaID != season.MediaID || edited.Ctime != season.Ctime || edited.Views != 123 || edited.Follow != 45 || edited.Danmakus != 6 || edited.Score != 8.7 || !edited.IsFinish || edited.SeasonType != 4 || edited.State != -1 || edited.Cover != "/cover.png" {
			t.Fatalf("edited season: %+v", edited)
		}
		if edited.Episodes[0].EpID != ep.EpID || edited.Episodes[0].Media != "/edit.mp4" || edited.Episodes[1].Title != "3" || edited.Episodes[1].EpID == oldSecond.EpID {
			t.Fatal("episode add/update/delete failed")
		}
		if adminTestCount(t, s, "SELECT COUNT(*) FROM episodes WHERE ep_id=?", oldSecond.EpID) != 0 {
			t.Fatal("removed episode exists")
		}
		var savedEp int64
		if err = s.Store.DB.QueryRowContext(t.Context(), "SELECT epid FROM history WHERE mid=? AND aid=?", h.MID, h.AID).Scan(&savedEp); err != nil || savedEp != ep.EpID {
			t.Fatalf("retained episode lost watch history: %d %v", savedEp, err)
		}
		adminTestLog(t, s, "编辑番剧", "T9 编辑")

		// Duplicate IDs fail on both SQL engines; the old season, episodes, IDs and log
		// must survive together when an insertion fails partway through replacement.
		var sequence int64
		if err = s.Store.DB.QueryRowContext(t.Context(), "SELECT value FROM ids WHERE name=?", "global").Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		logCount := adminTestCount(t, s, "SELECT COUNT(*) FROM logs")
		input["title"] = "should roll back"
		input["episodes"] = []any{map[string]any{"ep_id": ep.EpID, "title": "A"}, map[string]any{"ep_id": ep.EpID, "title": "B"}}
		adminTestError(t, adminTestSeasonSave(t, s, session, input), -400, "剧集 ID 重复（ep_id）")
		restored, err := s.Store.SeasonByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if restored.Title != edited.Title || len(restored.Episodes) != 2 || restored.Episodes[0].Media != "/edit.mp4" {
			t.Fatal("failed save partially committed")
		}
		var after int64
		if err = s.Store.DB.QueryRowContext(t.Context(), "SELECT value FROM ids WHERE name=?", "global").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != sequence || adminTestCount(t, s, "SELECT COUNT(*) FROM logs") != logCount {
			t.Fatal("failed save changed sequence/logs")
		}

		assertCode(t, adminTestPost(t, s, session, "banner/save", url.Values{"img": {"/mock/avatars/default1.png"}, "title": {"保留链接"}, "url": {"/bangumi/play/ss" + adminID(id)}, "position": {"anime"}}), 0)
		assertCode(t, adminTestPost(t, s, session, "season/delete", url.Values{"season_id": {adminID(id)}}), 0)
		adminTestLog(t, s, "删除番剧", "T9 编辑")
		if adminTestCount(t, s, "SELECT COUNT(*) FROM seasons WHERE season_id=?", id) != 0 || adminTestCount(t, s, "SELECT COUNT(*) FROM episodes WHERE season_id=?", id) != 0 {
			t.Fatal("season/episodes not deleted")
		}
		banners := adminTestList(t, get(t, s, "/x/admin/banners", session))
		found := false
		for _, b := range banners {
			v := b.(map[string]any)
			if v["title"] == "保留链接" {
				found = true
				if v["season_id"] != float64(id) {
					t.Fatal("banner should preserve parsed season ID")
				}
			}
		}
		if !found {
			t.Fatal("season deletion removed unrelated banner")
		}
		assertCode(t, adminTestPost(t, s, session, "season/delete", url.Values{"season_id": {adminID(id)}}), 0)
	})
}

func TestAdminSeasonsCounts(t *testing.T) {
	s := newTestServer(t)
	assertCode(t, get(t, s, "/x/admin/seasons"), -101)
	viewer := login(t, s, "lumen", "demo123")
	assertCode(t, get(t, s, "/x/admin/seasons", viewer), -403)
	admin := login(t, s, "jumedia", "admin123")
	for i, item := range []struct {
		title       string
		kind, state int
	}{
		{"T24 Alpha", 1, 0}, {"T24 Alpha second", 1, -1}, {"T24 Beta", 4, 0}, {"T24 Alpha film", 2, -1},
	} {
		data := adminTestData(t, adminTestSeasonSave(t, s, admin, map[string]any{"title": item.title, "season_type": item.kind, "state": item.state}))
		liveExec(t, s, "UPDATE seasons SET ctime=? WHERE season_id=?", 100+i, data["season_id"])
	}
	for _, tc := range []struct {
		query       string
		counts      map[string]any
		total, size int
		title       string
	}{
		{"keyword=T24", map[string]any{"0": float64(4), "1": float64(2), "2": float64(1), "4": float64(1)}, 4, 4, "T24 Alpha film"},
		{"keyword=t24+ALPHA&season_type=1&pn=2&ps=1", map[string]any{"0": float64(3), "1": float64(2), "2": float64(1)}, 2, 1, "T24 Alpha"},
		{"keyword=T24&season_type=99", map[string]any{"0": float64(4), "1": float64(2), "2": float64(1), "4": float64(1)}, 0, 0, ""},
		{"keyword=T24+missing", map[string]any{"0": float64(0)}, 0, 0, ""},
		{"keyword=T24&pn=20&ps=1", map[string]any{"0": float64(4), "1": float64(2), "2": float64(1), "4": float64(1)}, 4, 0, ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			resp := get(t, s, "/x/admin/seasons?"+tc.query, admin)
			assertCode(t, resp, 0)
			data := adminTestData(t, resp)
			if !reflect.DeepEqual(data["counts"], tc.counts) {
				t.Fatalf("counts = %#v, want %#v", data["counts"], tc.counts)
			}
			page := data["page"].(map[string]any)
			list := data["list"].([]any)
			if page["total"] != float64(tc.total) || len(list) != tc.size {
				t.Fatal(data)
			}
			if tc.title != "" && list[0].(map[string]any)["title"] != tc.title {
				t.Fatal(list)
			}
			if tc.size == 1 && (page["pn"] != float64(2) || page["ps"] != float64(1)) {
				t.Fatal(page)
			}
		})
	}
	// Empty keyword includes every season, independently of page size.
	seasons, err := s.Store.ListSeason(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"0": float64(len(seasons))}
	for _, season := range seasons {
		key := adminID(int64(season.SeasonType))
		count, _ := want[key].(float64)
		want[key] = count + 1
	}
	data := adminTestData(t, get(t, s, "/x/admin/seasons?ps=1", admin))
	if !reflect.DeepEqual(data["counts"], want) {
		t.Fatal(data["counts"])
	}
}
