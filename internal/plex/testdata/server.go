package testdata

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func Server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/identity") {
			if r.Header.Get("X-Plex-Token") != "" {
				t.Error("identity must be public")
			}
			io.WriteString(w, `{"MediaContainer":{"machineIdentifier":"fixture-server"}}`)
			return
		}
		if r.Header.Get("X-Plex-Token") != "fixture-token" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Error("missing JSON negotiation")
		}
		path := strings.TrimPrefix(r.URL.Path, "/base")
		movie := map[string]any{"ratingKey": "10", "type": "movie", "title": "电影", "duration": 120000, "thumb": "/library/metadata/10/thumb/1", "Media": []any{map[string]any{"container": "mp4", "videoCodec": "h264", "audioCodec": "aac", "Part": []any{map[string]any{"id": 100, "key": "/library/parts/100/file/movie.mp4"}}}}}
		show := map[string]any{"ratingKey": "20", "type": "show", "title": "剧集"}
		episode := map[string]any{"ratingKey": "21", "type": "episode", "title": "第二集", "index": 2, "parentIndex": 1, "duration": 240000, "Media": []any{map[string]any{"container": "mkv", "videoCodec": "hevc", "audioCodec": "aac", "Part": []any{map[string]any{"id": 210, "key": "/library/parts/210/file/episode.mkv"}}}}}
		var value any
		switch path {
		case "/":
			value = map[string]any{"friendlyName": "Plex fixture", "machineIdentifier": "fixture-server", "version": "1"}
		case "/library/sections":
			value = map[string]any{"Directory": []any{map[string]any{"key": "1", "type": "movie", "title": "电影库"}, map[string]any{"key": "2", "type": "show", "title": "剧集库"}}}
		case "/library/sections/1/all":
			value = map[string]any{"size": 1, "totalSize": 1, "Metadata": []any{movie}}
		case "/library/sections/2/all":
			value = map[string]any{"size": 1, "totalSize": 1, "Metadata": []any{show}}
		case "/library/metadata/10":
			value = map[string]any{"size": 1, "Metadata": []any{movie}}
		case "/library/metadata/20":
			value = map[string]any{"size": 1, "Metadata": []any{show}}
		case "/library/metadata/21":
			value = map[string]any{"size": 1, "Metadata": []any{episode}}
		case "/library/metadata/20/allLeaves":
			value = map[string]any{"size": 1, "totalSize": 1, "Metadata": []any{episode}}
		case "/library/metadata/10/thumb/1":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write([]byte("image"))
			return
		case "/library/parts/100/file/movie.mp4":
			if r.Header.Get("Range") != "bytes=0-4" {
				t.Error("lost Range")
			}
			w.Header().Set("Content-Range", "bytes 0-4/10")
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(206)
			if r.Method != "HEAD" {
				w.Write([]byte("video"))
			}
			return
		default:
			t.Errorf("unexpected Plex path %s", path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": value})
	}))
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Tests intercept the fixed account host; no credentials leave the fixture.
func Account(t *testing.T) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "plex.tv" {
			return old.RoundTrip(r)
		}
		if r.URL.Scheme != "https" {
			t.Error("account request must use HTTPS")
		}
		status := 200
		body := ""
		switch r.URL.Path {
		case "/api/v2/users/signin":
			if r.Method != "POST" || r.Header.Get("X-Plex-Token") != "" {
				t.Error("invalid account login request")
			}
			r.ParseForm()
			if r.PostForm.Get("login") != "fixture-user" || r.PostForm.Get("password") != "fixture-password" {
				status = 401
				body = `{}`
			} else {
				body = `{"authToken":"fixture-account-token"}`
			}
		case "/api/v2/resources":
			if r.Header.Get("X-Plex-Token") != "fixture-account-token" {
				t.Error("account authorization missing")
			}
			body = `[{"clientIdentifier":"other-server","provides":"server","accessToken":"wrong-server-token"},{"clientIdentifier":"fixture-server","provides":"server","accessToken":"fixture-token"}]`
		default:
			t.Errorf("unexpected account path %s", r.URL.Path)
			status = 404
			body = `{}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
}
