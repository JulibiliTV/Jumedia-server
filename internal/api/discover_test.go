package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverRoutes(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		as, err := s.Store.PublishedArchives(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		a := as[0]
		session := login(t, s, "jumedia", "admin123")
		for _, tc := range []struct {
			path, field string
			raw         bool
		}{
			{"/x/v2/feed/index", "items", false},
			{"/x/feed/dislike", "", false},
			{"/x/web-interface/popular", "list", false},
			{"/x/web-interface/ranking/v2", "list", false},
			{fmt.Sprintf("/x/web-interface/region/feed/rcmd?from_region=%d", a.TID), "archives", false},
			{"/x/polymer/web-dynamic/v1/feed/all", "items", false},
			{"/x/web-interface/search/all/v2", "result", false},
			{"/x/web-interface/wbi/search/type?search_type=video", "result", false},
			{"/x/web-interface/suggest?term=日", "result", true},
			{fmt.Sprintf("/x/web-interface/view?aid=%d", a.AID), "pages", false},
			{fmt.Sprintf("/x/web-interface/view/detail?aid=%d", a.AID), "View", false},
			{fmt.Sprintf("/x/player/wbi/v2?aid=%d&cid=%d", a.AID, a.Pages[0].CID), "subtitle", false},
			{"/x/player/videoshot", "image", false},
			{fmt.Sprintf("/x/player/wbi/playurl?cid=%d", a.Pages[0].CID), "durl", false},
			{fmt.Sprintf("/bfs/subtitle/%d-0.json", a.AID), "body", true},
		} {
			t.Run(tc.path, func(t *testing.T) {
				for _, method := range []string{"GET", "POST"} {
					v, _ := request(t, s, method, tc.path, nil, session)
					if strings.HasPrefix(tc.path, "/bfs/") {
						if _, ok := v["code"]; ok {
							t.Fatalf("subtitle wrapped: %v", v)
						}
					} else {
						assertCode(t, v, 0)
					}
					data := v
					if !tc.raw && tc.field != "" {
						data = v["data"].(map[string]any)
					}
					if tc.field != "" {
						if _, ok := data[tc.field]; !ok {
							t.Fatalf("missing %s: %v", tc.field, v)
						}
					}
					if tc.field == "" && v["data"] != nil {
						t.Fatalf("dislike: %v", v)
					}
				}
				// Some Mock handlers intentionally have no business error branch.
				// Every registered route must still reject malformed request bodies.
				r := httptest.NewRequest("POST", tc.path, strings.NewReader("broken=%zz"))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				var v map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
					t.Fatal(err)
				}
				assertCode(t, v, -400)
			})
		}
	})
}
