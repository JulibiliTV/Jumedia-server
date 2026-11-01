package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"jumedia-server/internal/store"
)

func spaceTestExec(t *testing.T, s *Server, q string, args ...any) {
	t.Helper()
	if _, err := s.Store.DB.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func spaceTestArc(t *testing.T, s *Server, id int64) *store.Archive {
	t.Helper()
	a, err := s.Store.ArchiveByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func spaceTestTarget(t *testing.T, s *Server, mid int64) *store.Archive {
	t.Helper()
	as, err := s.Store.ListArchive(context.Background(), "WHERE mid<>? AND state=0 ORDER BY aid", mid)
	if err != nil || len(as) == 0 {
		t.Fatalf("target: %v", err)
	}
	return spaceTestArc(t, s, as[0].AID)
}
func spaceTestCount(t *testing.T, s *Server, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.Store.DB.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func spaceTestData(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	assertCode(t, v, 0)
	m, ok := v["data"].(map[string]any)
	if !ok {
		t.Fatalf("object expected: %v", v)
	}
	return m
}
func spaceTestError(t *testing.T, v map[string]any, code int, msg string) {
	t.Helper()
	assertCode(t, v, code)
	if v["message"] != msg || v["ttl"] != float64(1) || v["data"] != nil {
		t.Fatalf("error envelope: %v", v)
	}
}
func spaceTestPost(t *testing.T, s *Server, session *store.Session, path string, params url.Values) map[string]any {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("csrf", session.CSRF)
	return post(t, s, path, params, session)
}

func TestSpaceInteractionAuth(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		a := spaceTestTarget(t, s, session.MID)
		base := url.Values{"aid": {fmt.Sprint(a.AID)}, "rid": {fmt.Sprint(a.AID)}, "fid": {fmt.Sprint(a.MID)}, "like": {"1"}, "act": {"1"}}
		paths := []string{"/x/web-interface/archive/like", "/x/web-interface/coin/add", "/x/v3/fav/resource/deal", "/x/relation/modify"}
		for _, path := range paths {
			spaceTestError(t, post(t, s, path, base), -101, "账号未登录")
			spaceTestError(t, post(t, s, path, base, session), -111, "csrf 校验失败")
		}
		spaceTestError(t, post(t, s, "/x/v2/view/dislike", base), -101, "账号未登录")
		base.Set("dislike", "0")
		assertCode(t, post(t, s, "/x/v2/view/dislike", base, session), 0) // No CSRF required.
		assertCode(t, post(t, s, "/x/web-interface/share/add", base), 0)  // Anonymous write.
		spaceTestExec(t, s, "UPDATE users SET status=? WHERE mid=?", 1, session.MID)
		base.Set("csrf", session.CSRF)
		for _, path := range append(paths, "/x/v2/view/dislike") {
			spaceTestError(t, post(t, s, path, base, session), -403, "账号已被封禁")
		}
	})
}

func TestSpaceLikeDislikeAndVisibility(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		a := spaceTestTarget(t, s, session.MID)
		aid := fmt.Sprint(a.AID)
		path := "/x/web-interface/archive/like"
		for range 2 {
			assertCode(t, spaceTestPost(t, s, session, path, url.Values{"aid": {aid}, "like": {"1"}}), 0)
		}
		if got := spaceTestArc(t, s, a.AID).Stat.Like; got != a.Stat.Like+1 {
			t.Fatalf("likes %d", got)
		}
		if get(t, s, "/x/web-interface/archive/has/like?aid="+aid, session)["data"] != float64(1) {
			t.Fatal("missing like")
		}
		if get(t, s, "/x/web-interface/archive/has/like?aid="+aid)["data"] != float64(0) {
			t.Fatal("anonymous like")
		}
		assertCode(t, spaceTestPost(t, s, session, path, url.Values{"aid": {aid}, "like": {"bogus"}}), 0)
		for range 2 {
			assertCode(t, spaceTestPost(t, s, session, path, url.Values{"aid": {aid}, "like": {"2"}}), 0)
		}
		if got := spaceTestArc(t, s, a.AID).Stat.Like; got != a.Stat.Like {
			t.Fatalf("unlike %d", got)
		}
		// GET with access_key and biliCSRF follows the same Mock handler.
		assertCode(t, get(t, s, path+"?oid="+aid+"&like=1&access_key="+session.Access+"&biliCSRF="+session.CSRF), 0)
		for _, value := range []string{"0", "0", "1", "nonsense"} {
			assertCode(t, post(t, s, "/x/v2/view/dislike", url.Values{"aid": {aid}, "dislike": {value}}, session), 0)
			n := spaceTestCount(t, s, "SELECT COUNT(*) FROM dislikes WHERE mid=? AND aid=?", session.MID, a.AID)
			if n != int64(store.BoolInt(value == "0")) {
				t.Fatalf("dislike %s: %d", value, n)
			}
		}
		spaceTestError(t, post(t, s, "/x/v2/view/dislike", url.Values{"aid": {"99999999"}, "dislike": {"0"}}, session), -404, "啥都木有")
		spaceTestError(t, spaceTestPost(t, s, session, path, url.Values{"aid": {"99999999"}, "like": {"1"}}), -404, "啥都木有")
		spaceTestExec(t, s, "UPDATE archives SET state=? WHERE aid=?", -1, a.AID)
		spaceTestError(t, get(t, s, "/x/web-interface/share/add?aid="+aid), -404, "啥都木有")
		assertCode(t, spaceTestPost(t, s, session, path, url.Values{"aid": {aid}, "like": {"1"}}), 0) // Admin sees draft.
		owner := spaceTestUser(t, s, a.MID)
		ownerSession := login(t, s, owner.Username, "demo123")
		assertCode(t, spaceTestPost(t, s, ownerSession, path, url.Values{"avid": {aid}, "like": {"1"}}), 0)
		// A different non-admin cannot interact with the draft.
		users, err := s.Store.ListUser(context.Background(), "WHERE mid<>? AND role<>? ORDER BY mid", a.MID, "admin")
		if err != nil {
			t.Fatal(err)
		}
		other := login(t, s, users[0].Username, "demo123")
		spaceTestError(t, spaceTestPost(t, s, other, path, url.Values{"aid": {aid}, "like": {"1"}}), -404, "啥都木有")
		// Explicit empty aid must not fall through to oid.
		spaceTestError(t, spaceTestPost(t, s, session, path, url.Values{"aid": {""}, "oid": {aid}, "like": {"1"}}), -404, "啥都木有")
	})
}

func TestSpaceCoinAccounting(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		a := spaceTestTarget(t, s, session.MID)
		aid := fmt.Sprint(a.AID)
		buyer, owner := spaceTestUser(t, s, session.MID), spaceTestUser(t, s, a.MID)
		path := "/x/web-interface/coin/add"
		params := url.Values{"aid": {aid}, "select_like": {"1"}}
		for range 2 {
			d := spaceTestData(t, spaceTestPost(t, s, session, path, params))
			if d["like"] != true {
				t.Fatalf("coin: %v", d)
			}
		}
		after := spaceTestArc(t, s, a.AID)
		if after.Stat.Coin != a.Stat.Coin+2 || after.Stat.Like != a.Stat.Like+1 {
			t.Fatalf("stats: %+v", after.Stat)
		}
		if spaceTestUser(t, s, session.MID).Coins != buyer.Coins-2 || math.Abs(spaceTestUser(t, s, a.MID).Coins-owner.Coins-.2) > 1e-8 {
			t.Fatal("coin balances")
		}
		if n := spaceTestCount(t, s, "SELECT n FROM coins WHERE mid=? AND aid=?", session.MID, a.AID); n != 2 {
			t.Fatalf("given %d", n)
		}
		spaceTestError(t, spaceTestPost(t, s, session, path, params), 34005, "超过投币上限啦")
		ownerSession := login(t, s, owner.Username, "demo123")
		spaceTestError(t, spaceTestPost(t, s, ownerSession, path, url.Values{"aid": {aid}}), 34002, "不能给自己投币哦")
		spaceTestError(t, spaceTestPost(t, s, session, path, url.Values{"aid": {"99999999"}}), -404, "啥都木有")
		spaceTestExec(t, s, "DELETE FROM coins WHERE mid=? AND aid=?", session.MID, a.AID)
		spaceTestExec(t, s, "UPDATE users SET coins=? WHERE mid=?", 0, session.MID)
		spaceTestError(t, spaceTestPost(t, s, session, path, params), -104, "硬币不足")
		if spaceTestArc(t, s, a.AID).Stat.Coin != after.Stat.Coin {
			t.Fatal("failed coin changed count")
		}
		spaceTestExec(t, s, "UPDATE users SET coins=? WHERE mid=?", 10, session.MID)
		// The Mock accepts zero and negative integers, including select_like with zero.
		for _, n := range []string{"0", "-1"} {
			assertCode(t, spaceTestPost(t, s, session, path, url.Values{"aid": {aid}, "multiply": {n}}), 0)
		}
		// Explicitly document the shared integer-schema limitation.
		spaceTestError(t, spaceTestPost(t, s, session, path, url.Values{"aid": {aid}, "multiply": {"0.5"}}), -400, "投币数量需为整数")
		spaceTestError(t, spaceTestPost(t, s, session, path, url.Values{"aid": {aid}, "multiply": {"-2147483648"}}), -400, "投币数量需为整数")
	})
}

func TestSpaceCoinRollbackAndConcurrentLimit(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		a := spaceTestTarget(t, s, session.MID)
		aid := fmt.Sprint(a.AID)
		buyer, owner := spaceTestUser(t, s, session.MID).Coins, spaceTestUser(t, s, a.MID).Coins
		// Force the last write to fail after balances, coin stats and ledger updates.
		spaceTestExec(t, s, "ALTER TABLE likes RENAME TO space_test_likes")
		v := spaceTestPost(t, s, session, "/x/web-interface/coin/add", url.Values{"aid": {aid}, "select_like": {"1"}})
		spaceTestExec(t, s, "ALTER TABLE space_test_likes RENAME TO likes")
		assertCode(t, v, -500)
		if spaceTestUser(t, s, session.MID).Coins != buyer || spaceTestUser(t, s, a.MID).Coins != owner || spaceTestArc(t, s, a.AID).Stat.Coin != a.Stat.Coin || spaceTestCount(t, s, "SELECT COUNT(*) FROM coins WHERE mid=? AND aid=?", session.MID, a.AID) != 0 {
			t.Fatal("partial coin transaction persisted")
		}
		result := make(chan map[string]any, 6)
		// Separate Server instances sharing the database also honor the limit.
		other := New(s.Store, s.Config)
		for i := 0; i < 6; i++ {
			go func(i int) {
				params := url.Values{"aid": {aid}, "csrf": {session.CSRF}, "select_like": {"1"}}
				req := httptest.NewRequest(http.MethodPost, "/x/web-interface/coin/add", strings.NewReader(params.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
				w := httptest.NewRecorder()
				srv := s
				if i%2 == 1 {
					srv = other
				}
				srv.ServeHTTP(w, req)
				var v map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
					v = map[string]any{"code": float64(-999)}
				}
				result <- v
			}(i)
		}
		successes := 0
		for range 6 {
			v := <-result
			if v["code"] == float64(0) {
				successes++
			} else {
				spaceTestError(t, v, 34005, "超过投币上限啦")
			}
		}
		if successes != 2 {
			t.Fatalf("successes %d", successes)
		}
		if spaceTestArc(t, s, a.AID).Stat.Coin != a.Stat.Coin+2 || spaceTestArc(t, s, a.AID).Stat.Like != a.Stat.Like+1 {
			t.Fatal("concurrent accounting")
		}
	})
}

func TestSpaceFavoritesAndShare(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		a := spaceTestTarget(t, s, session.MID)
		aid := fmt.Sprint(a.AID)
		folders, err := s.Store.ListFavFolder(context.Background(), "WHERE mid=? ORDER BY id", session.MID)
		if err != nil || len(folders) == 0 {
			t.Fatalf("folders: %v", err)
		}
		f1 := folders[0].ID
		// Start from a deterministic state without disturbing the seeded counters.
		spaceTestExec(t, s, "DELETE FROM fav_items WHERE aid=?", a.AID)
		f2, err := s.Store.NextID(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err = store.InsertFavFolder(context.Background(), s.Store.DB, &store.FavFolder{ID: f2, MID: session.MID, Title: "second"}); err != nil {
			t.Fatal(err)
		}
		foreign, err := s.Store.ListFavFolder(context.Background(), "WHERE mid<>? ORDER BY id", session.MID)
		if err != nil || len(foreign) == 0 {
			t.Fatalf("foreign: %v", err)
		}
		path := "/x/v3/fav/resource/deal"
		ids := fmt.Sprintf("%d,%d,%d,%d,99999999", f1, f1, f2, foreign[0].ID)
		for range 2 {
			d := spaceTestData(t, spaceTestPost(t, s, session, path, url.Values{"rid": {aid}, "add_media_ids": {ids}}))
			if d["prompt"] != false {
				t.Fatal(d)
			}
		}
		if spaceTestArc(t, s, a.AID).Stat.Favorite != a.Stat.Favorite+2 {
			t.Fatal("favorite count")
		}
		d := spaceTestData(t, get(t, s, "/x/v2/fav/video/favoured?aid="+aid, session))
		if d["count"] != float64(1) || d["favoured"] != true {
			t.Fatal(d)
		}
		if spaceTestData(t, get(t, s, "/x/v2/fav/video/favoured?aid="+aid))["favoured"] != false {
			t.Fatal("anonymous favorite")
		}
		if spaceTestCount(t, s, "SELECT COUNT(*) FROM fav_items WHERE folder_id=? AND aid=?", foreign[0].ID, a.AID) != 0 {
			t.Fatal("modified another user's folder")
		}
		for range 2 {
			assertCode(t, spaceTestPost(t, s, session, path, url.Values{"rid": {aid}, "add_media_ids": {fmt.Sprint(f1)}, "del_media_ids": {ids}}), 0)
		}
		if spaceTestArc(t, s, a.AID).Stat.Favorite != a.Stat.Favorite {
			t.Fatal("unfavorite count")
		}
		if spaceTestData(t, get(t, s, "/x/v2/fav/video/favoured?aid="+aid, session))["favoured"] != false {
			t.Fatal("unfavorite status")
		}
		spaceTestError(t, spaceTestPost(t, s, session, path, url.Values{"aid": {aid}}), -404, "啥都木有") // rid is mandatory here.
		share := spaceTestData(t, get(t, s, "/x/web-interface/share/add?rid="+aid))
		if share["share_url"] != "http://example.com/video/"+a.BVID || share["count"] != float64(a.Stat.Share+1) {
			t.Fatal(share)
		}
		s.Config.PublicOrigin = "https://media.example/"
		share = spaceTestData(t, get(t, s, "/x/web-interface/share/add?avid="+aid))
		if share["share_url"] != "https://media.example/video/"+a.BVID || share["count"] != float64(a.Stat.Share+2) {
			t.Fatal(share)
		}
		spaceTestError(t, get(t, s, "/x/web-interface/share/add?aid=99999999"), -404, "啥都木有")
	})
}

func TestSpaceRelations(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		fid := int64(2)
		path := "/x/relation/modify"
		spaceTestExec(t, s, "DELETE FROM relations WHERE (mid=? AND fid=?) OR (mid=? AND fid=?)", session.MID, fid, fid, session.MID)
		relation := func(session *store.Session, a, b int) {
			t.Helper()
			d := spaceTestData(t, get(t, s, "/x/space/wbi/acc/relation?mid=2", session))
			r, br := d["relation"].(map[string]any), d["be_relation"].(map[string]any)
			if r["mid"] != float64(2) || r["attribute"] != float64(a) || r["mtime"] != float64(0) || br["attribute"] != float64(b) {
				t.Fatal(d)
			}
		}
		relation(session, 0, 0)
		relation(nil, 0, 0)
		for range 2 {
			assertCode(t, spaceTestPost(t, s, session, path, url.Values{"fid": {"2"}, "act": {"1"}}), 0)
		}
		relation(session, 2, 0)
		if spaceTestCount(t, s, "SELECT COUNT(*) FROM relations WHERE mid=? AND fid=?", session.MID, fid) != 1 {
			t.Fatal("duplicate relation")
		}
		if err := store.InsertRelation(context.Background(), s.Store.DB, &store.Relation{MID: fid, FID: session.MID, Mtime: 99}); err != nil {
			t.Fatal(err)
		}
		relation(session, 6, 6)
		follows := spaceTestData(t, get(t, s, "/x/relation/followings?vmid=1&pn=1&ps=1"))
		rows := follows["list"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["mid"] != float64(2) || rows[0].(map[string]any)["attribute"] != float64(6) {
			t.Fatal(follows)
		}
		info := spaceTestData(t, get(t, s, "/x/space/wbi/acc/info?mid=2", session))
		if info["is_followed"] != true {
			t.Fatal(info)
		}
		stat := spaceTestData(t, get(t, s, "/x/relation/stat?vmid=2"))
		if stat["follower"] != float64(spaceTestCount(t, s, "SELECT COUNT(*) FROM relations WHERE fid=?", fid)+fid*9871%50000) || stat["following"] != float64(spaceTestCount(t, s, "SELECT COUNT(*) FROM relations WHERE mid=?", fid)) {
			t.Fatal(stat)
		}
		assertCode(t, spaceTestPost(t, s, session, path, url.Values{"fid": {"2"}, "act": {"unknown"}}), 0)
		relation(session, 0, 2)
		spaceTestError(t, spaceTestPost(t, s, session, path, url.Values{"fid": {strconv.FormatInt(session.MID, 10)}, "act": {"1"}}), 22001, "不能关注自己")
		spaceTestError(t, spaceTestPost(t, s, session, path, url.Values{"fid": {"99999999"}, "act": {"1"}}), 22003, "用户不存在")
	})
}
