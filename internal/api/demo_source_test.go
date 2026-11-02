package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func t30TestSource(t *testing.T, s *Server, address, username, password string) *store.DataSource {
	t.Helper()
	if err := s.InitDataSources(t.Context()); err != nil {
		t.Fatal(err)
	}
	encrypted, err := s.encryptPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	var ds store.DataSource
	err = s.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error {
		id, err := store.NextID(t.Context(), tx)
		if err != nil {
			return err
		}
		var mid int64
		if err := tx.QueryRowContext(t.Context(), "SELECT MIN(mid) FROM users").Scan(&mid); err != nil {
			return err
		}
		ds = store.DataSource{ID: id, MID: mid, System: true, Type: "emby", Name: "fixture", URL: address, Username: username, PasswordEnc: encrypted, Status: "idle", Libraries: []store.DataSourceLibrary{}}
		return store.SaveDataSource(t.Context(), tx, &ds)
	})
	if err != nil {
		t.Fatal(err)
	}
	return &ds
}
func t30Package(t *testing.T) seed.DemoPackage {
	t.Helper()
	raw, err := os.ReadFile("../seed/testdata/demo.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := seed.ParsePackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDemoDataSourceImportImagesAndAccess(t *testing.T) {

	s := initialServer(t)
	p := t30Package(t)
	if err := s.ImportDemo(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	byOwner := t30AssertOwnerSources(t, s)
	pages, err := s.Store.ListPage(t.Context(), "ORDER BY cid LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.Store.ArchiveByID(t.Context(), pages[0].AID)
	if err != nil {
		t.Fatal(err)
	}
	ds := byOwner[owner.MID]
	if n := adminTestCount(t, s, "SELECT COUNT(*) FROM archives WHERE origin_ds<>0 OR origin_item<>''"); n != 0 {
		t.Fatal("demo assigned origin", n)
	}
	for _, query := range []string{"SELECT pic FROM archives", "SELECT cover FROM collections", "SELECT cover FROM seasons", "SELECT horizontal FROM seasons", "SELECT cover FROM episodes", "SELECT cover FROM rooms", "SELECT img FROM banners", "SELECT top_photo FROM users"} {
		rows, err := s.Store.DB.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var image string
			if err = rows.Scan(&image); err != nil {
				t.Fatal(err)
			}
			if image != "" && !strings.HasPrefix(image, "/mock/") && !strings.HasPrefix(image, "/media/emby/") {
				t.Fatal("image host exposed", image)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	pages, err = s.Store.ListPage(t.Context(), "ORDER BY cid LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	page := pages[0]
	id, item, source, mode, ok := emby.ParseDSRef(page.Media)
	if !ok || id != ds.ID || mode != "direct" {
		t.Fatal(page)
	}
	path := fmt.Sprintf("/media/emby/%s.mp4?source=%s&ds=%d", item, source, ds.ID)
	for _, bad := range []string{fmt.Sprintf("/media/emby/unreferenced.mp4?source=src&ds=%d", ds.ID), fmt.Sprintf("/media/emby/unreferenced/image/Primary?ds=%d", ds.ID)} {
		if w := sourceHTTP(s, bad); w.Code != 404 {
			t.Fatal("open proxy", w.Code)
		}
	}
	t.Run("Proxy", func(t *testing.T) {
		serverRequireListener(t)
		f := newSourceFixture(t)
		if err := s.Store.SetSetting(t.Context(), "dataSourcePrivateNetwork", true); err != nil {
			t.Fatal(err)
		}
		localDS := ds
		localDS.URL = f.server.URL
		if err := store.SaveDataSource(t.Context(), s.Store.DB, &localDS); err != nil {
			t.Fatal(err)
		}
		if w := sourceHTTP(s, path); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		// Series parent images are allowed solely by persisted image references.
		var picture string
		if err = s.Store.DB.QueryRowContext(t.Context(), "SELECT cover FROM seasons ORDER BY season_id LIMIT 1").Scan(&picture); err != nil {
			t.Fatal(err)
		}
		pgcID := int64(NumberGo(urlQuery(t, picture, "ds"), 0))
		pgc, err := store.DataSourceByID(t.Context(), s.Store.DB, pgcID)
		if err != nil {
			t.Fatal(err)
		}
		pgc.URL = f.server.URL
		if err = store.SaveDataSource(t.Context(), s.Store.DB, pgc); err != nil {
			t.Fatal(err)
		}
		if w := sourceHTTP(s, picture); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
			t.Fatal(w.Code, w.Body.String())
		}
	})
	// Exercise the real T17 disk-cache handler without a listening upstream.
	var picture string
	if err = s.Store.DB.QueryRowContext(t.Context(), "SELECT cover FROM seasons ORDER BY season_id LIMIT 1").Scan(&picture); err != nil {
		t.Fatal(err)
	}
	t30PrimeImage(t, s, int64(NumberGo(urlQuery(t, picture, "ds"), 0)), picture)
	if w := sourceHTTP(s, picture); w.Code != 200 || w.Header().Get("X-Cache") != "HIT" || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("demo image cache", w.Code, w.Header())
	}
	unreferenced := fmt.Sprintf("/media/emby/unreferenced/image/Primary?ds=%d", ds.ID)
	t30PrimeImage(t, s, ds.ID, unreferenced)
	if w := sourceHTTP(s, unreferenced); w.Code != 404 {
		t.Fatal("cached image bypassed authorization", w.Code)
	}
	if err = seed.SeedDanmaku(t.Context(), s.Store, page.CID, 120); err != nil {
		t.Fatal(err)
	}
	if n := adminTestCount(t, s, "SELECT COUNT(*) FROM danmaku WHERE cid=?", page.CID); n != 46 {
		t.Fatal(n)
	}
	// Public payloads contain proxy images, never the imported host.
	a, err := s.Store.ArchiveByID(t.Context(), page.AID)
	if err != nil {
		t.Fatal(err)
	}
	payload := get(t, s, "/x/web-interface/view?aid="+fmt.Sprint(a.AID))
	assertCode(t, payload, 0)
	raw, _ := json.Marshal(payload)
	if strings.Contains(string(raw), "images.example") || strings.Contains(string(raw), p.Emby.URL) || !strings.Contains(string(raw), "/media/emby/") {
		t.Fatal(string(raw))
	}
	// Delete the source using existing rules: assembled demo content survives.
	admin := login(t, s, "owner", "secret123")
	assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/delete", url.Values{"id": {fmt.Sprint(ds.ID)}}), 0)
	if _, err = s.Store.ArchiveByID(t.Context(), page.AID); err != nil {
		t.Fatal("demo archive deleted", err)
	}
	if w := sourceHTTP(s, path); w.Code != 404 {
		t.Fatal("deleted source playable", w.Code)
	}
	if err = seed.Seed(t.Context(), s.Store); err != nil {
		t.Fatal(err)
	}
	if _, err = store.DataSourceByID(t.Context(), s.Store.DB, ds.ID); err == nil {
		t.Fatal("deleted source recreated")
	}
}

func TestDemoDataSourceStartupMigrationReadyCache(t *testing.T) {
	s := initialServer(t)
	ctx := t.Context()
	p := t30Package(t)
	if err := s.InitDataSources(ctx); err != nil {
		t.Fatal(err)
	}
	encrypted, err := s.encryptPassword("fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	// Construct a genuine legacy database with no datasource/marker.
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if err := seed.GenerateDemoTx(ctx, tx, 1700000000, p.Catalog, 2000); err != nil {
			return err
		}
		for k, v := range map[string]any{"_demo_emby_url": p.Emby.URL, "_demo_emby_username": p.Emby.Username, "_demo_emby_password": encrypted, "_demo_catalog": p.Catalog} {
			if err := store.SetSetting(ctx, tx, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := s.Store.ListLiveRoom(ctx, "ORDER BY room_id")
	if err != nil {
		t.Fatal(err)
	}
	var room store.LiveRoom
	for _, r := range rooms {
		if r.Source != nil && r.Source.Type == "video" {
			room = r
			break
		}
	}
	if room.Source == nil {
		t.Fatal("no video room")
	}
	a, err := s.Store.ArchiveByID(ctx, room.Source.AID)
	if err != nil {
		t.Fatal(err)
	}
	page := a.Pages[0]
	var cache *liveCache
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error { var e error; cache, e = s.liveCacheEnsure(ctx, tx, page, false); return e })
	if err != nil {
		t.Fatal(err)
	}
	cacheReady(t, s, cache)
	room.Source.Cache = cache.Key
	room.Source.URL = liveCachePath(cache.Key)
	raw, _ := json.Marshal(room.Source)
	liveExec(t, s, "UPDATE rooms SET source=?,live_status=1,pending_start=0 WHERE room_id=?", string(raw), room.RoomID)
	// The old global reference may already share one ready cache across UPs.
	var sharedRoom store.LiveRoom
	var sharedArchive *store.Archive
	for _, candidate := range rooms {
		if candidate.RoomID == room.RoomID || candidate.RoomID == rooms[0].RoomID || candidate.UID == a.MID {
			continue
		}
		archives, e := s.Store.ListArchive(ctx, "WHERE mid=? ORDER BY aid", candidate.UID)
		if e != nil {
			t.Fatal(e)
		}
		if len(archives) == 0 {
			continue
		}
		sharedArchive, e = s.Store.ArchiveByID(ctx, archives[0].AID)
		if e != nil {
			t.Fatal(e)
		}
		sharedRoom = candidate
		sharedRoom.Source = &store.StreamSource{Type: "video", AID: sharedArchive.AID, CID: sharedArchive.Pages[0].CID, Loop: true, Format: "ts", Kind: "vod"}
		break
	}
	if sharedArchive == nil {
		t.Fatal("another UP's room missing")
	}
	sharedPage := sharedArchive.Pages[0]
	sharedPage.Media = page.Media
	liveExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=?", page.Media, sharedPage.CID)
	sharedRoom.Source.Cache = cache.Key
	sharedRoom.Source.URL = liveCachePath(cache.Key)
	sharedRaw, err := json.Marshal(sharedRoom.Source)
	if err != nil {
		t.Fatal(err)
	}
	liveExec(t, s, "UPDATE rooms SET source=?,live_status=1,pending_start=0 WHERE room_id=?", string(sharedRaw), sharedRoom.RoomID)
	// Include URL-form references in pages, episodes, room media and source URL.
	legacyURL := "/media/emby/urlItem/hls/index.m3u8?source=urlSource"
	liveExec(t, s, "UPDATE episodes SET media=? WHERE ep_id=(SELECT MIN(ep_id) FROM episodes)", legacyURL)
	liveExec(t, s, "UPDATE rooms SET media=? WHERE room_id=?", "emby:roomItem:roomSource", rooms[0].RoomID)
	liveExec(t, s, "UPDATE rooms SET source=? WHERE room_id=?", `{"type":"m3u8","url":"/media/emby/liveItem.mp4?source=liveSource","loop":true}`, rooms[0].RoomID)
	liveExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=(SELECT MAX(cid) FROM archive_pages)", "/media/emby/pageItem.mp4?source=pageSource")
	// Only the ready room participates in startup cache recovery in this fixture.
	liveExec(t, s, "UPDATE rooms SET source=? WHERE room_id<>? AND room_id<>? AND room_id<>?", `{"type":"push","url":"","loop":true}`, room.RoomID, rooms[0].RoomID, sharedRoom.RoomID)
	// Covers may contain URL forms outside the saved catalog, with auth query
	// parameters. Only item/kind/tag survive in the public proxy address.
	liveExec(t, s, "UPDATE archives SET pic=? WHERE aid=?", p.Emby.URL+"/emby/Items/parentImage/Images/primary?tag=v2&api_key=private", page.AID)
	before, err := liveCacheByKey(ctx, s.Store.DB, cache.Key)
	if err != nil {
		t.Fatal(err)
	}
	// Migration uses ciphertext only; no runtime datasource key or client needed.
	if err = seed.Seed(ctx, s.Store); err != nil {
		t.Fatal(err)
	}
	byOwner := t30AssertOwnerSources(t, s)
	ds := byOwner[a.MID]
	for _, source := range byOwner {
		if source.PasswordEnc != encrypted {
			t.Fatal("ciphertext changed", source.ID)
		}
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"_demo_emby_url", "_demo_emby_username", "_demo_emby_password", "_demo_datasource_id"} {
		if _, ok := settings[k]; ok {
			t.Fatal("legacy setting", k)
		}
	}
	if settings["_demo_catalog"] == nil {
		t.Fatal("catalog lost")
	}
	var migratedPic string
	if err = s.Store.DB.QueryRowContext(ctx, "SELECT pic FROM archives WHERE aid=?", page.AID).Scan(&migratedPic); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("/media/emby/parentImage/image/Primary?ds=%d&tag=v2", ds.ID); migratedPic != want {
		t.Fatal("image URL migration", migratedPic, want)
	}
	allowed, err := s.sourceMediaAllowed(ctx, ds.ID, "parentImage", "", "image")
	if err != nil || !allowed {
		t.Fatal("parent image authorization", allowed, err)
	}
	after, err := liveCacheByKey(ctx, s.Store.DB, cache.Key)
	if err != nil {
		t.Fatal(err)
	}
	want := *before
	want.Media = fmt.Sprintf("embyds:%d:%s:direct", ds.ID, strings.TrimPrefix(before.Media, "emby:"))
	if !reflect.DeepEqual(&want, after) {
		t.Fatalf("cache changed: before=%+v after=%+v", want, after)
	}
	migrated := liveRoom(t, s, room.RoomID)
	if migrated.Source.Cache != cache.Key || migrated.Source.URL != liveCachePath(cache.Key) || migrated.LiveStatus != 1 {
		t.Fatal(migrated)
	}
	if w := sourceHTTP(s, liveCachePath(cache.Key)); w.Code != 200 {
		t.Fatal("ready cache unavailable", w.Code)
	}
	// Ensuring the new media finds the same ready cache without another job.
	a, err = s.Store.ArchiveByID(ctx, page.AID)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		got, e := s.liveCacheEnsure(ctx, tx, a.Pages[0], false)
		if e == nil && got.Key != cache.Key {
			t.Error("new cache after migration")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedArchive, err = s.Store.ArchiveByID(ctx, sharedPage.AID)
	if err != nil {
		t.Fatal(err)
	}
	sharedPage = sharedArchive.Pages[0]
	if sharedPage.Media == a.Pages[0].Media {
		t.Fatal("different owners retained one source")
	}
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		got, e := s.liveCacheEnsure(ctx, tx, sharedPage, false)
		if e == nil && got.Key != cache.Key {
			t.Error("shared ready cache was retranscoded")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedAfter := liveRoom(t, s, sharedRoom.RoomID)
	if sharedAfter.Source.Cache != cache.Key || sharedAfter.Source.URL != liveCachePath(cache.Key) || sharedAfter.LiveStatus != 1 {
		t.Fatal("shared cache association lost", sharedAfter)
	}
	beforeDump := t30Snapshot(t, s)
	if err = seed.Seed(ctx, s.Store); err != nil {
		t.Fatal(err)
	}
	if got := t30Snapshot(t, s); got != beforeDump {
		t.Fatal("migration is not idempotent")
	}
	if err = s.InitLiveCaches(ctx); err != nil {
		t.Fatal(err)
	}
	s.CloseLiveCaches()
	if n := adminTestCount(t, s, "SELECT COUNT(*) FROM live_caches"); n != 1 {
		t.Fatal("startup requeued cache", n)
	}
	// Editing one user's connection must stop cache sharing with the others.
	edited := byOwner[sharedArchive.MID]
	edited.URL = "https://different.test"
	if err = store.SaveDataSource(ctx, s.Store.DB, &edited); err != nil {
		t.Fatal(err)
	}
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		got, e := s.liveCacheEnsure(ctx, tx, sharedPage, false)
		if e == nil && (got.Key == cache.Key || got.Status != "pending") {
			t.Error("edited connection reused another user's file")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ResetApplication(ctx); err != nil {
		t.Fatal(err)
	}
	if n := adminTestCount(t, s, "SELECT COUNT(*) FROM data_sources"); n != 0 {
		t.Fatal("reset retained source", n)
	}
}
func t30Snapshot(t *testing.T, s *Server) string {
	t.Helper()
	snapshot := []any{}
	for _, query := range []string{"SELECT * FROM ids ORDER BY name", "SELECT * FROM settings ORDER BY key", "SELECT * FROM data_sources ORDER BY id", "SELECT * FROM archive_pages ORDER BY cid", "SELECT * FROM episodes ORDER BY ep_id", "SELECT * FROM rooms ORDER BY room_id", "SELECT * FROM live_caches ORDER BY key", "SELECT * FROM archives ORDER BY aid", "SELECT * FROM seasons ORDER BY season_id", "SELECT * FROM collections ORDER BY id", "SELECT * FROM banners ORDER BY id", "SELECT * FROM users ORDER BY mid"} {
		rows, err := s.Store.DB.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range dest {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			snapshot = append(snapshot, values)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func t30PrimeImage(t *testing.T, s *Server, ds int64, ref string) {
	t.Helper()
	u, err := url.Parse(ref)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/media/emby/"), "/")
	cfg, err := s.imageCacheSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	key := imageCacheKey(ds, parts[0], parts[2], u.Query().Get("tag"))
	if _, err = imageCacheWrite(cfg.dir, key, imageCacheMetadata{Type: "image/png", Created: now, Modified: now}, []byte("png"), now); err != nil {
		t.Fatal(err)
	}
}
func TestDemoSourceReferenceAuthorization(t *testing.T) {
	s := initialServer(t)
	if err := s.ImportDemo(t.Context(), t30Package(t)); err != nil {
		t.Fatal(err)
	}
	sources, err := s.Store.ListDataSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ds := sources[0]
	for _, tc := range []struct {
		name, query, item, mode string
		sourceJSON              bool
	}{
		{"page", "UPDATE archive_pages SET media=? WHERE cid=(SELECT MIN(cid) FROM archive_pages)", "pageOnly", "direct", false},
		{"episode", "UPDATE episodes SET media=? WHERE ep_id=(SELECT MIN(ep_id) FROM episodes)", "episodeOnly", "direct", false},
		{"room", "UPDATE rooms SET media=? WHERE room_id=(SELECT MIN(room_id) FROM rooms)", "roomOnly", "direct", false},
		{"room source", "UPDATE rooms SET source=? WHERE room_id=(SELECT MIN(room_id) FROM rooms)", "sourceOnly", "direct", true},
		{"parent image", "UPDATE seasons SET cover=? WHERE season_id=(SELECT MIN(season_id) FROM seasons)", "parentOnly", "image", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := fmt.Sprintf("embyds:%d:%s:src:direct", ds.ID, tc.item)
			if tc.sourceJSON {
				raw, _ := json.Marshal(store.StreamSource{Type: "m3u8", URL: ref})
				ref = string(raw)
			}
			if tc.mode == "image" {
				ref = fmt.Sprintf("/media/emby/%s/image/Primary?ds=%d", tc.item, ds.ID)
			}
			liveExec(t, s, tc.query, ref)
			allowed, err := s.sourceMediaAllowed(t.Context(), ds.ID, tc.item, "src", tc.mode)
			if err != nil || !allowed {
				t.Fatal(allowed, err)
			}
			allowed, err = s.sourceMediaAllowed(t.Context(), ds.ID, tc.item+"unknown", "src", tc.mode)
			if err != nil || allowed {
				t.Fatal("unreferenced", allowed, err)
			}
			if tc.mode != "image" {
				allowed, err = s.sourceMediaAllowed(t.Context(), ds.ID, tc.item, "wrong", tc.mode)
				if err != nil || allowed {
					t.Fatal("wrong source", allowed, err)
				}
			}
		})
	}
	t.Run("cache", func(t *testing.T) {
		ref := fmt.Sprintf("embyds:%d:cacheOnly:src:direct", ds.ID)
		liveExec(t, s, "INSERT INTO live_caches (key,media,height,aid,cid,dir,ctime,mtime) VALUES ('01234567890123456789012345678901',?,1080,1,1,'/tmp/unused',1,1)", ref)
		for _, status := range []string{"pending", "running", "ready", "failed"} {
			liveExec(t, s, "UPDATE live_caches SET status=? WHERE media=?", status, ref)
			allowed, err := s.sourceMediaAllowed(t.Context(), ds.ID, "cacheOnly", "src", "direct")
			if err != nil || allowed {
				t.Fatal("orphan cache authorized original", status, allowed, err)
			}
			w := sourceHTTP(s, fmt.Sprintf("/media/emby/cacheOnly.mp4?source=src&ds=%d", ds.ID))
			if w.Code != http.StatusNotFound {
				t.Fatal(w.Code)
			}
		}
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/media/emby/cacheOnly.mp4?source=src", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestDemoSourceMigrationRollback(t *testing.T) {
	s := newTestServer(t)
	for k, v := range map[string]any{"_demo_emby_url": "https://fixture.test", "_demo_emby_username": "fixture", "_demo_emby_password": "opaque-ciphertext", "_demo_catalog": testdata.Catalog()} {
		if err := s.Store.SetSetting(t.Context(), k, v); err != nil {
			t.Fatal(err)
		}
	}
	liveExec(t, s, "CREATE TRIGGER t30_fail_update BEFORE UPDATE ON episodes BEGIN SELECT RAISE(ABORT, 'rollback migration'); END")
	before := t30Snapshot(t, s)
	if err := seed.Seed(t.Context(), s.Store); err == nil {
		t.Fatal("expected failure")
	}
	if after := t30Snapshot(t, s); after != before {
		t.Fatal("partial migration committed")
	}
	liveExec(t, s, "DROP TRIGGER t30_fail_update")
	if err := seed.Seed(t.Context(), s.Store); err != nil {
		t.Fatal(err)
	}
	ds, err := s.Store.ListDataSource(t.Context())
	if err != nil || len(ds) == 0 {
		t.Fatal("ciphertext decrypted/replaced", ds, err)
	}
	for _, source := range ds {
		if source.PasswordEnc != "opaque-ciphertext" || source.System {
			t.Fatal(source.ID)
		}
	}
}

func t30MigrateLiveFixture(t *testing.T, s *Server) {
	t.Helper()
	if id, err := s.Store.Setting(t.Context(), "_demo_datasource_ids"); err == nil && id != nil {
		return
	}
	for k, v := range map[string]any{"_demo_emby_url": "https://fixture.test", "_demo_emby_username": "fixture", "_demo_catalog": testdata.Catalog()} {
		if err := s.Store.SetSetting(t.Context(), k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Seed(t.Context(), s.Store); err != nil {
		t.Fatal(err)
	}
}

func TestDemoDanmakuSourceBoundary(t *testing.T) {
	s := initialServer(t)
	if err := s.ImportDemo(t.Context(), t30Package(t)); err != nil {
		t.Fatal(err)
	}
	sources, err := s.Store.ListDataSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	demo := sources[0]
	other := t30TestSource(t, s, "https://other.test", "ordinary", "")
	pages, err := s.Store.ListPage(t.Context(), "ORDER BY cid LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	p := pages[0]
	for _, tc := range []struct {
		media string
		want  int
	}{
		{"emby:legacy:src", 0},
		{fmt.Sprintf("embyds:%d:ordinary:src:direct", other.ID), 0},
		{fmt.Sprintf("embyds:%d:demo:src:direct", demo.ID), 46},
	} {
		liveExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=?", tc.media, p.CID)
		if err := seed.SeedDanmaku(t.Context(), s.Store, p.CID, 120); err != nil {
			t.Fatal(err)
		}
		if got := adminTestCount(t, s, "SELECT COUNT(*) FROM danmaku WHERE cid=?", p.CID); got != tc.want {
			t.Fatal(tc.media, got, tc.want)
		}
	}
}

func urlQuery(t *testing.T, ref, key string) string {
	t.Helper()
	u, err := url.Parse(ref)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}

func t30AssertOwnerSources(t *testing.T, s *Server) map[int64]store.DataSource {
	t.Helper()
	ctx := t.Context()
	sources, err := s.Store.ListDataSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byOwner := map[int64]store.DataSource{}
	ids := map[int64]bool{}
	for _, ds := range sources {
		if ds.System || ds.AutoSync || ds.Type != "emby" || ds.Name != "演示 Emby" || ds.Status != "idle" {
			t.Fatalf("not an idle ordinary demo source: %+v", ds)
		}
		if _, ok := byOwner[ds.MID]; ok {
			t.Fatal("duplicate owner", ds.MID)
		}
		byOwner[ds.MID] = ds
		ids[ds.ID] = true
	}
	official, err := s.Store.Setting(ctx, "officialMid")
	if err != nil {
		t.Fatal(err)
	}
	mid := int64(official.(float64))
	users, err := s.Store.ListUser(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[int64]bool{mid: true}
	for _, u := range users {
		if u.Role == "user" {
			expected[u.MID] = true
		}
		if u.Role == "user" && byOwner[u.MID].ID == 0 {
			t.Fatal("demo user has no source", u.MID)
		}
	}
	if len(expected) != len(sources) {
		t.Fatal("wrong number of demo owners", len(expected), len(sources))
	}
	stored, err := s.Store.Setting(ctx, "_demo_datasource_ids")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var marker []int64
	if err = json.Unmarshal(raw, &marker); err != nil {
		t.Fatal(err)
	}
	if len(marker) != len(ids) {
		t.Fatal("marker length", marker)
	}
	for _, id := range marker {
		if !ids[id] {
			t.Fatal("marker source", id)
		}
	}
	if _, err = s.Store.Setting(ctx, "_demo_datasource_id"); err == nil {
		t.Fatal("scalar marker survived")
	}
	catalog, err := seed.StoredCatalog(ctx, s.Store)
	if err != nil {
		t.Fatal(err)
	}
	libraries := map[string]string{}
	for _, x := range catalog.Series {
		libraries[x.ID] = x.Library
		for _, e := range x.Episodes {
			libraries[e.ID] = x.Library
		}
	}
	for _, x := range catalog.Movies {
		libraries[x.ID] = x.Library
	}
	seenLibraries := map[int64]map[string]bool{}
	for _, check := range []struct {
		query         string
		image, source bool
		official      bool
	}{
		{"SELECT p.media,a.mid FROM archive_pages p JOIN archives a ON a.aid=p.aid", false, false, false},
		{"SELECT media,? FROM episodes", false, false, true},
		{"SELECT media,uid FROM rooms", false, false, false},
		{"SELECT source,uid FROM rooms", false, true, false},
		{"SELECT c.media,COALESCE(a.mid,?) FROM live_caches c LEFT JOIN archives a ON a.aid=c.aid", false, false, true},
		{"SELECT pic,mid FROM archives", true, false, false},
		{"SELECT cover,? FROM episodes", true, false, true},
		{"SELECT cover,? FROM seasons", true, false, true},
		{"SELECT horizontal,? FROM seasons", true, false, true},
		{"SELECT cover,mid FROM collections", true, false, false},
		{"SELECT cover,uid FROM rooms", true, false, false},
		{"SELECT top_photo,mid FROM users", true, false, false},
		{"SELECT b.img,COALESCE((SELECT a.mid FROM archives a WHERE b.url='/video/' || a.bvid),?) FROM banners b", true, false, true},
	} {
		var args []any
		if check.official {
			args = []any{mid}
		}
		rows, err := s.Store.DB.QueryContext(ctx, check.query, args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var ref string
			var owner int64
			if err = rows.Scan(&ref, &owner); err != nil {
				t.Fatal(err)
			}
			if check.source {
				var src *store.StreamSource
				if err = json.Unmarshal([]byte(ref), &src); err != nil {
					t.Fatal(err)
				}
				if src == nil {
					continue
				}
				ref = src.URL
			}
			if ref == "" || strings.HasPrefix(ref, "/mock/") || strings.HasPrefix(ref, "/live-cache/") {
				continue
			}
			var ds int64
			var item string
			if check.image {
				u, err := url.Parse(ref)
				if err != nil {
					t.Fatal(err)
				}
				ds = int64(NumberGo(u.Query().Get("ds"), 0))
				parts := strings.Split(strings.TrimPrefix(u.Path, "/media/emby/"), "/")
				if len(parts) != 3 {
					t.Fatal("image host exposed", ref)
				}
				item = parts[0]
			} else {
				var mode string
				var ok bool
				ds, item, _, mode, ok = emby.ParseDSRef(ref)
				if !ok || mode != "direct" {
					t.Fatal("legacy media", ref)
				}
			}
			if ds != byOwner[owner].ID || ds == 0 {
				t.Fatalf("wrong source for owner %d: %s (want %d)", owner, ref, byOwner[owner].ID)
			}
			if library := libraries[item]; library != "" {
				if seenLibraries[owner] == nil {
					seenLibraries[owner] = map[string]bool{}
				}
				seenLibraries[owner][library] = true
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	for owner, wanted := range seenLibraries {
		actual := map[string]bool{}
		for _, lib := range byOwner[owner].Libraries {
			actual[lib.Name] = true
		}
		if !reflect.DeepEqual(actual, wanted) {
			t.Fatal("libraries not scoped to owner", owner, actual, wanted)
		}
	}
	return byOwner
}

func TestDemoUserDataSourceManagement(t *testing.T) {
	s := initialServer(t)
	if err := s.ImportDemo(t.Context(), t30Package(t)); err != nil {
		t.Fatal(err)
	}
	byOwner := t30AssertOwnerSources(t, s)
	admin := login(t, s, "owner", "secret123")
	all := sourceData(t, get(t, s, "/x/admin/datasources?scope=user&ps=100", admin))
	if all["page"].(map[string]any)["total"] != float64(len(byOwner)) {
		t.Fatal(all)
	}
	systems := sourceData(t, get(t, s, "/x/admin/datasources?scope=system", admin))
	if systems["page"].(map[string]any)["total"] != float64(0) {
		t.Fatal(systems)
	}
	users, err := s.Store.ListUser(t.Context(), "WHERE role=? ORDER BY mid", "user")
	if err != nil || len(users) < 2 {
		t.Fatal(err)
	}
	for _, user := range users {
		session := login(t, s, user.Username, "demo123")
		list := sourceData(t, get(t, s, "/x/web/datasources", session))["list"].([]any)
		if len(list) != 1 || list[0].(map[string]any)["id"] != float64(byOwner[user.MID].ID) || list[0].(map[string]any)["system"] != false {
			t.Fatal("creator source isolation", user.MID, list)
		}
	}
	session := login(t, s, users[0].Username, "demo123")
	mine := byOwner[users[0].MID]
	other := byOwner[users[1].MID]
	for _, action := range []string{"test", "save", "delete"} {
		assertCode(t, sourcePost(t, s, session, "/x/web/datasource/"+action, url.Values{"id": {fmt.Sprint(other.ID)}}), -404)
	}
	// Own edit/test reaches the ordinary-source input validation.
	for _, action := range []string{"test", "save"} {
		result := sourcePost(t, s, session, "/x/web/datasource/"+action, url.Values{"id": {fmt.Sprint(mine.ID)}})
		assertCode(t, result, -400)
		if result["message"] != "请填写 Emby 服务器地址" {
			t.Fatal(result)
		}
	}
	t.Run("EditAndTestConnection", func(t *testing.T) {
		serverRequireListener(t)
		fixture := newSourceFixture(t)
		if err := s.Store.SetSetting(t.Context(), "dataSourcePrivateNetwork", true); err != nil {
			t.Fatal(err)
		}
		params := url.Values{"id": {fmt.Sprint(mine.ID)}, "name": {"演示 Emby"}, "url": {fixture.server.URL}, "username": {"fixture"}, "password": {"new-password"}, "auto_sync": {"false"}}
		assertCode(t, sourcePost(t, s, session, "/x/web/datasource/test", params), 0)
		assertCode(t, sourcePost(t, s, session, "/x/web/datasource/save", params), 0)
		saved, err := store.DataSourceByID(t.Context(), s.Store.DB, mine.ID)
		if err != nil || saved.System || saved.MID != mine.MID || saved.AutoSync || saved.URL != fixture.server.URL {
			t.Fatal(saved, err)
		}
	})
	before := adminTestCount(t, s, "SELECT COUNT(*) FROM archives WHERE mid=?", mine.MID)
	assertCode(t, sourcePost(t, s, session, "/x/web/datasource/delete", url.Values{"id": {fmt.Sprint(mine.ID)}}), 0)
	if after := adminTestCount(t, s, "SELECT COUNT(*) FROM archives WHERE mid=?", mine.MID); after != before {
		t.Fatal("deleted assembled demos", before, after)
	}
	if err := seed.Seed(t.Context(), s.Store); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.Store.ListDataSource(t.Context())
	if err != nil || len(remaining) != len(byOwner)-1 {
		t.Fatal("owner source resurrected", remaining, err)
	}
	if _, err := store.DataSourceByID(t.Context(), s.Store.DB, other.ID); err != nil {
		t.Fatal("other user's source deleted", err)
	}
}

func TestDemoSourceUnifiedDraftMigration(t *testing.T) {
	s := newTestServer(t)
	ctx := t.Context()
	catalog := testdata.Catalog()
	unified := t30TestSource(t, s, "https://fixture.test", "fixture", "")
	unified.PasswordEnc = "opaque-draft-ciphertext"
	unified.Name = "演示数据源"
	if err := store.SaveDataSource(ctx, s.Store.DB, unified); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{"_demo_datasource_id": unified.ID, "_demo_catalog": catalog} {
		if err := s.Store.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	// Emulate the already-written first T30 draft without rerunning its code.
	for _, field := range []struct{ table, key string }{{"archive_pages", "cid"}, {"episodes", "ep_id"}, {"rooms", "room_id"}} {
		rows, err := s.Store.DB.QueryContext(ctx, "SELECT "+field.key+",media FROM "+field.table)
		if err != nil {
			t.Fatal(err)
		}
		type change struct {
			id  int64
			ref string
		}
		changes := []change{}
		for rows.Next() {
			var id int64
			var media string
			if err = rows.Scan(&id, &media); err != nil {
				t.Fatal(err)
			}
			if item, source, ok := emby.ParseRef(media); ok {
				changes = append(changes, change{id, fmt.Sprintf("embyds:%d:%s:%s:direct", unified.ID, item, source)})
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, change := range changes {
			liveExec(t, s, "UPDATE "+field.table+" SET media=? WHERE "+field.key+"=?", change.ref, change.id)
		}
	}
	rooms, err := s.Store.ListLiveRoom(ctx, "ORDER BY room_id")
	if err != nil {
		t.Fatal(err)
	}
	var room store.LiveRoom
	for _, r := range rooms {
		if r.Source != nil && r.Source.Type == "video" {
			room = r
			break
		}
	}
	if room.Source == nil {
		t.Fatal("missing video room")
	}
	a, err := s.Store.ArchiveByID(ctx, room.Source.AID)
	if err != nil {
		t.Fatal(err)
	}
	page := a.Pages[0]
	var cache *liveCache
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error { var e error; cache, e = s.liveCacheEnsure(ctx, tx, page, false); return e })
	if err != nil {
		t.Fatal(err)
	}
	cacheReady(t, s, cache)
	room.Source.Cache = cache.Key
	room.Source.URL = liveCachePath(cache.Key)
	raw, err := json.Marshal(room.Source)
	if err != nil {
		t.Fatal(err)
	}
	liveExec(t, s, "UPDATE rooms SET source=?,live_status=1 WHERE room_id=?", string(raw), room.RoomID)
	liveExec(t, s, "UPDATE archives SET pic=? WHERE aid=?", fmt.Sprintf("/media/emby/parentImage/image/Primary?ds=%d&tag=v3", unified.ID), page.AID)
	if err = seed.Seed(ctx, s.Store); err != nil {
		t.Fatal(err)
	}
	byOwner := t30AssertOwnerSources(t, s)
	for _, ds := range byOwner {
		if ds.PasswordEnc != unified.PasswordEnc {
			t.Fatal("draft ciphertext replaced", ds.ID)
		}
	}
	if _, err = store.DataSourceByID(ctx, s.Store.DB, unified.ID); err == nil {
		t.Fatal("unified system source survived")
	}
	migrated, err := liveCacheByKey(ctx, s.Store.DB, cache.Key)
	if err != nil || migrated.Status != "ready" || migrated.Dir != cache.Dir || !strings.HasPrefix(migrated.Media, fmt.Sprintf("embyds:%d:", byOwner[a.MID].ID)) {
		t.Fatal(migrated, err)
	}
	if r := liveRoom(t, s, room.RoomID); r.Source.Cache != cache.Key || r.Source.URL != liveCachePath(cache.Key) || r.LiveStatus != 1 {
		t.Fatal(r)
	}
	before := t30Snapshot(t, s)
	if err = seed.Seed(ctx, s.Store); err != nil {
		t.Fatal(err)
	}
	if after := t30Snapshot(t, s); after != before {
		t.Fatal("draft upgrade repeated")
	}
}

func TestDemoSourceDeletedUnifiedDraftStaysDeleted(t *testing.T) {
	s := initialServer(t)
	if err := s.Store.SetSetting(t.Context(), "_demo_datasource_id", int64(99999)); err != nil {
		t.Fatal(err)
	}
	if err := seed.Seed(t.Context(), s.Store); err != nil {
		t.Fatal(err)
	}
	if n := adminTestCount(t, s, "SELECT COUNT(*) FROM data_sources"); n != 0 {
		t.Fatal("resurrected source", n)
	}
	marker, err := s.Store.Setting(t.Context(), "_demo_datasource_ids")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(marker)
	if err != nil || string(raw) != "[]" {
		t.Fatal(marker, err)
	}
	before := t30Snapshot(t, s)
	if err = seed.Seed(t.Context(), s.Store); err != nil {
		t.Fatal(err)
	}
	if after := t30Snapshot(t, s); after != before {
		t.Fatal("deleted source migration not idempotent")
	}
}
