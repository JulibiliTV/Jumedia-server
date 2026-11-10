package api

import (
	"fmt"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/plex/testdata"
	"jumedia-server/internal/store"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPlexDataSource(t *testing.T) {
	testdata.Account(t)
	testServers(t, func(t *testing.T, s *Server) {
		up := testdata.Server(t)
		defer up.Close()
		admin := login(t, s, "jumedia", "admin123")
		s.Store.SetSetting(t.Context(), "dataSourceSyncRate", float64(50))
		p := url.Values{"type": {"plex"}, "url": {up.URL + "/base"}, "username": {"fixture-user"}, "password": {"fixture-password"}}
		row := sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p))
		id := int64(row["id"].(float64))
		ds := sourceWait(t, s, id)
		a := sourceArchives(t, s, id)
		if ds.Status != "ok" || ds.Type != "plex" || len(a) != 2 || a["10"].Origin.Type != "plex" || len(a["20"].Pages) != 1 {
			t.Fatal(ds, a)
		}
		if !strings.HasSuffix(a["10"].Pages[0].Media, ":100:direct") || !strings.HasSuffix(a["20"].Pages[0].Media, ":210:raw-mkv") {
			t.Fatal("media mapping")
		}
		if w := sourceHTTP(s, a["10"].Pic); w.Code != 200 {
			t.Fatal(w.Code)
		}
		collection, err := store.CollectionFrom(t.Context(), s.Store.DB, ds.Libraries[0].CollectionID)
		if err != nil || collection.Cover != a["10"].Pic {
			t.Fatal("Plex collection must use a media artwork", err)
		}
		_, item, source, mode, _ := emby.ParseDSRef(a["10"].Pages[0].Media)
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, emby.DSPath(id, item, source, mode), nil)
			r.Header.Set("Range", "bytes=0-4")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 206 || w.Header().Get("Content-Range") != "bytes 0-4/10" {
				t.Fatal("proxy range failed", w.Code)
			}
		}
		if item != "10" || source != "100" || mode != "direct" {
			t.Fatal("reference")
		}
		p.Set("id", fmt.Sprint(id))
		p.Set("password", "")
		p.Set("sync_now", "false")
		p.Set("proxy_playback", "false")
		row = sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p))
		if row["proxy_playback"] != false || row["has_password"] != true {
			t.Fatal("credential preservation")
		}
		p.Set("type", "emby")
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p), -400)
		s.Store.SetSetting(t.Context(), "userDataSources", true)
		s.Store.SetSetting(t.Context(), "dataSourcePrivateNetwork", true)
		p.Del("id")
		p.Set("type", "plex")
		p.Set("password", "fixture-password")
		row = sourceData(t, sourcePost(t, s, admin, "/x/web/datasource/save", p))
		personal := int64(row["id"].(float64))
		q := url.Values{"id": {fmt.Sprint(personal)}, "library": {"1"}, "item_ids": {`["20"]`}}
		assertCode(t, sourcePost(t, s, admin, "/x/web/datasource/import", q), -400)
		q.Set("item_ids", `["10"]`)
		sourceData(t, sourcePost(t, s, admin, "/x/web/datasource/import", q))
		if sourceArchives(t, s, personal)["10"].State != -20 {
			t.Fatal("draft import")
		}
	})
}
