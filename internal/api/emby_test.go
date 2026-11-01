package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jumedia-server/internal/emby"
)

type fakeEmby struct {
	server                              *httptest.Server
	logins, sessions, streams, segments atomic.Int32
	expired                             atomic.Bool
	failAuth                            atomic.Bool
	canceled                            chan struct{}
}

func newFakeEmby(t *testing.T) *fakeEmby {
	t.Helper()
	f := &fakeEmby{canceled: make(chan struct{}, 1)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emby/Users/AuthenticateByName" {
			if r.Method != "POST" || !strings.Contains(r.Header.Get("X-Emby-Authorization"), emby.DeviceID) {
				t.Error("missing login identity")
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"Username":"fixture"`) || !strings.Contains(string(body), `"Pw":"secret"`) {
				t.Error("wrong credentials")
			}
			n := f.logins.Add(1)
			fmt.Fprintf(w, `{"AccessToken":"private-token-%d"}`, n)
			return
		}
		if r.Header.Get("X-Emby-Token") == "" {
			t.Error("missing private token")
			w.WriteHeader(401)
			return
		}
		if f.failAuth.Load() || f.expired.CompareAndSwap(true, false) {
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("MediaSourceId") == "" {
			t.Error("missing source")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/PlaybackInfo"):
			if r.Method != "POST" {
				t.Error("PlaybackInfo must be POST")
			}
			f.sessions.Add(1)
			fmt.Fprint(w, `{"PlaySessionId":"private-session"}`)
		case strings.HasSuffix(r.URL.Path, "/stream.mp4"):
			f.streams.Add(1)
			if r.Header.Get("Range") == "bytes=999-" {
				w.Header().Set("Content-Range", "bytes */10")
				w.WriteHeader(416)
				return
			}
			if r.Header.Get("Range") == "bytes=cancel-" {
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				f.canceled <- struct{}{}
				return
			}
			if r.URL.Query().Get("Static") != "true" {
				t.Error("not static")
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"test"`)
			w.Header().Set("Last-Modified", "Thu, 01 Jan 1970 00:00:00 GMT")
			if r.Header.Get("Range") == "bytes=2-4" {
				if r.Header.Get("If-Range") != `"test"` {
					t.Error("If-Range lost")
				}
				w.Header().Set("Content-Length", "3")
				w.Header().Set("Content-Range", "bytes 2-4/10")
				w.WriteHeader(206)
				fmt.Fprint(w, "234")
				return
			}
			w.Header().Set("Content-Length", "10")
			fmt.Fprint(w, "0123456789")
		case strings.HasSuffix(r.URL.Path, "/main.m3u8"):
			fakeHLSQuery(t, r)
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:6,\nhls1/main/0.ts?api_key=private-token&PlaySessionId=private-session\n#EXTINF:6,\nhls1/main/1.ts\n#EXT-X-ENDLIST\n")
		case strings.Contains(r.URL.Path, "/hls1/main/"):
			fakeHLSQuery(t, r)
			f.segments.Add(1)
			w.Header().Set("Content-Type", "video/mp2t")
			fmt.Fprint(w, "TS data")
		default:
			t.Errorf("unexpected Emby request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}
func fakeHLSQuery(t *testing.T, r *http.Request) {
	t.Helper()
	for k, want := range map[string]string{"VideoCodec": "h264", "AudioCodec": "aac", "DeviceId": emby.DeviceID, "PlaySessionId": "private-session"} {
		if r.URL.Query().Get(k) != want {
			t.Errorf("%s missing", k)
		}
	}
}
func embyResponse(t *testing.T, s *Server, path string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header = headers
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	for _, secret := range []string{"private-token", "private-session", "api_key="} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("upstream credential leaked: %s", w.Body.String())
		}
	}
	return w
}
func TestEmbyProxy(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newFakeEmby(t)
		s.Emby = emby.New(f.server.URL, "fixture", "secret")
		s.Config.PublicOrigin = "https://media.example.test"
		path := "/media/emby/item0_0.mp4?source=source_item0_0"
		for _, bad := range []string{"/media/emby/unknown.mp4?source=source_item0_0", "/media/emby/item0_0.mp4?source=wrong", "/media/emby/item0_0/hls/hls1/main/evil.ts?source=source_item0_0", "/media/emby/item0_0/hls/secret.ts?source=source_item0_0", path + "%2Fbad"} {
			if w := embyResponse(t, s, bad, nil); w.Code != 404 {
				t.Fatalf("not blocked %s: %d", bad, w.Code)
			}
		}
		if f.logins.Load() != 0 {
			t.Fatal("unauthorized requests reached Emby")
		}
		w := embyResponse(t, s, path, nil)
		if w.Code != 200 || w.Body.String() != "0123456789" || w.Header().Get("Content-Length") != "10" {
			t.Fatal(w)
		}
		f.expired.Store(true)
		w = embyResponse(t, s, path, http.Header{"Range": {"bytes=2-4"}, "If-Range": {`"test"`}})
		if w.Code != 206 || w.Body.String() != "234" || w.Header().Get("Content-Range") != "bytes 2-4/10" || w.Header().Get("ETag") != `"test"` || w.Header().Get("Last-Modified") == "" || w.Header().Get("Accept-Ranges") != "bytes" {
			t.Fatal(w)
		}
		if f.logins.Load() != 2 {
			t.Fatal("401 must relogin once")
		}
		w = embyResponse(t, s, path, http.Header{"Range": {"bytes=999-"}})
		if w.Code != 416 || w.Header().Get("Content-Range") != "bytes */10" {
			t.Fatal(w)
		}
		base := "/media/emby/item0_0/hls/"
		w = embyResponse(t, s, base+"index.m3u8?source=source_item0_0", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "https://media.example.test"+base+"main.m3u8?source=source_item0_0") {
			t.Fatal(w)
		}
		for i := 0; i < 2; i++ {
			w = embyResponse(t, s, base+"main.m3u8?source=source_item0_0", nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "https://media.example.test"+base+"hls1/main/0.ts?source=source_item0_0") {
				t.Fatal(w)
			}
		}
		w = embyResponse(t, s, base+"hls1/main/0.ts?source=source_item0_0", nil)
		if w.Code != 200 || w.Body.String() != "TS data" {
			t.Fatal(w)
		}
		if f.sessions.Load() != 1 {
			t.Fatal("session not cached by item/source")
		}
		// A database live source alone also authorizes its item/source pair.
		liveExec(t, s, "UPDATE rooms SET source=? WHERE room_id=?", `{"type":"m3u8","url":"/media/emby/liveonly/hls/index.m3u8?source=live-source","loop":true}`, 20000)
		if w = embyResponse(t, s, "/media/emby/liveonly.mp4?source=live-source", nil); w.Code != 200 {
			t.Fatal(w)
		}
		liveExec(t, s, "UPDATE rooms SET source=? WHERE room_id=?", `{"type":"push"}`, 20000)
		if w = embyResponse(t, s, "/media/emby/liveonly.mp4?source=live-source", nil); w.Code != 404 {
			t.Fatal("revoked reference still allowed")
		}
		f.failAuth.Store(true)
		before := f.logins.Load()
		if w = embyResponse(t, s, path, nil); w.Code != 502 || f.logins.Load() != before+1 {
			t.Fatal("retry not bounded", w)
		}
	})
}
func TestEmbyInProcessRelayAndCancellation(t *testing.T) {
	s := newTestServer(t)
	// This tests the legacy Emby HLS proxy, independently of v14 demo defaults.
	liveExec(t, s, "UPDATE rooms SET source=?,live_status=1 WHERE room_id=?", `{"type":"m3u8","url":"/media/emby/movie0/hls/index.m3u8?source=source_movie0","loop":true,"format":"ts","kind":"vod"}`, 20000)
	f := newFakeEmby(t)
	s.Emby = emby.New(f.server.URL, "fixture", "secret")
	s.Config.PublicOrigin = "https://no-loopback.invalid"
	w := embyResponse(t, s, "/live-relay/20000/index.m3u8", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://no-loopback.invalid/live-relay/20000/v0.m3u8") {
		t.Fatal(w)
	}
	w = embyResponse(t, s, "/live-relay/20000/v0.m3u8", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://no-loopback.invalid/media/emby/movie0/hls/hls1/main/") || strings.Contains(w.Body.String(), "#EXT-X-ENDLIST") {
		t.Fatal(w)
	}
	// Downstream cancellation reaches the streaming Emby request.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(s)
	defer server.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/media/emby/item0_0.mp4?source=source_item0_0", nil)
	req.Header.Set("Range", "bytes=cancel-")
	// io.Copy may not flush a header-only response, so cancel while Do is pending.
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			res.Body.Close()
		}
	}()
	deadline := time.After(3 * time.Second)
	for f.streams.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("stream not started")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-f.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not canceled")
	}
	<-done
}
func TestPushRoomCannotStart(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		liveExec(t, s, "UPDATE rooms SET source=?,live_status=1 WHERE room_id=?", `{"type":"push","url":""}`, 20000)
		v := post(t, s, "/room/v1/Room/startLive", url.Values{"csrf": {session.CSRF}}, session)
		liveFailure(t, v, -400, "暂未接入 RTMP 推流服务，请先配置 m3u8 直播源或选择转播视频")
		d := liveData(t, get(t, s, "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=20000"))
		liveWant(t, d, nil, "playurl_info")
	})
}
