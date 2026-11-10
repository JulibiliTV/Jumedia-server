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

func TestJellyfinDataSource(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "MediaBrowser ") {
				http.Error(w, "missing client identity", 401)
				return
			}
			if r.URL.Path == "/base/Users/AuthenticateByName" && r.Method == http.MethodPost {
				fmt.Fprint(w, `{"User":{"Id":"viewer"},"AccessToken":"fixture-token"}`)
				return
			}
			if !strings.Contains(auth, `Token="fixture-token"`) {
				http.Error(w, "missing session", 401)
				return
			}
			q := r.URL.Query()
			switch r.URL.Path {
			case "/base/System/Info/Public":
				fmt.Fprint(w, `{"Id":"instance","ServerName":"Test Library"}`)
			case "/base/UserViews":
				fmt.Fprint(w, `{"Items":[{"Id":"library","Name":"Movies","CollectionType":"movies"}]}`)
			case "/base/Items":
				if q.Get("UserId") != "viewer" || q.Get("ParentId") != "library" || strings.Contains(q.Get("Fields"), "RunTimeTicks") {
					t.Error("incorrect user, library or field selection")
				}
				_ = json.NewEncoder(w).Encode(sourceSyncItems{[]emby.Item{sourceMovie("movie", "Movie", "mp4")}, 1})
			case "/base/Items/movie/Ancestors":
				fmt.Fprint(w, `[{"Id":"foreign-library"}]`)
			case "/base/Users/Me":
				fmt.Fprint(w, `{"Id":"viewer"}`)
			case "/base/Videos/movie/stream.mp4":
				if q.Get("Static") != "true" || q.Get("MediaSourceId") != "moviesrc" {
					t.Error("incorrect original stream request")
				}
				fmt.Fprint(w, "video")
			default:
				t.Errorf("unexpected endpoint %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer upstream.Close()
		admin := login(t, s, "jumedia", "admin123")
		params := url.Values{"type": {"jellyfin"}, "url": {upstream.URL + "/base/"}, "username": {"viewer"}}
		assertCode(t, sourcePost(t, s, admin, "/x/web/datasource/test", params), -400) // private address
		d := sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", params))
		id := int64(d["id"].(float64))
		ds := sourceWait(t, s, id)
		movie := sourceArchives(t, s, id)["movie"]
		if ds.Type != "jellyfin" || movie == nil || movie.Origin.Type != ds.Type || movie.Source != "Jellyfin Test Library" {
			t.Fatal("incorrect source or archive origin")
		}
		proxy := sourceHTTP(s, emby.DSPath(id, "movie", "moviesrc", "direct"))
		if proxy.Code != http.StatusOK || proxy.Body.String() != "video" {
			t.Fatal("proxy playback failed", proxy.Code)
		}
		params.Set("id", fmt.Sprint(id))
		params.Set("type", "emby")
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/save", params), -400)
		params.Del("type") // Older editors omit type.
		params.Set("sync_now", "false")
		params.Set("proxy_playback", "false")
		sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", params))
		play := sourceData(t, get(t, s, fmt.Sprintf("/x/player/wbi/playurl?cid=%d", movie.Pages[0].CID)))
		link := play["durl"].([]any)[0].(map[string]any)["url"].(string)
		u, err := url.Parse(link)
		if err != nil || !strings.HasPrefix(link, upstream.URL+"/base/Videos/movie/stream.mp4?") || u.Query().Get("ApiKey") != "fixture-token" {
			t.Fatal("incorrect direct playback endpoint")
		}
		if err := s.Store.SetSetting(t.Context(), "dataSourcePrivateNetwork", true); err != nil {
			t.Fatal(err)
		}
		params.Del("id")
		params.Set("type", "jellyfin")
		personal := sourceData(t, sourcePost(t, s, admin, "/x/web/datasource/save", params))
		importParams := url.Values{"id": {fmt.Sprint(personal["id"])}, "library": {"library"}, "item_ids": {`["movie"]`}}
		liveFailure(t, sourcePost(t, s, admin, "/x/web/datasource/import", importParams), -400, "所选视频不在该媒体库")
		params.Set("type", "unknown")
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/test", params), -400)
	})
}
