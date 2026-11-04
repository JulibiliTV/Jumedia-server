package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jumedia-server/internal/emby"
)

func TestDandanplayOnDemand(t *testing.T) {
	for _, kind := range []string{"synced", "imported", "episode"} {
		t.Run(kind, func(t *testing.T) {
			testServers(t, func(t *testing.T, s *Server) {
				admin := login(t, s, "jumedia", "admin123")
				f := newSourceFixture(t)
				f.mu.Lock()
				f.views = []map[string]string{{"Id": "movies", "Name": "电影", "CollectionType": "movies"}}
				f.items = map[string][]emby.Item{"movies": {sourceMovie("movie", "按需测试电影", "mp4")}}
				f.mu.Unlock()
				id := sourceSave(t, s, admin, f, true, "fixture")
				a := sourceArchives(t, s, id)["movie"]
				cid := a.Pages[0].CID
				expectedName := "按需测试电影"
				var epID int64
				if kind == "imported" {
					liveExec(t, s, "UPDATE archives SET origin_ds=0,origin_item='',origin='' WHERE aid=?", a.AID)
					liveExec(t, s, "UPDATE archives SET title='待审核：按需测试剧' WHERE aid=?", a.AID)
					liveExec(t, s, "UPDATE archive_pages SET part='S02E03 分集标题' WHERE cid=?", cid)
					expectedName = "按需测试剧.S02E03"
				} else if kind == "episode" {
					if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT ep_id,cid FROM episodes ORDER BY ep_id LIMIT 1").Scan(&epID, &cid); err != nil {
						t.Fatal(err)
					}
					liveExec(t, s, "UPDATE episodes SET media=? WHERE ep_id=?", a.Pages[0].Media, epID)
					liveExec(t, s, "UPDATE seasons SET title='待审核：按需测试剧',season_type=1 WHERE season_id=(SELECT season_id FROM episodes WHERE ep_id=?)", epID)
					liveExec(t, s, "UPDATE episodes SET title='3' WHERE ep_id=?", epID)
					expectedName = "按需测试剧.S01E03"
				}
				liveExec(t, s, "INSERT INTO danmaku(id,cid,progress,mode,fontsize,color,mid,content,ctime,weight) VALUES (?,?,?,?,?,?,?,?,?,?)", 990001, cid, 1000, 1, 25, 16777215, admin.MID, "本地弹幕先显示", 1, 10)
				gate := make(chan struct{})
				var release sync.Once
				var calls atomic.Int32
				var failure atomic.Bool
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if failure.Load() {
						w.WriteHeader(503)
						return
					}
					select {
					case <-gate:
					case <-r.Context().Done():
						return
					}
					if r.URL.Path == "/api/v2/match" {
						var body struct{ FileName string }
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FileName != expectedName {
							t.Errorf("match query = %q, want %q (decode: %v)", body.FileName, expectedName, err)
						}
						fmt.Fprint(w, `{"success":true,"matches":[{"episodeId":123}]}`)
					} else {
						fmt.Fprint(w, `{"comments":[{"cid":1,"p":"2,1,16777215,user","m":"异步合并的外部弹幕"}]}`)
					}
				}))
				defer upstream.Close()
				defer release.Do(func() { close(gate) })
				endpoint := fmt.Sprintf("/x/v2/dm/list/seg.so?oid=%d&segment_index=1", cid)
				if response := sourceHTTP(s, endpoint); response.Header().Get("X-Jumedia-Danmaku-Pending") == "true" || adminTestCount(t, s, "SELECT count(*) FROM dandanplay_sync WHERE cid=?", cid) != 0 {
					t.Fatal("disabled provider was requested")
				}
				assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayApi": {upstream.URL}, "dandanplayEnabled": {"true"}}), 0)
				table := "archive_pages"
				if kind == "episode" {
					table = "episodes"
				}
				liveExec(t, s, "UPDATE "+table+" SET media=? WHERE cid=?", "/fixture/local.mp4", cid)
				if response := sourceHTTP(s, endpoint); response.Header().Get("X-Jumedia-Danmaku-Pending") == "true" || adminTestCount(t, s, "SELECT count(*) FROM dandanplay_sync WHERE cid=?", cid) != 0 {
					t.Fatal("non-source media was requested")
				}
				liveExec(t, s, "UPDATE "+table+" SET media=? WHERE cid=?", a.Pages[0].Media, cid)
				if kind == "imported" {
					liveExec(t, s, `INSERT INTO dandanplay_sync(cid,aid,archive_aid,media,filename,duration,status,attempted_at)
						VALUES (?,?,?,?,?,30,'unmatched',?)`, cid, a.AID, a.AID, a.Pages[0].Media, "待审核：按需测试剧 S02E03 分集标题", time.Now().Add(-31*time.Minute).Unix())
				}
				started := time.Now()
				first := sourceHTTP(s, endpoint)
				if first.Code != 200 || !bytes.Contains(first.Body.Bytes(), []byte("本地弹幕先显示")) || time.Since(started) > time.Second {
					t.Fatal("local comments did not return promptly")
				}
				if first.Header().Get("X-Jumedia-Danmaku-Pending") != "true" || first.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("missing pending response metadata")
				}
				waitDandanplay(t, s, cid, "fetching")
				for range 3 {
					if response := sourceHTTP(s, endpoint); response.Header().Get("X-Jumedia-Danmaku-Pending") != "true" {
						t.Fatal("in-flight task lost")
					}
				}
				release.Do(func() { close(gate) })
				waitDandanplay(t, s, cid, "ready")
				for range 3 {
					response := sourceHTTP(s, endpoint)
					if response.Header().Get("X-Jumedia-Danmaku-Pending") == "true" || bytes.Count(response.Body.Bytes(), []byte("本地弹幕先显示")) != 1 || bytes.Count(response.Body.Bytes(), []byte("异步合并的外部弹幕")) != 1 {
						t.Fatal("completed response did not merge both comment sources once")
					}
				}
				if calls.Load() != 2 {
					t.Fatal("duplicate upstream work", calls.Load())
				}
				failure.Store(true)
				liveExec(t, s, "UPDATE dandanplay_sync SET status='pending' WHERE cid=?", cid)
				waitDandanplay(t, s, cid, "ready")
				for range 3 {
					response := sourceHTTP(s, endpoint)
					if response.Header().Get("X-Jumedia-Danmaku-Pending") == "true" || !bytes.Contains(response.Body.Bytes(), []byte("异步合并的外部弹幕")) {
						t.Fatal("failure lost cache or ignored retry cooldown")
					}
				}
				failure.Store(false)
				liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=0,status='pending' WHERE cid=?", cid)
				if response := sourceHTTP(s, endpoint); response.Header().Get("X-Jumedia-Danmaku-Pending") != "true" {
					t.Fatal("failed task was not retried after cooldown")
				}
				waitDandanplay(t, s, cid, "ready")
				if calls.Load() != 5 {
					t.Fatal("unexpected retry count", calls.Load())
				}
				if kind == "episode" {
					liveExec(t, s, "DELETE FROM episodes WHERE ep_id=?", epID)
					if adminTestCount(t, s, "SELECT count(*) FROM dandanplay_sync WHERE cid=?", cid) != 0 {
						t.Fatal("episode deletion did not clean cache")
					}
				}
			})
		})
	}
}
