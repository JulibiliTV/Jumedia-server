package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jumedia-server/internal/config"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func TestServerDemoDataSourceSettings(t *testing.T) {
	serverRequireListener(t)
	type loginRecord struct{ Username, Pw string }
	var mu sync.Mutex
	var logins []loginRecord
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emby/Users/AuthenticateByName" {
			var p loginRecord
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
			}
			mu.Lock()
			logins = append(logins, p)
			n := len(logins)
			mu.Unlock()
			fmt.Fprintf(w, `{"AccessToken":"token-%d"}`, n)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/emby/") || r.Header.Get("X-Emby-Token") == "" {
			t.Error("missing prefix or token")
		}
		fmt.Fprint(w, "media")
	}))
	defer upstream.Close()
	base := newTestServer(t)
	for k, v := range map[string]string{"serverEmbyUrl": "http://unused.invalid", "serverEmbyUsername": "legacy-user", "serverEmbyPassword": "legacy-password"} {
		if err := base.Store.SetSetting(t.Context(), k, v); err != nil {
			t.Fatal(err)
		}
	}
	s := New(base.Store, config.Config{DataDir: t.TempDir()})
	t.Cleanup(s.CloseDataSources)
	if err := s.InitDataSources(t.Context()); err != nil {
		t.Fatal(err)
	}
	encrypted, err := s.encryptPassword("boot-password")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"_demo_emby_url": upstream.URL, "_demo_emby_username": "boot-user", "_demo_emby_password": encrypted} {
		if err = s.Store.SetSetting(t.Context(), k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err = seed.Seed(t.Context(), s.Store); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "jumedia", "admin123")
	sources, err := s.Store.ListDataSource(t.Context())
	if err != nil || len(sources) == 0 {
		t.Fatal(sources, err)
	}
	var ds *store.DataSource
	for i := range sources {
		if sources[i].MID == admin.MID {
			ds = &sources[i]
			break
		}
	}
	if ds == nil {
		t.Fatal("official user source missing")
	}
	if err = s.Store.SetSetting(t.Context(), "dataSourcePrivateNetwork", true); err != nil {
		t.Fatal(err)
	}
	sourceClient := func() *emby.Client {
		t.Helper()
		client, err := s.sourceClient(t.Context(), ds)
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	stream := func() {
		t.Helper()
		response, err := sourceClient().RawStream(t.Context(), "demo", "src", "mkv", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	stream()
	stream()
	before := sourceClient()
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverEmbyUrl": {upstream.URL + "/emby/"}, "serverEmbyUsername": {"runtime-user"}, "serverEmbyPassword": {"runtime-password"}}), 0)
	if sourceClient() != before {
		t.Fatal("retired settings rebuilt the client")
	}
	stream()
	stream()
	before = sourceClient()
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"siteName": {"no rebuild"}, "serverEmbyPassword": {""}}), 0)
	if sourceClient() != before {
		t.Fatal("unrelated save discarded token")
	}
	stream()
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverEmbyUrl": {""}, "serverEmbyUsername": {""}, "serverEmbyPasswordClear": {"true"}}), 0)
	stream()
	mu.Lock()
	got := append([]loginRecord(nil), logins...)
	mu.Unlock()
	want := []loginRecord{{"boot-user", "boot-password"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("logins: %#v", got)
	}
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverPublicOrigin": {"https://reset.test"}, "serverFfmpeg": {"reset-tool"}, "serverEmbyUsername": {"reset-user"}}), 0)
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"reset": {"true"}}), 0)
	cfg := s.serverSnapshot()
	if cfg.publicOrigin != "" || cfg.ffmpeg != s.runtime.startup.ffmpeg {
		t.Fatal("reset did not restore boot configuration")
	}
	if _, err := store.DataSourceByID(t.Context(), s.Store.DB, ds.ID); err == nil {
		t.Fatal("reset retained source")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logins) != 1 {
		t.Fatalf("reset discarded token: %#v", logins)
	}
}

func TestServerSaveRollbackAndConcurrentReads(t *testing.T) {
	t.Setenv("JUMEDIA_FFMPEG", "tool")
	t.Setenv("JUMEDIA_FFPROBE", "tool")
	s := newTestServer(t)
	admin := login(t, s, "jumedia", "admin123")
	before := s.serverSnapshot()
	liveExec(t, s, "ALTER TABLE logs RENAME TO t23_logs")
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverPublicOrigin": {"https://rollback.test"}, "serverEmbyUsername": {"rollback-user"}}), -500)
	after := s.serverSnapshot()
	if before != after {
		t.Fatal("rolled back settings were published")
	}
	settings, err := s.Store.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if settings["serverPublicOrigin"] != "" || settings["serverEmbyUsername"] != nil {
		t.Fatal("partial transaction")
	}
	liveExec(t, s, "ALTER TABLE t23_logs RENAME TO logs")
	var wg sync.WaitGroup
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				cfg := s.serverSnapshot()
				if cfg.ffmpeg != cfg.ffprobe {
					t.Error("torn configuration")
				}
				_ = s.Origin(httptest.NewRequest("GET", "http://request.test/", nil))
			}
		}()
	}
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			encoder := []string{"libx264", "h264_nvenc"}[n%2]
			assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverVideoEncoder": {encoder}, "serverFfmpeg": {encoder}, "serverFfprobe": {encoder}, "serverPublicOrigin": {"https://concurrent.test"}}), 0)
		}(n)
	}
	wg.Wait()
	cfg := s.serverSnapshot()
	stored, err := s.Store.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.encoder != stored["serverVideoEncoder"] || cfg.ffmpeg != stored["serverFfmpeg"] || cfg.ffprobe != stored["serverFfprobe"] {
		t.Fatal("runtime does not match committed settings")
	}
}

func TestServerTranscodeKeepsRunningSnapshot(t *testing.T) {
	serverRequireListener(t)
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	args := filepath.Join(dir, "args")
	t.Setenv("JUMEDIA_T23_STARTED", started)
	t.Setenv("JUMEDIA_T23_RELEASE", release)
	t.Setenv("JUMEDIA_T23_ARGS", args)
	oldProbe := cacheScript(t, `touch "$JUMEDIA_T23_STARTED"
while [ ! -f "$JUMEDIA_T23_RELEASE" ]; do sleep 0.02; done
printf '%s\n' '{"streams":[{"index":0,"codec_type":"video","height":720}],"format":{"duration":"10"}}'`)
	oldFFmpeg := cacheScript(t, `printf 'old\n%s\n' "$@" >> "$JUMEDIA_T23_ARGS"
echo old-finished >&2
exit 1`)
	newProbe := cacheScript(t, `echo new-probe >> "$JUMEDIA_T23_ARGS"
printf '%s\n' '{"streams":[{"index":0,"codec_type":"video","height":720}],"format":{"duration":"10"}}'`)
	newFFmpeg := cacheScript(t, `printf 'new\n%s\n' "$@" >> "$JUMEDIA_T23_ARGS"
echo new-finished >&2
exit 1`)
	t.Setenv("JUMEDIA_FFMPEG", oldFFmpeg)
	t.Setenv("JUMEDIA_FFPROBE", oldProbe)
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "libx264")
	s := newTestServer(t)
	admin, p, _ := cacheSetup(t, s)
	job := cacheChoose(t, s, admin, p, false)
	if err := s.InitLiveCaches(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.CloseLiveCaches)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probe not started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverFfmpeg": {newFFmpeg}, "serverFfprobe": {newProbe}, "serverVideoEncoder": {"h264_nvenc"}}), 0)
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	completed := cacheWait(t, s, job.Key, "failed")
	if completed.Message != "old-finished" {
		t.Fatal(completed.Message)
	}
	raw, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "libx264") || strings.Contains(string(raw), "h264_nvenc") || strings.Contains(string(raw), "new-probe") {
		t.Fatal(string(raw))
	}
	assertCode(t, adminTestPost(t, s, admin, "live/cache/retry", url.Values{"key": {job.Key}}), 0)
	completed = cacheWait(t, s, job.Key, "failed")
	if completed.Message != "new-finished" {
		t.Fatal(completed.Message)
	}
	raw, err = os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "new-probe") || !strings.Contains(string(raw), "h264_nvenc") {
		t.Fatal(string(raw))
	}
}

func TestServerDSNRedaction(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"postgres://user:secret@host/db", "postgres://user:•••@host/db"},
		{"host=localhost user=owner password='secret with spaces' dbname=db", "host=localhost user=owner password='•••' dbname=db"},
		{`host=localhost password=secret\ with\ spaces sslpassword='tls\'secret' dbname=db`, `host=localhost password='•••' sslpassword='•••' dbname=db`},
		{"host=localhost password\v=secret dbname=db", "host=localhost password\v='•••' dbname=db"},
		{"host=localhost password=prefix\\\nsecret dbname=db", "host=localhost password='•••' dbname=db"},
		{"host=localhost password='prefix\\\nsecret' dbname=db", "host=localhost password='•••' dbname=db"},
		{"data/jumedia.db", "data/jumedia.db"},
	} {
		if got := serverRedactDSN(tc.input); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func serverRequireListener(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local listening is unavailable: %v", err)
	}
	listener.Close()
}

func TestServerRuntimeValuesAndReset(t *testing.T) {
	t.Setenv("JUMEDIA_FFMPEG", "startup-ffmpeg")
	t.Setenv("JUMEDIA_FFPROBE", "startup-ffprobe")
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "h264_qsv")
	base := newTestServer(t)
	boot := config.Config{DataDir: t.TempDir(), PublicOrigin: "https://startup.test"}
	s := New(base.Store, boot)
	t.Cleanup(s.CloseDataSources)
	admin := login(t, s, "jumedia", "admin123")
	startup := s.serverSnapshot()
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverPublicOrigin": {"https://override.test"}, "serverEmbyUrl": {"https://override-emby.test"}, "serverEmbyUsername": {"override-user"}, "serverEmbyPassword": {"override-password"}, "serverFfmpeg": {"override-ffmpeg"}, "serverFfprobe": {"override-ffprobe"}, "serverVideoEncoder": {"h264_nvenc"}}), 0)
	changed := s.serverSnapshot()
	if changed.ffmpeg != "override-ffmpeg" || changed.ffprobe != "override-ffprobe" || changed.encoder != "h264_nvenc" || changed.publicOrigin != "https://override.test" {
		t.Fatal("runtime overrides not applied")
	}
	if startup.ffmpeg != "startup-ffmpeg" || startup.encoder != "h264_qsv" {
		t.Fatal("old snapshot mutated")
	}
	restarted := New(base.Store, boot)
	t.Cleanup(restarted.CloseDataSources)
	restored := restarted.serverSnapshot()
	if restored != changed {
		t.Fatal("overrides not restored on restart")
	}
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverPublicOrigin": {""}, "serverEmbyUrl": {""}, "serverEmbyUsername": {""}, "serverEmbyPasswordClear": {"true"}, "serverFfmpeg": {""}, "serverFfprobe": {""}, "serverVideoEncoder": {""}}), 0)
	fallback := s.serverSnapshot()
	if fallback != startup {
		t.Fatal("empty fields did not restore startup settings")
	}
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverPublicOrigin": {"https://reset.test"}, "serverFfmpeg": {"reset-tool"}}), 0)
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"reset": {"true"}}), 0)
	fallback = s.serverSnapshot()
	if fallback != startup {
		t.Fatal("reset did not restore startup")
	}
}
