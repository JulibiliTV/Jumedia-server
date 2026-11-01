package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestServiceDefaultsAndOrigin(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		assertCode(t, get(t, s, "/x/admin/settings"), -101)
		settings := adminTestData(t, get(t, s, "/x/admin/settings", admin))
		for _, k := range []string{"serviceApi", "serviceLive", "serviceApp", "servicePassport"} {
			if settings[k] != "" {
				t.Errorf("%s: %#v", k, settings[k])
			}
		}
		if settings["serviceCdns"] != "[]" || settings["serviceMtime"] != float64(0) {
			t.Fatal(settings)
		}
		for _, tc := range []struct{ public, proto, host, want string }{
			{"", "", "", "http://example.com"},
			{"", "https", "proxy.test, ignored.test", "https://proxy.test"},
			{"https://public.test/base/", "http", "ignored.test", "https://public.test/base"},
		} {
			s.Config.PublicOrigin = tc.public
			req := httptest.NewRequest("GET", "/x/jumedia/service", nil)
			req.Header.Set("X-Forwarded-Proto", tc.proto)
			req.Header.Set("X-Forwarded-Host", tc.host)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			var result map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err, w.Body.String())
			}
			data := adminTestData(t, result)
			want := map[string]any{"api": tc.want, "live": tc.want, "app": tc.want, "passport": tc.want, "cdn": []any{}, "mtime": float64(0)}
			if !reflect.DeepEqual(data, want) {
				t.Fatalf("got %#v, want %#v", data, want)
			}
		}
	})
}

func TestServiceSaveAndMtime(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		save := func(v url.Values) { t.Helper(); assertCode(t, adminTestPost(t, s, admin, "settings/save", v), 0) }
		settings := func() map[string]any { t.Helper(); return adminTestData(t, get(t, s, "/x/admin/settings", admin)) }
		sentinel := func() {
			t.Helper()
			if err := s.Store.SetSetting(t.Context(), "serviceMtime", 123); err != nil {
				t.Fatal(err)
			}
		}
		unchanged := func() {
			t.Helper()
			if settings()["serviceMtime"] != float64(123) {
				t.Fatal("mtime changed without service changes")
			}
		}
		save(url.Values{"serviceMtime": {"9999999999"}, "siteName": {"普通设置"}})
		if settings()["serviceMtime"] != float64(0) || settings()["siteName"] != "普通设置" {
			t.Fatal(settings())
		}
		params := url.Values{
			"serviceApi": {"  https://API.test/base///  "}, "serviceLive": {"http://live.test:8080/path/"},
			"serviceApp": {"https://app.test/"}, "servicePassport": {"https://passport.test/login//"},
			"serviceCdns":  {`[{"name":" 第一个 ","url":" https://cdn.test/video/// ","extra":true},{"name":"第二个","url":"http://two.test/"}]`},
			"serviceMtime": {"9999999999"},
		}
		before := time.Now().Unix()
		save(params)
		after := time.Now().Unix()
		got := settings()
		expected := map[string]any{"serviceApi": "https://API.test/base", "serviceLive": "http://live.test:8080/path", "serviceApp": "https://app.test", "servicePassport": "https://passport.test/login", "serviceCdns": `[{"name":"第一个","url":"https://cdn.test/video"},{"name":"第二个","url":"http://two.test"}]`}
		for k, v := range expected {
			if got[k] != v {
				t.Errorf("%s: %#v", k, got[k])
			}
		}
		stamp := got["serviceMtime"].(float64)
		if stamp < float64(before) || stamp > float64(after) {
			t.Fatal("mtime", stamp)
		}
		data := adminTestData(t, get(t, s, "/x/jumedia/service"))
		for k, v := range map[string]string{"api": "serviceApi", "live": "serviceLive", "app": "serviceApp", "passport": "servicePassport"} {
			if data[k] != expected[v] {
				t.Fatal(data)
			}
		}
		if data["mtime"] != stamp || len(data["cdn"].([]any)) != 2 || data["cdn"].([]any)[0].(map[string]any)["name"] != "第一个" {
			t.Fatal(data)
		}
		sentinel()
		save(params)
		unchanged()
		save(url.Values{"serviceMtime": {"456"}})
		unchanged()
		save(url.Values{"announcement": {"普通设置不更新时间"}})
		unchanged()
		// Every service field independently contributes to the comparison.
		for _, key := range []string{"serviceApi", "serviceLive", "serviceApp", "servicePassport", "serviceCdns"} {
			sentinel()
			value := ""
			if key == "serviceCdns" {
				value = `[{"name":"第二个","url":"http://two.test"},{"name":"第一个","url":"https://cdn.test/video"}]`
			}
			save(url.Values{key: {value}})
			if settings()["serviceMtime"].(float64) <= 123 {
				t.Fatalf("%s did not update mtime", key)
			}
		}
		save(url.Values{"serviceCdns": {""}})
		data = adminTestData(t, get(t, s, "/x/jumedia/service"))
		if data["api"] != "http://example.com" || len(data["cdn"].([]any)) != 0 {
			t.Fatal(data)
		}
		sentinel()
		save(url.Values{"serviceApi": {" "}, "serviceCdns": {"[]"}})
		unchanged()
		// JS counts Unicode code points and coerces primitive names to strings.
		save(url.Values{"serviceCdns": {fmt.Sprintf(`[{"name":%q,"url":"https://emoji.test"},{"name":123,"url":"https://number.test"}]`, strings.Repeat("😀", 20))}})
		data = adminTestData(t, get(t, s, "/x/jumedia/service"))
		if data["cdn"].([]any)[1].(map[string]any)["name"] != "123" {
			t.Fatal(data)
		}
	})
}

func TestServiceValidationAtomic(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		tests := []struct{ key, value, message string }{
			{"serviceApi", "bad", "主站接口地址格式不正确"},
			{"serviceLive", "http://[broken", "直播接口地址格式不正确"},
			{"serviceApp", "/relative", "客户端接口地址格式不正确"},
			{"servicePassport", "https://", "登录接口地址格式不正确"},
			{"serviceApi", "ftp://example.test", "主站接口地址需为 http(s) 地址，不能带参数"},
			{"serviceLive", "https://example.test/?q=1", "直播接口地址需为 http(s) 地址，不能带参数"},
			{"serviceApp", "https://example.test/#frag", "客户端接口地址需为 http(s) 地址，不能带参数"},
			{"servicePassport", "https://user:pass@example.test", "登录接口地址需为 http(s) 地址，不能带参数"},
			{"serviceApi", "https://:pass@example.test", "主站接口地址需为 http(s) 地址，不能带参数"},
			{"serviceApi", "https://example.test:65536", "主站接口地址格式不正确"},
			{"serviceCdns", "[", "CDN 集合格式不正确"},
			{"serviceCdns", "null", "CDN 集合格式不正确"},
			{"serviceCdns", "{}", "CDN 集合格式不正确"},
			{"serviceCdns", "[" + strings.Repeat("{},", 20) + "{}]", "CDN 集合最多 20 个"},
			{"serviceCdns", `[{"name":" ","url":"https://a.test"}]`, "每个 CDN 需要填写 1–20 字的名称和地址"},
			{"serviceCdns", `[{"name":"name","url":" "}]`, "每个 CDN 需要填写 1–20 字的名称和地址"},
			{"serviceCdns", `[null]`, "每个 CDN 需要填写 1–20 字的名称和地址"},
			{"serviceCdns", `[42]`, "每个 CDN 需要填写 1–20 字的名称和地址"},
			{"serviceCdns", fmt.Sprintf(`[{"name":%q,"url":"https://a.test"}]`, strings.Repeat("😀", 21)), "每个 CDN 需要填写 1–20 字的名称和地址"},
			{"serviceCdns", `[{"name":"name","url":"bad"}]`, "CDN 地址格式不正确"},
			{"serviceCdns", `[{"name":"name","url":"https://a.test/?q=1"}]`, "CDN 地址需为 http(s) 地址，不能带参数"},
			{"serviceCdns", `[{"name":"a","url":"https://a.test///"},{"name":"b","url":" https://a.test "}]`, "CDN 地址不能重复"},
			{"serviceCdns", `[{}, {"url":"bad"}]`, "CDN 地址格式不正确"},
		}
		for i, tc := range tests {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				before, err := s.Store.Settings(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				logs := adminTestCount(t, s, "SELECT COUNT(*) FROM logs")
				v := url.Values{"siteName": {"should roll back"}, "serviceMtime": {"9999"}, "serviceLive": {"https://valid.test"}, tc.key: {tc.value}}
				result := adminTestPost(t, s, admin, "settings/save", v)
				assertCode(t, result, -400)
				if result["message"] != tc.message {
					t.Fatalf("message: %v, want %s", result, tc.message)
				}
				after, err := s.Store.Settings(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, after) || adminTestCount(t, s, "SELECT COUNT(*) FROM logs") != logs {
					t.Fatal("failed save changed settings or logs")
				}
			})
		}
	})
}

func TestServiceQueryStorageFailure(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		if err := s.Store.SetSetting(t.Context(), "serviceCdns", "invalid JSON"); err != nil {
			t.Fatal(err)
		}
		assertCode(t, get(t, s, "/x/jumedia/service"), -500)
	})
}

func TestServiceURLBrowserSemantics(t *testing.T) {
	for _, value := range []string{
		"https:example.test", "https:///example.test", "https://example.test/%zz",
		"https://%65xample.test", "https://a.test/#", "https://a.test/?", "https://@a.test",
		`https://a.test\path`, "https://a.test/a b", "https://a\tb.test", "https://[::1]:80/base/",
		"https://127.1", "https://0x7f000001", "https://例子.测试/path", "\ufeffhttps://a.test///\ufeff",
	} {
		t.Run(value, func(t *testing.T) {
			got, err := serviceURL(value, "主站接口")
			if err != nil || got != strings.TrimRight(Trim(value), "/") {
				t.Fatalf("%q: %q, %v", value, got, err)
			}
		})
	}
	for _, value := range []string{"https://[wrong]", "https://999.1.1.1", "https://a.123", "https://example.test:65536", "https://a|b.test", "https://%00.test", "https://a%3a80", "https://a%3f", "https://0x100000000"} {
		t.Run(value, func(t *testing.T) {
			_, err := serviceURL(value, "主站接口")
			if err == nil || err.Error() != "主站接口地址格式不正确" {
				t.Fatalf("%q: %v", value, err)
			}
		})
	}
}

func TestServicePreservesCDNText(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		names := []string{"/upload/name", "/media/name", "/mock/name", "<a>&", "a\u2028b", `\u2028`}
		input := make([]map[string]string, 0, len(names))
		for i, name := range names {
			input = append(input, map[string]string{"name": name, "url": fmt.Sprintf("https://cdn%d.test", i)})
		}
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		assertCode(t, adminTestPost(t, s, admin, "settings/save", url.Values{"serviceCdns": {string(raw)}}), 0)
		got := adminTestData(t, get(t, s, "/x/jumedia/service"))
		for i, v := range got["cdn"].([]any) {
			if v.(map[string]any)["name"] != names[i] {
				t.Errorf("name changed: %#v", v)
			}
		}
		settings := adminTestData(t, get(t, s, "/x/admin/settings", admin))
		stored := settings["serviceCdns"].(string)
		if !strings.Contains(stored, "a\u2028b") || strings.Contains(stored, `\u003c`) || !strings.Contains(stored, `\\u2028`) {
			t.Fatalf("JSON.stringify mismatch: %s", stored)
		}
	})
}

func TestServiceSaveTransactionRollback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		before, err := s.Store.Settings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		// Force a failure after both the settings and mtime writes.
		if _, err := s.Store.DB.ExecContext(t.Context(), "ALTER TABLE logs RENAME TO t22_saved_logs"); err != nil {
			t.Fatal(err)
		}
		result := adminTestPost(t, s, admin, "settings/save", url.Values{"siteName": {"rollback"}, "serviceApi": {"https://api.test"}})
		assertCode(t, result, -500)
		after, err := s.Store.Settings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("settings/mtime escaped transaction rollback")
		}
	})
}
