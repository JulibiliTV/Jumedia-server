package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"jumedia-server/internal/config"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func initialServer(t *testing.T) *Server {
	t.Helper()
	st := testutil.Open(t, "sqlite")
	if err := seed.Seed(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	if err := seed.Admin(t.Context(), st, "owner", "secret123"); err != nil {
		t.Fatal(err)
	}
	s := New(st, config.Config{DataDir: t.TempDir()})
	t.Cleanup(s.CloseDataSources)
	return s
}
func demoUpload(t *testing.T, s *Server, session *store.Session, csrf string, raw []byte) map[string]any {
	t.Helper()
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	if raw != nil {
		f, err := m.CreateFormFile("file", "demo.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.WriteField("csrf", csrf); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/x/admin/demo/import", &body)
	r.Header.Set("Content-Type", m.FormDataContentType())
	if session != nil {
		r.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return result
}
func TestAdminDemoImportContract(t *testing.T) {
	s := initialServer(t)
	ctx := t.Context()
	session := login(t, s, "owner", "secret123")
	raw, err := os.ReadFile("../seed/testdata/demo.json")
	if err != nil {
		t.Fatal(err)
	}
	assertCode(t, demoUpload(t, s, nil, "", raw), -101)
	assertCode(t, demoUpload(t, s, session, "wrong", raw), -111)
	missing := demoUpload(t, s, session, session.CSRF, nil)
	assertCode(t, missing, -400)
	if missing["message"] != "请选择演示数据包文件" {
		t.Fatal(missing)
	}
	for _, body := range [][]byte{[]byte(`{`), []byte(`{}`), []byte(`{"format":"jumedia-demo","version":2}`), []byte(`{"format":"jumedia-demo","version":1,"catalog":{"series":[],"movies":[]}}`)} {
		assertCode(t, demoUpload(t, s, session, session.CSRF, body), -400)
	}
	assertCode(t, get(t, s, "/x/admin/demo/import?csrf="+session.CSRF, session), -400)
	assertCode(t, demoUpload(t, s, session, session.CSRF, raw), 0)
	settings := adminTestData(t, get(t, s, "/x/admin/settings", session))
	for key := range settings {
		if strings.HasPrefix(key, "_demo_") {
			t.Fatal("private demo settings leaked")
		}
	}
	ciphertext, err := s.Store.Setting(ctx, "_demo_emby_password")
	if err != nil || ciphertext == "fixture-password" || ciphertext == "" {
		t.Fatal("password not encrypted", err)
	}
	plain, err := s.decryptPassword(ciphertext.(string))
	if err != nil || plain != "fixture-password" {
		t.Fatal("password round trip", err)
	}
	again := demoUpload(t, s, session, session.CSRF, raw)
	assertCode(t, again, -400)
	if again["message"] != "请先重置为初始状态再导入演示数据" {
		t.Fatal(again)
	}
	var ordinary string
	if err = s.Store.DB.QueryRowContext(ctx, "SELECT username FROM users WHERE role=? ORDER BY mid LIMIT 1", "user").Scan(&ordinary); err != nil {
		t.Fatal(err)
	}
	user := login(t, s, ordinary, "demo123")
	assertCode(t, demoUpload(t, s, user, user.CSRF, raw), -403)
	if err = s.Store.SetSetting(ctx, liveChatSecretKey, "keep-chat-key"); err != nil {
		t.Fatal(err)
	}
	assertCode(t, post(t, s, "/x/admin/settings/save", url.Values{"reset": {"true"}, "csrf": {session.CSRF}}, session), 0)
	assertCode(t, get(t, s, "/x/admin/settings", session), -101)
	session = login(t, s, "owner", "secret123")
	if n := adminTestCount(t, s, "SELECT COUNT(*) FROM users"); n != 1 {
		t.Fatal(n)
	}
	for _, table := range []string{"archives", "seasons", "rooms", "data_sources", "live_caches"} {
		if n := adminTestCount(t, s, "SELECT COUNT(*) FROM "+table); n != 0 {
			t.Fatal(table, n)
		}
	}
	secret, err := s.Store.Setting(ctx, liveChatSecretKey)
	if err != nil || secret != "keep-chat-key" {
		t.Fatal(secret, err)
	}
	if _, err = s.embyClient().RawStream(ctx, "demo", "src", "mp4", nil); err == nil {
		t.Fatal("reset still has media")
	}
	assertCode(t, demoUpload(t, s, session, session.CSRF, raw), 0)
}
func TestDemoMediaPersistedConnection(t *testing.T) {
	s := initialServer(t)
	raw, err := os.ReadFile("../seed/testdata/demo.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := seed.ParsePackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ImportDemo(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.Store, s.Config)
	t.Cleanup(restarted.CloseDataSources)
	if err = restarted.InitDemoMedia(t.Context()); err != nil {
		t.Fatal(err)
	}
	connection, err := restarted.demoConnection(t.Context())
	if err != nil || connection.URL != "https://emby.example.test" || connection.Username != "demo" || connection.Password != "fixture-password" {
		t.Fatal("persisted connection not loaded", err)
	}
	if restarted.embyClient() == nil {
		t.Fatal("missing media client")
	}

}
