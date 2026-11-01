package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	seedfixture "jumedia-server/internal/seed/testdata"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"jumedia-server/internal/config"
	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return testServer(t, testutil.Open(t, "sqlite"))
}
func testServer(t *testing.T, s *store.Store) *Server {
	t.Helper()
	if err := seedfixture.Seed(context.Background(), s, testdata.Catalog()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := seed.Admin(context.Background(), s, "jumedia", "admin123"); err != nil {
		t.Fatal(err)
	}
	srv := New(s, config.Config{DataDir: dir})
	t.Cleanup(srv.CloseDataSources)
	return srv
}
func testServers(t *testing.T, fn func(*testing.T, *Server)) {
	t.Helper()
	testutil.Drivers(t, func(t *testing.T, s *store.Store) { fn(t, testServer(t, s)) })
}

// login creates a real persisted session after checking bcrypt. T1 has no login
// route: the helper intentionally does not depend on the future T2 implementation.
func login(t *testing.T, srv *Server, username, password string) *store.Session {
	t.Helper()
	ctx := context.Background()
	u, err := srv.Store.UserByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	if err = bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		t.Fatal(err)
	}
	token := func() string {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b)
	}
	s := &store.Session{Token: token(), CSRF: token(), Access: token(), Refresh: token(), MID: u.MID, Ctime: time.Now().Unix()}
	if err = store.InsertSession(ctx, srv.Store.DB, s); err != nil {
		t.Fatal(err)
	}
	return s
}
func request(t *testing.T, srv *Server, method, path string, params url.Values, session *store.Session) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	var body *strings.Reader
	if params == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(params.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if params != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if session != nil {
		req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var v map[string]any
	if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&v); err != nil {
		t.Fatalf("%s: %s (%v)", path, w.Body.String(), err)
	}
	if w.Code != 200 {
		t.Fatalf("HTTP %d", w.Code)
	}
	return v, w
}
func get(t *testing.T, srv *Server, path string, session ...*store.Session) map[string]any {
	t.Helper()
	var s *store.Session
	if len(session) > 0 {
		s = session[0]
	}
	v, _ := request(t, srv, http.MethodGet, path, nil, s)
	return v
}
func post(t *testing.T, srv *Server, path string, params url.Values, session ...*store.Session) map[string]any {
	t.Helper()
	var s *store.Session
	if len(session) > 0 {
		s = session[0]
	}
	v, _ := request(t, srv, http.MethodPost, path, params, s)
	return v
}
func assertCode(t *testing.T, v map[string]any, want int) {
	t.Helper()
	if v["code"] != float64(want) {
		t.Fatalf("code: %+v", v)
	}
}
