package api

import (
	"net/url"
	"testing"
)

var adminReadRoutes = []struct {
	path   string
	fields []string
}{
	{"dashboard", []string{"users", "banned", "archives", "pending", "seasons", "episodes", "rooms", "live_now", "replies", "danmaku", "views", "trend", "regions", "top", "recent"}},
	{"users", []string{"list", "page"}}, {"archives", []string{"list", "page", "counts"}},
	{"seasons", []string{"list", "page"}}, {"live/rooms", []string{"list", "page"}},
	{"replies", []string{"list", "page"}}, {"danmaku", []string{"list", "page"}},
	{"regions", []string{"list"}}, {"live/areas", []string{"list"}}, {"banners", []string{"list"}},
	{"settings", []string{"siteName", "allowRegister", "maxUploadMB"}}, {"logs", []string{"list", "page"}},
}
var adminWriteRoutes = []string{"user/update", "archive/audit", "archive/delete", "season/save", "season/delete", "live/room/ban", "reply/delete", "danmaku/delete", "region/save", "region/delete", "live/area/save", "banner/save", "banner/delete", "settings/save"}

func TestAdminRoutePermissions(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		users, err := s.Store.ListUser(t.Context(), "WHERE role=? ORDER BY mid", "user")
		if err != nil {
			t.Fatal(err)
		}
		ordinary := login(t, s, users[0].Username, "demo123")
		paths := append([]string{}, adminWriteRoutes...)
		for _, r := range adminReadRoutes {
			paths = append(paths, r.path)
		}
		for _, path := range paths {
			t.Run(path, func(t *testing.T) {
				route := "/x/admin/" + path
				assertCode(t, get(t, s, route), -101)
				assertCode(t, get(t, s, route, ordinary), -403)
				assertCode(t, post(t, s, route, url.Values{}, admin), -111)
				assertCode(t, post(t, s, route, url.Values{"csrf": {ordinary.CSRF}}, ordinary), -403)
			})
		}
	})
}
func TestAdminReadRoutes(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		for _, r := range adminReadRoutes {
			t.Run(r.path, func(t *testing.T) {
				result := get(t, s, "/x/admin/"+r.path, session)
				assertCode(t, result, 0)
				data, ok := result["data"].(map[string]any)
				if !ok {
					t.Fatalf("data: %v", result)
				}
				for _, field := range r.fields {
					if _, ok := data[field]; !ok {
						t.Errorf("missing %s", field)
					}
				}
			})
		}
	})
}

func TestAdminPaginationAndDashboard(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		all := adminTestList(t, get(t, s, "/x/admin/users?ps=999", session))
		for _, tc := range []struct {
			query      string
			pn, ps     float64
			start, end int
		}{
			{"pn=2&ps=3", 2, 3, 3, 6}, {"pn=0&ps=3", 0, 3, 0, 3}, {"pn=2&ps=0", 2, 0, 0, 0},
			{"pn=1&ps=-2", 1, -2, 0, len(all) - 2}, {"pn=1.5&ps=3", 1.5, 3, 1, 4},
			{"pn=bad&ps=2", 1, 2, 0, 2}, {"pn=999999&ps=2", 999999, 2, len(all), len(all)},
		} {
			data := adminTestData(t, get(t, s, "/x/admin/users?"+tc.query, session))
			page := data["page"].(map[string]any)
			list := data["list"].([]any)
			if page["pn"] != tc.pn || page["ps"] != tc.ps || page["total"] != float64(len(all)) || len(list) != tc.end-tc.start {
				t.Fatalf("pagination %s: %v", tc.query, page)
			}
			for i, row := range list {
				if row.(map[string]any)["mid"] != all[tc.start+i].(map[string]any)["mid"] {
					t.Fatalf("page contents %s", tc.query)
				}
			}
		}
		for _, path := range []string{"users", "archives", "seasons", "live/rooms", "replies", "danmaku"} {
			data := adminTestData(t, get(t, s, "/x/admin/"+path+"?keyword="+url.QueryEscape("%' OR 1=1 --"), session))
			if data["page"].(map[string]any)["total"] != float64(0) || len(data["list"].([]any)) != 0 {
				t.Fatalf("keyword must be literal: %s", path)
			}
		}
		// Read routes accept POST too, and GET mutations deliberately follow the mock.
		for _, r := range adminReadRoutes {
			assertCode(t, adminTestPost(t, s, session, r.path, nil), 0)
		}
		assertCode(t, get(t, s, "/x/admin/region/save?name=GET+region", session), 0)
		assertCode(t, get(t, s, "/x/admin/settings?access_key="+session.Access), 0)
		assertCode(t, post(t, s, "/x/admin/settings", url.Values{"biliCSRF": {session.CSRF}}, session), 0)
		dashboard := adminTestData(t, get(t, s, "/x/admin/dashboard", session))
		queries := map[string]string{"users": "SELECT COUNT(*) FROM users", "banned": "SELECT COUNT(*) FROM users WHERE status<>0", "archives": "SELECT COUNT(*) FROM archives WHERE state=0", "pending": "SELECT COUNT(*) FROM archives WHERE state=-1", "seasons": "SELECT COUNT(*) FROM seasons", "episodes": "SELECT COUNT(*) FROM episodes", "rooms": "SELECT COUNT(*) FROM rooms", "live_now": "SELECT COUNT(*) FROM rooms WHERE live_status=1", "replies": "SELECT COUNT(*) FROM replies", "danmaku": "SELECT COUNT(*) FROM danmaku", "views": "SELECT SUM(stat_view) FROM archives"}
		for key, q := range queries {
			if dashboard[key] != float64(adminTestCount(t, s, q)) {
				t.Errorf("dashboard %s: %v", key, dashboard[key])
			}
		}
		trend := dashboard["trend"].([]any)
		if len(trend) != 14 {
			t.Fatal("expected 14 trend days")
		}
		for _, v := range trend {
			row := v.(map[string]any)
			for _, key := range []string{"date", "uploads", "users", "views"} {
				if _, ok := row[key]; !ok {
					t.Errorf("trend missing %s", key)
				}
			}
		}
		top := dashboard["top"].([]any)
		if len(top) != 6 {
			t.Fatal("top must have 6 archives")
		}
		var previous float64 = 1e30
		for _, v := range top {
			row := v.(map[string]any)
			views := row["stat"].(map[string]any)["view"].(float64)
			if views > previous || row["state"] != float64(0) {
				t.Fatal("top order/state")
			}
			previous = views
		}
		for _, v := range dashboard["regions"].([]any) {
			row := v.(map[string]any)
			if row["count"] != float64(adminTestCount(t, s, "SELECT COUNT(*) FROM archives WHERE tid=? AND state=0", row["tid"])) {
				t.Fatal("region count")
			}
		}
		logs := adminTestData(t, get(t, s, "/x/admin/logs", session))
		if logs["page"].(map[string]any)["ps"] != float64(30) {
			t.Fatal("logs default size")
		}
	})
}
