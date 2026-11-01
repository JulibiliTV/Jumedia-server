package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"jumedia-server/internal/store"
)

func TestSpaceRoutes(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		arcs, err := s.Store.ListArchive(context.Background(), "WHERE mid<>? AND state=0 ORDER BY aid", session.MID)
		if err != nil || len(arcs) == 0 {
			t.Fatalf("archives: %v", err)
		}
		a := arcs[0]
		cols, err := s.Store.ListCollection(context.Background(), "ORDER BY id")
		if err != nil || len(cols) == 0 {
			t.Fatalf("collections: %v", err)
		}
		aid, mid, col := strconv.FormatInt(a.AID, 10), strconv.FormatInt(a.MID, 10), strconv.FormatInt(cols[0].ID, 10)
		cases := []struct {
			path, query, key string
			null             bool
		}{
			{"/x/web-interface/archive/has/like", "aid=" + aid, "", false},
			{"/x/web-interface/archive/like", "aid=" + aid + "&like=1", "", true},
			{"/x/v2/view/dislike", "aid=" + aid + "&dislike=0", "", true},
			{"/x/web-interface/coin/add", "aid=" + aid + "&select_like=1", "like", false},
			{"/x/v2/fav/video/favoured", "aid=" + aid, "favoured", false},
			{"/x/v3/fav/resource/deal", "rid=" + aid, "prompt", false},
			{"/x/web-interface/share/add", "aid=" + aid, "share_url", false},
			{"/x/relation/modify", "fid=" + mid + "&act=1", "", true},
			{"/x/relation/followings", "vmid=1", "list", false},
			{"/x/space/wbi/acc/info", "mid=" + mid, "live_room", false},
			{"/x/space/wbi/acc/relation", "mid=" + mid, "be_relation", false},
			{"/x/relation/stat", "vmid=" + mid, "follower", false},
			{"/x/web-interface/card", "mid=" + mid, "card", false},
			{"/x/space/wbi/arc/search", "mid=" + mid, "list", false},
			{"/x/v2/space/archive/cursor", "vmid=" + mid, "item", false},
			{"/x/space/top/arc", "vmid=" + mid, "reason", false},
			{"/x/space/masterpiece", "vmid=" + mid, "", false},
			{"/x/polymer/web-space/seasons_series_list", "mid=" + mid, "items_lists", false},
			{"/x/polymer/web-space/seasons_archives_list", "season_id=" + col, "archives", false},
			{"/x/series/archives", "series_id=" + col, "aids", false},
		}
		// There are 20 paths: the task's abbreviated /archive/like means the full
		// /x/web-interface/archive/like path in server.ts, not an extra alias.
		for _, tc := range cases {
			t.Run(tc.path, func(t *testing.T) {
				p, _ := url.ParseQuery(tc.query)
				p.Set("csrf", session.CSRF)
				v := post(t, s, tc.path, p, session)
				assertCode(t, v, 0)
				if v["message"] != "0" || v["ttl"] != float64(1) {
					t.Fatalf("envelope: %v", v)
				}
				if tc.null && v["data"] != nil {
					t.Fatalf("expected null: %v", v)
				}
				if tc.key != "" {
					m, ok := v["data"].(map[string]any)
					if !ok {
						t.Fatalf("data: %v", v)
					}
					if _, ok = m[tc.key]; !ok {
						t.Fatalf("missing %s", tc.key)
					}
				}
				// Every route also uses the same pipeline error contract, including read
				// routes whose Mock intentionally never rejects absent IDs.
				req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("csrf=%zz"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, req)
				var bad map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &bad); err != nil {
					t.Fatal(err)
				}
				assertCode(t, bad, -400)
				if bad["message"] != "请求参数错误" {
					t.Fatalf("error: %v", bad)
				}
			})
		}
	})
}

func spaceTestUser(t *testing.T, s *Server, id int64) *store.User {
	t.Helper()
	u, err := s.Store.UserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
