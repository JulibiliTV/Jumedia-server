package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"jumedia-server/internal/config"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

// Exercise the HEAD raw-playback security boundary without a listening upstream:
// all denied requests must stop at our HTTP handler and return 404.
func TestSourceMediaAuthorizationRevocation(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, st *store.Store) {
		if err := seed.Seed(t.Context(), st); err != nil {
			t.Fatal(err)
		}
		if err := seed.Admin(t.Context(), st, "owner", "secret123"); err != nil {
			t.Fatal(err)
		}
		s := New(st, config.Config{DataDir: t.TempDir()})
		t.Cleanup(s.CloseDataSources)
		if err := s.ImportDemo(t.Context(), t30Package(t)); err != nil {
			t.Fatal(err)
		}
		pages, err := s.Store.ListPage(t.Context(), "ORDER BY cid LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		p := pages[0]
		ds, _, _, _, ok := emby.ParseDSRef(p.Media)
		if !ok {
			t.Fatal(p)
		}
		ref := fmt.Sprintf("embyds:%d:security_item:security_source:raw-mkv", ds)
		liveExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=?", ref, p.CID)
		liveExec(t, s, "UPDATE archives SET origin_ds=? WHERE aid=?", ds, p.AID)
		rooms, err := s.Store.ListLiveRoom(t.Context(), "ORDER BY room_id LIMIT 1")
		if err != nil || len(rooms) != 1 {
			t.Fatal(rooms, err)
		}
		rid := rooms[0].RoomID
		stale := fmt.Sprintf(`{"type":"video","aid":%d,"cid":%d,"url":%q}`, p.AID, p.CID, ref)
		liveExec(t, s, "UPDATE rooms SET media=?,source=? WHERE room_id=?", ref, stale, rid)
		key := "11234567890123456789012345678901"
		liveExec(t, s, "INSERT INTO live_caches (key,media,height,aid,cid,dir,ctime,mtime) VALUES (?,?,1080,?,?,?,1,1)", key, ref, p.AID, p.CID, t.TempDir())
		path := emby.DSPath(ds, "security_item", "security_source", "raw-mkv")
		checkAllowed := func(t *testing.T, want bool) {
			t.Helper()
			got, err := s.sourceMediaAllowed(t.Context(), ds, "security_item", "security_source", "raw-mkv")
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
		}
		checkDenied := func(t *testing.T) {
			t.Helper()
			checkAllowed(t, false)
			if w := sourceHTTP(s, path); w.Code != http.StatusNotFound {
				t.Fatal("original file was not revoked", w.Code, w.Body.String())
			}
		}
		checkAllowed(t, true)
		for _, status := range []string{"pending", "running", "ready", "failed"} {
			t.Run(status, func(t *testing.T) {
				liveExec(t, s, "UPDATE live_caches SET status=? WHERE key=?", status, key)
				liveExec(t, s, "UPDATE archives SET state=-100 WHERE aid=?", p.AID)
				checkDenied(t)
				liveExec(t, s, "UPDATE archives SET state=0,origin_ds=? WHERE aid=?", ds+999999, p.AID)
				checkDenied(t)
				// A cache or expired live reference cannot stand in for a replaced page.
				liveExec(t, s, "UPDATE archives SET origin_ds=? WHERE aid=?", ds, p.AID)
				liveExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=?", "/upload/replaced.mp4", p.CID)
				checkDenied(t)
				liveExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=?", ref, p.CID)
				checkAllowed(t, true)
			})
		}
		// Assembled demo pages have no origin; they still require a live page.
		liveExec(t, s, "UPDATE archives SET origin_ds=0 WHERE aid=?", p.AID)
		checkAllowed(t, true)
		liveExec(t, s, "UPDATE archives SET state=-100 WHERE aid=?", p.AID)
		checkDenied(t)
	})
}

func TestLiveCacheDataSourceInitialization(t *testing.T) {
	s := newTestServer(t)
	_, p, _ := cacheSetup(t, s)
	if s.sources.aead == nil || s.sources.err != nil {
		t.Fatal("cache fixture must initialize datasource runtime before cancellable jobs", s.sources.err)
	}
	id, _, _, _, ok := emby.ParseDSRef(p.Media)
	if !ok {
		t.Fatal(p)
	}
	ds, err := store.DataSourceByID(t.Context(), s.Store.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = s.sourceClient(cancelled, ds); err == nil {
		t.Fatal("cancelled request accepted")
	}
	if _, err = s.sourceClient(t.Context(), ds); err != nil {
		t.Fatal("cancelled job poisoned later datasource access", err)
	}
}

func TestSourceMediaSQLFilters(t *testing.T) {
	s := initialServer(t)
	if err := s.ImportDemo(t.Context(), t30Package(t)); err != nil {
		t.Fatal(err)
	}
	sources, err := s.Store.ListDataSource(t.Context())
	if err != nil || len(sources) == 0 {
		t.Fatal(sources, err)
	}
	ds := sources[0].ID
	// Underscores must remain literal in SQL LIKE, and SQLite LIKE's
	// case-insensitivity must not grant a differently cased item.
	if got := sourceLikeLiteral("a!b%c_d"); got != "a!!b!%c!_d" {
		t.Fatal(got)
	}
	check := func(t *testing.T, item, mode string, want bool) {
		t.Helper()
		got, err := s.sourceMediaAllowed(t.Context(), ds, item, "src", mode)
		if err != nil || got != want {
			t.Fatal(item, mode, got, want, err)
		}
	}
	for _, tc := range []struct{ name, query string }{
		{"archive", "UPDATE archives SET pic=? WHERE aid=(SELECT MIN(aid) FROM archives)"},
		{"collection", "UPDATE collections SET cover=? WHERE id=(SELECT MIN(id) FROM collections)"},
		{"episode", "UPDATE episodes SET cover=? WHERE ep_id=(SELECT MIN(ep_id) FROM episodes)"},
		{"season", "UPDATE seasons SET cover=? WHERE season_id=(SELECT MIN(season_id) FROM seasons)"},
		{"horizontal", "UPDATE seasons SET horizontal=? WHERE season_id=(SELECT MIN(season_id) FROM seasons)"},
		{"room", "UPDATE rooms SET cover=? WHERE room_id=(SELECT MIN(room_id) FROM rooms)"},
		{"face", "UPDATE users SET face=? WHERE mid=(SELECT MIN(mid) FROM users)"},
		{"top_photo", "UPDATE users SET top_photo=? WHERE mid=(SELECT MIN(mid) FROM users)"},
		{"banner", "UPDATE banners SET img=? WHERE id=(SELECT MIN(id) FROM banners)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := "filter_" + tc.name
			for _, ref := range []string{
				fmt.Sprintf("/media/emby/%s/image/Primary?ds=%d", item, ds),
				fmt.Sprintf("https://proxy.invalid/media/emby/%s/image/Backdrop?tag=x&ds=%d&x=1", item, ds),
			} {
				liveExec(t, s, tc.query, ref)
				check(t, item, "image", true)
				check(t, "Filter_"+tc.name, "image", false)
				check(t, "filterX"+tc.name, "image", false)
				check(t, item+"suffix", "image", false)
				check(t, item, "direct", false)
			}
			liveExec(t, s, tc.query, fmt.Sprintf("/media/emby/%s/image/?ds=%d", item, ds))
			check(t, item, "image", false)
			liveExec(t, s, tc.query, "")
			check(t, item, "image", false)
		})
	}
	pages, err := s.Store.ListPage(t.Context(), "ORDER BY cid LIMIT 1")
	if err != nil || len(pages) == 0 {
		t.Fatal(pages, err)
	}
	p := pages[0]
	ref := fmt.Sprintf("embyds:%d:filter_page:src:direct", ds)
	liveExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=?", ref, p.CID)
	for _, mode := range []string{"direct", "image"} {
		check(t, "filter_page", mode, true)
		check(t, "filterXpage", mode, false)
		check(t, "Filter_page", mode, false)
	}
	for _, tc := range []struct {
		aid, cid int64
		want     bool
	}{
		{p.AID, p.CID, true}, {p.AID + 999999, p.CID, false}, {p.AID, p.CID + 999999, false},
	} {
		got, err := s.sourceOriginalMediaAllowed(t.Context(), ds, "filter_page", "src", "direct", tc.aid, tc.cid)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	liveExec(t, s, "UPDATE archives SET state=-100 WHERE aid=?", p.AID)
	if _, closeInput, err := s.liveCacheInput(t.Context(), ref, p.AID, p.CID); err == nil {
		closeInput()
		t.Fatal("deleted page accepted as transcode input")
	}
}
