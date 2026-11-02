package api

import (
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"jumedia-server/internal/db"
)

func TestReplyAddContract(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "lumen", "demo123")
		arcs := communityArchives(t, s)
		a := arcs[0]
		send := func(text, root, parent string) map[string]any {
			return communityWrite(t, s, u, "/x/v2/reply/add", url.Values{"type": {"1"}, "oid": {fmt.Sprint(a.AID)}, "message": {text}, "root": {root}, "parent": {parent}})
		}
		check := func(v map[string]any, root, parent string) string {
			d := communityData(t, v)
			for key, want := range map[string]any{"success_action": float64(0), "success_toast": "发送成功", "need_captcha": false, "url": "", "root_str": root, "parent_str": parent, "dialog_str": parent} {
				if d[key] != want {
					t.Fatalf("%s: got %v, want %v", key, d[key], want)
				}
			}
			if d["rpid_str"] != fmt.Sprint(d["rpid"]) || d["root"] != float64(ParseID(root)) || d["parent"] != float64(ParseID(parent)) || d["dialog"] != d["parent"] {
				t.Fatal(d)
			}
			if emote, ok := d["emote"].(map[string]any); !ok || len(emote) != 0 {
				t.Fatal(d)
			}
			if d["reply"].(map[string]any)["rpid"] != d["rpid"] {
				t.Fatal(d)
			}
			for _, key := range []string{"root", "root_str", "parent", "parent_str", "dialog", "dialog_str"} {
				if d["reply"].(map[string]any)[key] != d[key] {
					t.Fatalf("reply.%s differs from posting result", key)
				}
			}
			return d["rpid_str"].(string)
		}
		root := check(send("root", "0", "0"), "0", "0")
		child := check(send("child", root, root), root, root)
		check(send("nested", root, child), root, child)
		other := check(send("other root", "0", "0"), "0", "0")
		for _, pair := range [][2]string{{root, other}, {child, child}, {"0", root}, {root, "999999999"}, {root, "0"}} {
			assertCode(t, send("invalid thread", pair[0], pair[1]), 12006)
		}
		a = arcs[1]
		assertCode(t, send("cross archive", root, root), 12006)
		assertCode(t, communityWrite(t, s, u, "/x/v2/reply/add", url.Values{"type": {"2"}, "oid": {fmt.Sprint(a.AID)}, "message": {"bad type"}}), 12009)
		assertCode(t, post(t, s, "/x/v2/reply/add", url.Values{"csrf": {u.CSRF}, "oid": {fmt.Sprint(a.AID)}, "message": {"missing type"}}, u), 12009)
		communityExec(t, s, "UPDATE settings SET value=? WHERE key=?", `"forbidden"`, "sensitiveWords")
		assertCode(t, send("FORBIDDEN", "0", "0"), 12016)
		communityExec(t, s, "UPDATE archives SET state=? WHERE aid=?", -100, a.AID)
		assertCode(t, send("deleted archive", "0", "0"), -404)
		communityExec(t, s, "UPDATE users SET status=1 WHERE mid=?", u.MID)
		assertCode(t, send("banned", "0", "0"), -102)
	})
}

func TestReplyAddPolicy(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "lumen", "demo123")
		a := communityArchives(t, s)[0]
		communityExec(t, s, "DELETE FROM replies WHERE mid=?", u.MID)
		set := func(key string, value any) {
			t.Helper()
			if err := s.Store.SetSetting(t.Context(), key, value); err != nil {
				t.Fatal(err)
			}
		}
		send := func(message, root string) map[string]any {
			return communityWrite(t, s, u, "/x/v2/reply/add", url.Values{"type": {"1"}, "oid": {fmt.Sprint(a.AID)}, "message": {message}, "root": {root}, "parent": {root}})
		}
		set("commentEnabled", false)
		beforeRows := adminTestCount(t, s, "SELECT COUNT(*) FROM replies")
		beforeID := adminTestCount(t, s, "SELECT value FROM ids WHERE name=?", "global")
		assertCode(t, send("closed", "0"), 12002)
		if adminTestCount(t, s, "SELECT COUNT(*) FROM replies") != beforeRows || adminTestCount(t, s, "SELECT value FROM ids WHERE name=?", "global") != beforeID {
			t.Fatal("rejected request changed rows or IDs")
		}
		set("commentEnabled", true)
		root := communityData(t, send("root", "0"))["rpid_str"].(string)
		set("commentReplyEnabled", false)
		assertCode(t, send("no replies", root), 12003)
		set("commentReplyEnabled", true)
		communityExec(t, s, "UPDATE archives SET mid=? WHERE aid=?", int64(1), a.AID)
		communityExec(t, s, "INSERT INTO user_blocks(mid,fid,ctime) VALUES (?,?,1) ON CONFLICT DO NOTHING", int64(1), u.MID)
		assertCode(t, send("owner blocked me", "0"), 12035)
		communityExec(t, s, "DELETE FROM user_blocks WHERE mid=? AND fid=?", int64(1), u.MID)
		set("commentMinInterval", 60)
		assertCode(t, send("too fast", "0"), -509)
		set("commentMinInterval", 0)
		set("commentDuplicateWindow", 60)
		assertCode(t, send("root", "0"), 12051)
		// Same text in another thread is a different reply target.
		assertCode(t, send("root", root), 0)
		communityExec(t, s, "UPDATE replies SET ctime=1 WHERE mid=?", u.MID)
		assertCode(t, send("root", "0"), 0)
		before := adminTestCount(t, s, "SELECT COUNT(*) FROM replies WHERE mid=?", u.MID)
		var wg sync.WaitGroup
		codes := make(chan int, 4)
		for range 4 {
			wg.Add(1)
			go func() { defer wg.Done(); codes <- int(send("concurrent unique", "0")["code"].(float64)) }()
		}
		wg.Wait()
		close(codes)
		success := 0
		for code := range codes {
			if code == 0 {
				success++
			} else if code != 12051 {
				t.Fatalf("unexpected code %d", code)
			}
		}
		if success != 1 || adminTestCount(t, s, "SELECT COUNT(*) FROM replies WHERE mid=?", u.MID) != before+1 {
			t.Fatal("duplicate submission bypassed policy")
		}
		set("commentDuplicateWindow", 0)
		set("commentMinInterval", 60)
		communityExec(t, s, "UPDATE replies SET ctime=1 WHERE mid=?", u.MID)
		// Invalid requests don't consume the freshly available allowance.
		assertCode(t, send("bad parent", "99999999"), 12006)
		codes = make(chan int, 4)
		for i := range 4 {
			wg.Add(1)
			go func() { defer wg.Done(); codes <- int(send(fmt.Sprintf("interval %d", i), "0")["code"].(float64)) }()
		}
		wg.Wait()
		close(codes)
		success = 0
		for code := range codes {
			if code == 0 {
				success++
			} else if code != -509 {
				t.Fatalf("unexpected interval code %d", code)
			}
		}
		if success != 1 {
			t.Fatalf("interval allowed %d concurrent requests", success)
		}
	})
}

func TestReplyAddTimestampAfterLock(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		if s.Store.DB.Driver != "postgres" {
			t.Skip("needs an independent connection to observe a waiting transaction")
		}
		u := login(t, s, "lumen", "demo123")
		a := communityArchives(t, s)[0]
		locked, release := make(chan struct{}), make(chan struct{})
		finished := make(chan error, 1)
		go func() {
			finished <- s.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error {
				if err := LockTx(t.Context(), tx); err != nil {
					return err
				}
				close(locked)
				<-release
				return nil
			})
		}()
		<-locked
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
			<-finished
		}()
		result := make(chan map[string]any, 1)
		go func() {
			result <- communityWrite(t, s, u, "/x/v2/reply/add", url.Values{"type": {"1"}, "oid": {fmt.Sprint(a.AID)}, "message": {"wait behind writer"}})
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			n := adminTestCount(t, s, "SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE ?", "%UPDATE ids SET value=value%")
			if n > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("request never waited for the lock")
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(1100 * time.Millisecond)
		unlockedAt := time.Now().Unix()
		close(release)
		d := communityData(t, <-result)
		if int64(d["reply"].(map[string]any)["ctime"].(float64)) < unlockedAt {
			t.Fatal("reply timestamp predates the acquired write lock")
		}
	})
}

func TestReplySettingsValidation(t *testing.T) {
	s := newTestServer(t)
	admin := login(t, s, "jumedia", "admin123")
	for _, key := range []string{"commentMinInterval", "commentDuplicateWindow"} {
		for _, value := range []string{"-1", "1.5", "86401", "NaN", ""} {
			assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{key: {value}}), -400)
		}
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{key: {"60"}}), 0)
	}
}
