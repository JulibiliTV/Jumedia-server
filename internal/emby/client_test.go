package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlaylistRejectsUnproxyableResources(t *testing.T) {
	for _, line := range []string{"https://elsewhere.test/0.ts", "../secret.ts", "hls1/main/not-a-number.ts", `#EXT-X-KEY:URI="https://elsewhere.test/key"`, `#API_KEY=secret`} {
		t.Run(line, func(t *testing.T) {
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "AuthenticateByName"):
					fmt.Fprint(w, `{"AccessToken":"secret"}`)
				case strings.HasSuffix(r.URL.Path, "PlaybackInfo"):
					fmt.Fprint(w, `{"PlaySessionId":"session"}`)
				default:
					fmt.Fprintf(w, "#EXTM3U\n%s\n", line)
				}
			}))
			defer fake.Close()
			client := New(fake.URL, "user", "")
			if _, err := client.Playlist(t.Context(), "item", "source", "main.m3u8", "https://local.test"); err == nil {
				t.Fatal("unproxyable URI accepted")
			}
		})
	}
}
func TestConcurrentSessionAndLogin(t *testing.T) {
	var logins, sessions atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "AuthenticateByName"):
			logins.Add(1)
			fmt.Fprint(w, `{"AccessToken":"secret"}`)
		case strings.HasSuffix(r.URL.Path, "PlaybackInfo"):
			sessions.Add(1)
			fmt.Fprint(w, `{"PlaySessionId":"session"}`)
		default:
			fmt.Fprint(w, "#EXTM3U\n#EXTINF:6,\nhls1/main/0.ts\n")
		}
	}))
	defer fake.Close()
	client := New(fake.URL, "user", "")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Playlist(t.Context(), "item", "source", "main.m3u8", "https://local.test"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if logins.Load() != 1 || sessions.Load() != 1 {
		t.Fatal(logins.Load(), sessions.Load())
	}
	if _, err := client.Playlist(t.Context(), "item", "different-source", "main.m3u8", "https://local.test"); err != nil {
		t.Fatal(err)
	}
	if sessions.Load() != 2 {
		t.Fatal("source not in session cache key")
	}
}

// This is the only test allowed to contact an actual Emby server.
func TestRealEmbySmoke(t *testing.T) {
	base := os.Getenv("JUMEDIA_TEST_EMBY_URL")
	user := os.Getenv("JUMEDIA_TEST_EMBY_USERNAME")
	password, passwordSet := os.LookupEnv("JUMEDIA_TEST_EMBY_PASSWORD")
	path := os.Getenv("JUMEDIA_TEST_EMBY_PACKAGE")
	if base == "" || user == "" || !passwordSet || path == "" {
		t.Skip("set JUMEDIA_TEST_EMBY_URL/USERNAME/PASSWORD/PACKAGE for opt-in smoke test")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pack struct {
		Catalog Catalog `json:"catalog"`
	}
	if err = json.Unmarshal(raw, &pack); err != nil {
		t.Fatal(err)
	}
	catalog := pack.Catalog
	item, source := "", ""
	if len(catalog.Movies) > 0 {
		item, source = catalog.Movies[0].ID, catalog.Movies[0].SourceID
	} else {
		for _, s := range catalog.Series {
			if len(s.Episodes) > 0 {
				item, source = s.Episodes[0].ID, s.Episodes[0].SourceID
				break
			}
		}
	}
	if item == "" {
		t.Fatal("smoke test requires a playable catalog")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client := New(base, user, password)
	res, err := client.Stream(ctx, item, source, http.Header{"Range": {"bytes=0-1023"}})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 206 && res.StatusCode != 200 {
		t.Fatalf("stream HTTP %d", res.StatusCode)
	}
	if _, err = io.CopyN(io.Discard, res.Body, 1024); err != nil {
		t.Fatal(err)
	}
	playlist, err := client.Playlist(ctx, item, source, "main.m3u8", "http://localhost:18080")
	if err != nil || !strings.Contains(playlist, "/media/emby/") {
		t.Fatal("HLS smoke failed", err)
	}
}
