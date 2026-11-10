package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"jumedia-server/internal/emby"
)

func TestFnTVDataSource(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authx") == "" {
				t.Error("signature missing")
			}
			if strings.HasSuffix(r.URL.Path, "loginByPassword") {
				fmt.Fprint(w, `{"code":0,"data":{"token":"fixture-token"}}`)
				return
			}
			if r.Header.Get("Authorization") != "fixture-token" {
				http.Error(w, "unauthorized", 401)
				return
			}
			var data any
			switch r.URL.Path {
			case "/base/v/api/v1/sys/config":
				data = map[string]any{"server_name": "Fixture Library", "server_guid": "fixture-server"}
			case "/base/v/api/v1/mediadb/list":
				data = []any{map[string]any{"guid": "library", "title": "影视", "category": "Mixed"}}
			case "/base/v/api/v1/item/list":
				var p map[string]any
				_ = json.NewDecoder(r.Body).Decode(&p)
				if p["ancestor_guid"] != "library" {
					t.Error("library scope lost")
				}
				data = map[string]any{"total": 2, "list": []any{map[string]any{"guid": "movie", "title": "电影", "type": "Movie"}, map[string]any{"guid": "show", "title": "剧集", "type": "TV"}}}
			case "/base/v/api/v1/item/movie":
				data = map[string]any{"guid": "movie", "title": "电影", "type": "Movie", "duration": 120, "file_name": "movie.mp4", "posters": "/aa/poster.webp"}
			case "/base/v/api/v1/item/show":
				data = map[string]any{"guid": "show", "title": "剧集", "type": "TV"}
			case "/base/v/api/v1/item/episode":
				data = map[string]any{"guid": "episode", "title": "第2集", "type": "Episode", "duration": 240, "file_name": "episode.mkv", "episode_number": 2}
			case "/base/v/api/v1/season/list/show":
				data = []any{map[string]any{"guid": "season", "season_number": 2}}
			case "/base/v/api/v1/episode/list/season":
				data = []any{map[string]any{"guid": "episode", "title": "第2集", "episode_number": 2}}
			case "/base/v/api/v1/play/info":
				var p map[string]string
				_ = json.NewDecoder(r.Body).Decode(&p)
				data = map[string]any{"media_guid": p["item_guid"] + "-media"}
			case "/base/v/api/v1/media/range/movie-media":
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Content-Range", "bytes 0-4/10")
				w.WriteHeader(206)
				if r.Method != "HEAD" {
					fmt.Fprint(w, "video")
				}
				return
			case "/base/v/api/v1/sys/img/aa/poster.webp":
				w.Header().Set("Content-Type", "image/webp")
				fmt.Fprint(w, "image")
				return
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
		}))
		defer upstream.Close()
		admin := login(t, s, "jumedia", "admin123")
		for key, value := range map[string]any{"dataSourceSyncRate": float64(50), "userDataSources": true} {
			if err := s.Store.SetSetting(t.Context(), key, value); err != nil {
				t.Fatal(err)
			}
		}
		p := url.Values{"type": {"fntv"}, "url": {upstream.URL + "/base/"}, "username": {"viewer"}, "password": {"fixture-password"}}
		assertCode(t, sourcePost(t, s, admin, "/x/web/datasource/test", p), -400)
		p.Set("proxy_playback", "false")
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p), -400)
		p.Set("proxy_playback", "true")
		row := sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p))
		id := int64(row["id"].(float64))
		ds := sourceWait(t, s, id)
		archives := sourceArchives(t, s, id)
		movie, show := archives["movie"], archives["show"]
		if ds.Type != "fntv" || ds.Status != "ok" || !ds.ProxyPlayback || movie == nil || show == nil {
			t.Fatalf("source: %+v archives: %v", ds, archives)
		}
		if movie.Origin.Type != "fntv" || movie.Pages[0].Duration != 120 || !strings.HasSuffix(movie.Pages[0].Media, ":movie-media:raw-mp4") {
			t.Fatal("movie metadata not mapped")
		}
		if len(show.Pages) != 1 || show.Pages[0].Duration != 240 || !strings.HasSuffix(show.Pages[0].Media, ":episode-media:raw-mkv") {
			t.Fatal("episodes not mapped")
		}
		ref := movie.Pages[0].Media
		_, item, source, mode, ok := emby.ParseDSRef(ref)
		if !ok {
			t.Fatal(ref)
		}
		path := emby.DSPath(id, item, source, mode)
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, path, nil)
			r.Header.Set("Range", "bytes=0-4")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 206 || w.Header().Get("Content-Range") != "bytes 0-4/10" {
				t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
			}
		}
		if w := sourceHTTP(s, movie.Pic); w.Code != 200 {
			t.Fatalf("image: %d", w.Code)
		}
		if w := sourceHTTP(s, emby.DSPath(id, item, "other-media", mode)); w.Code != 404 {
			t.Fatal("unreferenced stream granted")
		}
		p.Set("id", fmt.Sprint(id))
		p.Set("sync_now", "false")
		p.Set("password", "")
		p.Del("type")
		sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p))
		p.Set("type", "emby")
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p), -400)
		if err := s.Store.SetSetting(t.Context(), "dataSourcePrivateNetwork", true); err != nil {
			t.Fatal(err)
		}
		p.Del("id")
		p.Set("type", "fntv")
		p.Set("password", "fixture-password")
		p.Set("sync_now", "false")
		personal := sourceData(t, sourcePost(t, s, admin, "/x/web/datasource/save", p))
		personalID := int64(personal["id"].(float64))
		q := url.Values{"id": {fmt.Sprint(personalID)}, "library": {"library"}, "item_ids": {`["foreign"]`}}
		assertCode(t, sourcePost(t, s, admin, "/x/web/datasource/import", q), -400)
		q.Set("item_ids", `["movie"]`)
		sourceData(t, sourcePost(t, s, admin, "/x/web/datasource/import", q))
		imported := sourceArchives(t, s, personalID)["movie"]
		if imported == nil || imported.State != -20 {
			t.Fatal("draft import failed")
		}
	})
}
