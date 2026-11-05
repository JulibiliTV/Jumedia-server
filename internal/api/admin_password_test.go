package api

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestAdminPasswordChange(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		users, err := s.Store.ListUser(t.Context(), "WHERE role='user' ORDER BY mid")
		if err != nil {
			t.Fatal(err)
		}
		u := users[0]
		one := login(t, s, u.Username, "demo123")
		two := login(t, s, u.Username, "demo123")
		p := url.Values{"mid": {adminID(u.MID)}, "new_password": {"fixture-new-password"}}
		assertCode(t, post(t, s, "/x/admin/user/password", p), -101)
		assertCode(t, adminTestPost(t, s, one, "user/password", p), -403)
		p.Del("csrf")
		assertCode(t, post(t, s, "/x/admin/user/password", p, admin), -111)
		assertCode(t, get(t, s, "/x/admin/user/password?"+p.Encode(), admin), -405)
		for _, password := range []string{"", "short"} {
			p.Set("new_password", password)
			assertCode(t, adminTestPost(t, s, admin, "user/password", p), -400)
		}
		p.Set("new_password", "fixture-new-password")
		for _, mid := range []string{"999999999", "2.5"} {
			p.Set("mid", mid)
			assertCode(t, adminTestPost(t, s, admin, "user/password", p), -404)
		}
		p.Set("mid", adminID(u.MID))
		liveExec(t, s, "INSERT INTO qrcodes(code,status,mid,ctime) VALUES ('fixture-reset-qr','confirmed',?,1)", u.MID)
		result := adminTestPost(t, s, admin, "user/password", p)
		assertCode(t, result, 0)
		got, err := s.Store.UserByID(t.Context(), u.MID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Password == p.Get("new_password") || !accountCheckPassword(got.Password, p.Get("new_password")) || accountCheckPassword(got.Password, "demo123") {
			t.Fatal("password hash not replaced correctly")
		}
		if got.Role != u.Role || got.Coins != u.Coins || got.Status != u.Status {
			t.Fatal("password reset changed other user fields")
		}
		assertCode(t, get(t, s, "/x/web-interface/nav", one), -101)
		assertCode(t, get(t, s, "/x/web-interface/nav?access_key="+two.Access), -101)
		if adminTestCount(t, s, "SELECT COUNT(*) FROM sessions WHERE mid=?", u.MID) != 0 || adminTestCount(t, s, "SELECT COUNT(*) FROM qrcodes WHERE mid=?", u.MID) != 0 {
			t.Fatal("old login credentials retained")
		}
		assertCode(t, get(t, s, "/x/web-interface/nav", admin), 0)
		adminTestLog(t, s, "修改用户密码", u.Uname)
		for _, response := range []map[string]any{result, get(t, s, "/x/admin/users", admin), get(t, s, "/x/admin/logs", admin)} {
			raw, _ := json.Marshal(response)
			if strings.Contains(string(raw), p.Get("new_password")) || strings.Contains(string(raw), got.Password) {
				t.Fatal("password exposed in response or log")
			}
		}
		assertCode(t, post(t, s, "/x/passport-login/web/login", url.Values{"username": {u.Username}, "password": {"demo123"}}), -629)
		assertCode(t, post(t, s, "/x/passport-login/web/login", url.Values{"username": {u.Username}, "password": {p.Get("new_password")}}), 0)
		// Reuse the existing long-password hashing behavior and support changing oneself.
		password := strings.Repeat("长密码", 40)
		assertCode(t, adminTestPost(t, s, admin, "user/password", url.Values{"mid": {adminID(admin.MID)}, "new_password": {password}}), 0)
		assertCode(t, get(t, s, "/x/web-interface/nav", admin), -101)
		assertCode(t, post(t, s, "/x/passport-login/web/login", url.Values{"username": {"jumedia"}, "password": {password}}), 0)
	})
}

func TestAdminPasswordAuditRollback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		before, err := s.Store.UserByID(t.Context(), admin.MID)
		if err != nil {
			t.Fatal(err)
		}
		liveExec(t, s, "DROP TABLE logs")
		assertCode(t, adminTestPost(t, s, admin, "user/password", url.Values{"mid": {adminID(admin.MID)}, "new_password": {"fixture-changed"}}), -500)
		after, err := s.Store.UserByID(t.Context(), admin.MID)
		if err != nil || after.Password != before.Password {
			t.Fatal("password change did not roll back", err)
		}
		assertCode(t, get(t, s, "/x/web-interface/nav", admin), 0)
	})
}
