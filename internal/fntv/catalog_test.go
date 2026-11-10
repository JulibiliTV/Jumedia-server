package fntv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibraryPosterArray(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/api/v2/user/loginByPassword":
			fmt.Fprint(w, `{"code":0,"data":{"token":"fixture"}}`)
		case "/v/api/v1/sys/config":
			fmt.Fprint(w, `{"code":0,"data":{"server_name":"Fixture","server_guid":"instance"}}`)
		case "/v/api/v1/mediadb/list":
			fmt.Fprint(w, `{"code":0,"data":[{"guid":"library","title":"Movies","category":"Movie","poster":"","posters":["/aa/one.webp","/aa/two.webp"]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	c, _ := New(up.URL, "viewer", "fixture-password", true)
	defer c.Close()
	info, err := c.Probe(context.Background())
	if err != nil || len(info.Libraries) != 1 || info.Libraries[0].ID != "library" {
		t.Fatalf("library poster array prevented connection: %+v %v", info, err)
	}
}

func TestArtworkPathVariants(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{{`"/one.webp"`, "/one.webp"}, {`["","/two.webp","/three.webp"]`, "/two.webp"}, {`[]`, ""}, {`null`, ""}} {
		var v artworkPath
		if err := json.Unmarshal([]byte(tc.raw), &v); err != nil || string(v) != tc.want {
			t.Fatalf("artwork shape %s: %q %v", tc.raw, v, err)
		}
	}
	var v artworkPath
	if err := json.Unmarshal([]byte(`{"unexpected":"object"}`), &v); err == nil {
		t.Fatal("invalid artwork shape accepted")
	}
}

func TestSignatureFixture(t *testing.T) {
	want := "nonce=123456&timestamp=1700000000000&sign=e66026ea494b211557f15d2d1e2a3c46"
	if got := signature("/v/api/v1/mediadb/list", nil, "123456", "1700000000000"); got != want {
		t.Fatal("signature differs from protocol fixture")
	}
}

func TestBrowseLibraryScope(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "loginByPassword") {
			fmt.Fprint(w, `{"code":0,"data":{"token":"fixture"}}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/item/list") {
			t.Fatal("must filter library listing")
		}
		var input struct {
			Library string `json:"ancestor_guid"`
			Page    int    `json:"page"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Library != "library" {
			t.Error("scope changed")
		}
		list := []map[string]any{}
		start := (input.Page - 1) * 100
		for i := start; i < min(start+100, 101); i++ {
			list = append(list, map[string]any{"guid": fmt.Sprintf("movie-%d", i), "title": fmt.Sprintf("Movie %d", i), "type": "Movie"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"total": 101, "list": list}})
	}))
	defer up.Close()
	c, _ := New(up.URL, "viewer", "fixture-password", true)
	defer c.Close()
	page, err := c.Browse(context.Background(), "library", "", 0, 2, []string{"movie-100", "foreign"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "movie-100" {
		t.Fatalf("scoped ids: %+v %v", page, err)
	}
	page, err = c.Browse(context.Background(), "library", "Movie 10", 0, 20, nil)
	if err != nil || page.TotalRecordCount != 2 {
		t.Fatalf("search: %+v %v", page, err)
	}
}

func TestIncompletePageRejected(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "loginByPassword") {
			fmt.Fprint(w, `{"code":0,"data":{"token":"fixture"}}`)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"total":101,"list":[{"guid":"one","title":"One","type":"Movie"}]}}`)
	}))
	defer up.Close()
	c, _ := New(up.URL, "viewer", "fixture-password", true)
	defer c.Close()
	if _, err := c.Browse(context.Background(), "library", "", 0, 100, nil); err == nil {
		t.Fatal("short upstream page would skip unseen items")
	}
}

func TestHydrateRetainsListingMetadata(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/api/v2/user/loginByPassword":
			fmt.Fprint(w, `{"code":0,"data":{"token":"fixture"}}`)
		case "/v/api/v1/item/movie":
			fmt.Fprint(w, `{"code":0,"data":{"guid":"movie","title":"Movie","type":"Movie"}}`)
		case "/v/api/v1/play/info":
			fmt.Fprint(w, `{"code":0,"data":{"media_guid":"media"}}`)
		default:
			t.Error("listing metadata should avoid extra discovery")
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	c, _ := New(up.URL, "viewer", "fixture-password", true)
	defer c.Close()
	item, _ := (entry{GUID: "movie", Title: "Movie", Type: "Movie", FileName: "movie.ts", Duration: 120}).item()
	got, err := c.Hydrate(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaSources[0].Container != "ts" || got.RunTimeTicks != 1200000000 {
		t.Fatal("lost listing container or duration")
	}
}

func TestHydrateDiscoversMissingFileMetadata(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/api/v2/user/loginByPassword":
			fmt.Fprint(w, `{"code":0,"data":{"token":"fixture"}}`)
		case "/v/api/v1/item/movie":
			fmt.Fprint(w, `{"code":0,"data":{"guid":"movie","title":"Movie","type":"Movie"}}`)
		case "/v/api/v1/play/info":
			fmt.Fprint(w, `{"code":0,"data":{"media_guid":"media"}}`)
		case "/v/api/v1/stream":
			var p struct {
				Media  string              `json:"media_guid"`
				Level  int                 `json:"level"`
				IP     string              `json:"ip"`
				Nonce  string              `json:"nonce"`
				Header map[string][]string `json:"header"`
			}
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p.Media != "media" || p.Level != 1 || len(p.IP) != 32 || len(p.Nonce) != 6 || len(p.Header["User-Agent"]) != 1 {
				t.Error("invalid metadata discovery request")
			}
			fmt.Fprint(w, `{"code":0,"data":{"file_stream":{"file_name":"movie.m2ts","duration":180}}}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	c, _ := New(up.URL, "viewer", "fixture-password", true)
	defer c.Close()
	item, _ := (entry{GUID: "movie", Title: "Movie", Type: "Movie"}).item()
	got, err := c.Hydrate(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaSources[0].Container != "m2ts" || got.RunTimeTicks != 1800000000 {
		t.Fatal("file metadata was not discovered")
	}
}
