package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func adminTestData(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	assertCode(t, result, 0)
	data, ok := result["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object: %v", result)
	}
	return data
}
func adminTestList(t *testing.T, result map[string]any) []any {
	t.Helper()
	v, ok := adminTestData(t, result)["list"].([]any)
	if !ok {
		t.Fatal("list must be an array")
	}
	return v
}
func adminTestPost(t *testing.T, s *Server, session *store.Session, path string, params url.Values) map[string]any {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("csrf", session.CSRF)
	return post(t, s, "/x/admin/"+path, params, session)
}
func adminTestError(t *testing.T, result map[string]any, code int, message string) {
	t.Helper()
	assertCode(t, result, code)
	if result["message"] != message {
		t.Fatalf("message: %v", result)
	}
}
func adminTestCount(t *testing.T, s *Server, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.Store.DB.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func adminTestLog(t *testing.T, s *Server, action, target string) {
	t.Helper()
	rows, err := s.Store.ListLog(t.Context(), "ORDER BY id DESC LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != action || rows[0].Target != target {
		t.Fatalf("log: %+v", rows)
	}
}
func TestAdminUsersAndSessions(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		users, err := s.Store.ListUser(t.Context(), "WHERE role=? ORDER BY mid", "user")
		if err != nil {
			t.Fatal(err)
		}
		u := users[0]
		one := login(t, s, u.Username, "demo123")
		two := login(t, s, u.Username, "demo123")
		data := adminTestData(t, get(t, s, "/x/admin/users?keyword="+url.QueryEscape(strings.ToUpper(u.Username))+"&role=user&status=0", session))
		list := data["list"].([]any)
		if len(list) != 1 {
			t.Fatalf("filtered users: %v", data["page"])
		}
		row := list[0].(map[string]any)
		for _, k := range []string{"mid", "uname", "username", "face", "role", "status", "coins", "level", "email", "sign", "ctime", "archives", "follower"} {
			if _, ok := row[k]; !ok {
				t.Errorf("missing %s", k)
			}
		}
		if _, ok := row["password"]; ok {
			t.Fatal("password disclosed")
		}
		if row["archives"] != float64(adminTestCount(t, s, "SELECT COUNT(*) FROM archives WHERE mid=?", u.MID)) {
			t.Fatal("archive count mismatch")
		}
		for _, p := range []url.Values{{"status": {"1"}}, {"role": {"user"}}, {"status": {"2"}}, {"role": {"unknown"}}} {
			p.Set("mid", adminID(session.MID))
			adminTestError(t, adminTestPost(t, s, session, "user/update", p), -400, "不能修改自己的管理权限或封禁自己")
		}
		adminTestError(t, adminTestPost(t, s, session, "user/update", url.Values{"mid": {"999999999"}}), -404, "用户不存在")
		adminTestError(t, adminTestPost(t, s, session, "user/update", url.Values{"mid": {"2.5"}}), -404, "用户不存在")
		r := adminTestPost(t, s, session, "user/update", url.Values{"mid": {adminID(u.MID)}, "coins": {"1.25"}, "role": {"admin"}})
		assertCode(t, r, 0)
		if r["data"] != nil {
			t.Fatal("write response must be null")
		}
		assertCode(t, get(t, s, "/x/admin/settings", one), 0)
		assertCode(t, adminTestPost(t, s, session, "user/update", url.Values{"mid": {adminID(u.MID)}, "status": {"2"}}), 0)
		for _, sess := range []*store.Session{one, two} {
			assertCode(t, get(t, s, "/x/admin/settings", sess), -101)
		}
		if n := adminTestCount(t, s, "SELECT COUNT(*) FROM sessions WHERE mid=?", u.MID); n != 0 {
			t.Fatalf("sessions: %d", n)
		}
		got, err := s.Store.UserByID(t.Context(), u.MID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Coins != 1.25 || got.Status != 1 || got.Role != "admin" {
			t.Fatalf("updated user: %+v", got)
		}
		adminTestLog(t, s, "封禁用户", u.Uname)
		assertCode(t, adminTestPost(t, s, session, "user/update", url.Values{"mid": {adminID(u.MID)}, "status": {"0"}, "role": {"other"}}), 0)
		adminTestLog(t, s, "更新用户", u.Uname)
		logs := adminTestList(t, get(t, s, "/x/admin/logs?ps=1", session))
		log := logs[0].(map[string]any)
		if log["action"] != "更新用户" || log["member"].(map[string]any)["mid"] != float64(session.MID) {
			t.Fatalf("log projection: %v", log)
		}
	})
}

func TestAdminArchiveWorkflow(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		archives, err := s.Store.ListArchive(t.Context(), "WHERE state=-1 ORDER BY aid")
		if err != nil {
			t.Fatal(err)
		}
		a := archives[0]
		data := adminTestData(t, get(t, s, "/x/admin/archives?state=-1&ps=1", session))
		row := data["list"].([]any)[0].(map[string]any)
		for _, k := range []string{"aid", "bvid", "title", "owner", "pages", "state", "stat", "reason", "media", "tname", "duration"} {
			if _, ok := row[k]; !ok {
				t.Errorf("missing archive %s", k)
			}
		}
		if data["page"].(map[string]any)["total"] != float64(len(archives)) {
			t.Fatal("state filter")
		}
		adminTestError(t, adminTestPost(t, s, session, "archive/audit", url.Values{"aid": {"999999999"}}), -404, "稿件不存在")
		assertCode(t, adminTestPost(t, s, session, "archive/audit", url.Values{"aid": {adminID(a.AID)}, "state": {"-2"}}), 0)
		rejected, err := s.Store.ArchiveByID(t.Context(), a.AID)
		if err != nil {
			t.Fatal(err)
		}
		if rejected.State != -2 || rejected.Reason != "内容不符合社区规范" || rejected.Pubdate != a.Pubdate {
			t.Fatalf("rejection: %+v", rejected)
		}
		adminTestLog(t, s, "退回稿件", a.Title)
		filtered := adminTestData(t, get(t, s, "/x/admin/archives?keyword=nonexistent&tid=99999", session))
		if filtered["page"].(map[string]any)["total"] != float64(0) {
			t.Fatal("filter not applied")
		}
		counts := filtered["counts"].(map[string]any)
		if counts["pending"] != float64(len(archives)-1) || counts["rejected"] != float64(adminTestCount(t, s, "SELECT COUNT(*) FROM archives WHERE state=-2")) {
			t.Fatal("counts must be global")
		}
		before := time.Now().Unix()
		assertCode(t, adminTestPost(t, s, session, "archive/audit", url.Values{"aid": {adminID(a.AID)}, "state": {"0"}, "reason": {"ignored"}}), 0)
		approved, err := s.Store.ArchiveByID(t.Context(), a.AID)
		if err != nil {
			t.Fatal(err)
		}
		if approved.State != 0 || approved.Reason != "" || approved.Pubdate < before {
			t.Fatal("approval did not update pubdate/clear reason")
		}
		adminTestLog(t, s, "审核通过", a.Title)
		assertCode(t, adminTestPost(t, s, session, "archive/delete", url.Values{"aid": {adminID(a.AID)}}), 0)
		adminTestLog(t, s, "删除稿件", a.Title)
		if list := adminTestList(t, get(t, s, "/x/admin/archives?keyword="+a.BVID, session)); len(list) != 0 {
			t.Fatal("deleted archive exposed by default")
		}
		if list := adminTestList(t, get(t, s, "/x/admin/archives?state=-100&keyword="+strings.ToLower(a.BVID), session)); len(list) != 1 {
			t.Fatal("deleted archive filter")
		}
		logCount := adminTestCount(t, s, "SELECT COUNT(*) FROM logs")
		assertCode(t, adminTestPost(t, s, session, "archive/delete", url.Values{"aid": {"999999999"}}), 0)
		if adminTestCount(t, s, "SELECT COUNT(*) FROM logs") != logCount {
			t.Fatal("missing archive deletion should not log")
		}
	})
}

func TestAdminLiveAndCommunity(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		rooms, err := s.Store.ListLiveRoom(t.Context(), "ORDER BY room_id")
		if err != nil {
			t.Fatal(err)
		}
		r := rooms[0]
		if _, err = s.Store.DB.ExecContext(t.Context(), "UPDATE rooms SET source=?,live_status=1 WHERE room_id=?", "null", r.RoomID); err != nil {
			t.Fatal(err)
		}
		list := adminTestList(t, get(t, s, "/x/admin/live/rooms?keyword="+adminID(r.RoomID), session))
		row := list[0].(map[string]any)
		for _, k := range []string{"roomid", "room_id", "uid", "uname", "title", "cover", "area_v2_parent_name", "online", "banned", "live_status", "source"} {
			if _, ok := row[k]; !ok {
				t.Errorf("missing room %s", k)
			}
		}
		source := row["source"].(map[string]any)
		if source["type"] != "push" || source["url"] != "" || source["loop"] != true {
			t.Fatalf("source default: %v", source)
		}
		adminTestError(t, adminTestPost(t, s, session, "live/room/ban", url.Values{"room_id": {"999999999"}, "banned": {"true"}}), 19002003, "房间不存在或已被封禁")
		assertCode(t, adminTestPost(t, s, session, "live/room/ban", url.Values{"room_id": {adminID(r.RoomID)}, "banned": {"1"}}), 0)
		room, err := s.Store.RoomByID(t.Context(), r.RoomID)
		if err != nil {
			t.Fatal(err)
		}
		if !room.Banned || room.LiveStatus != 0 {
			t.Fatal("ban must stop live")
		}
		adminTestLog(t, s, "封禁直播间", r.Title)
		assertCode(t, adminTestPost(t, s, session, "live/room/ban", url.Values{"room_id": {adminID(r.RoomID)}, "banned": {"false"}}), 0)
		room, err = s.Store.RoomByID(t.Context(), r.RoomID)
		if err != nil {
			t.Fatal(err)
		}
		if room.Banned || room.LiveStatus != 0 {
			t.Fatal("unban should not start live")
		}

		replies, err := s.Store.ListReply(t.Context(), "WHERE root IS NOT NULL ORDER BY rpid")
		if err != nil {
			t.Fatal(err)
		}
		child := replies[0]
		root, err := s.Store.ReplyByID(t.Context(), child.Root)
		if err != nil {
			t.Fatal(err)
		}
		got := adminTestList(t, get(t, s, "/x/admin/replies?keyword="+url.QueryEscape(root.Message), session))
		if len(got) == 0 {
			t.Fatal("reply search")
		}
		reply := got[0].(map[string]any)
		for _, k := range []string{"rpid", "member", "content", "archive", "like", "action", "replies"} {
			if _, ok := reply[k]; !ok {
				t.Errorf("missing reply %s", k)
			}
		}
		if reply["action"] != float64(0) || len(reply["replies"].([]any)) != 0 {
			t.Fatal("admin reply must omit children and reaction")
		}
		assertCode(t, adminTestPost(t, s, session, "reply/delete", url.Values{"rpid": {adminID(root.RPID)}}), 0)
		if n := adminTestCount(t, s, "SELECT COUNT(*) FROM replies WHERE rpid=? OR root=?", root.RPID, root.RPID); n != 0 {
			t.Fatal("reply children survived")
		}
		assertCode(t, adminTestPost(t, s, session, "reply/delete", url.Values{"rpid": {adminID(root.RPID)}}), 0)

		archives, err := s.Store.ListArchive(t.Context(), "ORDER BY aid LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		a, err := s.Store.ArchiveByID(t.Context(), archives[0].AID)
		if err != nil {
			t.Fatal(err)
		}
		seasonList, err := s.Store.ListSeason(t.Context(), "ORDER BY season_id LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		season, err := s.Store.SeasonByID(t.Context(), seasonList[0].SeasonID)
		if err != nil {
			t.Fatal(err)
		}
		for i, cid := range []int64{a.Pages[0].CID, season.Episodes[0].CID, 987654321} {
			d := store.Danmaku{ID: int64(900000 + i), CID: cid, MID: session.MID, Content: fmt.Sprintf("T9弹幕%d", i), Weight: 10 - i, Ctime: time.Now().Unix(), Mode: 1, Fontsize: 25}
			if err = store.InsertDanmaku(t.Context(), s.Store.DB, &d); err != nil {
				t.Fatal(err)
			}
		}
		defaults := adminTestList(t, get(t, s, "/x/admin/danmaku?ps=10000", session))
		for _, v := range defaults {
			if v.(map[string]any)["weight"] != float64(10) {
				t.Fatal("default danmaku must be user posts")
			}
		}
		dms := adminTestList(t, get(t, s, "/x/admin/danmaku?keyword="+url.QueryEscape("T9弹幕"), session))
		if len(dms) != 3 {
			t.Fatalf("danmaku search got %d", len(dms))
		}
		targets := map[float64]string{900000: a.Title, 900001: season.Title, 900002: ""}
		for _, v := range dms {
			d := v.(map[string]any)
			if d["target"] != targets[d["id"].(float64)] || d["member"].(map[string]any)["mid"] != float64(session.MID) {
				t.Fatalf("danmaku projection: %v", d)
			}
		}
		assertCode(t, adminTestPost(t, s, session, "danmaku/delete", url.Values{"id": {"900000"}}), 0)
		adminTestLog(t, s, "删除弹幕", "900000")
		if adminTestCount(t, s, "SELECT COUNT(*) FROM danmaku WHERE id=?", 900000) != 0 {
			t.Fatal("danmaku not deleted")
		}
		assertCode(t, adminTestPost(t, s, session, "danmaku/delete", url.Values{"id": {"900000"}}), 0)
		if _, err = s.Store.ReplyByID(t.Context(), root.RPID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("reply still exists: %v", err)
		}
	})
}

func adminID(id int64) string { return strconv.FormatInt(id, 10) }
