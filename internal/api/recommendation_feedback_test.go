package api

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"jumedia-server/internal/store"
)

const feedbackPath = "/x/web-interface/feedback/dislike"

func feedbackForm(session *store.Session, aid, mid int64, reason string) url.Values {
	return url.Values{"csrf": {session.CSRF}, "goto": {"av"}, "id": {fmt.Sprint(aid)}, "mid": {fmt.Sprint(mid)}, "reason_id": {reason}}
}

func TestRecommendationFeedback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		arcs := discoverArchives(t, s)
		a := arcs[0]
		// Keep one author's two videos and a second author's video as a small feed.
		b, other := a, a
		for _, v := range arcs[1:] {
			if v.MID == a.MID {
				b = v
			} else {
				other = v
			}
		}
		if b.AID == a.AID || other.MID == a.MID {
			t.Fatal("fixture needs two authors and sibling videos")
		}
		discoverExec(t, s, "UPDATE archives SET state=-1 WHERE aid NOT IN (?,?,?)", a.AID, b.AID, other.AID)
		check := func(auth *store.Session, want ...int64) {
			t.Helper()
			for _, path := range []string{"/x/web-interface/wbi/index/top/feed/rcmd?ps=12&fresh_idx=1", "/x/web-interface/wbi/index/top/feed/rcmd?ps=12&fresh_idx=2", "/x/v2/feed/index?idx=0", "/x/v2/feed/index?idx=20"} {
				d := discoverData(t, get(t, s, path, auth))
				raw := d["item"]
				if raw == nil {
					raw = d["items"]
				}
				found := map[int64]bool{}
				for _, row := range raw.([]any) {
					v := row.(map[string]any)
					if d["item"] != nil {
						if v["dislike_switch"] != (auth != nil) || v["pubdate"].(float64) <= 0 {
							t.Fatal("invalid recommendation metadata", v)
						}
					} else {
						reasons := v["three_point"].(map[string]any)["dislike_reasons"].([]any)
						if len(reasons) != 2 || reasons[0].(map[string]any)["id"] != float64(1) || reasons[1].(map[string]any)["id"] != float64(4) {
							t.Fatal("unsupported feedback reasons", reasons)
						}
					}
					id, ok := v["aid"].(float64)
					if !ok {
						id = v["player_args"].(map[string]any)["aid"].(float64)
					}
					found[int64(id)] = true
				}
				if len(found) != len(want) {
					t.Fatalf("%s got %v want %v", path, found, want)
				}
				for _, id := range want {
					if !found[id] {
						t.Fatalf("missing %d in %v", id, found)
					}
				}
			}
		}
		video := feedbackForm(session, a.AID, a.MID, "1")
		author := feedbackForm(session, b.AID, b.MID, "4")
		check(session, a.AID, b.AID, other.AID)
		for range 2 {
			assertCode(t, post(t, s, feedbackPath, video, session), 0)
		}
		check(session, b.AID, other.AID)
		for range 2 {
			assertCode(t, post(t, s, feedbackPath, author, session), 0)
		}
		check(session, other.AID)
		check(nil, a.AID, b.AID, other.AID)
		// A second persisted session for a different user must see the full feed.
		second := &store.Session{Token: "feedback-other", Access: "feedback-other-access", CSRF: "feedback-other-csrf", Refresh: "feedback-other-refresh", MID: other.MID, Ctime: 1}
		if second.MID == session.MID {
			second.MID = a.MID
		}
		if err := store.InsertSession(context.Background(), s.Store.DB, second); err != nil {
			t.Fatal(err)
		}
		check(second, a.AID, b.AID, other.AID)
		var count int
		if err := s.Store.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM recommendation_feedback WHERE mid=?", session.MID).Scan(&count); err != nil || count != 2 {
			t.Fatalf("persisted feedback count=%d err=%v", count, err)
		}
		// Reconstruct the repository facade: behavior must come from DB state.
		s.Store = store.New(s.Store.DB)
		if err := s.Store.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		check(session, other.AID)
		for range 2 {
			assertCode(t, post(t, s, feedbackPath+"/cancel", author, session), 0)
		}
		check(session, b.AID, other.AID)
		for range 2 {
			assertCode(t, post(t, s, feedbackPath+"/cancel", video, session), 0)
		}
		check(session, a.AID, b.AID, other.AID)
		// Cancelling a video preference cannot remove its author preference.
		assertCode(t, post(t, s, feedbackPath, author, session), 0)
		assertCode(t, post(t, s, feedbackPath, video, session), 0)
		assertCode(t, post(t, s, feedbackPath+"/cancel", video, session), 0)
		check(session, other.AID)
		assertCode(t, post(t, s, feedbackPath, feedbackForm(session, other.AID, other.MID, "4"), session), 0)
		check(session)
		assertCode(t, get(t, s, fmt.Sprintf("/x/web-interface/view?aid=%d", a.AID), session), 0)
		if rows := discoverData(t, get(t, s, "/x/web-interface/popular?ps=12", session))["list"].([]any); len(rows) != 3 {
			t.Fatal("feedback changed popular")
		}
	})
}

func TestRecommendationFeedbackValidationAndLegacy(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		a := discoverArchives(t, s)[0]
		for _, path := range []string{feedbackPath, feedbackPath + "/cancel"} {
			assertCode(t, post(t, s, path, nil), -101)
			assertCode(t, post(t, s, path, nil, session), -111)
			assertCode(t, get(t, s, path+"?"+feedbackForm(session, a.AID, a.MID, "1").Encode(), session), -405)
			for _, override := range []url.Values{{"id": {"0"}}, {"id": {"1.5"}}, {"id": {"999999999"}}, {"goto": {"live"}}, {"reason_id": {"3"}}, {"mid": {"999999"}}, {"mid": {"bad"}}} {
				p := feedbackForm(session, a.AID, a.MID, "4")
				for k, v := range override {
					p[k] = v
				}
				code := -400
				if p.Get("id") == "999999999" {
					code = -404
				}
				assertCode(t, post(t, s, path, p, session), code)
			}
		}
		another := login(t, s, "jumedia", "admin123")
		p := feedbackForm(session, a.AID, a.MID, "1")
		p.Set("csrf", another.CSRF)
		assertCode(t, post(t, s, feedbackPath, p, session), -111)
		p = feedbackForm(session, a.AID, a.MID, "1")
		p.Set("biliCSRF", session.CSRF)
		p.Del("csrf")
		assertCode(t, post(t, s, feedbackPath, p, session), 0)
		assertCode(t, post(t, s, feedbackPath+"/cancel", p, session), 0)
		p.Del("biliCSRF")
		p.Del("mid")
		assertCode(t, get(t, s, "/x/feed/dislike?"+p.Encode(), session), -111)
		p.Set("access_key", session.Access)
		p.Set("reason_id", "4")
		assertCode(t, get(t, s, "/x/feed/dislike?"+p.Encode()), 0)
		assertCode(t, post(t, s, feedbackPath+"/cancel", feedbackForm(session, a.AID, a.MID, "4"), session), 0)
		discoverExec(t, s, "UPDATE users SET status=1 WHERE mid=?", session.MID)
		assertCode(t, post(t, s, feedbackPath, feedbackForm(session, a.AID, a.MID, "1"), session), -403)
	})
}
