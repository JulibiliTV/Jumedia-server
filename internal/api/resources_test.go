package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsoluteResourceEnvelopes(t *testing.T) {
	for name, value := range map[string]any{"data": map[string]any{"pic": "/mock/a.png", "id": int64(9007199254740993)}, "result": Result(map[string]any{"pic": "/upload/b.png"}), "root": Root(map[string]any{"pic": "/media/c"}), "raw": Raw([]string{"/mock/d.png", "words /mock/d.png", "https://outside.test/x", "/other/path"})} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeResponse(w, value, nil, "https://public.test")
			if strings.Contains(w.Body.String(), `"/mock/`) || strings.Contains(w.Body.String(), `"/upload/`) || strings.Contains(w.Body.String(), `"/media/`) {
				t.Fatal(w.Body.String())
			}
			if name == "data" && !strings.Contains(w.Body.String(), "9007199254740993") {
				t.Fatal("integer precision lost")
			}
			if name == "raw" && !strings.Contains(w.Body.String(), "words /mock/d.png") {
				t.Fatal("modified substring")
			}
		})
	}
	w := httptest.NewRecorder()
	writeResponse(w, nil, FailData(-400, "error", map[string]any{"pic": "/mock/error.png"}), "https://public.test")
	if !strings.Contains(w.Body.String(), "https://public.test/mock/error.png") {
		t.Fatal(w.Body.String())
	}
}
func TestMainAPIResources(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		s.Config.PublicOrigin = "https://public.example.test"
		paths := []string{"/x/jumedia/site", "/x/v2/feed/index", "/x/web-interface/view?aid=100100", "/x/web-interface/view/detail?aid=100100", "/pgc/view/web/season?season_id=40000", "/pgc/page/web/feed?name=variety", "/room/v1/Room/get_info?room_id=20000", "/live_user/v1/Master/info?uid=1", "/x/space/wbi/acc/info?mid=1", "/x/v2/reply?oid=100100&type=1", "/x/v3/fav/resource/list?media_id=9010", "/x/web-interface/history/cursor", "/x/web-interface/search/all/v2?keyword=星", "/x/admin/users", "/x/admin/archives", "/x/admin/seasons", "/x/admin/banners"}
		for _, path := range paths {
			t.Run(path, func(t *testing.T) {
				value, w := request(t, s, "GET", path, nil, session)
				assertCode(t, value, 0)
				if bytes.Contains(w.Body.Bytes(), []byte("data:")) {
					t.Fatal("inline resource", w.Body.String())
				}
				checkImageURLs(t, value, "")
			})
		}
	})
}
func checkImageURLs(t *testing.T, v any, key string) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			checkImageURLs(t, v, k)
		}
	case []any:
		for _, v := range x {
			checkImageURLs(t, v, key)
		}
	case string:
		if strings.HasPrefix(x, "/mock/") || strings.HasPrefix(x, "/upload/") || strings.HasPrefix(x, "/media/") {
			t.Fatalf("relative resource: %s", x)
		}
		if x == "" {
			return
		}
		switch key {
		case "pic", "cover", "face", "avatar", "top_photo", "img", "user_cover", "keyframe", "horizontal", "ss_horizontal_cover", "square_cover", "big_cover", "imgUrl":
			if !strings.HasPrefix(x, "http://") && !strings.HasPrefix(x, "https://") {
				t.Fatalf("%s is not absolute: %s", key, x)
			}
		}
	}
}
func TestRasterUploadsAndBannerImage(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		for _, data := range [][]byte{[]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), []byte("plain text"), []byte("%PDF-1.4")} {
			v := creatorUpload(t, s, session, "/x/upload/web/image", "fake.png", data)
			discoverFailure(t, v, -400, "仅支持 PNG、JPEG、WebP 或 GIF 图片")
		}
		for _, tc := range []struct {
			bytes []byte
			ext   string
		}{{[]byte("\x89PNG\r\n\x1a\n"), ".png"}, {[]byte{0xff, 0xd8, 0xff, 0xe0}, ".jpg"}, {[]byte("GIF89a"), ".gif"}, {[]byte("RIFF\x10\x00\x00\x00WEBPVP8 "), ".webp"}} {
			d := creatorData(t, creatorUpload(t, s, session, "/x/upload/web/image", "wrong.svg", tc.bytes))
			if filepath.Ext(d["url"].(string)) != tc.ext || !strings.HasPrefix(d["url"].(string), "http://example.com/upload/") {
				t.Fatal(d)
			}
		}
		v := adminTestPost(t, s, session, "banner/save", url.Values{"title": {"missing"}})
		discoverFailure(t, v, -400, "请上传轮播图片")
		v = adminTestPost(t, s, session, "banner/save", url.Values{"title": {"image"}, "img": {"/mock/avatars/default1.png"}})
		assertCode(t, v, 0)
	})
}
func TestResourceTransformTypes(t *testing.T) {
	raw := []byte(`{"n":9223372036854775807,"array":[{"x":"/media/one"}],"url":"https://public.test/mock/one"}`)
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(absoluteResources(v, "https://public.test"))
	if !strings.Contains(string(encoded), "9223372036854775807") || strings.Contains(string(encoded), "https://public.testhttps:") {
		t.Fatal(string(encoded))
	}
}

func TestInlineResourceWritesRejected(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		for _, tc := range []struct {
			path   string
			params url.Values
		}{
			{"/x/member/web/update", url.Values{"face": {"data:image/png;base64,AAA="}}},
			{"/x/admin/banner/save", url.Values{"title": {"inline"}, "img": {"data:image/png;base64,AAA="}}},
			{"/x/admin/season/save", url.Values{"season": {`{"title":"inline","episodes":[{"media":"data:video/mp4;base64,AAA="}]}`}}},
			{"/x/vu/web/add/v3", url.Values{"title": {"inline"}, "videos": {`[{"filename":"data:video/mp4;base64,AAA="}]`}}},
		} {
			tc.params.Set("csrf", session.CSRF)
			v := post(t, s, tc.path, tc.params, session)
			discoverFailure(t, v, -400, "不支持内嵌资源，请上传文件")
		}
	})
}
