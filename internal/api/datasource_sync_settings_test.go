package api

import (
	"net/url"
	"testing"
)

func TestSourceSyncSettings(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		data := adminTestData(t, get(t, s, "/x/admin/settings", session))
		if data["dataSourceSyncRate"] != float64(5) || data["dataSourceSyncConcurrency"] != float64(2) {
			t.Fatalf("defaults: %v", data)
		}
		for _, tt := range []struct {
			key, message string
			max          string
		}{
			{"dataSourceSyncRate", "每秒请求数应为 1–50 的整数", "50"},
			{"dataSourceSyncConcurrency", "并发请求数应为 1–8 的整数", "8"},
		} {
			for _, bad := range []string{"", "0", "-1", "1.5", "NaN", "Infinity", "51", "9.1"} {
				adminTestError(t, adminTestPost(t, s, session, "settings/save", url.Values{tt.key: {bad}}), -400, tt.message)
			}
			for _, good := range []string{"1", tt.max, " 2 ", "0x2", "2e0"} {
				assertCode(t, adminTestPost(t, s, session, "settings/save", url.Values{tt.key: {good}}), 0)
			}
		}
		data = adminTestData(t, get(t, s, "/x/admin/settings", session))
		if data["dataSourceSyncRate"] != float64(2) || data["dataSourceSyncConcurrency"] != float64(2) {
			t.Fatal(data)
		}
		assertCode(t, post(t, s, "/x/admin/settings/save", url.Values{"dataSourceSyncRate": {"5"}}, nil), -101)
	})
}
