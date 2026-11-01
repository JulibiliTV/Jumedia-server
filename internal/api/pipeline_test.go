package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"jumedia-server/internal/config"
	"jumedia-server/internal/store"
)

func TestCookies(t *testing.T) {
	s := &store.Session{Token: "token+encoded", CSRF: "csrf", MID: 12}
	w := httptest.NewRecorder()
	SetCookies(w, s)
	cs := w.Result().Cookies()
	if len(cs) != 3 {
		t.Fatal(cs)
	}
	for _, c := range cs {
		if c.Path != "/" || c.MaxAge != SessionMaxAge || c.HttpOnly != (c.Name == "SESSDATA") {
			t.Fatal(c)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Cookie", "SESSDATA=hello%2Bworld")
	if CookieValue(r, "SESSDATA") != "hello+world" {
		t.Fatal("decode")
	}
	w = httptest.NewRecorder()
	ClearCookies(w)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge != -1 || c.Value != "" || c.Path != "/" {
			t.Fatal(c)
		}
	}
}
func TestParamsPermissionsAndWrappers(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		s.handle("/test/params", func(c *Ctx) (any, error) { return c.P, nil })
		v := post(t, s, "/test/params?a=query&b=1&a=last", url.Values{"a": {"body", "bodylast"}})
		assertCode(t, v, 0)
		if v["data"].(map[string]any)["a"] != "bodylast" {
			t.Fatal(v)
		}
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		if err := mw.WriteField("a", "multipart"); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/test/params?a=query", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		var decoded map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["data"].(map[string]any)["a"] != "multipart" {
			t.Fatal(decoded)
		}
		s.handle("/test/write", func(c *Ctx) (any, error) {
			u, err := c.CSRF()
			if err != nil {
				return nil, err
			}
			return u.MID, nil
		})
		s.handle("/test/admin", func(c *Ctx) (any, error) {
			u, err := c.Admin()
			if err != nil {
				return nil, err
			}
			return u.MID, nil
		})
		assertCode(t, post(t, s, "/test/write", nil), -101)
		session := login(t, s, "jumedia", "admin123")
		assertCode(t, post(t, s, "/test/write", nil, session), -111)
		assertCode(t, post(t, s, "/test/write", url.Values{"biliCSRF": {session.CSRF}}, session), 0)
		assertCode(t, get(t, s, "/test/admin", session), 0)
		assertCode(t, post(t, s, "/test/admin", nil, session), -111)
		user := login(t, s, "lumen", "demo123")
		v = get(t, s, "/test/admin", user)
		assertCode(t, v, -403)
		if v["message"] != "需要管理员权限" {
			t.Fatal(v)
		}
		if _, err := s.Store.DB.ExecContext(context.Background(), "UPDATE users SET status=1 WHERE mid=?", user.MID); err != nil {
			t.Fatal(err)
		}
		v = post(t, s, "/test/write", url.Values{"csrf": {user.CSRF}}, user)
		assertCode(t, v, -403)
		if v["message"] != "账号已被封禁" {
			t.Fatal(v)
		}
		// Invalid Cookie must still permit an access_key session, as in the mock.
		req = httptest.NewRequest("GET", "/x/web-interface/nav?access_key="+session.Access, nil)
		req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: "unknown"})
		w = httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		assertCode(t, decoded, 0)
		for _, tc := range []struct {
			path   string
			out    any
			want   string
			status int
			ct     string
		}{{"result", Result(3), `{"code":0,"message":"success","result":3}`, 200, "application/json; charset=utf-8"}, {"root", Root(map[string]any{"ok": true}), `{"ok":true}`, 200, "application/json; charset=utf-8"}, {"raw", Raw([]int{1}), `[1]`, 200, "application/json; charset=utf-8"}, {"text", Text("missing", 404, "text/plain"), "missing", 404, "text/plain"}, {"bytes", Bytes([]byte{1, 2}, "application/octet-stream"), string([]byte{1, 2}), 200, "application/octet-stream"}} {
			s.handle("/test/"+tc.path, func(c *Ctx) (any, error) { return tc.out, nil })
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", "/test/"+tc.path, nil))
			if w.Code != tc.status || w.Body.String() != tc.want || w.Header().Get("Content-Type") != tc.ct {
				t.Fatalf("%s: %d %q", tc.path, w.Code, w.Body.String())
			}
		}
	})
}
func TestOriginCORSAndStatic(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest("GET", "http://internal/x", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "media.example, proxy")
	if s.Origin(r) != "https://media.example" {
		t.Fatal(s.Origin(r))
	}
	s.Config.PublicOrigin = "https://public.example/"
	if s.Origin(r) != "https://public.example" {
		t.Fatal(s.Origin(r))
	}
	for _, origin := range []string{"http://localhost:5173", "http://localhost.evil:5173", "https://evil.example", "http://user@localhost:5173"} {
		r = httptest.NewRequest("OPTIONS", "/x/web-interface/nav", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := ""
		if origin == "http://localhost:5173" {
			want = origin
			if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatal(w.Header())
			}
		}
		if w.Header().Get("Access-Control-Allow-Origin") != want {
			t.Fatal(w.Header())
		}
	}
	dir := t.TempDir()
	for _, p := range []string{"static/mock", "data/uploads", "web"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []struct{ p, v string }{{"static/mock/a.mp4", "media"}, {"data/uploads/a.txt", "upload"}, {"web/index.html", "<h1>SPA</h1>"}, {"web/app.js", "js"}, {"secret.txt", "secret"}} {
		if err := os.WriteFile(filepath.Join(dir, f.p), []byte(f.v), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "secret.txt"), filepath.Join(dir, "web", "escape.txt")); err != nil {
		t.Fatal(err)
	}
	s = New(s.Store, config.Config{DataDir: filepath.Join(dir, "data"), StaticDir: filepath.Join(dir, "static"), WebDir: filepath.Join(dir, "web")})
	for _, tc := range []struct {
		path, body string
		status     int
	}{{"/mock/a.mp4", "media", 200}, {"/upload/a.txt", "upload", 200}, {"/video/BV1", "<h1>SPA</h1>", 200}, {"/app.js", "js", 200}, {"/mock/missing", "404 page not found\n", 404}, {"/escape.txt", "404 page not found\n", 404}} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || w.Body.String() != tc.body {
			t.Fatalf("%s: %d %q", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestSessionSelectionMatchesMockInsertionOrder(t *testing.T) {
	s := newTestServer(t)
	older := login(t, s, "jumedia", "admin123")
	newer := login(t, s, "lumen", "demo123")
	// Mock uses Array.find(token match OR access match), so the first persisted
	// session wins when both credentials identify different users.
	req := httptest.NewRequest("GET", "/x/web-interface/nav?access_key="+older.Access, nil)
	req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: newer.Token})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["data"].(map[string]any)["mid"] != float64(older.MID) {
		t.Fatal(v)
	}
}
