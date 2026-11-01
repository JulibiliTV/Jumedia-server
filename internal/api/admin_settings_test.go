package api

import (
	"net/url"
	"testing"

	"jumedia-server/internal/store"
)

func TestAdminRegionsAreasAndBanners(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		regions, err := s.Store.ListRegion(t.Context(), "WHERE COALESCE(parent,0)=0 ORDER BY tid DESC")
		if err != nil {
			t.Fatal(err)
		}
		newID := regions[0].TID + 1
		adminTestError(t, adminTestPost(t, s, session, "region/save", url.Values{"name": {"  "}}), -400, "请填写分区名称")
		assertCode(t, adminTestPost(t, s, session, "region/save", url.Values{"tid": {"999999999"}, "name": {"  T9 新分区  "}, "visible": {"1"}, "sort": {"-1"}}), 0)
		list := adminTestList(t, get(t, s, "/x/admin/regions", session))
		first := list[0].(map[string]any)
		if first["tid"] != float64(newID) || first["name"] != "T9 新分区" || first["visible"] != true || first["count"] != float64(0) {
			t.Fatalf("region: %v", first)
		}
		adminTestLog(t, s, "新建分区", "T9 新分区")
		assertCode(t, adminTestPost(t, s, session, "region/save", url.Values{"tid": {adminID(newID)}, "name": {"改名"}, "visible": {"false"}}), 0)
		adminTestLog(t, s, "编辑分区", "改名")
		archives, err := s.Store.ListArchive(t.Context(), "WHERE state<>-100 ORDER BY aid LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		a := archives[0]
		adminTestError(t, adminTestPost(t, s, session, "region/delete", url.Values{"tid": {adminID(a.TID)}}), -400, "该分区下仍有稿件，请先迁移")
		if _, err = s.Store.DB.ExecContext(t.Context(), "UPDATE archives SET tid=?,state=-100 WHERE aid=?", newID, a.AID); err != nil {
			t.Fatal(err)
		}
		assertCode(t, adminTestPost(t, s, session, "region/delete", url.Values{"tid": {adminID(newID)}}), 0)
		if adminTestCount(t, s, "SELECT COUNT(*) FROM regions WHERE tid=?", newID) != 0 {
			t.Fatal("deleted-only region must be deletable")
		}
		assertCode(t, adminTestPost(t, s, session, "region/delete", url.Values{"tid": {adminID(newID)}}), 0)

		areas, err := s.Store.LiveAreas(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		maxID := int64(0)
		for _, area := range areas {
			maxID = max(maxID, area.ID)
		}
		assertCode(t, adminTestPost(t, s, session, "live/area/save", url.Values{"name": {"T9 直播分区"}}), 0)
		areas, err = s.Store.LiveAreas(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		area := areas[len(areas)-1]
		if area.ID != maxID+1 || len(area.Children) != 1 || area.Children[0].Name != area.Name {
			t.Fatalf("new live area: %+v", area)
		}
		assertCode(t, adminTestPost(t, s, session, "live/area/save", url.Values{"id": {adminID(area.ID)}, "name": {"直播改名"}}), 0)
		list = adminTestList(t, get(t, s, "/x/admin/live/areas", session))
		row := list[len(list)-1].(map[string]any)
		if row["name"] != "直播改名" || row["rooms"] != float64(0) || row["children"].([]any)[0].(map[string]any)["name"] != "T9 直播分区" {
			t.Fatal("editing parent must not rename child")
		}

		assertCode(t, adminTestPost(t, s, session, "banner/save", url.Values{"img": {"/mock/avatars/default1.png"}, "title": {"T9 Banner"}, "url": {"/bangumi/play/ss999999999"}, "position": {"anime"}, "sort": {"-100"}}), 0)
		list = adminTestList(t, get(t, s, "/x/admin/banners", session))
		banner := list[0].(map[string]any)
		if banner["title"] != "T9 Banner" || banner["position"] != "anime" || banner["season_id"] != float64(999999999) || banner["img"] != "http://example.com/mock/avatars/default1.png" {
			t.Fatalf("banner defaults: %v", banner)
		}
		bannerID := int64(banner["id"].(float64))
		adminTestLog(t, s, "新建轮播", "T9 Banner")
		assertCode(t, adminTestPost(t, s, session, "banner/save", url.Values{"id": {adminID(bannerID)}, "title": {"修改轮播"}, "img": {"/custom.png"}, "url": {"/video/BVtest"}, "position": {"unknown"}}), 0)
		list = adminTestList(t, get(t, s, "/x/admin/banners", session))
		found := false
		for _, b := range list {
			v := b.(map[string]any)
			if v["id"] == float64(bannerID) {
				found = true
				if v["img"] != "/custom.png" || v["position"] != "home" {
					t.Fatal("banner update")
				}
				if _, exists := v["season_id"]; exists {
					t.Fatal("old season_id not cleared")
				}
			}
		}
		if !found {
			t.Fatal("updated banner missing")
		}
		adminTestLog(t, s, "编辑轮播", "修改轮播")
		assertCode(t, adminTestPost(t, s, session, "banner/delete", url.Values{"id": {adminID(bannerID)}}), 0)
		if adminTestCount(t, s, "SELECT COUNT(*) FROM banners WHERE id=?", bannerID) != 0 {
			t.Fatal("banner not deleted")
		}
		assertCode(t, adminTestPost(t, s, session, "banner/delete", url.Values{"id": {adminID(bannerID)}}), 0)
	})
}

func TestAdminSettingsAndReset(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		assertCode(t, adminTestPost(t, s, session, "settings/save", url.Values{"siteName": {"测试站"}, "allowRegister": {"1"}, "auditRequired": {"true"}, "maxUploadMB": {"2.5"}, "defaultQuality": {"0x50"}, "unknown": {"ignore"}, "reset": {"1"}}), 0)
		data := adminTestData(t, get(t, s, "/x/admin/settings", session))
		if data["siteName"] != "测试站" || data["allowRegister"] != false || data["auditRequired"] != true || data["maxUploadMB"] != 2.5 || data["defaultQuality"] != float64(80) {
			t.Fatalf("type conversion: %v", data)
		}
		if _, exists := data["unknown"]; exists {
			t.Fatal("unknown setting persisted")
		}
		adminTestLog(t, s, "修改站点设置", "")
		assertCode(t, adminTestPost(t, s, session, "settings/save", url.Values{"maxUploadMB": {"invalid"}}), 0)
		data = adminTestData(t, get(t, s, "/x/admin/settings", session))
		if data["maxUploadMB"] != nil {
			t.Fatal("nonfinite JS numbers must serialize to null")
		}
		assertCode(t, adminTestPost(t, s, session, "settings/save", url.Values{"maxUploadMB": {"32"}}), 0)
		data = adminTestData(t, get(t, s, "/x/admin/settings", session))
		if data["maxUploadMB"] != "32" {
			t.Fatal("settings must use current value type")
		}
		assertCode(t, adminTestPost(t, s, session, "region/save", url.Values{"name": {"reset removes me"}}), 0)
		assertCode(t, adminTestPost(t, s, session, "settings/save", url.Values{"reset": {"true"}, "siteName": {"must be ignored"}}), 0)
		assertCode(t, get(t, s, "/x/admin/settings", session), -101)
		session = login(t, s, "jumedia", "admin123")
		data = adminTestData(t, get(t, s, "/x/admin/settings", session))
		if data["siteName"] != "Jumedia" || data["maxUploadMB"] != float64(2048) {
			t.Fatal("settings not reseeded")
		}
		if adminTestCount(t, s, "SELECT COUNT(*) FROM regions WHERE name=?", "reset removes me") != 0 {
			t.Fatal("reset didn't clear data")
		}
		adminTestLog(t, s, "重置站点数据", "")
		// A custom admin disappears during reset, so audit logging must use a nullable actor.
		id, err := s.Store.NextID(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		base, err := s.Store.UserByID(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		custom := *base
		custom.MID = id
		custom.Username = "t9custom"
		if err = store.InsertUser(t.Context(), s.Store.DB, &custom); err != nil {
			t.Fatal(err)
		}
		customSession := login(t, s, "t9custom", "admin123")
		assertCode(t, adminTestPost(t, s, customSession, "settings/save", url.Values{"reset": {"true"}}), 0)
		if adminTestCount(t, s, "SELECT COUNT(*) FROM users WHERE mid=?", id) != 0 {
			t.Fatal("custom user survived reset")
		}
	})
}
