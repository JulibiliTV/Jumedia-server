package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"jumedia-server/internal/config"
)

func TestServerSettingsValidationAndIgnoredEmby(t *testing.T) {
	s := newTestServer(t)
	admin := login(t, s, "jumedia", "admin123")
	assertCode(t, get(t, s, "/x/admin/settings"), -101)
	assertCode(t, post(t, s, "/x/admin/settings/save", url.Values{"serverPublicOrigin": {"https://unauthorized.test"}}, admin), -111)
	save := func(p url.Values) map[string]any { return adminTestPost(t, s, admin, "settings/save", p) }
	for _, tc := range []struct{ key, value, message string }{
		{"serverPublicOrigin", "ftp://host", "公开地址需为 http(s)://主机[:端口]，不能带路径或参数"},
		{"serverPublicOrigin", "https://host/path", "公开地址需为 http(s)://主机[:端口]，不能带路径或参数"},
		{"serverPublicOrigin", "https://u:p@host", "公开地址需为 http(s)://主机[:端口]，不能带路径或参数"},
		{"serverPublicOrigin", "https://host?q=1", "公开地址需为 http(s)://主机[:端口]，不能带路径或参数"},
		{"serverPublicOrigin", "https://host#frag", "公开地址需为 http(s)://主机[:端口]，不能带路径或参数"},
		{"serverPublicOrigin", "https://host:99999", "公开地址需为 http(s)://主机[:端口]，不能带路径或参数"},
		{"serverFfmpeg", "relative/path", "ffmpeg 路径需为绝对路径或命令名"},
		{"serverFfmpeg", "/", "ffmpeg 路径需为绝对路径或命令名"},
		{"serverFfmpeg", "/bin/ff\nunsafe", "ffmpeg 路径需为绝对路径或命令名"},
		{"serverFfprobe", "command --flag", "ffprobe 路径需为绝对路径或命令名"},
		{"serverVideoEncoder", "injected", "不支持的视频编码器"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			before, err := s.Store.Settings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			adminTestError(t, save(url.Values{tc.key: {tc.value}, "siteName": {"should rollback"}, "serverEmbyPassword": {"never-save"}}), -400, tc.message)
			after, err := s.Store.Settings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("partial save")
			}
		})
	}
	viewer := login(t, s, "lumen", "demo123")
	assertCode(t, get(t, s, "/x/admin/settings", viewer), -403)
	assertCode(t, adminTestPost(t, s, viewer, "settings/save", url.Values{"serverEmbyUrl": {"bad"}}), -403)
	assertCode(t, save(url.Values{"serverPublicOrigin": {" HTTPS://EXAMPLE.ORG:443/ "}, "serverFfmpeg": {" /opt/ff mpeg "}, "serverFfprobe": {"ffprobe-7"}, "serverVideoEncoder": {"h264_nvenc"}}), 0)
	for _, p := range []url.Values{
		{"serverEmbyUrl": {"bad"}, "serverEmbyUsername": {strings.Repeat("😀", 65)}, "serverEmbyPassword": {"private-pass"}, "serverEmbyPasswordClear": {"true"}, "unknownSetting": {"ignored"}},
		{"serverEmbyUrl": {"ftp://host"}, "serverEmbyPassword": {""}, "serverEmbyPasswordClear": {"false"}},
		{"serverEmbyPasswordClear": {"1"}},
	} {
		before, err := s.Store.Settings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertCode(t, save(p), 0)
		body, w := request(t, s, http.MethodGet, "/x/admin/settings", nil, admin)
		assertCode(t, body, 0)
		data := adminTestData(t, body)
		for k := range data {
			if strings.HasPrefix(k, "serverEmby") {
				t.Fatalf("retired setting returned: %s", k)
			}
		}
		if data["serverPublicOrigin"] != "https://example.org" || data["serverFfmpeg"] != "/opt/ff mpeg" || data["serverFfprobe"] != "ffprobe-7" || data["serverVideoEncoder"] != "h264_nvenc" {
			t.Fatal(data)
		}
		if strings.Contains(w.Body.String(), "private-pass") {
			t.Fatal("password leaked")
		}
		after, err := s.Store.Settings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("retired/unknown parameters changed settings")
		}
	}
	// Even stray legacy rows must neither be exposed nor be writable.
	for _, key := range []string{"serverEmbyUrl", "serverEmbyUsername", "serverEmbyPassword", "serverEmbyPasswordClear", "serverEmbyHasPassword"} {
		if err := s.Store.SetSetting(t.Context(), key, "legacy"); err != nil {
			t.Fatal(err)
		}
	}
	assertCode(t, save(url.Values{"serverEmbyUrl": {"bad"}, "serverEmbyUsername": {"changed"}, "serverEmbyPassword": {"changed"}, "serverEmbyPasswordClear": {"true"}, "serverEmbyHasPassword": {"changed"}}), 0)
	data := adminTestData(t, get(t, s, "/x/admin/settings", admin))
	stored, err := s.Store.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range stored {
		if strings.HasPrefix(k, "serverEmby") {
			if v != "legacy" {
				t.Fatalf("legacy row changed: %s", k)
			}
			if _, ok := data[k]; ok {
				t.Fatalf("legacy row exposed: %s", k)
			}
		}
	}
}

func TestServerPublicOriginAndRestart(t *testing.T) {
	s := newTestServer(t)
	s.Config.PublicOrigin = "https://startup.test"
	admin := login(t, s, "jumedia", "admin123")
	check := func(want string) {
		t.Helper()
		data := adminTestData(t, get(t, s, "/x/passport-tv-login/qrcode/auth_code"))
		if !strings.HasPrefix(data["url"].(string), want+"/tv-login?") {
			t.Fatal(data)
		}
		data = adminTestData(t, get(t, s, "/x/jumedia/service"))
		if data["api"] != want || data["passport"] != want {
			t.Fatal(data)
		}
	}
	check("https://startup.test")
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverPublicOrigin": {"https://public.test/"}}), 0)
	check("https://public.test")
	restarted := New(s.Store, config.Config{DataDir: t.TempDir(), PublicOrigin: "https://startup.test"})
	t.Cleanup(restarted.CloseDataSources)
	if got := restarted.Origin(httptest.NewRequest("GET", "http://request.test/", nil)); got != "https://public.test" {
		t.Fatal(got)
	}
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverPublicOrigin": {""}}), 0)
	check("https://startup.test")
	s.Config.PublicOrigin = ""
	check("http://example.com")
}

func TestServerStartupPermissionsAndRedaction(t *testing.T) {
	t.Setenv("JUMEDIA_DATASOURCE_KEY", "do-not-leak-source-key")
	t.Setenv("JUMEDIA_FFMPEG", "")
	t.Setenv("JUMEDIA_FFPROBE", "")
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "")
	base := newTestServer(t)
	cfg := config.Config{Addr: ":18081", DBDriver: "postgres", DBDSN: "postgres://owner:dsn-secret@db.test/jumedia?password=query-secret", DataDir: "/data", WebDir: "/web", StaticDir: "/static", PublicOrigin: "https://boot.test"}
	s := New(base.Store, cfg)
	t.Cleanup(s.CloseDataSources)
	assertCode(t, get(t, s, "/x/admin/server"), -101)
	viewer := login(t, s, "lumen", "demo123")
	assertCode(t, get(t, s, "/x/admin/server", viewer), -403)
	admin := login(t, s, "jumedia", "admin123")
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverEmbyPassword": {"runtime-secret"}, "serverPublicOrigin": {"https://changed.test"}, "serverFfmpeg": {"custom-ffmpeg"}}), 0)
	data, w := request(t, s, http.MethodGet, "/x/admin/server", nil, admin)
	v := adminTestData(t, data)
	startup := v["startup"].(map[string]any)
	if !reflect.DeepEqual(startup, map[string]any{"publicOrigin": cfg.PublicOrigin, "ffmpeg": "ffmpeg", "ffprobe": "ffprobe", "videoEncoder": "auto"}) {
		t.Fatal(startup)
	}
	boot := v["boot"].([]any)
	for i, key := range []string{"JUMEDIA_ADDR", "JUMEDIA_DB_DRIVER", "JUMEDIA_DB_DSN", "JUMEDIA_DATA_DIR", "JUMEDIA_WEB_DIR", "JUMEDIA_STATIC_DIR", "JUMEDIA_DATASOURCE_KEY"} {
		if boot[i].(map[string]any)["key"] != key {
			t.Fatal(boot)
		}
	}
	if boot[6].(map[string]any)["value"] != "已设置" {
		t.Fatal(boot)
	}
	responses := w.Body.String()
	for _, path := range []string{"/x/admin/settings", "/x/admin/logs", "/x/jumedia/site", "/x/jumedia/service"} {
		raw, _ := json.Marshal(get(t, s, path, admin))
		responses += string(raw)
	}
	for _, secret := range []string{"do-not-leak-source-key", "dsn-secret", "query-secret", "boot-secret", "runtime-secret"} {
		if strings.Contains(responses, secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
}

func TestServerHardwareEncoderSettings(t *testing.T) {
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "")
	s := newTestServer(t)
	if s.serverSnapshot().encoder != "auto" {
		t.Fatal("automatic encoding must be the default")
	}
	admin := login(t, s, "jumedia", "admin123")
	for _, encoder := range []string{"auto", "h264_vaapi", "libx264"} {
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverVideoEncoder": {encoder}}), 0)
		if s.serverSnapshot().encoder != encoder {
			t.Fatalf("encoder %s not applied", encoder)
		}
	}
	assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serverVideoEncoder": {""}}), 0)
	if s.serverSnapshot().encoder != "auto" {
		t.Fatal("clearing override must restore automatic default")
	}
}

func TestServerOriginNormalization(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://EXAMPLE.test:0443/", "https://example.test"},
		{`https:\\EXAMPLE.test\`, "https://example.test"},
		{"http://127.1/", "http://127.0.0.1"},
		{"http://0x7f000001/", "http://127.0.0.1"},
		{"http://%65xample.test/", "http://example.test"},
		{"http://[2001:0db8::1]:80/", "http://[2001:db8::1]"},
		{"https://example.test/a/../", "https://example.test"},
		{"https://example.test/%2e", "https://example.test"},
		{"https://example.test/?#", "https://example.test"},
		{"https://BÜCHER.example/", "https://xn--bcher-kva.example"},
	} {
		got, err := serverOrigin(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%q => %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}
