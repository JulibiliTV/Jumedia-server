package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

var pgcRoutes = []string{
	"/pgc/web/rank/list", "/pgc/page/web/v3/feed", "/pgc/page/web/feed", "/pgc/page/web/v3",
	"/pgc/web/timeline", "/pgc/season/rank/index", "/pgc/season/rank/list", "/pgc/view/web/season",
	"/pgc/season/index/result", "/pgc/player/web/playurl", "/x/space/bangumi/follow/list",
	"/pgc/view/web/season/user/status", "/pgc/web/follow/add", "/pgc/web/follow/del", "/pgc/web/follow/status/update",
}

func TestPgcRoutes(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		seasons, err := srv.Store.ListSeason(t.Context(), "ORDER BY season_id")
		if err != nil || len(seasons) == 0 {
			t.Fatalf("seasons: %v", err)
		}
		season, err := srv.Store.SeasonByID(t.Context(), seasons[0].SeasonID)
		if err != nil {
			t.Fatal(err)
		}
		session := login(t, srv, "jumedia", "admin123")
		for _, path := range pgcRoutes {
			t.Run(path, func(t *testing.T) {
				for _, method := range []string{http.MethodGet, http.MethodPost} {
					params := url.Values{"season_id": {fmt.Sprint(season.SeasonID)}, "ep_id": {fmt.Sprint(season.Episodes[0].EpID)}, "vmid": {"1"}, "type": {"1"}, "csrf": {session.CSRF}}
					if path == "/pgc/web/follow/status/update" {
						assertCode(t, post(t, srv, "/pgc/web/follow/add", params, session), 0)
					}
					target := path
					body := params
					if method == http.MethodGet {
						target += "?" + params.Encode()
						body = nil
					}
					v, _ := request(t, srv, method, target, body, session)
					assertCode(t, v, 0)
					key := "data"
					if path == "/pgc/web/rank/list" || path == "/pgc/web/timeline" || path == "/pgc/view/web/season" || path == "/pgc/player/web/playurl" || path == "/pgc/view/web/season/user/status" || strings.HasPrefix(path, "/pgc/web/follow/") {
						key = "result"
					}
					if v[key] == nil {
						t.Fatalf("missing %s: %+v", key, v)
					}
					if key == "result" && (v["message"] != "success" || v["ttl"] != nil || v["data"] != nil) {
						t.Fatalf("result envelope: %+v", v)
					}
				}
				// Read-only mock handlers have no domain error; malformed forms exercise
				// their actual request error path without inventing new validation.
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("bad=%zz"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)
				if !strings.Contains(w.Body.String(), `"code":-400`) || !strings.Contains(w.Body.String(), "请求参数错误") {
					t.Fatalf("malformed form: %s", w.Body.String())
				}
			})
		}
	})
}
