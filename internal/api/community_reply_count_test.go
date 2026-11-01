package api

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func replyCountCheck(t *testing.T, s *Server, oid int64, want int) {
	t.Helper()
	actual := adminTestCount(t, s, "SELECT COUNT(*) FROM replies WHERE oid=?", oid)
	stored := adminTestCount(t, s, "SELECT stat_reply FROM archives WHERE aid=?", oid)
	view := communityData(t, get(t, s, "/x/web-interface/view?aid="+fmt.Sprint(oid)))
	list := communityData(t, get(t, s, "/x/v2/reply?oid="+fmt.Sprint(oid)+"&ps=1"))
	if actual != want || stored != want || view["stat"].(map[string]any)["reply"] != float64(want) || list["page"].(map[string]any)["acount"] != float64(want) {
		t.Fatalf("oid=%d want=%d rows=%d stored=%d view=%v page=%v", oid, want, actual, stored, view["stat"], list["page"])
	}
}

func replyCountAdd(t *testing.T, s *Server, session *store.Session, oid, root int64) int64 {
	t.Helper()
	v := communityData(t, communityWrite(t, s, session, "/x/v2/reply/add", url.Values{
		"oid": {fmt.Sprint(oid)}, "message": {"T25 评论"}, "root": {fmt.Sprint(root)}, "parent": {fmt.Sprint(root)},
	}))
	id := int64(v["rpid"].(float64))
	if v["rpid_str"] != fmt.Sprint(id) || v["success_toast"] != "发送成功" || v["reply"].(map[string]any)["oid"] != float64(oid) {
		t.Fatal(v)
	}
	return id
}

func TestReplyCountMutations(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		user := login(t, s, "lumen", "demo123")
		arcs := communityArchives(t, s)
		oid := arcs[len(arcs)-1].AID
		base := adminTestCount(t, s, "SELECT COUNT(*) FROM replies WHERE oid=?", oid)
		if base != 0 {
			t.Fatal("expected an initially empty archive")
		}
		replyCountCheck(t, s, oid, 0)
		// A write repairs historical drift instead of incrementing the stale value.
		communityExec(t, s, "UPDATE archives SET stat_reply=? WHERE aid=?", 1301, oid)
		root := replyCountAdd(t, s, user, oid, 0)
		replyCountCheck(t, s, oid, base+1)
		child := replyCountAdd(t, s, user, oid, root)
		replyCountCheck(t, s, oid, base+2)
		replyCountAdd(t, s, user, oid, root)
		replyCountCheck(t, s, oid, base+3)
		communityFailure(t, communityWrite(t, s, user, "/x/v2/reply/add", url.Values{"oid": {fmt.Sprint(oid)}, "message": {" "}}), 12025, "评论字数需在 1–1000 字之间")
		assertCode(t, adminTestPost(t, s, user, "reply/delete", url.Values{"rpid": {fmt.Sprint(root)}}), -403)
		replyCountCheck(t, s, oid, base+3)
		communityExec(t, s, "UPDATE archives SET stat_reply=? WHERE aid=?", -9, oid)
		assertCode(t, communityWrite(t, s, user, "/x/v2/reply/del", url.Values{"rpid": {fmt.Sprint(child)}}), 0)
		replyCountCheck(t, s, oid, base+2)
		assertCode(t, communityWrite(t, s, user, "/x/v2/reply/del", url.Values{"rpid": {fmt.Sprint(root)}}), 0)
		replyCountCheck(t, s, oid, base)
		communityFailure(t, communityWrite(t, s, user, "/x/v2/reply/del", url.Values{"rpid": {fmt.Sprint(root)}}), 12022, "评论已被删除")
		replyCountCheck(t, s, oid, base)

		root = replyCountAdd(t, s, user, oid, 0)
		child = replyCountAdd(t, s, user, oid, root)
		replyCountAdd(t, s, user, oid, root)
		communityExec(t, s, "UPDATE archives SET stat_reply=? WHERE aid=?", 999, oid)
		assertCode(t, adminTestPost(t, s, admin, "reply/delete", url.Values{"rpid": {fmt.Sprint(child)}}), 0)
		replyCountCheck(t, s, oid, base+2)
		assertCode(t, adminTestPost(t, s, admin, "reply/delete", url.Values{"rpid": {fmt.Sprint(root)}}), 0)
		replyCountCheck(t, s, oid, base)
		logs := adminTestCount(t, s, "SELECT COUNT(*) FROM logs")
		assertCode(t, adminTestPost(t, s, admin, "reply/delete", url.Values{"rpid": {fmt.Sprint(root)}}), 0)
		if got := adminTestCount(t, s, "SELECT COUNT(*) FROM logs"); got != logs {
			t.Fatal("missing reply deletion must not log")
		}
	})
}

func TestReplyCountCascade(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		arcs := communityArchives(t, s)[:3]
		base := make([]int, len(arcs))
		for i, a := range arcs {
			base[i] = adminTestCount(t, s, "SELECT COUNT(*) FROM replies WHERE oid=?", a.AID)
		}
		for _, path := range []string{"/x/v2/reply/del", "/x/admin/reply/delete"} {
			root := replyCountAdd(t, s, admin, arcs[0].AID, 0)
			child := replyCountAdd(t, s, admin, arcs[1].AID, root)
			replyCountAdd(t, s, admin, arcs[2].AID, child)
			assertCode(t, communityWrite(t, s, admin, path, url.Values{"rpid": {fmt.Sprint(root)}}), 0)
			for i, a := range arcs {
				replyCountCheck(t, s, a.AID, base[i])
			}
		}
	})
}

func TestReplyCountRollback(t *testing.T) {
	s := newTestServer(t)
	admin := login(t, s, "jumedia", "admin123")
	oid := communityArchives(t, s)[0].AID
	root := replyCountAdd(t, s, admin, oid, 0)
	replyCountAdd(t, s, admin, oid, root)
	base := adminTestCount(t, s, "SELECT COUNT(*) FROM replies WHERE oid=?", oid)
	logs := adminTestCount(t, s, "SELECT COUNT(*) FROM logs")
	seq := adminTestCount(t, s, "SELECT value FROM ids WHERE name=?", "global")
	// Inject a failure specifically in the count update after the reply mutation.
	communityExec(t, s, "CREATE TRIGGER fail_reply_count BEFORE UPDATE OF stat_reply ON archives BEGIN SELECT RAISE(ABORT, 'count failed'); END")
	for _, tc := range []struct {
		path   string
		params url.Values
	}{
		{"/x/v2/reply/add", url.Values{"oid": {fmt.Sprint(oid)}, "message": {"rollback"}}},
		{"/x/v2/reply/del", url.Values{"rpid": {fmt.Sprint(root)}}},
		{"/x/admin/reply/delete", url.Values{"rpid": {fmt.Sprint(root)}}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			assertCode(t, communityWrite(t, s, admin, tc.path, tc.params), -500)
			replyCountCheck(t, s, oid, base)
			if adminTestCount(t, s, "SELECT COUNT(*) FROM logs") != logs || adminTestCount(t, s, "SELECT value FROM ids WHERE name=?", "global") != seq {
				t.Fatal("failed mutation changed audit log or sequence")
			}
		})
	}
}

func TestReplyCountConcurrent(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		oid := communityArchives(t, s)[0].AID
		base := adminTestCount(t, s, "SELECT COUNT(*) FROM replies WHERE oid=?", oid)
		var ids []int64
		for i := 0; i < 6; i++ {
			ids = append(ids, replyCountAdd(t, s, admin, oid, 0))
		}
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(2)
			go func() {
				defer wg.Done()
				assertCode(t, communityWrite(t, s, admin, "/x/v2/reply/add", url.Values{"oid": {fmt.Sprint(oid)}, "message": {"concurrent"}}), 0)
			}()
			go func() {
				defer wg.Done()
				path := "/x/v2/reply/del"
				if id%2 == 0 {
					path = "/x/admin/reply/delete"
				}
				assertCode(t, communityWrite(t, s, admin, path, url.Values{"rpid": {fmt.Sprint(id)}}), 0)
			}()
		}
		wg.Wait()
		replyCountCheck(t, s, oid, base+len(ids))
	})
}

func TestReplyCountNonArchiveOID(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		var oid int64
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT aid FROM episodes WHERE aid NOT IN (SELECT aid FROM archives) ORDER BY aid LIMIT 1").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		before := adminTestCount(t, s, "SELECT SUM(stat_reply) FROM archives")
		c := &Ctx{R: httptest.NewRequest("POST", "/x/v2/reply/add", nil), Server: s}
		if err := s.WriteTx(c, func(tx *db.Tx) error { return communitySyncReplyCount(c, tx, oid) }); err != nil {
			t.Fatal(err)
		}
		if adminTestCount(t, s, "SELECT SUM(stat_reply) FROM archives") != before || adminTestCount(t, s, "SELECT COUNT(*) FROM archives WHERE aid=?", oid) != 0 {
			t.Fatal("episode count synchronization changed archives")
		}
	})
}
