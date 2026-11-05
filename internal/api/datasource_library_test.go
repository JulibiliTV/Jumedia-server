package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"

	"jumedia-server/internal/store"
)

func TestDataSourceImportNamedLibrary(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		ctx := context.Background()
		f := newSourceFixture(t)
		var invalidParents, itemRequests atomic.Int32
		upstream := f.server.Config.Handler
		f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/emby/Users/fixture/Items" {
				itemRequests.Add(1)
				if r.URL.Query().Get("ParentId") == "电影" {
					invalidParents.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
			upstream.ServeHTTP(w, r)
		})
		user := login(t, s, "lumen", "demo123")
		for key, value := range map[string]any{"dataSourcePrivateNetwork": true, "userDataSources": true, "dataSourceSyncRate": 50} {
			if err := s.Store.SetSetting(ctx, key, value); err != nil {
				t.Fatal(err)
			}
		}
		d := sourceData(t, sourcePost(t, s, user, "/x/web/datasource/save", url.Values{"url": {f.server.URL}, "username": {"fixture"}, "auto_sync": {"false"}, "sync_now": {"false"}}))
		id := int64(d["id"].(float64))
		ds, err := store.DataSourceByID(ctx, s.Store.DB, id)
		if err != nil {
			t.Fatal(err)
		}
		// Imported catalogs store the display name until the source is reconnected.
		ds.Libraries = []store.DataSourceLibrary{{ID: "电影", Name: "电影", Enabled: true}}
		if err := store.SaveDataSource(ctx, s.Store.DB, ds); err != nil {
			t.Fatal(err)
		}
		browse := fmt.Sprintf("/x/web/datasource/items?id=%d&library=%s&keyword=B&ps=1", id, url.QueryEscape("电影"))
		listed := sourceData(t, get(t, s, browse, user))
		if items := listed["list"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != "mkv" {
			t.Fatal(listed)
		}
		imported := sourceData(t, sourcePost(t, s, user, "/x/web/datasource/import", url.Values{"id": {fmt.Sprint(id)}, "library": {"电影"}, "item_ids": {`["movie"]`}}))
		if imported["archives"].([]any)[0].(map[string]any)["state"] != float64(-20) {
			t.Fatal(imported)
		}
		a := sourceArchives(t, s, id)["movie"]
		if a.Origin.Library != "电影" || sourceLibraryName(ds, a.Origin.Library) != "电影" {
			t.Fatal(a.Origin)
		}
		stored, err := store.DataSourceByID(ctx, s.Store.DB, id)
		if err != nil || !reflect.DeepEqual(stored.Libraries, ds.Libraries) {
			t.Fatal("read changed source configuration", stored, err)
		}
		if invalidParents.Load() != 0 {
			t.Fatal("sent library name as upstream ID")
		}
		// A missing/ambiguous name must not query an unrelated library.
		before := itemRequests.Load()
		f.mu.Lock()
		f.views = append(f.views, map[string]string{"Id": "other", "Name": "电影", "CollectionType": "movies"})
		f.mu.Unlock()
		liveFailure(t, get(t, s, browse, user), -400, "媒体库名称无法唯一匹配，请重新连接并保存数据源")
		f.mu.Lock()
		f.views = nil
		f.mu.Unlock()
		liveFailure(t, get(t, s, browse, user), -400, "媒体库名称无法唯一匹配，请重新连接并保存数据源")
		if itemRequests.Load() != before {
			t.Fatal("unresolved name queried upstream items")
		}
	})
}
