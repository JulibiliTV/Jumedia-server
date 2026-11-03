package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jumedia-server/internal/config"
	"jumedia-server/internal/store"
)

// This acceptance test uses a real HTTP listener and reopens the on-disk
// database. It never connects to a deployed service or uses real user data.
func TestModerationHTTPPersistenceAcceptance(t *testing.T) {
	ctx := t.Context()
	dsn := filepath.Join(t.TempDir(), "acceptance.db")
	db, err := store.Open(ctx, "sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t, db)
	server := httptest.NewServer(s)
	t.Cleanup(func() { server.Close(); s.CloseDataSources(); _ = db.Close() })
	client := &http.Client{Timeout: 10 * time.Second}
	call := func(path string, values url.Values, session *store.Session, code int) map[string]any {
		t.Helper()
		method, body := http.MethodGet, ""
		if values != nil {
			method = http.MethodPost
			if session != nil {
				values.Set("csrf", session.CSRF)
			}
			body = values.Encode()
		}
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if values != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if session != nil {
			req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK || result["code"] != float64(code) {
			t.Fatalf("%s: HTTP=%d code=%v, want %d", path, res.StatusCode, result["code"], code)
		}
		data, _ := result["data"].(map[string]any)
		return data
	}
	admin := login(t, s, "jumedia", "admin123")
	viewer := login(t, s, "lumen", "demo123")
	author := login(t, s, "oolong", "demo123")
	oid := fmt.Sprint(communityArchives(t, s)[0].AID)
	communityExec(t, s, "DELETE FROM replies WHERE oid=?", ParseID(oid))
	add := func(session *store.Session, message string) string {
		return fmt.Sprint(call("/x/v2/reply/add", url.Values{"type": {"1"}, "oid": {oid}, "message": {message}}, session, 0)["rpid"])
	}
	removed := add(author, "acceptance remove")
	dismissed := add(author, "acceptance dismiss")
	add(admin, "acceptance unaffected")
	for _, id := range []string{removed, dismissed, removed} {
		call("/x/v2/reply/report", url.Values{"type": {"1"}, "oid": {oid}, "rpid": {id}, "reason": {"7"}, "add_blacklist": {"false"}}, viewer, 0)
	}
	call("/x/relation/modify", url.Values{"fid": {fmt.Sprint(author.MID)}, "act": {"5"}}, viewer, 0)
	call("/x/admin/replies?reported=1", nil, viewer, -403)
	queue := func(want int) {
		t.Helper()
		rows := call("/x/admin/replies?reported=1", nil, admin, 0)["list"].([]any)
		if len(rows) != want {
			t.Fatalf("pending reports=%d, want %d", len(rows), want)
		}
		for _, row := range rows {
			r := row.(map[string]any)
			if r["report_count"] != float64(1) {
				t.Fatal("duplicate report increased count")
			}
		}
	}
	visible := func(session *store.Session, want int) {
		t.Helper()
		rows := call("/x/v2/reply?oid="+oid+"&ps=20", nil, session, 0)["replies"].([]any)
		if len(rows) != want {
			t.Fatalf("visible comments=%d, want %d", len(rows), want)
		}
	}
	queue(2)
	visible(viewer, 1)
	visible(author, 3)
	t.Log("PASS: HTTP report deduplication, queue, access control and viewer isolation")
	server.Close()
	s.CloseDataSources()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, "sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	s = New(db, config.Config{DataDir: t.TempDir()})
	server = httptest.NewServer(s)
	queue(2)
	visible(viewer, 1)
	visible(author, 3)
	if call("/x/relation/blacks", nil, viewer, 0)["total"] != float64(1) {
		t.Fatal("block lost after reopening database")
	}
	t.Log("PASS: reports, blocks and sessions survive server/database restart")
	call("/x/admin/reply/report/dismiss", url.Values{"rpid": {dismissed}}, admin, 0)
	queue(1)
	visible(viewer, 1)
	visible(author, 3)
	call("/x/admin/reply/delete", url.Values{"rpid": {removed}}, admin, 0)
	queue(0)
	visible(viewer, 1)
	visible(author, 2)
	for id, want := range map[string]string{removed: "removed", dismissed: "dismissed"} {
		var status string
		if err := db.DB.QueryRowContext(ctx, "SELECT status FROM comment_reports WHERE rpid=?", ParseID(id)).Scan(&status); err != nil || status != want {
			t.Fatalf("resolution=%s, want %s: %v", status, want, err)
		}
	}
	call("/x/relation/modify", url.Values{"fid": {fmt.Sprint(author.MID)}, "act": {"6"}}, viewer, 0)
	visible(viewer, 1) // Dismissal keeps the reporting user's personal hide.
	add(author, "acceptance after unblock")
	visible(viewer, 2)
	t.Log("PASS: dismiss/delete resolution and unblocking preserve personal report choice")
	communityExec(t, s, "UPDATE settings SET value=? WHERE key='sensitiveWords'", `"AcceptanceForbidden"`)
	call("/x/v2/reply/add", url.Values{"type": {"1"}, "oid": {oid}, "message": {"acceptanceforbidden"}}, viewer, 12016)
	visible(viewer, 2)
	add(viewer, "acceptance normal comment")
	call("/x/admin/user/update", url.Values{"mid": {fmt.Sprint(author.MID)}, "status": {"1"}}, admin, 0)
	call("/x/v2/reply/report", url.Values{"type": {"1"}, "oid": {oid}, "rpid": {dismissed}, "reason": {"7"}}, author, -101)
	call(accountLoginPath, url.Values{"username": {"oolong"}, "password": {"demo123"}}, nil, -403)
	logs := call("/x/admin/logs?ps=50", nil, admin, 0)["list"].([]any)
	for _, action := range []string{"驳回评论举报", "删除评论", "封禁用户"} {
		found := false
		for _, row := range logs {
			if row.(map[string]any)["action"] == action {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing administrator audit action: %s", action)
		}
	}
	t.Log("PASS: text filtering, normal posting, session revocation, login denial and audit trail")
}
