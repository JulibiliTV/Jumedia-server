package emby

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDataSourceDirectAndAddressPolicy(t *testing.T) {
	for _, tt := range []struct {
		container string
		streams   []MediaStream
		want      bool
	}{
		{"MP4", []MediaStream{{"Video", "H264"}, {"Audio", "AAC"}}, true}, {"m4v", []MediaStream{{"Video", "h264"}, {"Audio", "mp3"}}, true}, {"mov", []MediaStream{{"Video", "h264"}}, true}, {"mkv", []MediaStream{{"Video", "h264"}}, false}, {"mp4", nil, false}, {"mp4", []MediaStream{{"Video", "h264"}, {"Video", "hevc"}}, false}, {"mp4", []MediaStream{{"Video", "h264"}, {"Audio", "aac"}, {"Audio", "ac3"}}, false},
	} {
		if got := Direct(MediaSource{Container: tt.container, MediaStreams: tt.streams}); got != tt.want {
			t.Fatal(tt, got)
		}
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "100.127.255.255", "::1", "fc00::1", "fdff::1", "fe80::1", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "::ffff:127.0.0.1"} {
		if !PrivateIP(net.ParseIP(address)) {
			t.Fatal("allowed", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "100.128.0.1", "2001:4860:4860::8888"} {
		if PrivateIP(net.ParseIP(address)) {
			t.Fatal("refused", address)
		}
	}
	for _, tt := range []struct{ input, want string }{{" https://example.com/ ", "https://example.com/emby"}, {"https://example.com/EmBy/", "https://example.com/EmBy"}, {"http://example.com/base/?q=x#hash", "http://example.com/base/emby"}} {
		got, e := NormalizeBase(tt.input)
		if e != nil || got != tt.want {
			t.Fatal(got, e)
		}
	}
	ref := DSRef(12, "movie", MediaSource{ID: "src", Container: "mkv"})
	ds, item, source, mode, ok := ParseDSRef(ref)
	if !ok || ds != 12 || item != "movie" || source != "src" || mode != "raw-mkv" {
		t.Fatal(ref)
	}
}
func TestDataSourceRedirectAndJSONLimits(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	client, e := NewDataSource(redirect.URL, "fixture", "", 1, true)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	_, e = client.Probe(context.Background())
	if e == nil || e.Error() != "Emby 返回 HTTP 302" || calls.Load() != 0 {
		t.Fatal("redirect followed", e, calls.Load())
	}
	client, e = NewDataSource(redirect.URL, "fixture", "", 1, false)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	_, e = client.Probe(context.Background())
	if !errors.Is(e, ErrPrivate) {
		t.Fatal("private IP dial", e)
	}
	var out map[string]any
	if e = decodeJSONBody(io.MultiReader(strings.NewReader(`{}`), strings.NewReader(strings.Repeat(" ", 32<<20))), &out); e == nil || e.Error() != "Emby JSON 响应超过 32 MB" {
		t.Fatal("oversize accepted", e)
	}
	if e = decodeJSONBody(strings.NewReader(`{} {}`), &out); e == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestDataSourceRawReferences(t *testing.T) {
	for _, tc := range []struct{ container, video, audio, mode string }{
		{"MP4", "h264", "aac", "direct"}, {"mkv", "hevc", "dts", "raw-mkv"},
		{"mp4", "hevc", "aac", "raw-mp4"}, {" MK_V ,mp4", "hevc", "ac3", "raw-mkv"},
		{"?!,mp4", "h264", "aac", "raw-mkv"}, {"", "hevc", "aac", "raw-mkv"},
		{"WEBM", "vp9", "opus", "raw-webm"},
	} {
		ref := DSRef(12, "item", MediaSource{ID: "src", Container: tc.container, MediaStreams: []MediaStream{{"Video", tc.video}, {"Audio", tc.audio}}})
		if ref != "embyds:12:item:src:"+tc.mode {
			t.Fatal(ref, tc.mode)
		}
		ds, item, src, mode, ok := ParseDSRef(ref)
		if !ok || ds != 12 || item != "item" || src != "src" || mode != tc.mode {
			t.Fatal(ref, mode, ok)
		}
		want := "/media/emby/item/file." + strings.TrimPrefix(mode, "raw-") + "?source=src&ds=12"
		if mode == "direct" {
			want = "/media/emby/item.mp4?source=src&ds=12"
		}
		if got := DSPath(ds, item, src, mode); got != want {
			t.Fatal(got, want)
		}
	}
	for _, mode := range []string{"mp4", "hls", "raw-", "raw-MKV", "raw-mkv/evil", "raw-mkv?x=1", "raw-mkv.extra"} {
		if _, _, _, _, ok := ParseDSRef("embyds:12:item:src:" + mode); ok {
			t.Fatal("invalid mode accepted", mode)
		}
	}
}
