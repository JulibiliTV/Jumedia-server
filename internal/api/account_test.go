package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"jumedia-server/internal/store"
)

const accountLoginPath = "/x/passport-login/web/login"
const accountRegisterPath = "/x/passport-login/web/register"
const accountQRPath = "/x/passport-tv-login/qrcode/"
const accountH5Path = "/x/passport-tv-login/h5/qrcode/"
const accountRefreshPath = "/api/v2/oauth2/refresh_token"

func accountData(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	assertCode(t, v, 0)
	d, ok := v["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data: %#v", v)
	}
	return d
}

func accountFailure(t *testing.T, v map[string]any, code int, message string) {
	t.Helper()
	assertCode(t, v, code)
	if v["message"] != message || v["ttl"] != float64(1) || v["data"] != nil {
		t.Fatalf("error envelope: %#v", v)
	}
}

func accountExec(t *testing.T, srv *Server, query string, args ...any) {
	t.Helper()
	if _, err := srv.Store.DB.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func accountCookieSession(t *testing.T, srv *Server, cookies []*http.Cookie, mid float64) *store.Session {
	t.Helper()
	if len(cookies) != 3 {
		t.Fatalf("cookies: %#v", cookies)
	}
	values := map[string]string{}
	for _, c := range cookies {
		values[c.Name] = c.Value
		if c.Path != "/" || c.MaxAge != SessionMaxAge || c.HttpOnly != (c.Name == "SESSDATA") {
			t.Fatalf("cookie: %#v", c)
		}
	}
	s, err := srv.Store.SessionByToken(context.Background(), values["SESSDATA"])
	if err != nil {
		t.Fatal(err)
	}
	if values["bili_jct"] != s.CSRF || values["DedeUserID"] != strconv.FormatInt(s.MID, 10) || float64(s.MID) != mid {
		t.Fatalf("session/cookie mismatch: %#v", s)
	}
	if !regexp.MustCompile(`^[a-z0-9]{10}-[0-9]+-[a-z0-9]{6}$`).MatchString(s.Token) {
		t.Fatalf("token: %q", s.Token)
	}
	for _, v := range []string{s.CSRF, s.Access, s.Refresh} {
		if !regexp.MustCompile(`^[a-z0-9]{32}$`).MatchString(v) {
			t.Fatalf("credential: %q", v)
		}
	}
	exp, err := strconv.ParseInt(strings.Split(s.Token, "-")[1], 10, 64)
	if err != nil || exp != s.Ctime+SessionMaxAge {
		t.Fatalf("token expiry: %q", s.Token)
	}
	return s
}

func TestAccountWebLoginAndLogout(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		// The Mock calls username.trim() directly: an omitted field reaches
		// its generic error envelope, unlike an explicitly empty username.
		missing := post(t, srv, accountLoginPath, url.Values{"password": {"admin123"}})
		assertCode(t, missing, -500)
		if _, ok := missing["data"]; ok || missing["message"] != "服务器内部错误" || missing["ttl"] != float64(1) {
			t.Fatalf("missing username: %#v", missing)
		}
		for _, username := range []string{"jumedia", "missing", "' OR 1=1 --", ""} {
			accountFailure(t, post(t, srv, accountLoginPath, url.Values{"username": {username}, "password": {"wrong"}}), -629, "账号或密码错误")
		}
		v, w := request(t, srv, "POST", accountLoginPath, url.Values{"username": {"  jumedia  "}, "password": {"admin123"}}, nil)
		d := accountData(t, v)
		if d["status"] != float64(0) || d["message"] != "" || d["url"] != "/" {
			t.Fatalf("login: %#v", d)
		}
		s := accountCookieSession(t, srv, w.Result().Cookies(), d["mid"].(float64))
		if d["refresh_token"] != s.Refresh {
			t.Fatal("refresh token mismatch")
		}
		assertCode(t, get(t, srv, "/x/web-interface/nav?access_key="+s.Access), 0)
		accountExec(t, srv, "UPDATE users SET email=? WHERE mid=?", "login@example.test", s.MID)
		assertCode(t, get(t, srv, accountLoginPath+"?username=login%40example.test&password=admin123"), 0)
		accountExec(t, srv, "UPDATE users SET status=? WHERE mid=?", 1, s.MID)
		accountFailure(t, post(t, srv, accountLoginPath, url.Values{"username": {"jumedia"}, "password": {"admin123"}}), -403, "账号已被封禁，请联系管理员")
		accountExec(t, srv, "UPDATE users SET status=? WHERE mid=?", 0, s.MID)
		accountFailure(t, post(t, srv, "/login/exit/v2", nil), -101, "账号未登录")
		accountFailure(t, post(t, srv, "/login/exit/v2", nil, s), -111, "csrf 校验失败")
		other := login(t, srv, "jumedia", "admin123")
		v, w = request(t, srv, "POST", "/login/exit/v2", url.Values{"biliCSRF": {s.CSRF}}, s)
		assertCode(t, v, 0)
		if len(v) != 4 || v["status"] != true || v["ts"].(float64) <= 0 || accountData(t, v)["redirectUrl"] != "/" {
			t.Fatalf("logout root: %#v", v)
		}
		for _, c := range w.Result().Cookies() {
			if c.Value != "" || c.MaxAge != -1 || c.Path != "/" {
				t.Fatalf("clear cookie: %#v", c)
			}
		}
		if len(w.Result().Cookies()) != 3 {
			t.Fatal("missing cleared cookies")
		}
		if _, err := srv.Store.SessionByToken(context.Background(), s.Token); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("session remains: %v", err)
		}
		assertCode(t, get(t, srv, "/x/web-interface/nav", other), 0)
		logs, err := srv.Store.ListLog(context.Background(), "WHERE mid=? AND action=?", s.MID, "登录")
		if err != nil || len(logs) != 2 {
			t.Fatalf("login logs: %v %v", logs, err)
		}
	})
}

func TestAccountRegisterValidationAndDefaults(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		ctx := context.Background()
		params := func() url.Values {
			return url.Values{"username": {"new_user"}, "password": {"secret123"}, "uname": {"新用户"}, "email": {"new@example.test"}}
		}
		if err := srv.Store.SetSetting(ctx, "allowRegister", false); err != nil {
			t.Fatal(err)
		}
		accountFailure(t, post(t, srv, accountRegisterPath, params()), -403, "当前暂停开放注册")
		if err := srv.Store.SetSetting(ctx, "allowRegister", true); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ key, value, message string }{
			{"username", "a-b", "用户名需为 4–20 位字母、数字或下划线"},
			{"username", strings.Repeat("a", 21), "用户名需为 4–20 位字母、数字或下划线"},
			{"password", "12345", "密码至少 6 位"},
			{"uname", " ", "昵称需为 1–16 个字符"},
			{"uname", strings.Repeat("😀", 9), "昵称需为 1–16 个字符"},
			{"username", "jumedia", "用户名已被占用"},
		} {
			p := params()
			p.Set(tc.key, tc.value)
			accountFailure(t, post(t, srv, accountRegisterPath, p), -400, tc.message)
		}
		var maxMID int64
		if err := srv.Store.DB.QueryRowContext(ctx, "SELECT MAX(mid) FROM users").Scan(&maxMID); err != nil {
			t.Fatal(err)
		}
		p := params()
		p.Set("username", "\ufeff new_user \ufeff")
		p.Set("uname", " 新用户 ")
		v, w := request(t, srv, "POST", accountRegisterPath, p, nil)
		d := accountData(t, v)
		if d["mid"] != float64(maxMID+1) || len(d) != 1 {
			t.Fatalf("register: %#v", d)
		}
		s := accountCookieSession(t, srv, w.Result().Cookies(), d["mid"].(float64))
		u, err := srv.Store.UserByID(ctx, s.MID)
		if err != nil {
			t.Fatal(err)
		}
		if u.Username != "new_user" || u.Uname != "新用户" || u.Level != 1 || u.Exp != 0 || u.Coins != 10 || u.Role != "user" || u.Status != 0 || u.Sex != "保密" || u.Sign != "" || u.Birthday != "" || u.VIP != 0 || u.Email != "new@example.test" || u.Face == "" || u.TopPhoto != "" {
			t.Fatalf("defaults: %#v", u)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte("secret123")); err != nil {
			t.Fatal(err)
		}
		folders, err := srv.Store.ListFavFolder(ctx, "WHERE mid=?", s.MID)
		if err != nil || len(folders) != 1 || folders[0].Title != "默认收藏夹" || folders[0].Privacy != 0 || folders[0].Attr != 0 {
			t.Fatalf("folders: %#v %v", folders, err)
		}
		p = params()
		p.Set("username", "another_user")
		accountFailure(t, post(t, srv, accountRegisterPath, p), -400, "昵称已被占用")
		assertCode(t, post(t, srv, accountLoginPath, url.Values{"username": {u.Email}, "password": {"secret123"}}), 0)
		logs, err := srv.Store.ListLog(ctx, "WHERE mid=? AND action=?", s.MID, "注册")
		if err != nil || len(logs) != 1 || logs[0].Target != u.Uname {
			t.Fatalf("register log: %v %v", logs, err)
		}
	})
}

func accountTVSession(t *testing.T, srv *Server, d map[string]any, flat bool) *store.Session {
	t.Helper()
	token := d["token_info"].(map[string]any)
	if token["expires_in"] != float64(SessionMaxAge) {
		t.Fatalf("token info: %#v", token)
	}
	if flat {
		for k, v := range token {
			if d[k] != v {
				t.Fatalf("flat %s: %#v", k, d)
			}
		}
	}
	s, err := srv.Store.SessionByAccess(context.Background(), token["access_token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if float64(s.MID) != token["mid"] || s.Refresh != token["refresh_token"] {
		t.Fatalf("token info: %#v", token)
	}
	info := d["cookie_info"].(map[string]any)
	domains := info["domains"].([]any)
	if len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("domains: %#v", domains)
	}
	cs := info["cookies"].([]any)
	if len(cs) != 3 {
		t.Fatalf("cookies: %#v", cs)
	}
	want := map[string]string{"SESSDATA": s.Token, "bili_jct": s.CSRF, "DedeUserID": strconv.FormatInt(s.MID, 10)}
	for _, raw := range cs {
		c := raw.(map[string]any)
		name := c["name"].(string)
		httpOnly := float64(0)
		if name == "SESSDATA" {
			httpOnly = 1
		}
		if c["value"] != want[name] || c["http_only"] != httpOnly || c["secure"] != float64(0) || c["expires"] != float64(s.Ctime+SessionMaxAge) {
			t.Fatalf("cookie info: %#v", c)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing cookie info: %#v", want)
	}
	return s
}

func TestAccountQRCodeAndRefresh(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		s := login(t, srv, "jumedia", "admin123")
		d := accountData(t, get(t, srv, accountQRPath+"auth_code"))
		code := d["auth_code"].(string)
		if !regexp.MustCompile(`^[a-z0-9]{32}$`).MatchString(code) || d["url"] != "http://example.com/tv-login?auth_code="+code {
			t.Fatalf("auth code: %#v", d)
		}
		p := url.Values{"auth_code": {code}, "csrf": {s.CSRF}}
		accountFailure(t, post(t, srv, accountQRPath+"poll", p), 86039, "二维码尚未确认")
		accountFailure(t, post(t, srv, accountH5Path+"scan", p), -101, "账号未登录")
		v := post(t, srv, accountH5Path+"scan", p, s)
		assertCode(t, v, 0)
		if v["data"] != nil {
			t.Fatalf("scan: %#v", v)
		}
		accountFailure(t, post(t, srv, accountQRPath+"poll", p), 86090, "二维码已扫码未确认")
		accountFailure(t, post(t, srv, accountH5Path+"confirm", url.Values{"auth_code": {code}}, s), -111, "csrf 校验失败")
		v = post(t, srv, accountH5Path+"confirm", p, s)
		assertCode(t, v, 0)
		if v["data"] != nil {
			t.Fatalf("confirm: %#v", v)
		}
		assertCode(t, post(t, srv, accountH5Path+"scan", p, s), 0)
		v, w := request(t, srv, "POST", accountQRPath+"poll", p, nil)
		tv := accountTVSession(t, srv, accountData(t, v), true)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("TV poll should return cookie_info only")
		}
		accountFailure(t, post(t, srv, accountQRPath+"poll", p), 86038, "二维码已失效")
		for _, route := range []string{accountQRPath + "poll", accountH5Path + "scan", accountH5Path + "confirm"} {
			accountFailure(t, post(t, srv, route, p, s), 86038, "二维码已失效")
		}
		code = accountData(t, get(t, srv, accountQRPath+"auth_code"))["auth_code"].(string)
		p.Set("auth_code", code)
		accountExec(t, srv, "UPDATE qrcodes SET ctime=? WHERE code=?", time.Now().Unix()-181, code)
		for _, route := range []string{accountQRPath + "poll", accountH5Path + "scan", accountH5Path + "confirm"} {
			accountFailure(t, post(t, srv, route, p, s), 86038, "二维码已失效")
		}
		accountFailure(t, post(t, srv, accountRefreshPath, url.Values{"refresh_token": {"bad"}}), -101, "刷新令牌无效")
		v, w = request(t, srv, "POST", accountRefreshPath, url.Values{"refresh_token": {tv.Refresh}}, nil)
		refreshed := accountTVSession(t, srv, accountData(t, v), false)
		if refreshed.Access == tv.Access || refreshed.Refresh == tv.Refresh || refreshed.Token == tv.Token || len(w.Result().Cookies()) != 0 {
			t.Fatal("session did not rotate cleanly")
		}
		accountFailure(t, post(t, srv, accountRefreshPath, url.Values{"refresh_token": {tv.Refresh}}), -101, "刷新令牌无效")
		assertCode(t, get(t, srv, "/x/web-interface/nav?access_key="+tv.Access), -101)
		assertCode(t, get(t, srv, "/x/web-interface/nav?access_key="+refreshed.Access), 0)
		assertCode(t, get(t, srv, "/x/web-interface/nav", s), 0)
		logs, err := srv.Store.ListLog(context.Background(), "WHERE mid=? AND action=?", s.MID, "TV 扫码登录")
		if err != nil || len(logs) != 1 {
			t.Fatalf("TV logs: %v %v", logs, err)
		}
	})
}

func TestAccountPasswordAndProfile(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		s := login(t, srv, "jumedia", "admin123")
		passwordPath := "/x/passport-login/web/password/update"
		profilePath := "/x/member/web/update"
		for _, route := range []string{passwordPath, profilePath} {
			accountFailure(t, post(t, srv, route, nil), -101, "账号未登录")
			accountFailure(t, post(t, srv, route, nil, s), -111, "csrf 校验失败")
		}
		p := url.Values{"csrf": {s.CSRF}, "old_password": {"wrong"}, "new_password": {"new123"}}
		accountFailure(t, post(t, srv, passwordPath, p, s), -400, "原密码错误")
		p.Set("old_password", "admin123")
		p.Set("new_password", "12345")
		accountFailure(t, post(t, srv, passwordPath, p, s), -400, "新密码至少 6 位")
		p.Set("new_password", "new123")
		v := post(t, srv, passwordPath, p, s)
		assertCode(t, v, 0)
		if v["data"] != nil {
			t.Fatalf("password result: %#v", v)
		}
		accountFailure(t, post(t, srv, accountLoginPath, url.Values{"username": {"jumedia"}, "password": {"admin123"}}), -629, "账号或密码错误")
		assertCode(t, post(t, srv, accountLoginPath, url.Values{"username": {"jumedia"}, "password": {"new123"}}), 0)
		p = url.Values{"csrf": {s.CSRF}}
		accountFailure(t, post(t, srv, profilePath, p, s), -400, "昵称需为 1–16 个字符")
		other, err := srv.Store.UserByID(context.Background(), 2)
		if err != nil {
			t.Fatal(err)
		}
		p.Set("uname", other.Uname)
		accountFailure(t, post(t, srv, profilePath, p, s), -400, "昵称已被占用")
		p.Set("uname", " 新昵称 ")
		p.Set("sign", strings.Repeat("😀", 36))
		p.Set("sex", "任意")
		p.Set("birthday", "2001-02-03")
		p.Set("face", "/upload/avatar.png")
		v = post(t, srv, profilePath, p, s)
		assertCode(t, v, 0)
		if v["data"] != nil {
			t.Fatalf("profile result: %#v", v)
		}
		u, err := srv.Store.UserByID(context.Background(), s.MID)
		if err != nil {
			t.Fatal(err)
		}
		if u.Uname != "新昵称" || u.Sign != strings.Repeat("😀", 35) || u.Sex != "任意" || u.Birthday != "2001-02-03" || u.Face != "/upload/avatar.png" {
			t.Fatalf("profile: %#v", u)
		}
		assertCode(t, post(t, srv, profilePath, url.Values{"csrf": {s.CSRF}, "uname": {"新昵称"}}, s), 0)
		u, err = srv.Store.UserByID(context.Background(), s.MID)
		if err != nil {
			t.Fatal(err)
		}
		if u.Sign != "" || u.Sex != "保密" || u.Birthday != "" || u.Face != "/upload/avatar.png" {
			t.Fatalf("profile defaults: %#v", u)
		}
	})
}
