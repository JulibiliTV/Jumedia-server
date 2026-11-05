package api

import (
	"fmt"
	"net/url"
	"testing"

	"jumedia-server/internal/store"
)

func TestAdminEditPersonalDataSource(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		admin := login(t, s, "jumedia", "admin123")
		users, err := s.Store.ListUser(t.Context(), "WHERE role='user' ORDER BY mid")
		if err != nil {
			t.Fatal(err)
		}
		owner := login(t, s, users[0].Username, "demo123")
		other := login(t, s, users[1].Username, "demo123")
		if err := s.Store.SetSetting(t.Context(), "dataSourcePrivateNetwork", true); err != nil {
			t.Fatal(err)
		}
		id := sourceSave(t, s, owner, f, false, "owner-source")
		second := sourceSave(t, s, owner, f, false, "second-source")
		p := url.Values{"id": {fmt.Sprint(id)}, "name": {"管理员调整"}, "url": {f.server.URL}, "username": {"owner-source"}, "proxy_playback": {"false"}, "auto_sync": {"false"}, "libraries": {"movies"}}
		assertCode(t, sourcePost(t, s, other, "/x/admin/datasource/save", p), -403)
		assertCode(t, sourcePost(t, s, other, "/x/web/datasource/save", p), -404)
		sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/test", p))
		d := sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p))
		if d["system"] != false || d["mid"] != float64(owner.MID) || d["proxy_playback"] != false || d["has_password"] != true {
			t.Fatal("owner, password presence or mode changed unexpectedly")
		}
		ds := sourceWait(t, s, id)
		password, err := s.decryptPassword(ds.PasswordEnc)
		if err != nil || password != "secret-password" {
			t.Fatal("blank password did not preserve saved credential")
		}
		if ds.Name != "管理员调整" || ds.AutoSync || ds.System || ds.MID != owner.MID {
			t.Fatal("source settings not preserved")
		}
		for _, library := range ds.Libraries {
			if library.Enabled != (library.ID == "movies") {
				t.Fatal("library selection not updated")
			}
		}
		adminTestLog(t, s, "修改数据源", "管理员调整")
		list := sourceData(t, get(t, s, "/x/admin/datasources?scope=user&keyword=管理员调整", admin))["list"].([]any)
		if len(list) != 1 || list[0].(map[string]any)["proxy_playback"] != false {
			t.Fatal("admin list missing playback mode")
		}
		p.Set("id", fmt.Sprint(second))
		assertCode(t, sourcePost(t, s, admin, "/x/admin/datasource/save", p), -400)
		stored, err := store.DataSourceByID(t.Context(), s.Store.DB, second)
		if err != nil || stored.Username != "second-source" {
			t.Fatal("duplicate update changed source", err)
		}
	})
}
