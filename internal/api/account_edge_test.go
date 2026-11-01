package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func TestAccountAuthCodeOriginsAndFailure(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		for _, tc := range []struct{ public, forwarded, host, want string }{
			{"", "", "", "http://example.com"},
			{"", "https", "media.example:8443", "https://media.example:8443"},
			{"https://public.example/", "http", "ignored.example", "https://public.example"},
		} {
			srv.Config.PublicOrigin = tc.public
			r := httptest.NewRequest("POST", accountQRPath+"auth_code", nil)
			r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			r.Header.Set("X-Forwarded-Host", tc.host)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			var v map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			d := accountData(t, v)
			if d["url"] != tc.want+"/tv-login?auth_code="+d["auth_code"].(string) {
				t.Fatalf("origin: %#v", d)
			}
			// cookie_info must use the same public host, without scheme/port.
			s := login(t, srv, "jumedia", "admin123")
			payload := srv.accountTokenPayload(&Ctx{R: r}, s, false)
			u, err := url.Parse(tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if payload["cookie_info"].(map[string]any)["domains"].([]string)[0] != u.Hostname() {
				t.Fatalf("cookie domain: %#v", payload)
			}
		}
		// auth_code has no business validation errors; exercise a storage error.
		accountExec(t, srv, "ALTER TABLE qrcodes RENAME TO account_test_qrcodes")
		v, w := request(t, srv, "POST", accountQRPath+"auth_code", nil, nil)
		assertCode(t, v, -500)
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("failed auth code issued cookies")
		}
		accountExec(t, srv, "ALTER TABLE account_test_qrcodes RENAME TO qrcodes")
	})
}

func TestAccountUnicodeAndLongPasswords(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		for i, password := range []string{"😀😀😀", strings.Repeat("a", 72), strings.Repeat("长", 40)} {
			username := []string{"emoji_user", "exact_user", "long_user"}[i]
			p := url.Values{"username": {username}, "uname": {username}, "password": {password}}
			v, w := request(t, srv, "POST", accountRegisterPath, p, nil)
			s := accountCookieSession(t, srv, w.Result().Cookies(), accountData(t, v)["mid"].(float64))
			assertCode(t, post(t, srv, accountLoginPath, url.Values{"username": {username}, "password": {password}}), 0)
			accountFailure(t, post(t, srv, accountLoginPath, url.Values{"username": {username}, "password": {password + "x"}}), -629, "账号或密码错误")
			newPassword := strings.Repeat("新", 30) + "!"
			assertCode(t, post(t, srv, "/x/passport-login/web/password/update", url.Values{"csrf": {s.CSRF}, "old_password": {password}, "new_password": {newPassword}}, s), 0)
			assertCode(t, post(t, srv, accountLoginPath, url.Values{"username": {username}, "password": {newPassword}}), 0)
			accountFailure(t, post(t, srv, accountLoginPath, url.Values{"username": {username}, "password": {newPassword[:72]}}), -629, "账号或密码错误")
		}
	})
}

func TestAccountQRCodeExactExpiryAndAuthentication(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		ctx := context.Background()
		s := login(t, srv, "jumedia", "admin123")
		code := accountData(t, get(t, srv, accountQRPath+"auth_code"))["auth_code"].(string)
		const created int64 = 1700000000
		accountExec(t, srv, "UPDATE qrcodes SET ctime=? WHERE code=?", created, code)
		// Fixed clock verifies the exact boundary without a second-boundary race.
		if _, err := accountQRCode(ctx, srv.Store.DB, code, created+180); err != nil {
			t.Fatal(err)
		}
		if _, err := accountQRCode(ctx, srv.Store.DB, code, created+181); err == nil {
			t.Fatal("181-second QR should expire")
		}
		accountExec(t, srv, "UPDATE qrcodes SET ctime=? WHERE code=?", time.Now().Unix(), code)
		p := url.Values{"auth_code": {code}, "csrf": {s.CSRF}}
		accountExec(t, srv, "UPDATE users SET status=? WHERE mid=?", 1, s.MID)
		for _, route := range []string{accountH5Path + "scan", accountH5Path + "confirm", "/x/member/web/update", "/x/passport-login/web/password/update", "/login/exit/v2"} {
			accountFailure(t, post(t, srv, route, p, s), -403, "账号已被封禁")
		}
		accountExec(t, srv, "UPDATE users SET status=? WHERE mid=?", 0, s.MID)
		// Mock accepts direct confirmation without a preceding scan.
		assertCode(t, get(t, srv, accountH5Path+"confirm?"+p.Encode(), s), 0)
		assertCode(t, get(t, srv, accountQRPath+"poll?auth_code="+code), 0)
		// Mock's logout only deletes a cookie-selected session, even when an
		// access key and another session's valid CSRF authenticate the request.
		assertCode(t, post(t, srv, "/login/exit/v2", url.Values{"access_key": {s.Access}, "csrf": {s.CSRF}}), 0)
		if _, err := srv.Store.SessionByToken(ctx, s.Token); err != nil {
			t.Fatal(err)
		}
	})
}

func accountCount(t *testing.T, srv *Server, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := srv.Store.DB.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAccountTransactionRollback(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		ctx := context.Background()
		s := login(t, srv, "jumedia", "admin123")
		code := accountData(t, get(t, srv, accountQRPath+"auth_code"))["auth_code"].(string)
		users := accountCount(t, srv, "SELECT COUNT(*) FROM users")
		folders := accountCount(t, srv, "SELECT COUNT(*) FROM fav_folders")
		sessions := accountCount(t, srv, "SELECT COUNT(*) FROM sessions")
		globalID := accountCount(t, srv, "SELECT value FROM ids WHERE name=?", "global")
		accountExec(t, srv, "ALTER TABLE logs RENAME TO account_test_logs")
		for _, tc := range []struct {
			path    string
			p       url.Values
			session *store.Session
		}{
			{accountLoginPath, url.Values{"username": {"jumedia"}, "password": {"admin123"}}, nil},
			{accountRegisterPath, url.Values{"username": {"rollback_user"}, "uname": {"回滚用户"}, "password": {"secret123"}}, nil},
			{accountH5Path + "confirm", url.Values{"auth_code": {code}, "csrf": {s.CSRF}}, s},
		} {
			v, w := request(t, srv, "POST", tc.path, tc.p, tc.session)
			assertCode(t, v, -500)
			if len(w.Result().Cookies()) != 0 {
				t.Fatalf("failed %s issued cookies", tc.path)
			}
		}
		accountExec(t, srv, "ALTER TABLE account_test_logs RENAME TO logs")
		if accountCount(t, srv, "SELECT COUNT(*) FROM users") != users || accountCount(t, srv, "SELECT COUNT(*) FROM fav_folders") != folders || accountCount(t, srv, "SELECT COUNT(*) FROM sessions") != sessions || accountCount(t, srv, "SELECT value FROM ids WHERE name=?", "global") != globalID {
			t.Fatal("partial login/register writes survived rollback")
		}
		accountFailure(t, post(t, srv, accountQRPath+"poll", url.Values{"auth_code": {code}}), 86039, "二维码尚未确认")
		assertCode(t, post(t, srv, accountH5Path+"confirm", url.Values{"auth_code": {code}, "csrf": {s.CSRF}}, s), 0)
		other := login(t, srv, "jumedia", "admin123")
		// Force InsertSession's position uniqueness check to fail after refresh
		// has deleted the old session, proving both writes share a transaction.
		accountExec(t, srv, "UPDATE ids SET value=? WHERE name=?", other.Position-1, "sessions")
		assertCode(t, post(t, srv, accountRefreshPath, url.Values{"refresh_token": {s.Refresh}}), -500)
		if _, err := srv.Store.SessionByToken(ctx, s.Token); err != nil {
			t.Fatalf("old session lost: %v", err)
		}
		assertCode(t, post(t, srv, accountQRPath+"poll", url.Values{"auth_code": {code}}), -500)
		qs, err := srv.Store.ListQRCode(ctx, "WHERE code=?", code)
		if err != nil || len(qs) != 1 || qs[0].Status != "confirmed" {
			t.Fatalf("QR consumed on failure: %#v %v", qs, err)
		}
		accountExec(t, srv, "UPDATE ids SET value=? WHERE name=?", other.Position, "sessions")
		assertCode(t, post(t, srv, accountQRPath+"poll", url.Values{"auth_code": {code}}), 0)
		assertCode(t, post(t, srv, accountRefreshPath, url.Values{"refresh_token": {s.Refresh}}), 0)
	})
}

func TestAccountConcurrentConsumptionAndRegistration(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		s := login(t, srv, "jumedia", "admin123")
		code := accountData(t, get(t, srv, accountQRPath+"auth_code"))["auth_code"].(string)
		assertCode(t, post(t, srv, accountH5Path+"confirm", url.Values{"auth_code": {code}, "csrf": {s.CSRF}}, s), 0)
		for _, tc := range []struct {
			path    string
			params  []url.Values
			failure int
		}{
			{accountQRPath + "poll", []url.Values{{"auth_code": {code}}, {"auth_code": {code}}}, 86038},
			{accountRefreshPath, []url.Values{{"refresh_token": {s.Refresh}}, {"refresh_token": {s.Refresh}}}, -101},
			{accountRegisterPath, []url.Values{
				{"username": {"concurrent_a"}, "uname": {"同名用户"}, "password": {"secret123"}},
				{"username": {"concurrent_b"}, "uname": {"同名用户"}, "password": {"secret123"}},
			}, -400},
		} {
			var wg sync.WaitGroup
			start := make(chan struct{})
			results := make([]*httptest.ResponseRecorder, len(tc.params))
			for i, p := range tc.params {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r := httptest.NewRequest("POST", tc.path, strings.NewReader(p.Encode()))
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					w := httptest.NewRecorder()
					<-start
					srv.ServeHTTP(w, r)
					results[i] = w
				}()
			}
			close(start)
			wg.Wait()
			counts := map[int]int{}
			for _, w := range results {
				var v map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
					t.Fatal(err)
				}
				counts[int(v["code"].(float64))]++
			}
			if counts[0] != 1 || counts[tc.failure] != 1 {
				t.Fatalf("concurrent %s: %#v", tc.path, counts)
			}
		}
		if accountCount(t, srv, "SELECT COUNT(*) FROM users WHERE uname=?", "同名用户") != 1 {
			t.Fatal("duplicate nickname created")
		}
	})
}
