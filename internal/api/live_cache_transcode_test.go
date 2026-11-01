package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/hls"
)

func cacheScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool")
	if e := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestLiveCacheRealTranscode(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, e = exec.LookPath("ffprobe"); e != nil {
		t.Skip("ffprobe not on PATH")
	}
	t.Setenv("JUMEDIA_FFMPEG", ffmpeg)
	t.Setenv("JUMEDIA_FFPROBE", "ffprobe")
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "libx264")
	path := filepath.Join(t.TempDir(), "input.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "10", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	silentPath := filepath.Join(t.TempDir(), "silent.mkv")
	if out, e := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-i", path, "-an", "-c:v", "copy", silentPath).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	silent, e := os.ReadFile(silentPath)
	if e != nil {
		t.Fatal(e)
	}
	testServers(t, func(t *testing.T, s *Server) {
		u, _, rid := cacheSetup(t, s)
		f := newSourceFixture(t)
		var ranges atomic.Int32
		f.stream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("Static") != "true" || r.URL.Query().Get("api_key") != "" {
				t.Error("not raw", r.URL)
			}
			if r.Header.Get("Range") != "" {
				ranges.Add(1)
			}
			input := raw
			if strings.Contains(r.URL.Path, "/movie/") {
				input = silent
			}
			http.ServeContent(w, r, "input.mkv", time.Time{}, bytes.NewReader(input))
		})
		id := sourceSave(t, s, u, f, true, "transcode")
		a := sourceArchives(t, s, id)["mkv"]
		x := cacheChoose(t, s, u, a.Pages[0], true)
		if x.Status != "pending" {
			t.Fatal(x)
		}
		// A second room shares the cache, but a banned pending room must stay offline.
		liveExec(t, s, "UPDATE rooms SET source=(SELECT source FROM rooms WHERE room_id=?),pending_start=1,banned=1,live_status=0 WHERE room_id=?", rid, 21000)
		if e := s.InitLiveCaches(context.Background()); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(s.CloseLiveCaches)
		cacheWait(t, s, x.Key, "running")
		ready := cacheWait(t, s, x.Key, "ready")
		if ready.Progress != 100 || ready.Size == 0 || ready.Duration < 9.9 {
			t.Fatal(ready)
		}
		r := liveRoom(t, s, rid)
		if r.LiveStatus != 1 || r.PendingStart || r.Online < 1 || r.LiveTime == 0 {
			t.Fatal(r)
		}
		if liveRoom(t, s, 21000).LiveStatus != 0 {
			t.Fatal("banned room started")
		}
		body, e := liveCacheRead(ready, "index.m3u8")
		if e != nil {
			t.Fatal(e)
		}
		playlist, e := hls.ParsePlaylist(string(body), "http://example.com"+liveCachePath(x.Key))
		if e != nil || !playlist.Ended || playlist.Kind != "media" || len(playlist.Segments) < 2 {
			t.Fatal(playlist, e)
		}
		if !strings.Contains(string(body), "#EXT-X-PLAYLIST-TYPE:VOD") {
			t.Fatal(string(body))
		}
		w := sourceHTTP(s, liveCachePath(x.Key))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		req := httptest.NewRequest("GET", playlist.Segments[0].URI, nil)
		req.Header.Set("Range", "bytes=0-187")
		w = httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != 206 || w.Body.Len() != 188 {
			t.Fatal(w.Code, w.Body.Len())
		}
		w = sourceHTTP(s, fmt.Sprintf("/live-relay/%d/index.m3u8", rid))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "/live-cache/"+x.Key) || strings.Contains(w.Body.String(), "#EXT-X-ENDLIST") {
			t.Fatal(w.Code, w.Body.String())
		}
		if ranges.Load() == 0 {
			t.Fatal("Range not forwarded")
		}
		// Validate actual output codecs, pixel format, audio layout and no upscale.
		probe := exec.Command("ffprobe", "-v", "error", "-show_streams", "-of", "json", filepath.Join(x.Dir, "index.m3u8"))
		out, e := probe.Output()
		if e != nil || !bytes.Contains(out, []byte(`"codec_name": "h264"`)) || !bytes.Contains(out, []byte(`"codec_name": "aac"`)) || !bytes.Contains(out, []byte(`"height": 240`)) || !bytes.Contains(out, []byte(`"pix_fmt": "yuv420p"`)) {
			t.Fatal(e, string(out))
		}
		assertCode(t, sourcePost(t, s, u, "/room/v1/Room/stopLive", nil), 0)
		silentArchive := sourceArchives(t, s, id)["movie"]
		next := cacheChoose(t, s, u, silentArchive.Pages[0], true)
		cacheWait(t, s, next.Key, "ready")
		out, e = exec.Command("ffprobe", "-v", "error", "-show_streams", "-of", "json", filepath.Join(next.Dir, "index.m3u8")).Output()
		if e != nil || !bytes.Contains(out, []byte(`"codec_name": "aac"`)) || !bytes.Contains(out, []byte(`"channels": 2`)) {
			t.Fatal("silent audio missing", e, string(out))
		}

	})
}
func TestLiveCacheProcessFailureRecoveryAndCancel(t *testing.T) {
	probe := cacheScript(t, `printf '%s\n' '{"streams":[{"index":0,"codec_type":"video","height":1080},{"index":1,"codec_type":"audio","disposition":{"default":1}}],"format":{"duration":"10"}}'`)
	failure := cacheScript(t, `echo "first error" >&2
 echo "bad input $*" >&2
 exit 1`)
	stall := cacheScript(t, `exec sleep 30`)
	t.Setenv("JUMEDIA_FFPROBE", probe)
	t.Setenv("JUMEDIA_FFMPEG", failure)
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "libx264")
	testServers(t, func(t *testing.T, s *Server) {
		u, p, _ := cacheSetup(t, s)
		x := cacheChoose(t, s, u, p, true)
		s.liveCaches.stallTimeout = time.Second
		if e := s.InitLiveCaches(context.Background()); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(s.CloseLiveCaches)
		y := cacheWait(t, s, x.Key, "failed")
		if !strings.HasPrefix(y.Message, "bad input ") || strings.Contains(y.Message, "127.0.0.1") || len([]rune(y.Message)) > 200 {
			t.Fatal(y.Message)
		}
		// Retry after switching the tool while no job runs; pending transitions through running.
		assertCode(t, sourcePost(t, s, u, "/x/admin/settings/save", url.Values{"serverFfmpeg": {stall}}), 0)
		assertCode(t, sourcePost(t, s, u, "/xlive/app-blink/v1/streamSource/retry", nil), 0)
		cacheWait(t, s, x.Key, "running")
		y = cacheWait(t, s, x.Key, "failed")
		if y.Message != "转码超过 10 分钟没有进度，请重试" {
			t.Fatal(y.Message)
		}
		s.liveCaches.mu.Lock()
		s.liveCaches.stallTimeout = time.Minute
		s.liveCaches.mu.Unlock()
		assertCode(t, sourcePost(t, s, u, "/x/admin/live/cache/retry", url.Values{"key": {x.Key}}), 0)
		cacheWait(t, s, x.Key, "running")
		s.CloseLiveCaches()
		y, e := liveCacheByKey(context.Background(), s.Store.DB, x.Key)
		if e != nil || y.Status != "pending" {
			t.Fatal(y, e)
		}
		if _, e = os.Stat(x.Dir + ".tmp"); !os.IsNotExist(e) {
			t.Fatal("shutdown retained tmp", e)
		}
	})
}
func TestLiveCacheDeleteRunningAndReset(t *testing.T) {
	t.Setenv("JUMEDIA_FFPROBE", cacheScript(t, `printf '%s\n' '{"streams":[{"index":0,"codec_type":"video","height":720}],"format":{"duration":"10"}}'`))
	t.Setenv("JUMEDIA_FFMPEG", cacheScript(t, `exec sleep 30`))
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "libx264")
	testServers(t, func(t *testing.T, s *Server) {
		u, p, _ := cacheSetup(t, s)
		x := cacheChoose(t, s, u, p, false)
		if e := s.InitLiveCaches(context.Background()); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(s.CloseLiveCaches)
		cacheWait(t, s, x.Key, "running")
		assertCode(t, sourcePost(t, s, u, cacheUpdate, url.Values{"source_type": {"push"}}), 0)
		assertCode(t, sourcePost(t, s, u, "/x/admin/live/cache/delete", url.Values{"key": {x.Key}}), 0)
		time.Sleep(30 * time.Millisecond)
		y, e := liveCacheByKey(context.Background(), s.Store.DB, x.Key)
		if e != nil || y != nil {
			t.Fatal("deleted job resurrected", y, e)
		}
		if _, e = os.Stat(x.Dir + ".tmp"); !os.IsNotExist(e) {
			t.Fatal(e)
		}
		x = cacheChoose(t, s, u, p, false)
		cacheWait(t, s, x.Key, "running")
		assertCode(t, sourcePost(t, s, u, "/x/admin/settings/save", url.Values{"reset": {"true"}}), 0)
		time.Sleep(30 * time.Millisecond)
		xs, e := liveCacheList(context.Background(), s.Store.DB)
		if e != nil || len(xs) != 0 {
			t.Fatal(xs, e)
		}
		if _, e = os.Stat(x.Dir + ".tmp"); !os.IsNotExist(e) {
			t.Fatal(e)
		}
	})
}
func TestLiveCacheToolsAndArguments(t *testing.T) {
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "injected")
	s := newTestServer(t)
	if s.InitLiveCaches(context.Background()) == nil {
		t.Fatal("invalid encoder accepted")
	}
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "libx264")
	t.Setenv("JUMEDIA_FFMPEG", "/nonexistent/ffmpeg")
	s = newTestServer(t)
	u, p, _ := cacheSetup(t, s)
	x := cacheChoose(t, s, u, p, false)
	if e := s.InitLiveCaches(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.CloseLiveCaches)
	y := cacheWait(t, s, x.Key, "failed")
	if y.Message != "服务器未安装 ffmpeg" {
		t.Fatal(y.Message)
	}
	var probe liveCacheProbe
	probe.Streams = append(probe.Streams, struct {
		Index       int    `json:"index"`
		Type        string `json:"codec_type"`
		Height      int    `json:"height"`
		Transfer    string `json:"color_transfer"`
		Disposition struct {
			Default int `json:"default"`
		} `json:"disposition"`
	}{Index: 0, Type: "video", Height: 2160, Transfer: "smpte2084"})
	args, e := liveCacheArgs(x, probe, "http://127.0.0.1:12345/token", "libx264", true)
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"anullsrc=channel_layout=stereo:sample_rate=48000", "-shortest", "tonemap=tonemap=hable", "scale=-2:1080", "-maxrate 8M -bufsize 16M", "-force_key_frames expr:gte(t,n_forced*2)", "-map 0:v:0", "-hls_time 6", "-c:a aac -b:a 160k -ar 48000 -ac 2"} {
		if !strings.Contains(joined, want) {
			t.Fatal("missing", want, joined)
		}
	}
	// Demo emby refs use the configured client; the loopback endpoint alone receives credentials.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "AuthenticateByName") {
			fmt.Fprint(w, `{"AccessToken":"private-token"}`)
			return
		}
		if r.Header.Get("X-Emby-Token") != "private-token" || r.URL.Query().Get("Static") != "true" {
			t.Error("missing upstream authorization")
		}
		http.ServeContent(w, r, "file.mkv", time.Time{}, strings.NewReader("0123456789"))
	}))
	defer upstream.Close()
	s.Emby = emby.New(upstream.URL, "secret-user", "secret-password")
	local, closeInput, e := s.liveCacheInput(context.Background(), "emby:demo:src")
	if e != nil {
		t.Fatal(e)
	}
	req, _ := http.NewRequest("GET", local, nil)
	req.Header.Set("Range", "bytes=2-4")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	if res.StatusCode != 206 {
		t.Fatal(res.StatusCode)
	}
	res.Body.Close()
	if strings.Contains(local, upstream.URL) || strings.Contains(joined, "api_key") || strings.Contains(joined, "secret") {
		t.Fatal("credential leakage")
	}
	bad, _ := url.Parse(local)
	bad.Path = "/wrong"
	res, e = http.Get(bad.String())
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("unscoped proxy")
	}
	closeInput()
	if res, e = http.Get(local); e == nil {
		res.Body.Close()
		t.Fatal("proxy still serving")
	}
}

func TestLiveCacheOldestFirstAndPrivateCommandLine(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "args")
	// The log destination is supplied through the script environment, never interpolated as shell code.
	t.Setenv("JUMEDIA_TEST_CACHE_ARGS", capture)
	t.Setenv("JUMEDIA_FFPROBE", cacheScript(t, `printf '%s\n' "$@" >> "$JUMEDIA_TEST_CACHE_ARGS"
 printf '%s\n' '{"streams":[{"index":0,"codec_type":"video","height":720}],"format":{"duration":"10"}}'`))
	t.Setenv("JUMEDIA_FFMPEG", cacheScript(t, `printf '%s\n' "$@" >> "$JUMEDIA_TEST_CACHE_ARGS"
 sleep 1
 echo "expected failure" >&2
 exit 1`))
	t.Setenv("JUMEDIA_FFMPEG_VIDEO_ENCODER", "libx264")
	s := newTestServer(t)
	u, p, _ := cacheSetup(t, s)
	x := cacheChoose(t, s, u, p, false)
	pages, e := s.Store.ListPage(context.Background(), "WHERE cid<>? AND aid IN (SELECT aid FROM archives WHERE mid=?) ORDER BY cid", p.CID, u.MID)
	if e != nil || len(pages) == 0 {
		t.Fatal(e)
	}
	y := cacheChoose(t, s, u, pages[0], false)
	liveExec(t, s, "UPDATE live_caches SET ctime=? WHERE key=?", 1, y.Key)
	if e := s.InitLiveCaches(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.CloseLiveCaches)
	cacheWait(t, s, y.Key, "running")
	current, e := liveCacheByKey(context.Background(), s.Store.DB, x.Key)
	if e != nil || current.Status != "pending" {
		t.Fatal("not serial oldest first", current, e)
	}
	cacheWait(t, s, y.Key, "failed")
	cacheWait(t, s, x.Key, "failed")
	b, e := os.ReadFile(capture)
	if e != nil {
		t.Fatal(e)
	}
	args := string(b)
	if !strings.Contains(args, "http://127.0.0.1:") || strings.Contains(args, "api_key") || strings.Contains(args, "emby:") || strings.Contains(args, "sensitive-token") {
		t.Fatal("unsafe arguments", args)
	}
}
