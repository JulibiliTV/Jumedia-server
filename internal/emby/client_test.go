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

func TestConcurrentLoginStaticStreams(t *testing.T) {
	var logins atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "AuthenticateByName") {
			logins.Add(1)
			fmt.Fprint(w, `{"AccessToken":"secret"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "PlaybackInfo") || strings.HasSuffix(r.URL.Path, "m3u8") {
			t.Error("static playback requested a session/transcode")
		}
		if r.URL.Query().Get("Static") != "true" {
			t.Error("not static")
		}
		fmt.Fprint(w, "media")
	}))
	defer fake.Close()
	client := New(fake.URL, "user", "")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := client.Stream(t.Context(), "item", "source", nil)
			if err != nil {
				t.Error(err)
				return
			}
			res.Body.Close()
		}()
	}
	wg.Wait()
	if logins.Load() != 1 {
		t.Fatal(logins.Load())
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
}
