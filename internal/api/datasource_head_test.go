package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestDataSourceHeadDoesNotDownloadMedia(t *testing.T) {
	const fileSize int64 = 30 << 30
	s := newTestServer(t)
	f := newSourceFixture(t)
	admin := login(t, s, "jumedia", "admin123")
	id := sourceSave(t, s, admin, f, true, "fixture")
	var downloads atomic.Int32
	f.stream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		downloads.Add(1)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	server := httptest.NewServer(s)
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/media/emby/mkv/file.mkv?source=mkvsrc", "/media/emby/movie.mp4?source=moviesrc"} {
		request, _ := http.NewRequest(http.MethodHead, server.URL+path+fmt.Sprintf("&ds=%d", id), nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("media size request blocked on video body", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK || response.ContentLength != fileSize || response.Header.Get("Accept-Ranges") != "bytes" {
			t.Fatal(response.StatusCode, response.Header)
		}
	}
	if downloads.Load() != 0 {
		t.Fatal("HEAD caused upstream media download", downloads.Load())
	}
}
