package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

func TestDandanplaySettings(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		viewer := login(t, s, "lumen", "demo123")
		before := adminTestCount(t, s, "SELECT COUNT(*) FROM settings")
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"unknownSetting": {"ignored"}}), 0)
		if adminTestCount(t, s, "SELECT COUNT(*) FROM settings") != before {
			t.Fatal("unrelated save created credential settings")
		}
		assertCode(t, adminTestPost(t, s, viewer, "settings/save", url.Values{"dandanplayEnabled": {"true"}}), -403)
		assertCode(t, post(t, s, "/x/admin/settings/save", url.Values{"dandanplayEnabled": {"true"}}, admin), -111)
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayEnabled": {"true"}, "dandanplayApi": {" https://fixture.invalid/fixture-token/api/v2/ "}}), 0)
		selfHosted := adminTestData(t, get(t, s, "/x/admin/settings", admin))
		if selfHosted["dandanplayApi"] != "https://fixture.invalid/fixture-token/api/v2" || selfHosted["dandanplayHasSecret"] != false || selfHosted["dandanplayEnabled"] != true {
			t.Fatal("self-hosted settings not saved")
		}
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayAppId": {"fixture-app"}}), -400)
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayAppSecret": {"fixture-secret"}}), -400)
		for _, address := range []string{"invalid", "ftp://host", "https://user:password@host", "https://host?token=private"} {
			assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayApi": {address}, "siteName": {"invalid save"}}), -400)
		}
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayEnabled": {"true"}, "dandanplayAppId": {"fixture-app"}, "dandanplayAppSecret": {"fixture-secret"}}), 0)
		settings := adminTestData(t, get(t, s, "/x/admin/settings", admin))
		if settings["dandanplayHasSecret"] != true || settings[dandanplaySecretKey] != nil || settings["dandanplayAppSecret"] != nil {
			t.Fatal("secret exposed", settings)
		}
		stored, err := s.Store.Settings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		enc := serviceString(stored[dandanplaySecretKey])
		if enc == "" || strings.Contains(enc, "fixture-secret") {
			t.Fatal("secret was not encrypted")
		}
		plain, err := s.decryptPassword(enc)
		if err != nil || plain != "fixture-secret" {
			t.Fatal("secret cannot be decrypted", err)
		}
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayAppSecret": {""}, dandanplaySecretKey: {"injected"}}), 0)
		stored, _ = s.Store.Settings(t.Context())
		if stored[dandanplaySecretKey] != enc {
			t.Fatal("blank secret changed stored credentials")
		}
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplaySecretClear": {"true"}}), -400)
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayAppId": {""}, "dandanplaySecretClear": {"true"}}), 0)
		settings = adminTestData(t, get(t, s, "/x/admin/settings", admin))
		if settings["dandanplayHasSecret"] != false || settings["dandanplayEnabled"] != true || settings["dandanplayAppId"] != "" {
			t.Fatal("credentials not cleared while enabled")
		}
		for _, endpoint := range []string{"/x/admin/logs", "/x/jumedia/site", "/x/jumedia/service"} {
			body, _ := json.Marshal(get(t, s, endpoint, admin))
			if bytes.Contains(body, []byte("fixture-secret")) || bytes.Contains(body, []byte(enc)) {
				t.Fatal("credentials leaked", endpoint)
			}
		}
	})
}

func waitDandanplay(t *testing.T, s *Server, cid int64, status string) *store.DandanplaySync {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		job, err := store.ScanDandanplaySync(s.Store.DB.QueryRowContext(t.Context(), "SELECT "+store.DandanplaySyncColumns+" FROM dandanplay_sync WHERE cid=?", cid))
		if err == nil && job.Status == status {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("danmaku status did not reach %s", status)
	return nil
}

func TestDandanplayEmbyAsyncAndCache(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		viewer := login(t, s, "lumen", "demo123")
		gate := make(chan struct{})
		var release sync.Once
		defer release.Do(func() { close(gate) })
		var failure atomic.Bool
		var calls atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			for _, h := range []string{"X-AppId", "X-Signature", "X-Timestamp"} {
				if _, ok := r.Header[http.CanonicalHeaderKey(h)]; ok {
					t.Errorf("self-hosted sync contains %s", h)
				}
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
			if failure.Load() {
				w.WriteHeader(503)
				return
			}
			if r.URL.Path == "/fixture-token/api/v2/match" {
				var body struct{ FileName string }
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.FileName != "测试电影" {
					t.Error("full source path leaked or filename mismatch", body.FileName)
				}
				fmt.Fprint(w, `{"success":true,"matches":[{"episodeId":123,"animeTitle":"测试电影","episodeTitle":"正片"}]}`)
			} else if r.URL.Path == "/fixture-token/api/v2/comment/123" {
				fmt.Fprint(w, `{"comments":[{"cid":1,"p":"0,1,16777215,user","m":"已同步的外部弹幕"},{"cid":2,"p":"1,5,255,user","m":"顶部弹幕"}]}`)
			} else {
				http.NotFound(w, r)
			}
		}))
		defer upstream.Close()
		// Release a blocked request before httptest waits for its handlers at cleanup.
		defer release.Do(func() { close(gate) })
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"dandanplayEnabled": {"true"}, "dandanplayApi": {upstream.URL + "/fixture-token/api/v2/"}}), 0)
		f := newSourceFixture(t)
		f.mu.Lock()
		f.views = []map[string]string{{"Id": "movies", "Name": "电影", "CollectionType": "movies"}}
		movie := sourceMovie("movie", "测试电影", "mp4")
		movie.Path = `C:\private\release.1080p.mkv`
		f.items = map[string][]emby.Item{"movies": {movie}}
		f.mu.Unlock()
		id := sourceSave(t, s, admin, f, true, "fixture")
		a := sourceArchives(t, s, id)["movie"]
		if a == nil {
			t.Fatal("media sync blocked by danmaku")
		}
		cid := a.Pages[0].CID
		if n := adminTestCount(t, s, "SELECT COUNT(*) FROM dandanplay_comments WHERE cid=?", cid); n != 0 {
			t.Fatal("unexpected cached comments")
		}
		release.Do(func() { close(gate) })
		job := waitDandanplay(t, s, cid, "ready")
		if job.Count != 2 || job.EpisodeID != 123 {
			t.Fatal(job)
		}
		assertCode(t, get(t, s, fmt.Sprintf("/x/admin/archive/danmaku?aid=%d", a.AID)), -101)
		assertCode(t, get(t, s, fmt.Sprintf("/x/admin/archive/danmaku?aid=%d", a.AID), viewer), -403)
		assertCode(t, get(t, s, fmt.Sprintf("/x/admin/archive/danmaku?aid=%d&cid=1", a.AID), admin), -404)
		preview := adminTestData(t, get(t, s, fmt.Sprintf("/x/admin/archive/danmaku?aid=%d&cid=%d&ps=1", a.AID, cid), admin))
		if len(preview["list"].([]any)) != 1 || preview["page"].(map[string]any)["total"] != float64(2) {
			t.Fatal(preview)
		}
		// User comments remain independent of provider cache replacement.
		err := s.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error {
			id, err := store.NextID(t.Context(), tx)
			if err != nil {
				return err
			}
			return store.InsertDanmaku(t.Context(), tx, &store.Danmaku{ID: id, CID: cid, MID: admin.MID, Content: "本地用户弹幕", Mode: 1, Fontsize: 25, Weight: 10})
		})
		if err != nil {
			t.Fatal(err)
		}
		w := sourceHTTP(s, fmt.Sprintf("/x/v2/dm/list/seg.so?oid=%d&segment_index=1", cid))
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("已同步的外部弹幕")) || !bytes.Contains(w.Body.Bytes(), []byte("本地用户弹幕")) {
			t.Fatal("playback did not merge comments")
		}
		failure.Store(true)
		liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=0 WHERE cid=?", cid)
		sourceSync(t, s, admin, id)
		job = waitDandanplay(t, s, cid, "ready")
		if job.Count != 2 || job.UpdatedAt == 0 {
			t.Fatal("failed fetch lost cached metadata")
		}
		if adminTestCount(t, s, "SELECT COUNT(*) FROM dandanplay_comments WHERE cid=?", cid) != 2 {
			t.Fatal("failed fetch removed cache")
		}
		failure.Store(false)
		liveExec(t, s, "UPDATE dandanplay_sync SET attempted_at=0 WHERE cid=?", cid)
		sourceSync(t, s, admin, id)
		waitDandanplay(t, s, cid, "ready")
		if adminTestCount(t, s, "SELECT COUNT(*) FROM dandanplay_comments WHERE cid=?", cid) != 2 || adminTestCount(t, s, "SELECT COUNT(*) FROM danmaku WHERE cid=?", cid) != 1 {
			t.Fatal("resync duplicated cache or deleted user comment")
		}
		if sourceArchives(t, s, id)["movie"].Pages[0].CID != cid {
			t.Fatal("resync changed cid")
		}
		if adminTestCount(t, s, "SELECT stat_danmaku FROM archives WHERE aid=?", a.AID) != 2 {
			t.Fatal("external comment count drifted")
		}
		// Restore an interrupted job after restarting the server.
		s.CloseDataSources()
		liveExec(t, s, "UPDATE dandanplay_sync SET status='fetching' WHERE cid=?", cid)
		restart := New(s.Store, s.Config)
		t.Cleanup(restart.CloseDataSources)
		if err := restart.InitDataSources(context.Background()); err != nil {
			t.Fatal(err)
		}
		waitDandanplay(t, restart, cid, "ready")
		// A missing source item is hidden without another provider download.
		f.mu.Lock()
		f.items["movies"] = nil
		f.mu.Unlock()
		beforeCalls := calls.Load()
		sourceSync(t, restart, admin, id)
		if calls.Load() != beforeCalls || sourceArchives(t, restart, id)["movie"].State != -6 {
			t.Fatal("missing item was fetched again or remained visible")
		}
		if calls.Load() < 7 {
			t.Fatal("worker did not perform requests")
		}
	})
}

func TestDandanplayPrune(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		archives, err := s.Store.ListArchive(t.Context(), "ORDER BY aid LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		a := archives[0]
		pages, err := s.Store.ListPage(t.Context(), "WHERE aid=? ORDER BY position", a.AID)
		if err != nil {
			t.Fatal(err)
		}
		err = s.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error {
			if e := queueDandanplay(t.Context(), tx, pages[0], "fixture"); e != nil {
				return e
			}
			if _, e := tx.ExecContext(t.Context(), "UPDATE dandanplay_sync SET count=2 WHERE cid=?", pages[0].CID); e != nil {
				return e
			}
			if _, e := tx.ExecContext(t.Context(), "UPDATE archives SET stat_danmaku=2 WHERE aid=?", a.AID); e != nil {
				return e
			}
			if _, e := tx.ExecContext(t.Context(), "DELETE FROM archive_pages WHERE cid=?", pages[0].CID); e != nil {
				return e
			}
			return pruneDandanplay(t.Context(), tx, a.AID)
		})
		if err != nil {
			t.Fatal(err)
		}
		if adminTestCount(t, s, "SELECT COUNT(*) FROM dandanplay_sync WHERE aid=?", a.AID) != 0 || adminTestCount(t, s, "SELECT stat_danmaku FROM archives WHERE aid=?", a.AID) != 0 {
			t.Fatal("stale cache survived page removal")
		}
	})
}
