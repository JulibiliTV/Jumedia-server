package dandanplay

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient(t *testing.T) {
	ambiguous, fail, empty := false, false, false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(429)
			return
		}
		h := sha256.Sum256([]byte("fixture-app" + r.Header.Get("X-Timestamp") + r.URL.Path + "fixture-secret"))
		if r.Header.Get("X-AppId") != "fixture-app" || r.Header.Get("X-Signature") != base64.StdEncoding.EncodeToString(h[:]) || r.Header.Get("X-AppSecret") != "" {
			t.Error("invalid authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/match":
			if ambiguous {
				fmt.Fprint(w, `{"success":true,"matches":[{"episodeId":1},{"episodeId":2}]}`)
				return
			}
			fmt.Fprint(w, `{"success":true,"isMatched":true,"matches":[{"episodeId":123,"animeTitle":"测试作品","episodeTitle":"第1集"}]}`)
		case "/api/v2/comment/123":
			if empty {
				fmt.Fprint(w, `{"count":0,"comments":null}`)
				return
			}
			if r.URL.Query().Get("withRelated") != "true" {
				t.Error("missing related comments")
			}
			fmt.Fprint(w, `{"count":6,"comments":[{"cid":1,"p":"12.34,1,16777215,user","m":"测试弹幕"},{"cid":1,"p":"12.34,1,16777215,user","m":"重复编号"},{"cid":2,"p":"0,4,255,user","m":"底部"},{"cid":3,"p":"-1,1,1,user","m":"无效"},{"cid":4,"p":"NaN,1,1,user","m":"无效"},{"cid":5,"p":"1,9,1,user","m":"无效"}]}`)
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer upstream.Close()
	c := New(upstream.URL, "fixture-app", "fixture-secret")
	m, err := c.Match(context.Background(), "测试作品 S01E01", 1200)
	if err != nil || m == nil || m.EpisodeID != 123 {
		t.Fatalf("match: %+v %v", m, err)
	}
	d, err := c.Comments(context.Background(), m.EpisodeID)
	if err != nil || len(d) != 2 || d[0].Progress != 12340 || d[1].Mode != 4 || d[1].Color != 255 {
		t.Fatalf("comments: %+v %v", d, err)
	}
	ambiguous = true
	if m, err := c.Match(context.Background(), "模糊名称", 1); err != nil || m != nil {
		t.Fatalf("ambiguous match: %+v %v", m, err)
	}
	empty = true
	if d, err := c.Comments(context.Background(), 123); err != nil || len(d) != 0 {
		t.Fatalf("empty library: %+v %v", d, err)
	}
	fail = true
	if _, err := c.Comments(context.Background(), 123); err == nil {
		t.Fatal("upstream failure ignored")
	}
}

func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{"X-AppId", "X-Signature", "X-Timestamp"} {
			if r.Header.Get(h) != "" {
				t.Error("credential forwarded", h)
			}
		}
		fmt.Fprint(w, `{"comments":[]}`)
	}))
	defer cdn.Close()
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, cdn.URL, 302) }))
	defer u.Close()
	if _, err := New(u.URL, "fixture-app", "fixture-secret").Comments(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
}

func TestSelfHostedClient(t *testing.T) {
	for _, suffix := range []string{"", "/", "/api/v2", "/api/v2/", "/fixture-token", "/fixture-token/api/v2/"} {
		t.Run(suffix, func(t *testing.T) {
			prefix := "/api/v2"
			if strings.Contains(suffix, "fixture-token") {
				prefix = "/fixture-token/api/v2"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, h := range []string{"X-AppId", "X-Signature", "X-Timestamp"} {
					if _, ok := r.Header[http.CanonicalHeaderKey(h)]; ok {
						t.Errorf("unsigned request contains %s", h)
					}
				}
				switch r.URL.Path {
				case prefix + "/match":
					fmt.Fprint(w, `{"success":true,"isMatched":true,"matches":[{"episodeId":123}]}`)
				case prefix + "/comment/123":
					if r.URL.Query().Get("format") != "json" || r.URL.Query().Get("segmentflag") != "false" {
						t.Error("full JSON comments not requested")
					}
					fmt.Fprint(w, `{"comments":[{"cid":1,"p":"1,1,16777215,user","m":"自部署弹幕"}]}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			client := New(upstream.URL+suffix, "", "")
			match, err := client.Match(t.Context(), "测试电影", 1200)
			if err != nil || match == nil || match.EpisodeID != 123 {
				t.Fatalf("match: %+v %v", match, err)
			}
			comments, err := client.Comments(t.Context(), match.EpisodeID)
			if err != nil || len(comments) != 1 || comments[0].Content != "自部署弹幕" {
				t.Fatalf("comments: %+v %v", comments, err)
			}
		})
	}
}
