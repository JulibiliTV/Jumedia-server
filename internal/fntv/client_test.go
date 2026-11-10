package fntv

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProtocol(t *testing.T) {
	logins := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(r.Header.Get("Authx"), "&sign=") {
			t.Error("missing signature")
		}
		if r.URL.Path == "/nas/v/api/v2/user/loginByPassword" {
			logins++
			var p map[string]string
			_ = json.Unmarshal(body, &p)
			if p["password"] != fmt.Sprintf("%x", sha256.Sum256([]byte("fixture-password"))) {
				t.Error("password must be hashed")
			}
			fmt.Fprintf(w, `{"code":0,"data":{"token":"fixture-%d"}}`, logins)
			return
		}
		if r.Header.Get("Authorization") == "fixture-1" {
			fmt.Fprint(w, `{"code":401,"data":null}`)
			return
		}
		if r.Header.Get("Authorization") != "fixture-2" {
			t.Error("missing renewed session")
		}
		switch r.URL.Path {
		case "/nas/v/api/v1/sys/config":
			fmt.Fprint(w, `{"code":0,"data":{"server_name":"Fixture","server_guid":"instance"}}`)
		case "/nas/v/api/v1/mediadb/list":
			fmt.Fprint(w, `{"code":0,"data":[{"guid":"library","title":"Movies","category":"Movie"}]}`)
		case "/nas/v/api/v1/item/list":
			var p map[string]any
			_ = json.Unmarshal(body, &p)
			if p["ancestor_guid"] != "library" || p["page"] != float64(2) || p["page_size"] != float64(100) {
				t.Error("wrong page scope")
			}
			fmt.Fprint(w, `{"code":0,"data":{"total":101,"list":[{"guid":"movie","title":"Movie","type":"Movie","ancestor_guid":"library","poster":"/aa/bb.webp"}]}}`)
		case "/nas/v/api/v1/item/movie":
			fmt.Fprint(w, `{"code":0,"data":{"guid":"movie","title":"Movie","type":"Movie","ancestor_guid":"library","posters":"/aa/bb.webp","overview":"Description","duration":120}}`)
		case "/nas/v/api/v1/play/info":
			fmt.Fprint(w, `{"code":0,"data":{"media_guid":"media","item":{"duration":120}}}`)
		case "/nas/v/api/v1/stream":
			fmt.Fprint(w, `{"code":0,"data":{"file_stream":{"file_name":"movie.mkv","duration":120}}}`)
		case "/nas/v/api/v1/media/range/media":
			if r.Header.Get("Range") != "bytes=2-4" {
				t.Error("missing range")
			}
			w.Header().Set("Content-Range", "bytes 2-4/10")
			w.WriteHeader(206)
			if r.Method != "HEAD" {
				fmt.Fprint(w, "234")
			}
		case "/nas/v/api/v1/sys/img/aa/bb.webp":
			w.Header().Set("Content-Type", "image/webp")
			fmt.Fprint(w, "image")
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	c, err := New(up.URL+"/nas/v/", "viewer", "fixture-password", true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	info, err := c.Probe(ctx)
	if err != nil || info.ServerID != "instance" || len(info.Libraries) != 1 || logins != 2 {
		t.Fatalf("probe: %+v %v", info, err)
	}
	page, err := c.Browse(ctx, "library", "", 100, 100, nil)
	if err != nil || len(page.Items) != 1 || page.TotalRecordCount != 101 {
		t.Fatalf("page: %+v %v", page, err)
	}
	item, err := c.Hydrate(ctx, page.Items[0])
	if err != nil || len(item.MediaSources) != 1 || item.MediaSources[0].ID != "media" || item.RunTimeTicks != 1200000000 {
		t.Fatalf("hydrate: %+v %v", item, err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		var res *http.Response
		if method == "GET" {
			res, err = c.RawStream(ctx, "movie", "media", "mkv", http.Header{"Range": {"bytes=2-4"}})
		} else {
			res, err = c.HeadStream(ctx, "movie", "media", "mkv", http.Header{"Range": {"bytes=2-4"}})
		}
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 206 || res.Header.Get("Content-Range") != "bytes 2-4/10" {
			t.Fatal("range response lost")
		}
	}
	res, err := c.Image(ctx, "movie", "Primary", "/aa/bb.webp")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if _, err = c.Image(ctx, "movie", "Primary", "../../user/info"); err == nil {
		t.Fatal("image path traversal accepted")
	}
	if _, err = c.StreamURL(ctx, "movie", "media", "mkv"); err == nil {
		t.Fatal("unauthenticated direct URL returned")
	}
}

func TestLoginFallbackAndFailure(t *testing.T) {
	for _, status := range []int{404, 401} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			legacy := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "loginByPassword") {
					w.WriteHeader(status)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/login") {
					legacy++
					fmt.Fprint(w, `{"code":0,"data":{"token":"fixture"}}`)
					return
				}
				fmt.Fprint(w, `{"code":0,"data":[]}`)
			}))
			defer up.Close()
			c, _ := New(up.URL, "viewer", "fixture-password", true)
			defer c.Close()
			_, err := c.Libraries(context.Background())
			if status == 404 && (err != nil || legacy != 1) {
				t.Fatalf("fallback: %v", err)
			}
			if status == 401 && (!errors.Is(err, ErrAuth) || legacy != 0) {
				t.Fatalf("rejected password retried: %v", err)
			}
		})
	}
}

func TestPrivateNetworkAndBase(t *testing.T) {
	for _, address := range []string{"ftp://example.test", "http://user:password@example.test"} {
		if _, err := New(address, "viewer", "fixture-password", false); err == nil {
			t.Fatal("invalid address accepted")
		}
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("private destination reached") }))
	defer up.Close()
	c, err := New(up.URL, "viewer", "fixture-password", false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Libraries(context.Background()); err == nil {
		t.Fatal("private address accepted")
	}
}

func TestMediaForbiddenRenews(t *testing.T) {
	logins := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v/api/v2/user/loginByPassword" {
			logins++
			fmt.Fprintf(w, `{"code":0,"data":{"token":"fixture-%d"}}`, logins)
			return
		}
		if r.Header.Get("Authorization") == "fixture-1" {
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, "media")
	}))
	defer up.Close()
	c, _ := New(up.URL, "viewer", "fixture-password", true)
	defer c.Close()
	res, err := c.RawStream(context.Background(), "movie", "media", "ts", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || logins != 2 {
		t.Fatalf("status=%d logins=%d", res.StatusCode, logins)
	}
}
