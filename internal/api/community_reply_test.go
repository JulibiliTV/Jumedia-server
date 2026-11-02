package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"jumedia-server/internal/store"
)

func communityData(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	assertCode(t, v, 0)
	d, ok := v["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object: %+v", v)
	}
	return d
}
func communityWrite(t *testing.T, s *Server, session *store.Session, path string, p url.Values) map[string]any {
	t.Helper()
	if p == nil {
		p = url.Values{}
	}
	if path == "/x/v2/reply/add" && !p.Has("type") {
		p.Set("type", "1")
	}
	p.Set("csrf", session.CSRF)
	return post(t, s, path, p, session)
}
func communityExec(t *testing.T, s *Server, q string, args ...any) {
	t.Helper()
	if _, e := s.Store.DB.ExecContext(context.Background(), q, args...); e != nil {
		t.Fatal(e)
	}
}
func communityArchives(t *testing.T, s *Server) []store.Archive {
	t.Helper()
	a, e := s.Store.PublishedArchives(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(a) < 3 {
		t.Fatal("missing seeded archives")
	}
	return a
}
func communityFailure(t *testing.T, v map[string]any, code int, message string) {
	t.Helper()
	assertCode(t, v, code)
	if v["message"] != message || v["data"] != nil || v["ttl"] != float64(1) {
		t.Fatalf("failure envelope: %+v", v)
	}
}

func TestCommunityReplies(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		user := login(t, s, "lumen", "demo123")
		outsider := login(t, s, "oolong", "demo123")
		a := communityArchives(t, s)[0]
		oid := fmt.Sprint(a.AID)
		communityExec(t, s, "DELETE FROM replies WHERE oid=?", a.AID)
		add := func(session *store.Session, message string, root, parent float64) map[string]any {
			return communityData(t, communityWrite(t, s, session, "/x/v2/reply/add", url.Values{"oid": {oid}, "message": {message}, "root": {fmt.Sprint(root)}, "parent": {fmt.Sprint(parent)}}))
		}
		one := add(user, " \ufeff第一条\n", 0, 0)
		rpid := one["rpid"].(float64)
		if one["rpid_str"] != fmt.Sprint(rpid) || one["success_toast"] != "发送成功" || one["reply"].(map[string]any)["content"].(map[string]any)["message"] != "第一条" {
			t.Fatal(one)
		}
		two := add(user, "第二条", 0, 0)
		newID := two["rpid"].(float64)
		communityExec(t, s, "UPDATE replies SET ctime=? WHERE rpid=?", 100, rpid)
		communityExec(t, s, "UPDATE replies SET ctime=? WHERE rpid=?", 200, newID)
		for i := 0; i < 22; i++ {
			add(user, fmt.Sprintf("楼中楼%d", i), rpid, rpid)
		}
		react := func(path string, id float64, action string) {
			v := communityWrite(t, s, admin, path, url.Values{"rpid": {fmt.Sprint(id)}, "action": {action}})
			assertCode(t, v, 0)
			if v["data"] != nil {
				t.Fatal(v)
			}
		}
		react("/x/v2/reply/action", rpid, "1")
		react("/x/v2/reply/action", rpid, "1")
		hot := communityData(t, get(t, s, "/x/v2/reply?oid="+oid+"&sort=1&ps=1", admin))
		list := hot["replies"].([]any)
		r := list[0].(map[string]any)
		if r["rpid"] != rpid || r["action"] != float64(1) || r["like"] != float64(1) || len(r["replies"].([]any)) != 3 || r["rcount"] != float64(22) {
			t.Fatal(r)
		}
		page := hot["page"].(map[string]any)
		if page["count"] != float64(2) || page["acount"] != float64(24) || hot["upper"].(map[string]any)["mid"] != float64(a.MID) {
			t.Fatal(hot)
		}
		newest := communityData(t, get(t, s, "/x/v2/reply?oid="+oid+"&sort=0&ps=1"))
		if newest["replies"].([]any)[0].(map[string]any)["rpid"] != newID {
			t.Fatal(newest)
		}
		children := communityData(t, get(t, s, fmt.Sprintf("/x/v2/reply/reply?root=%.0f&pn=2", rpid)))
		if len(children["replies"].([]any)) != 2 || children["page"].(map[string]any)["count"] != float64(22) || children["root"].(map[string]any)["rcount"] != float64(0) {
			t.Fatal(children)
		}
		react("/x/v2/reply/hate", rpid, "1")
		hot = communityData(t, get(t, s, "/x/v2/reply?oid="+oid, admin))
		for _, x := range hot["replies"].([]any) {
			r = x.(map[string]any)
			if r["rpid"] == rpid && (r["action"] != float64(2) || r["like"] != float64(0)) {
				t.Fatal(r)
			}
		}
		react("/x/v2/reply/action", rpid, "0")
		// 500 astral symbols are exactly 1,000 JS UTF-16 code units.
		add(user, strings.Repeat("😀", 500), 0, 0)
		for _, message := range []string{" \n\ufeff", strings.Repeat("😀", 501)} {
			communityFailure(t, communityWrite(t, s, user, "/x/v2/reply/add", url.Values{"oid": {oid}, "message": {message}}), 12025, "评论字数需在 1–1000 字之间")
		}
		// Explicitly select an outsider different from the UP owner.
		if a.MID == outsider.MID {
			outsider = login(t, s, "pixel", "demo123")
		}
		communityFailure(t, communityWrite(t, s, outsider, "/x/v2/reply/del", url.Values{"rpid": {fmt.Sprint(rpid)}}), -403, "没有权限")
		assertCode(t, communityWrite(t, s, user, "/x/v2/reply/del", url.Values{"rpid": {fmt.Sprint(rpid)}}), 0)
		communityFailure(t, get(t, s, fmt.Sprintf("/x/v2/reply/reply?root=%.0f", rpid)), 12022, "评论已被删除")
		var remaining int
		if e := s.Store.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM replies WHERE root=?", rpid).Scan(&remaining); e != nil || remaining != 0 {
			t.Fatalf("children retained: %d %v", remaining, e)
		}
		assertCode(t, communityWrite(t, s, admin, "/x/v2/reply/del", url.Values{"rpid": {fmt.Sprint(newID)}}), 0)
		// An uploader can moderate another user's comment.
		owner, e := s.Store.UserByID(context.Background(), a.MID)
		if e != nil {
			t.Fatal(e)
		}
		password := "demo123"
		if owner.Username == "jumedia" {
			password = "admin123"
		}
		ownerSession := login(t, s, owner.Username, password)
		foreign := add(outsider, "UP 可删除", 0, 0)
		assertCode(t, communityWrite(t, s, ownerSession, "/x/v2/reply/del", url.Values{"rpid": {fmt.Sprint(foreign["rpid"])}}), 0)
	})
}

func TestCommunityAuthentication(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		user := login(t, s, "jumedia", "admin123")
		reads := []string{"/x/dm/filter/user", "/x/web-interface/history/cursor", "/x/v2/history/shadow", "/x/v2/history/toview", "/x/v2/history/toview/web"}
		writes := []string{"/x/v2/reply/add", "/x/v2/reply/del", "/x/v2/reply/action", "/x/v2/reply/hate", "/x/v2/dm/web/config", "/x/v2/dm/post", "/x/click-interface/web/heartbeat", "/x/v2/history/shadow/set", "/x/v2/history/clear", "/x/v2/history/toview/add", "/x/v2/history/toview/v2/dels", "/x/v3/fav/folder/add", "/x/v3/fav/folder/edit", "/x/v3/fav/folder/del"}
		for _, path := range append(reads, writes...) {
			communityFailure(t, get(t, s, path), -101, "账号未登录")
		}
		for _, path := range writes {
			communityFailure(t, post(t, s, path, url.Values{"csrf": {"wrong"}}, user), -111, "csrf 校验失败")
		}
		communityExec(t, s, "UPDATE users SET status=1 WHERE mid=?", user.MID)
		for _, path := range append(reads, writes...) {
			if path == "/x/v2/reply/add" {
				communityFailure(t, get(t, s, path, user), -102, "账号已被封停")
			} else {
				communityFailure(t, get(t, s, path, user), -403, "账号已被封禁")
			}
		}
	})
}

// Concurrent repeats must be idempotent on PostgreSQL as well as SQLite.
func TestCommunityConcurrentReaction(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		a := communityArchives(t, s)[0]
		added := communityData(t, communityWrite(t, s, session, "/x/v2/reply/add", url.Values{"oid": {fmt.Sprint(a.AID)}, "message": {"并发点赞"}}))
		rpid := added["rpid"]
		var wg sync.WaitGroup
		results := make(chan map[string]any, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p := url.Values{"csrf": {session.CSRF}, "rpid": {fmt.Sprint(rpid)}, "action": {"1"}}
				r := httptest.NewRequest("POST", "/x/v2/reply/action", strings.NewReader(p.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				v := map[string]any{}
				if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
					v["decode_error"] = e.Error()
				}
				results <- v
			}()
		}
		wg.Wait()
		close(results)
		for v := range results {
			assertCode(t, v, 0)
		}
		full, e := s.Store.ReplyByID(context.Background(), int64(rpid.(float64)))
		if e != nil {
			t.Fatal(e)
		}
		if len(full.Likers) != 1 || full.Likers[0] != session.MID {
			t.Fatal(full)
		}
	})
}

func TestCommunityFractionalIDDoesNotTargetInteger(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		user := login(t, s, "jumedia", "admin123")
		a := communityArchives(t, s)[0]
		reply := communityData(t, communityWrite(t, s, user, "/x/v2/reply/add", url.Values{"oid": {fmt.Sprint(a.AID)}, "message": {"不可误删"}}))
		fractional := fmt.Sprintf("%.1f", reply["rpid"].(float64)+.5)
		communityFailure(t, communityWrite(t, s, user, "/x/v2/reply/del", url.Values{"rpid": {fractional}}), 12022, "评论已被删除")
		communityData(t, get(t, s, "/x/v2/reply/reply?root="+fmt.Sprint(reply["rpid"])))
		communityFailure(t, get(t, s, "/x/v3/fav/resource/list?media_id=9010.5"), -403, "收藏夹不存在或已设为私密")
	})
}
