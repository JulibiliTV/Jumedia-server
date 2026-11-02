package api

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"jumedia-server/internal/store"
)

func TestCommentModeration(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		viewer := login(t, s, "lumen", "demo123")
		author := login(t, s, "oolong", "demo123")
		a := communityArchives(t, s)[0]
		oid := fmt.Sprint(a.AID)
		communityExec(t, s, "DELETE FROM replies WHERE oid=?", a.AID)
		add := func(session *store.Session, message, root string) string {
			d := communityData(t, communityWrite(t, s, session, "/x/v2/reply/add", url.Values{"oid": {oid}, "message": {message}, "root": {root}, "parent": {root}}))
			return fmt.Sprint(d["rpid"])
		}
		root := add(admin, "root", "0")
		child := add(author, "child to hide", root)
		other := add(author, "root to hide", "0")
		report := url.Values{"oid": {oid}, "rpid": {child}, "type": {"1"}, "reason": {"7"}, "add_blacklist": {"false"}}
		assertCode(t, post(t, s, "/x/v2/reply/report", report), -101)
		assertCode(t, post(t, s, "/x/v2/reply/report", report, viewer), -111)
		assertCode(t, communityWrite(t, s, author, "/x/v2/reply/report", report), -400)
		bad := url.Values{"oid": {"1"}, "rpid": {child}, "type": {"1"}, "reason": {"7"}}
		assertCode(t, communityWrite(t, s, viewer, "/x/v2/reply/report", bad), 12022)
		bad.Set("oid", oid)
		bad.Set("reason", "invalid")
		assertCode(t, communityWrite(t, s, viewer, "/x/v2/reply/report", bad), -400)
		for range 2 {
			assertCode(t, communityWrite(t, s, viewer, "/x/v2/reply/report", report), 0)
		}
		var n int
		if err := s.Store.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM comment_reports WHERE rpid=?", ParseID(child)).Scan(&n); err != nil || n != 1 {
			t.Fatalf("dedup %d %v", n, err)
		}
		queue := communityData(t, get(t, s, "/x/admin/replies?reported=1", admin))
		rows := queue["list"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["report_count"] != float64(1) {
			t.Fatal(queue)
		}
		assertCode(t, get(t, s, "/x/admin/replies?reported=1", viewer), -403)
		children := communityData(t, get(t, s, "/x/v2/reply/reply?root="+root, viewer))
		if len(children["replies"].([]any)) != 0 {
			t.Fatal("reported child visible", children)
		}
		assertCode(t, communityWrite(t, s, viewer, "/x/relation/modify", url.Values{"fid": {fmt.Sprint(viewer.MID)}, "act": {"5"}}), 22001)
		block := url.Values{"fid": {fmt.Sprint(author.MID)}, "act": {"5"}, "re_src": {"11"}}
		assertCode(t, get(t, s, "/x/relation/blacks"), -101)
		assertCode(t, post(t, s, "/x/relation/modify", block, viewer), -111)
		for range 2 {
			assertCode(t, communityWrite(t, s, viewer, "/x/relation/modify", block), 0)
		}
		blocks := communityData(t, get(t, s, "/x/relation/blacks", viewer))
		if len(blocks["list"].([]any)) != 1 {
			t.Fatal(blocks)
		}
		if blocks["total"] != float64(1) || blocks["list"].([]any)[0].(map[string]any)["attribute"] != float64(128) {
			t.Fatal("blacklist contract", blocks)
		}
		relation := communityData(t, get(t, s, fmt.Sprintf("/x/space/wbi/acc/relation?mid=%d", author.MID), viewer))
		if relation["relation"].(map[string]any)["attribute"] != float64(128) {
			t.Fatal(relation)
		}
		for _, path := range []string{"/x/v2/reply?oid=" + oid, "/x/v2/reply/wbi/main?oid=" + oid} {
			d := communityData(t, get(t, s, path, viewer))
			list := d["replies"].([]any)
			if len(list) != 1 || len(list[0].(map[string]any)["replies"].([]any)) != 0 {
				t.Fatal("block leaked into list or preview", d)
			}
			if len(communityData(t, get(t, s, path, admin))["replies"].([]any)) != 2 {
				t.Fatal("block crossed viewer")
			}
		}
		assertCode(t, get(t, s, "/x/v2/reply/reply?root="+other, viewer), 12022)
		// Dismissal resolves the queue, but the reporter's choice still hides the content.
		assertCode(t, communityWrite(t, s, admin, "/x/admin/reply/report/dismiss", url.Values{"rpid": {child}}), 0)
		queue = communityData(t, get(t, s, "/x/admin/replies?reported=1", admin))
		if len(queue["list"].([]any)) != 0 {
			t.Fatal(queue)
		}
		block.Set("act", "6")
		assertCode(t, communityWrite(t, s, viewer, "/x/relation/modify", block), 0)
		if len(communityData(t, get(t, s, "/x/v2/reply?oid="+oid, viewer))["replies"].([]any)) != 2 {
			t.Fatal("unblock failed")
		}
		report.Set("rpid", other)
		report.Set("reason", "0")
		assertCode(t, communityWrite(t, s, viewer, "/x/v2/reply/report", report), -400)
		report.Set("content", "测试举报说明")
		report.Set("add_blacklist", "true")
		assertCode(t, communityWrite(t, s, viewer, "/x/v2/reply/report", report), 0)
		if len(communityData(t, get(t, s, "/x/relation/blacks", viewer))["list"].([]any)) != 1 {
			t.Fatal("report add_blacklist ignored")
		}
		assertCode(t, communityWrite(t, s, admin, "/x/admin/reply/delete", url.Values{"rpid": {other}}), 0)
		var status string
		if err := s.Store.DB.QueryRowContext(context.Background(), "SELECT status FROM comment_reports WHERE rpid=?", ParseID(other)).Scan(&status); err != nil || status != "removed" {
			t.Fatalf("report resolution %s %v", status, err)
		}
		communityExec(t, s, "UPDATE settings SET value=? WHERE key='sensitiveWords'", `"ForbiddenWord"`)
		assertCode(t, communityWrite(t, s, viewer, "/x/v2/reply/add", url.Values{"oid": {oid}, "message": {"has forbiddenword here"}}), -400)
		add(viewer, "ordinary https://example.test link", "0")
	})
}
