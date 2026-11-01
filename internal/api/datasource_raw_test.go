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
)

func TestDataSourceRawPlayback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		f.items["movies"][1].MediaSources[0].MediaStreams[0].Codec = "hevc"
		hevc := sourceMovie("hevc", "HEVC MP4", "MP4")
		hevc.MediaSources[0].MediaStreams[0].Codec = "hevc"
		f.items["movies"] = append(f.items["movies"], hevc)
		var requests atomic.Int32
		f.stream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.URL.Path != "/emby/Videos/mkv/stream.mkv" || r.URL.Query().Get("Static") != "true" || r.URL.Query().Get("MediaSourceId") != "mkvsrc" || r.URL.Query().Get("VideoCodec") != "" {
				t.Error("raw upstream", r.URL)
			}
			if r.Header.Get("Range") != "" && r.Header.Get("If-Range") != `"raw-etag"` {
				t.Error("If-Range not forwarded", r.Header)
			}
			w.Header().Set("Content-Type", "video/x-matroska")
			w.Header().Set("ETag", `"raw-etag"`)
			http.ServeContent(w, r, "movie.mkv", time.Unix(1700000000, 0), strings.NewReader("raw-original"))
		})
		admin := login(t, s, "jumedia", "admin123")
		id := sourceSave(t, s, admin, f, true, "raw")
		archives := sourceArchives(t, s, id)
		for _, tc := range []struct{ item, mode, format, path string }{
			{"movie", "direct", "mp4", "/media/emby/movie.mp4"},
			{"mkv", "raw-mkv", "mkv", "/media/emby/mkv/file.mkv"},
			{"hevc", "raw-mp4", "mp4", "/media/emby/hevc/file.mp4"},
		} {
			p := archives[tc.item].Pages[0]
			if !strings.HasSuffix(p.Media, ":"+tc.mode) {
				t.Fatal(p.Media, tc.mode)
			}
			play := sourceData(t, get(t, s, fmt.Sprintf("/x/player/wbi/playurl?cid=%d", p.CID)))
			want := "http://example.com" + tc.path + fmt.Sprintf("?source=%ssrc&ds=%d", tc.item, id)
			u := play["durl"].([]any)[0].(map[string]any)["url"].(string)
			if u != want || play["format"] != tc.format || play["timelength"] != float64(p.Duration*1000) || play["quality"] != float64(80) {
				t.Fatal(play, want)
			}
		}
		path := fmt.Sprintf("/media/emby/mkv/file.mkv?source=mkvsrc&ds=%d", id)
		for _, tc := range []struct {
			rangeHeader        string
			status             int
			body, contentRange string
		}{
			{"", 200, "raw-original", ""}, {"bytes=0-2", 206, "raw", "bytes 0-2/12"}, {"bytes=99-", 416, "", "bytes */12"},
		} {
			r := httptest.NewRequest("GET", path, nil)
			if tc.rangeHeader != "" {
				r.Header.Set("Range", tc.rangeHeader)
				r.Header.Set("If-Range", `"raw-etag"`)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.status || (tc.body != "" && w.Body.String() != tc.body) || w.Header().Get("Content-Range") != tc.contentRange {
				t.Fatal(tc, w.Code, w.Header(), w.Body.String())
			}
			if tc.status != 416 && (w.Header().Get("Content-Type") != "video/x-matroska" || w.Header().Get("ETag") != `"raw-etag"` || w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("Last-Modified") == "" || w.Header().Get("Content-Length") != fmt.Sprint(len(tc.body))) {
				t.Fatal(w.Header())
			}
		}
		before := requests.Load()
		for _, bad := range []string{
			strings.Replace(path, "file.mkv", "file.mp4", 1), strings.Replace(path, "file.mkv", "file.MKV", 1), strings.Replace(path, "file.mkv", "file.mkv.exe", 1),
			strings.Replace(path, "source=mkvsrc", "source=wrong", 1), strings.Replace(path, "/mkv/", "/unknown/", 1), strings.Replace(path, fmt.Sprintf("ds=%d", id), "ds=999999999", 1),
			fmt.Sprintf("/media/emby/mkv.mp4?source=mkvsrc&ds=%d", id),
			fmt.Sprintf("/media/emby/movie/file.mp4?source=moviesrc&ds=%d", id),
			fmt.Sprintf("/media/emby/mkv/image/mp4?source=wrong&ds=%d", id),
			fmt.Sprintf("/media/emby/mkv/image/file?source=mkvsrc&ds=%d", id),
		} {
			if w := sourceHTTP(s, bad); w.Code != 404 {
				t.Fatal(bad, w.Code, w.Body.String())
			}
		}
		if requests.Load() != before {
			t.Fatal("whitelist reached upstream")
		}
		// Migration's raw-mkv placeholder is replaced on sync without changing CID.
		oldPage := archives["hevc"].Pages[0]
		liveExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=?", fmt.Sprintf("embyds:%d:hevc:hevcsrc:raw-mkv", id), oldPage.CID)
		sourceSync(t, s, admin, id)
		refreshed := sourceArchives(t, s, id)["hevc"].Pages[0]
		if refreshed.CID != oldPage.CID || !strings.HasSuffix(refreshed.Media, ":raw-mp4") {
			t.Fatal(refreshed)
		}

		// A raw MKV queues a local cache; datasource HLS stays removed.
		aRaw := archives["mkv"]
		room := sourceData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", admin))["room_id"].(float64)
		livePath := fmt.Sprintf("/media/emby/mkv/hls/main.m3u8?source=mkvsrc&ds=%d", id)
		if w := sourceHTTP(s, livePath); w.Code != 404 {
			t.Fatal("unreferenced HLS", w.Code)
		}
		assertCode(t, sourcePost(t, s, admin, "/room/v1/Room/stopLive", nil), 0)
		sourceData(t, sourcePost(t, s, admin, "/xlive/app-blink/v1/streamSource/update", url.Values{"source_type": {"video"}, "aid": {fmt.Sprint(aRaw.AID)}, "cid": {fmt.Sprint(aRaw.Pages[0].CID)}, "loop": {"true"}}))
		assertCode(t, sourcePost(t, s, admin, "/room/v1/Room/startLive", nil), -400)
		for _, path := range []string{livePath, fmt.Sprintf("/media/emby/mkv/hls/hls1/main/0.ts?source=mkvsrc&ds=%d", id), fmt.Sprintf("/live-relay/%.0f/index.m3u8", room), fmt.Sprintf("/live-relay/%.0f/v0.m3u8", room)} {
			if w := sourceHTTP(s, path); w.Code != 404 {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
		if w := sourceHTTP(s, fmt.Sprintf("/media/emby/mkv/hls/mp4?source=mkvsrc&ds=%d", id)); w.Code != 404 {
			t.Fatal("invalid HLS path", w.Code)
		}
		sourceData(t, sourcePost(t, s, admin, "/xlive/app-blink/v1/streamSource/update", url.Values{"source_type": {"push"}}))
		if w := sourceHTTP(s, livePath); w.Code != 404 {
			t.Fatal("revoked HLS", w.Code)
		}
		// A stale live reference cannot authorize a deleted archive's original file.
		a := archives["mkv"]
		liveExec(t, s, "UPDATE rooms SET source=? WHERE room_id=?", fmt.Sprintf(`{"type":"video","aid":%d,"cid":%d}`, a.AID, a.Pages[0].CID), 20000)
		liveExec(t, s, "UPDATE archives SET state=? WHERE aid=?", -100, a.AID)
		if w := sourceHTTP(s, path); w.Code != 404 {
			t.Fatal("deleted file", w.Code)
		}
		if w := sourceHTTP(s, livePath); w.Code != 404 {
			t.Fatal("existing live HLS reference", w.Code)
		}
		// A mismatched datasource identity must not authorize either.
		a = archives["hevc"]
		liveExec(t, s, "UPDATE archives SET origin_ds=? WHERE aid=?", id+1, a.AID)
		if w := sourceHTTP(s, fmt.Sprintf("/media/emby/hevc/file.mp4?source=hevcsrc&ds=%d", id)); w.Code != 404 {
			t.Fatal("cross source", w.Code)
		}
	})
}

func TestDataSourceRawStreamingCancellation(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		canceled := make(chan struct{})
		f.stream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "video/x-matroska")
			_, _ = io.WriteString(w, strings.Repeat("x", 64<<10))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(canceled)
		})
		admin := login(t, s, "jumedia", "admin123")
		id := sourceSave(t, s, admin, f, true, "stream")
		server := httptest.NewServer(s)
		defer server.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", server.URL+fmt.Sprintf("/media/emby/mkv/file.mkv?source=mkvsrc&ds=%d", id), nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Timeout: 5 * time.Second}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b := make([]byte, 1)
		if _, err = io.ReadFull(res.Body, b); err != nil || res.StatusCode != 200 || b[0] != 'x' {
			t.Fatal(res.StatusCode, err, b)
		}
		cancel()
		select {
		case <-canceled:
		case <-time.After(5 * time.Second):
			t.Fatal("upstream not canceled")
		}
	})
}
