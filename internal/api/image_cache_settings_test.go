package api

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageCacheSettingsAndRoutes(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		users, err := s.Store.ListUser(t.Context(), "WHERE role<>? ORDER BY mid", "admin")
		if err != nil || len(users) == 0 {
			t.Fatal(err)
		}
		ordinary := login(t, s, users[0].Username, "demo123")
		assertCode(t, get(t, s, "/x/admin/imagecache", ordinary), -403)
		assertCode(t, post(t, s, "/x/admin/imagecache/clear", url.Values{"csrf": {ordinary.CSRF}}, ordinary), -403)
		assertCode(t, get(t, s, "/x/admin/imagecache/clear", admin), -404)

		assertCode(t, get(t, s, "/x/admin/imagecache"), -101)
		state := adminTestData(t, get(t, s, "/x/admin/imagecache", admin))
		if state["enabled"] != true || state["max_mb"] != float64(1024) || state["dir"] != filepath.Join(s.Config.DataDir, "image-cache") || state["files"] != float64(0) || state["bytes"] != float64(0) {
			t.Fatal(state)
		}
		assertCode(t, post(t, s, "/x/admin/imagecache/clear", nil, admin), -111)
		assertCode(t, post(t, s, "/x/admin/imagecache/clear", nil), -101)
		for _, v := range []string{"", "63", "1048577", "64.5", "NaN", "Infinity"} {
			adminTestError(t, adminTestPost(t, s, admin, "settings/save", url.Values{"imageCacheMaxMB": {v}}), -400, "图片缓存上限应为 64–1048576 MB 的整数")
		}
		adminTestError(t, adminTestPost(t, s, admin, "settings/save", url.Values{"imageCacheDir": {" relative "}}), -400, "图片缓存目录必须是绝对路径")
		blocked := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		v := adminTestPost(t, s, admin, "settings/save", url.Values{"imageCacheDir": {filepath.Join(blocked, "child")}})
		assertCode(t, v, -400)
		if !strings.HasPrefix(v["message"].(string), "图片缓存目录不可写：") {
			t.Fatal(v)
		}
		dir := filepath.Join(t.TempDir(), "cache")
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"imageCacheDir": {" " + dir + " "}, "imageCacheEnabled": {"false"}, "imageCacheMaxMB": {"0x40"}}), 0)
		state = adminTestData(t, get(t, s, "/x/admin/imagecache", admin))
		if state["dir"] != dir || state["enabled"] != false || state["max_mb"] != float64(64) {
			t.Fatal(state)
		}
		saved := adminTestData(t, get(t, s, "/x/admin/settings", admin))
		if saved["imageCacheDir"] != dir {
			t.Fatal(saved)
		}
		cleared := adminTestData(t, adminTestPost(t, s, admin, "imagecache/clear", nil))
		if cleared["files"] != float64(0) || cleared["bytes"] != float64(0) {
			t.Fatal(cleared)
		}
		if adminTestCount(t, s, "SELECT COUNT(*) FROM logs WHERE action=?", "清空图片缓存") != 1 {
			t.Fatal("missing log")
		}
	})
}
