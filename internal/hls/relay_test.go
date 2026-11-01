package hls

import (
	"fmt"
	"strings"
	"testing"
)

func vod() string {
	s := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n"
	for i := 0; i < 18; i++ {
		s += fmt.Sprintf("#EXTINF:4,\ns%d.ts\n", i)
	}
	return s + "#EXT-X-ENDLIST\n"
}
func TestWindowSequence(t *testing.T) {
	p, err := ParsePlaylist(vod(), "https://media.test/hls/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	if p.Duration != 72 || len(p.Segments) != 18 {
		t.Fatal(p)
	}
	for _, tt := range []struct {
		elapsed           float64
		seq               int
		loop, ended, disc bool
	}{
		{0, 0, true, false, false}, {3.9, 0, true, false, false}, {4, 1, true, false, false}, {48, 12, true, false, false}, {52, 13, true, false, true}, {72, 18, true, false, false}, {144, 36, true, false, false},
		{0, 0, false, false, false}, {52, 12, false, false, false}, {71.9, 12, false, false, false}, {72, 12, false, true, false}, {144, 12, false, true, false},
	} {
		w := LiveWindow(p, 100, 100+tt.elapsed, WindowOptions{Loop: tt.loop})
		if !strings.Contains(w.Text, fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", tt.seq)) || w.Ended != tt.ended || strings.Contains(w.Text, "#EXT-X-DISCONTINUITY\n") != tt.disc || strings.Contains(w.Text, "#EXT-X-ENDLIST") != tt.ended {
			t.Fatalf("%+v: %+v", tt, w)
		}
		if strings.Count(w.Text, "#EXTINF:") != 6 {
			t.Fatal(w.Text)
		}
		if !strings.Contains(w.Text, fmt.Sprintf("#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", tt.seq/18)) {
			t.Fatal(w.Text)
		}
	}
}
func TestPlaylistTransforms(t *testing.T) {
	base := "https://media.test/a/index.m3u8"
	master, err := ParsePlaylist("#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=800,RESOLUTION=1280x720\nv0.m3u8\n", base)
	if err != nil || master.Kind != "master" || master.Variants[0].URI != "https://media.test/a/v0.m3u8" || master.Variants[0].Bandwidth != 800 || master.Variants[0].Resolution != "1280x720" {
		t.Fatalf("%+v %v", master, err)
	}
	if got := RelayMaster(master, func(i int) string { return "/relay/v0.m3u8" }); !strings.Contains(got, "URI=\"https://media.test/a/audio.m3u8\"") || !strings.HasSuffix(got, "/relay/v0.m3u8\n") {
		t.Fatal(got)
	}
	source := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n#EXTINF:2.5,\n1.m4s\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:3,\n2.m4s\n#EXT-X-PLAYLIST-TYPE:VOD\n"
	p, err := ParsePlaylist(source, base)
	if err != nil || !p.Ended || p.Format != "fmp4" || p.Duration != 5.5 || p.TargetDuration != 3 || p.Segments[1].Key != "" {
		t.Fatalf("%+v %v", p, err)
	}
	w := LiveWindow(p, 0, 3, WindowOptions{Loop: true})
	if !strings.Contains(LiveWindow(p, 0, 0).Text, "#EXT-X-KEY:METHOD=NONE") || !strings.Contains(w.Text, "#EXT-X-DISCONTINUITY\n#EXT-X-MAP") {
		t.Fatal(w.Text)
	}
	live := "#EXTM3U\r\n#EXT-X-MEDIA-SEQUENCE:81\r\n#EXT-X-KEY:METHOD=AES-128,URI=\"../key\"\r\n#EXTINF:4,\r\n seg.ts \r\n"
	got := Passthrough(live, base)
	if !strings.Contains(got, "#EXT-X-MEDIA-SEQUENCE:81") || !strings.Contains(got, "URI=\"https://media.test/key\"") || !strings.Contains(got, "https://media.test/a/seg.ts") {
		t.Fatal(got)
	}
	for _, bad := range []string{"bad", "#EXTM3U\n", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n#invalid"} {
		if _, err := ParsePlaylist(bad, base); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestWindowDefaultsAndTimingValidation(t *testing.T) {
	p, e := ParsePlaylist("#EXTM3U\n#EXT-X-TARGETDURATION:7\nfirst.ts\n#EXTINF:2,\nsecond.ts\n#EXT-X-ENDLIST\n", "https://media.test/index.m3u8")
	if e != nil || p.Duration != 9 || p.Segments[0].Duration != 7 {
		t.Fatalf("%+v %v", p, e)
	}
	w := LiveWindow(p, 100, 90)
	if w.Position != 0 || strings.Count(w.Text, "#EXTINF:") != 2 || !strings.Contains(w.Text, "#EXT-X-MEDIA-SEQUENCE:0\n") {
		t.Fatal(w)
	}
	w = LiveWindow(p, 100, 100, WindowOptions{Loop: true, Size: 1})
	if strings.Count(w.Text, "#EXTINF:") != 1 {
		t.Fatal(w)
	}
	for _, duration := range []string{"-1", "+Inf", "1e309"} {
		if _, e := ParsePlaylist("#EXTM3U\n#EXTINF:"+duration+",\na.ts\n", "https://media.test/index.m3u8"); e == nil {
			t.Fatalf("accepted invalid duration %s", duration)
		}
	}
}
