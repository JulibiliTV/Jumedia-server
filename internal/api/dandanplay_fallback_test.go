package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"

	"jumedia-server/internal/emby"
)

func TestDandanplayFailureKeepsNormalResult(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		f := newSourceFixture(t)
		f.mu.Lock()
		f.views = []map[string]string{{"Id": "movies", "Name": "电影", "CollectionType": "movies"}}
		f.items = map[string][]emby.Item{"movies": {sourceMovie("movie", "失败回退测试", "mp4")}}
		f.mu.Unlock()
		id := sourceSave(t, s, admin, f, true, "fixture")
		cid := sourceArchives(t, s, id)["movie"].Pages[0].CID
		var fail, empty, unmatched atomic.Bool
		fail.Store(true)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if fail.Load() {
				w.WriteHeader(503)
				return
			}
			if r.URL.Path == "/api/v2/match" {
				if unmatched.Load() {
					fmt.Fprint(w, `{"success":true,"matches":[]}`)
				} else {
					fmt.Fprint(w, `{"success":true,"matches":[{"episodeId":123,"animeTitle":"正常结果","episodeTitle":"正片"}]}`)
				}
			} else if empty.Load() {
				fmt.Fprint(w, `{"count":0,"comments":[]}`)
			} else {
				fmt.Fprint(w, `{"comments":[{"cid":1,"p":"1,1,16777215,user","m":"保留正常弹幕"}]}`)
			}
		}))
		defer upstream.Close()
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayEnabled": {"true"}, "dandanplayApi": {upstream.URL}}), 0)
		endpoint := fmt.Sprintf("/x/v2/dm/list/seg.so?oid=%d&segment_index=1", cid)
		sourceHTTP(s, endpoint)
		job := waitDandanplay(t, s, cid, "idle")
		if job.Message != "" || job.UpdatedAt != 0 || job.EpisodeID != 0 {
			t.Fatal("first failure was saved as a result", job)
		}
		if sourceHTTP(s, endpoint).Header().Get("X-Jumedia-Danmaku-Pending") != "false" {
			t.Fatal("attempt throttle was lost")
		}
		for _, emptyResult := range []bool{false, true} {
			fail.Store(false)
			empty.Store(emptyResult)
			unmatched.Store(false)
			liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=0 WHERE cid=?", cid)
			if emptyResult {
				sourceSync(t, s, admin, id)
			} else if sourceHTTP(s, endpoint).Header().Get("X-Jumedia-Danmaku-Pending") != "true" {
				t.Fatal("uncached failure did not retry after the interval")
			}
			normal := waitDandanplay(t, s, cid, "ready")
			comments, err := s.Store.DandanplayComments(t.Context(), cid)
			if err != nil || (len(comments) == 0) != emptyResult {
				t.Fatal("normal result not stored", err)
			}
			fail.Store(true)
			liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=0 WHERE cid=?", cid)
			sourceSync(t, s, admin, id)
			fallback := waitDandanplay(t, s, cid, "ready")
			retained, err := s.Store.DandanplayComments(t.Context(), cid)
			if err != nil || !reflect.DeepEqual(normal, fallback) || !reflect.DeepEqual(comments, retained) {
				t.Fatal("failed retry replaced the last normal result", err)
			}
			if sourceHTTP(s, endpoint).Header().Get("X-Jumedia-Danmaku-Pending") != "false" {
				t.Fatal("failed retry bypassed the 30-minute interval")
			}
			if !emptyResult {
				fail.Store(false)
				unmatched.Store(true)
				liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=0 WHERE cid=?", cid)
				sourceSync(t, s, admin, id)
				fallback = waitDandanplay(t, s, cid, "ready")
				retained, err = s.Store.DandanplayComments(t.Context(), cid)
				if err != nil || !reflect.DeepEqual(normal, fallback) || !reflect.DeepEqual(comments, retained) {
					t.Fatal("unmatched refresh replaced usable cached comments", err)
				}
			}
		}
	})
}
