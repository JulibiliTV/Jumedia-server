package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func TestPgcSeasonDetail(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := login(t, srv, "jumedia", "admin123")
		season := pgcSeedSeason(t, srv, 40000)
		ep := season.Episodes[1]
		for i, item := range []store.History{
			{MID: 1, AID: season.Episodes[0].AID, CID: season.Episodes[0].CID, EpID: season.Episodes[0].EpID, SID: season.SeasonID, ViewAt: 100, Progress: 5},
			{MID: 1, AID: ep.AID, CID: ep.CID, EpID: ep.EpID, SID: season.SeasonID, ViewAt: 200, Progress: 42},
			{MID: 2, AID: ep.AID, CID: ep.CID, EpID: ep.EpID, SID: season.SeasonID, ViewAt: 300, Progress: 999},
		} {
			if err := store.InsertHistory(t.Context(), srv.Store.DB, &item); err != nil {
				t.Fatalf("history %d: %v", i, err)
			}
		}
		detail := pgcPayload(t, srv, "/pgc/view/web/season?season_id=40000", "result", session)
		byEP := pgcPayload(t, srv, fmt.Sprintf("/pgc/view/web/season?season_id=999&ep_id=%d", ep.EpID), "result", session)
		if !reflect.DeepEqual(detail, byEP) {
			t.Fatal("ep_id must take precedence")
		}
		pgcFields(t, detail, map[string]any{"season_id": float64(season.SeasonID), "media_id": float64(season.MediaID), "season_title": season.Title, "title": season.Title, "type": float64(season.SeasonType), "total": float64(len(season.Episodes)), "evaluate": season.Evaluate})
		publish := pgcObject(t, detail["publish"])
		pgcFields(t, publish, map[string]any{"pub_time": time.Unix(season.Ctime, 0).UTC().Format("2006-01-02"), "weekday": float64(season.Weekday)})
		pgcFields(t, pgcObject(t, detail["rating"]), map[string]any{"score": season.Score, "count": float64(season.Follow / 30)})
		pgcFields(t, pgcObject(t, detail["stat"]), map[string]any{"views": float64(season.Views), "favorites": float64(season.Follow), "follow": float64(season.Follow), "danmakus": float64(season.Danmakus), "coins": float64(season.Views / 200), "likes": float64(season.Views / 40), "reply": float64(season.Views / 900), "share": float64(season.Views / 1200)})
		status := pgcObject(t, detail["user_status"])
		pgcFields(t, status, map[string]any{"follow": float64(1), "follow_status": float64(2)})
		pgcFields(t, pgcObject(t, status["progress"]), map[string]any{"last_ep_id": float64(ep.EpID), "last_time": float64(42), "last_ep_index": ep.Title})
		eps := pgcArray(t, detail["episodes"])
		for i, item := range eps {
			e := season.Episodes[i]
			pgcFields(t, pgcObject(t, item), map[string]any{"id": float64(e.EpID), "ep_id": float64(e.EpID), "aid": float64(e.AID), "cid": float64(e.CID), "bvid": store.AV2BV(e.AID), "duration": float64(e.Duration * 1000), "title": e.Title, "long_title": e.LongTitle, "show_title": fmt.Sprintf("第%s话 %s", e.Title, e.LongTitle), "index": float64(i + 1), "link": fmt.Sprintf("/bangumi/play/ep%d", e.EpID), "badge": e.Badge})
		}
		newEP := pgcObject(t, detail["new_ep"])
		pgcFields(t, newEP, map[string]any{"id": float64(season.Episodes[len(season.Episodes)-1].EpID), "title": season.Episodes[len(season.Episodes)-1].Title})
		if len(pgcArray(t, detail["section"])) != 0 {
			t.Fatal("section must be empty")
		}
		for i, area := range pgcArray(t, detail["areas"]) {
			pgcFields(t, pgcObject(t, area), map[string]any{"id": float64(i + 1), "name": season.Areas[i]})
		}
		pgcFields(t, pgcObject(t, detail["up_info"]), map[string]any{"mid": float64(1), "uname": "聚媒番剧"})
		guest := pgcObject(t, pgcPayload(t, srv, "/pgc/view/web/season?season_id=40000", "result")["user_status"])
		pgcFields(t, guest, map[string]any{"follow": float64(0), "follow_status": float64(0), "progress": map[string]any{}})
		for _, path := range []string{"/pgc/view/web/season", "/pgc/view/web/season?season_id=999", "/pgc/view/web/season?season_id=40000&ep_id=999", "/pgc/view/web/season?season_id=40000.5"} {
			response := get(t, srv, path)
			assertCode(t, response, -404)
			if response["message"] != "番剧不存在" {
				t.Fatal("detail error message")
			}
		}
		pgcExec(t, srv, "UPDATE seasons SET state=-1 WHERE season_id=?", season.SeasonID)
		assertCode(t, get(t, srv, "/pgc/view/web/season?season_id=40000", session), -404)
		// A film uses 正片 and a finished season describes the final count.
		all, err := srv.Store.ListSeason(t.Context(), "WHERE season_type=2 ORDER BY season_id LIMIT 1")
		if err != nil || len(all) == 0 {
			t.Fatal("missing film fixture")
		}
		film := pgcSeedSeason(t, srv, all[0].SeasonID)
		pgcExec(t, srv, "UPDATE seasons SET is_finish=1 WHERE season_id=?", film.SeasonID)
		filmData := pgcPayload(t, srv, fmt.Sprintf("/pgc/view/web/season?season_id=%d", film.SeasonID), "result")
		pgcFields(t, pgcObject(t, pgcArray(t, filmData["episodes"])[0]), map[string]any{"show_title": "正片"})
		pgcFields(t, pgcObject(t, filmData["new_ep"]), map[string]any{"is_new": float64(0), "desc": fmt.Sprintf("已完结, 全%d话", len(film.Episodes))})
	})
}

func TestPgcPlayURLContract(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		season := pgcSeedSeason(t, srv, 40000)
		ep := season.Episodes[0]
		if err := srv.Store.SetSetting(t.Context(), "allowGuestPlay", false); err != nil {
			t.Fatal(err)
		}
		pgcExec(t, srv, "UPDATE seasons SET state=-1 WHERE season_id=?", season.SeasonID)
		srv.Config.PublicOrigin = "https://media.example.test/base"
		for _, tc := range []struct {
			qn      string
			quality float64
		}{{"", 80}, {"120", 80}, {"80", 80}, {"79", 64}, {"64", 64}, {"63", 32}, {"0", 32}, {"-1", 32}, {"NaN", 80}, {"0x50", 80}} {
			path := fmt.Sprintf("/pgc/player/web/playurl?ep_id=%d&qn=%s", ep.EpID, tc.qn)
			data := pgcPayload(t, srv, path, "result")
			pgcFields(t, data, map[string]any{"quality": tc.quality, "from": "local", "result": "suee", "format": "mp4", "timelength": float64(ep.Duration * 1000), "accept_format": "mp4,mp4720,mp4480", "video_codecid": float64(7)})
			pgcFields(t, pgcObject(t, pgcArray(t, data["durl"])[0]), map[string]any{"url": "https://media.example.test/media/emby/item0_0.mp4?source=source_item0_0", "length": float64(ep.Duration * 1000), "order": float64(1), "size": float64(0), "ahead": "", "vhead": "", "backup_url": []any{}})
			if !reflect.DeepEqual(data["accept_quality"], []any{float64(80), float64(64), float64(32)}) {
				t.Fatal("qualities")
			}
			formats := pgcArray(t, data["support_formats"])
			pgcFields(t, pgcObject(t, formats[0]), map[string]any{"quality": float64(80), "format": "flv", "new_description": "1080P 高清", "display_desc": "1080P"})
		}
		if pgcSeedSeason(t, srv, season.SeasonID).Views != season.Views+10 {
			t.Fatal("play count must increment once per playurl")
		}
		for _, path := range []string{"/pgc/player/web/playurl", "/pgc/player/web/playurl?ep_id=999", "/pgc/player/web/playurl?ep_id=300001.5"} {
			response := get(t, srv, path)
			assertCode(t, response, -404)
			if response["message"] != "剧集不存在" {
				t.Fatal("playurl error message")
			}
		}
		for _, media := range []string{"https://cdn.example.test/a.mp4?token=a%20b", "/upload/a.mp4", "mock/media/a.mp4", "//cdn.example.test/a.mp4"} {
			pgcExec(t, srv, "UPDATE episodes SET media=? WHERE ep_id=?", media, ep.EpID)
			data := pgcPayload(t, srv, fmt.Sprintf("/pgc/player/web/playurl?ep_id=%d", ep.EpID), "result")
			base, _ := url.Parse(srv.Config.PublicOrigin + "/")
			ref, _ := url.Parse(media)
			pgcFields(t, pgcObject(t, pgcArray(t, data["durl"])[0]), map[string]any{"url": base.ResolveReference(ref).String()})
		}
		srv.Config.PublicOrigin = ""
		pgcExec(t, srv, "UPDATE episodes SET media=? WHERE ep_id=?", "/upload/test.mp4", ep.EpID)
		req := httptest.NewRequest("GET", fmt.Sprintf("http://backend.test/pgc/player/web/playurl?ep_id=%d", ep.EpID), nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "video.example.test")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		assertCode(t, response, 0)
		pgcFields(t, pgcObject(t, pgcArray(t, pgcObject(t, response["result"])["durl"])[0]), map[string]any{"url": "https://video.example.test/upload/test.mp4"})
	})
}

func TestPgcFollowLifecycle(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := login(t, srv, "jumedia", "admin123")
		sid := int64(40011)
		season := pgcSeedSeason(t, srv, sid)
		params := url.Values{"season_id": {fmt.Sprint(sid)}, "csrf": {session.CSRF}}
		statusPath := fmt.Sprintf("/pgc/view/web/season/user/status?season_id=%d", sid)
		pgcFields(t, pgcPayload(t, srv, statusPath, "result"), map[string]any{"follow": float64(0), "follow_status": float64(0), "vip": float64(0), "pay": float64(0), "sponsor": float64(0)})
		for _, path := range []string{"/pgc/web/follow/add", "/pgc/web/follow/del", "/pgc/web/follow/status/update"} {
			response := post(t, srv, path, params)
			assertCode(t, response, -101)
			if response["message"] != "账号未登录" {
				t.Fatal("login message")
			}
			response = post(t, srv, path, url.Values{"season_id": {fmt.Sprint(sid)}, "csrf": {"wrong"}}, session)
			assertCode(t, response, -111)
			if response["message"] != "csrf 校验失败" {
				t.Fatal("csrf message")
			}
		}
		missing := post(t, srv, "/pgc/web/follow/status/update", params, session)
		assertCode(t, missing, -404)
		if missing["message"] != "尚未追番" {
			t.Fatal("update error message")
		}
		for i := 0; i < 2; i++ {
			added := post(t, srv, "/pgc/web/follow/add", params, session)
			assertCode(t, added, 0)
			pgcFields(t, pgcObject(t, added["result"]), map[string]any{"toast": "追番成功", "relation": true, "status": float64(2)})
		}
		if pgcSeedSeason(t, srv, sid).Follow != season.Follow+1 {
			t.Fatal("duplicate add incremented count")
		}
		current := pgcPayload(t, srv, statusPath, "result", session)
		pgcFields(t, current, map[string]any{"follow": float64(1), "follow_status": float64(1)})
		user, err := srv.Store.UserByID(t.Context(), session.MID)
		if err != nil {
			t.Fatal(err)
		}
		if current["vip"] != float64(user.VIP) {
			t.Fatal("VIP")
		}
		// Access-key auth and biliCSRF alias use the same common pipeline.
		updated := post(t, srv, "/pgc/web/follow/status/update", url.Values{"season_id": {fmt.Sprint(sid)}, "status": {"3"}, "access_key": {session.Access}, "biliCSRF": {session.CSRF}})
		assertCode(t, updated, 0)
		pgcFields(t, pgcObject(t, updated["result"]), map[string]any{"toast": "已更新"})
		pgcFields(t, pgcPayload(t, srv, statusPath, "result", session), map[string]any{"follow_status": float64(3)})
		assertCode(t, post(t, srv, "/pgc/web/follow/add", params, session), 0)
		pgcFields(t, pgcPayload(t, srv, statusPath, "result", session), map[string]any{"follow_status": float64(3)})
		// Follow lists are public and read the requested user's latest PGC history.
		ep := season.Episodes[1]
		history := store.History{MID: 1, AID: ep.AID, CID: ep.CID, EpID: ep.EpID, SID: sid, Progress: 33, ViewAt: time.Now().Unix()}
		if err := store.InsertHistory(t.Context(), srv.Store.DB, &history); err != nil {
			t.Fatal(err)
		}
		kind := pgcFollowKind(season.SeasonType)
		data := pgcPayload(t, srv, "/x/space/bangumi/follow/list?vmid=1&follow_status=3&pn=1&ps=1&type="+kind, "data")
		pgcFields(t, data, map[string]any{"pn": float64(1), "ps": float64(1), "total": float64(1)})
		rows := pgcArray(t, data["list"])
		pgcFields(t, pgcObject(t, rows[0]), map[string]any{"season_id": float64(sid), "follow_status": float64(3), "progress": "看到第" + ep.Title + "话"})
		renewal := ""
		if !season.IsFinish {
			renewal = fmt.Sprintf("每周%s %s", pgcWeekday(season.Weekday), season.PubTime)
		}
		pgcFields(t, pgcObject(t, rows[0]), map[string]any{"renewal_time": renewal})
		all := pgcPayload(t, srv, "/x/space/bangumi/follow/list?vmid=1&type="+kind, "data")
		pgcFields(t, all, map[string]any{"pn": float64(1), "ps": float64(15), "total": float64(pgcFollowCount(t, srv, 1, kind))})
		if pgcObject(t, pgcArray(t, all["list"])[0])["season_id"] != float64(sid) {
			t.Fatal("mtime descending")
		}
		for _, query := range []string{"vmid=1&pn=99", "vmid=999", "vmid=1&follow_status=99"} {
			data := pgcPayload(t, srv, "/x/space/bangumi/follow/list?type=1&"+query, "data")
			if len(pgcArray(t, data["list"])) != 0 {
				t.Fatal("empty follow page")
			}
		}
		// No validation of status enum in the mock; missing/invalid strings default 1.
		for _, value := range []string{"9", "invalid", ""} {
			params.Set("status", value)
			assertCode(t, post(t, srv, "/pgc/web/follow/status/update", params, session), 0)
			want := float64(1)
			if value == "9" {
				want = 9
			}
			pgcFields(t, pgcPayload(t, srv, statusPath, "result", session), map[string]any{"follow_status": want})
		}
		for i := 0; i < 2; i++ {
			deleted := post(t, srv, "/pgc/web/follow/del", params, session)
			assertCode(t, deleted, 0)
			pgcFields(t, pgcObject(t, deleted["result"]), map[string]any{"toast": "已取消追番", "relation": false, "status": float64(0)})
		}
		if pgcSeedSeason(t, srv, sid).Follow != season.Follow+1 {
			t.Fatal("del must retain cumulative count")
		}
		pgcFields(t, pgcPayload(t, srv, statusPath, "result", session), map[string]any{"follow": float64(0), "follow_status": float64(0)})
		assertCode(t, post(t, srv, "/pgc/web/follow/add", params, session), 0)
		if pgcSeedSeason(t, srv, sid).Follow != season.Follow+2 {
			t.Fatal("re-add increments count")
		}
		params.Set("season_id", "999")
		invalid := post(t, srv, "/pgc/web/follow/add", params, session)
		assertCode(t, invalid, -404)
		if invalid["message"] != "番剧不存在" {
			t.Fatal("FK validation message")
		}
		pgcExec(t, srv, "UPDATE users SET status=1 WHERE mid=?", session.MID)
		for _, path := range []string{"/pgc/web/follow/add", "/pgc/web/follow/del", "/pgc/web/follow/status/update"} {
			response := post(t, srv, path, params, session)
			assertCode(t, response, -403)
			if response["message"] != "账号已被封禁" {
				t.Fatal("banned message")
			}
		}
	})
}

func TestPgcFollowTransactionRollback(t *testing.T) {
	srv := newTestServer(t)
	session := login(t, srv, "jumedia", "admin123")
	// Force the second write to fail and verify that the new follow rolls back.
	pgcExec(t, srv, "CREATE TRIGGER pgc_reject_count BEFORE UPDATE OF follow ON seasons BEGIN SELECT RAISE(ABORT, 'test rollback'); END")
	params := url.Values{"season_id": {"40011"}, "csrf": {session.CSRF}}
	response := post(t, srv, "/pgc/web/follow/add", params, session)
	assertCode(t, response, -500)
	pgcFields(t, pgcPayload(t, srv, "/pgc/view/web/season/user/status?season_id=40011", "result", session), map[string]any{"follow": float64(0)})
	pgcExec(t, srv, "DROP TRIGGER pgc_reject_count")
	assertCode(t, post(t, srv, "/pgc/web/follow/add", params, session), 0)
}

func TestPgcQueriesDoNotInterpolateInput(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		for _, path := range []string{"/pgc/season/index/result?order=", "/pgc/season/rank/list?style_id=", "/pgc/page/web/feed?coursor=", "/x/space/bangumi/follow/list?type=1&vmid="} {
			response := get(t, srv, path+url.QueryEscape("1; DROP TABLE seasons; --"))
			assertCode(t, response, 0)
		}
		pgcSeedSeason(t, srv, 40000)
	})
}

func TestPgcNumericDatabaseBoundaries(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := login(t, srv, "jumedia", "admin123")
		for _, number := range []string{"40000.5", "1e100"} {
			pgcFields(t, pgcPayload(t, srv, "/pgc/view/web/season/user/status?season_id="+number, "result", session), map[string]any{"follow": float64(0)})
			response := post(t, srv, "/pgc/web/follow/del", url.Values{"season_id": {number}, "csrf": {session.CSRF}}, session)
			assertCode(t, response, 0)
		}
		pgcFields(t, pgcPayload(t, srv, "/pgc/view/web/season/user/status?season_id=40000", "result", session), map[string]any{"follow": float64(1)})
		for _, query := range []string{"vmid=1.5", "vmid=1e100", "vmid=1&follow_status=2.5"} {
			result := pgcPayload(t, srv, "/x/space/bangumi/follow/list?type=1&"+query, "data")
			if len(pgcArray(t, result["list"])) != 0 {
				t.Fatal("nonintegral identifier matched an integer row")
			}
		}
		for _, status := range []string{"1.5", "2147483648", "-2147483649"} {
			response := post(t, srv, "/pgc/web/follow/status/update", url.Values{"season_id": {"40000"}, "status": {status}, "csrf": {session.CSRF}}, session)
			assertCode(t, response, -400)
		}
		pgcFields(t, pgcPayload(t, srv, "/pgc/view/web/season/user/status?season_id=40000", "result", session), map[string]any{"follow_status": float64(2)})
	})
}

// pgcFollowKind is the follow/list `type` a season belongs to: 1 追番 (番剧 / 国创), 2 追剧.
func pgcFollowKind(seasonType int) string {
	if seasonType == 1 || seasonType == 4 {
		return "1"
	}
	return "2"
}

func pgcFollowCount(t *testing.T, srv *Server, mid int64, kind string) int {
	t.Helper()
	rows, err := srv.Store.ListBangumiFollow(t.Context(), "WHERE mid=?", mid)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range rows {
		if pgcFollowKind(pgcSeedSeason(t, srv, f.SeasonID).SeasonType) == kind {
			n++
		}
	}
	return n
}
