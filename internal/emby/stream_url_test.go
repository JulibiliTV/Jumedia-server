package emby

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestStreamURLAuthentication(t *testing.T) {
	var logins atomic.Int32
	var revoked, denied atomic.Bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prefix/emby/Users/AuthenticateByName":
			n := logins.Add(1)
			fmt.Fprintf(w, `{"AccessToken":"fixture+token/%d&","User":{"Id":"viewer"}}`, n)
		case "/prefix/emby/Users/viewer":
			if denied.Load() || revoked.CompareAndSwap(true, false) {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"Id":"viewer"}`)
		default:
			t.Error("URL generation must not request video", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	c := New(fake.URL+"/prefix", "viewer", "fixture-password")
	defer c.Close()
	for _, container := range []string{"mp4", "mkv", "webm"} {
		link, err := c.StreamURL(t.Context(), "item", "source", container)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		if u.Path != "/prefix/emby/Videos/item/stream."+container || u.Query().Get("api_key") != "fixture+token/1&" || u.Query().Get("DeviceId") != DeviceID || u.Query().Get("Static") != "true" || u.Query().Get("MediaSourceId") != "source" {
			t.Fatal("incorrect static URL or query encoding")
		}
	}
	if logins.Load() != 1 {
		t.Fatal("valid session should be reused")
	}
	revoked.Store(true)
	link, err := c.StreamURL(t.Context(), "item", "source", "mp4")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	if logins.Load() != 2 || u.Query().Get("api_key") != "fixture+token/2&" {
		t.Fatal("revoked session not refreshed")
	}
	denied.Store(true)
	if link, err := c.StreamURL(t.Context(), "item", "source", "mp4"); err == nil || link != "" {
		t.Fatal("failed authentication returned a URL")
	}
	for _, args := range [][3]string{{"../item", "source", "mp4"}, {"item", "source?x=1", "mp4"}, {"item", "source", "mkv?x=1"}} {
		if link, err := c.StreamURL(t.Context(), args[0], args[1], args[2]); err == nil || link != "" {
			t.Fatal("invalid identifier returned a URL")
		}
	}
}
