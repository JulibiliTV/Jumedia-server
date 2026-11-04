package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"jumedia-server/internal/emby"
)

func TestDandanplayRetryWindow(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		f := newSourceFixture(t)
		f.mu.Lock()
		f.views = []map[string]string{{"Id": "movies", "Name": "电影", "CollectionType": "movies"}}
		f.items = map[string][]emby.Item{"movies": {sourceMovie("movie", "续播测试", "mp4")}}
		f.mu.Unlock()
		id := sourceSave(t, s, admin, f, true, "fixture")
		a := sourceArchives(t, s, id)["movie"]
		cid := a.Pages[0].CID
		var mode, calls atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if mode.Load() == 2 {
				w.WriteHeader(503)
			} else if r.URL.Path == "/api/v2/match" {
				if mode.Load() == 1 {
					fmt.Fprint(w, `{"success":true,"matches":[]}`)
				} else {
					fmt.Fprint(w, `{"success":true,"matches":[{"episodeId":123}]}`)
				}
			} else if mode.Load() == 3 {
				fmt.Fprint(w, `{"comments":[{"cid":1,"p":"600,1,16777215,user","m":"续播弹幕"}]}`)
			} else {
				fmt.Fprint(w, `{"count":0,"comments":[]}`)
			}
		}))
		defer upstream.Close()
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayEnabled": {"true"}, "dandanplayApi": {upstream.URL}}), 0)
		endpoint := fmt.Sprintf("/x/v2/dm/list/seg.so?oid=%d&segment_index=2", cid)
		if sourceHTTP(s, endpoint).Header().Get("X-Jumedia-Danmaku-Pending") != "true" {
			t.Fatal("first resume did not queue a request")
		}
		waitDandanplay(t, s, cid, "ready")
		var attempted int64
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT attempted_at FROM dandanplay_sync WHERE cid=?", cid).Scan(&attempted); err != nil || attempted == 0 {
			t.Fatal("attempt time was not persisted", err)
		}
		// A restarted process must retain the empty result and its cooldown.
		s.CloseDataSources()
		restart := New(s.Store, s.Config)
		t.Cleanup(restart.CloseDataSources)
		if err := restart.InitDataSources(t.Context()); err != nil {
			t.Fatal(err)
		}
		s = restart
		for _, test := range []struct {
			status string
			mode   int32
		}{{"ready", 0}, {"unmatched", 1}, {"error", 2}} {
			mode.Store(test.mode)
			for _, age := range []int64{0, 60, 1700} {
				liveExec(t, s, "UPDATE dandanplay_sync SET status=?,count=0,attempted_at=? WHERE cid=?", test.status, time.Now().Unix()-age, cid)
				before := calls.Load()
				for range 3 {
					if sourceHTTP(s, endpoint).Header().Get("X-Jumedia-Danmaku-Pending") != "false" {
						t.Fatalf("%s retried within 30 minutes (age=%d)", test.status, age)
					}
				}
				if calls.Load() != before {
					t.Fatal("cooldown made an upstream request")
				}
			}
			// Media synchronization must not bypass the same-media attempt window.
			liveExec(t, s, "UPDATE dandanplay_sync SET filename='旧查询名称' WHERE cid=?", cid)
			sourceSync(t, s, admin, id)
			if sourceHTTP(s, endpoint).Header().Get("X-Jumedia-Danmaku-Pending") != "false" {
				t.Fatal("source synchronization bypassed cooldown")
			}
			liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=? WHERE cid=?", time.Now().Unix()-1801, cid)
			if sourceHTTP(s, endpoint).Header().Get("X-Jumedia-Danmaku-Pending") != "true" {
				t.Fatalf("%s empty result never retried after 30 minutes", test.status)
			}
			resultStatus := test.status
			if test.mode == 2 {
				resultStatus = "unmatched" // The preceding normal empty match remains cached.
			}
			job := waitDandanplay(t, s, cid, resultStatus)
			if job.Count != 0 {
				t.Fatal("empty result was not persisted")
			}
		}
		mode.Store(3)
		liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=? WHERE cid=?", time.Now().Unix()-1801, cid)
		sourceHTTP(s, endpoint)
		if job := waitDandanplay(t, s, cid, "ready"); job.Count != 1 {
			t.Fatal("retry did not save fetched comments")
		}
		liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=0 WHERE cid=?", cid)
		if sourceHTTP(s, endpoint).Header().Get("X-Jumedia-Danmaku-Pending") != "false" {
			t.Fatal("nonempty cache was fetched again")
		}
	})
}
