package api

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hardwareProbe(t *testing.T, transfer string) liveCacheProbe {
	t.Helper()
	var p liveCacheProbe
	if err := json.Unmarshal([]byte(`{"streams":[{"codec_type":"video","height":1080,"color_transfer":"`+transfer+`"}]}`), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLiveCacheVAAPIArgs(t *testing.T) {
	x := &liveCache{Height: 720, Dir: filepath.Join(t.TempDir(), "cache")}
	for _, transfer := range []string{"bt709", "smpte2084", "arib-std-b67"} {
		t.Run(transfer, func(t *testing.T) {
			args, err := liveCacheArgs(x, hardwareProbe(t, transfer), "http://127.0.0.1/input", "h264_vaapi", "/dev/dri/renderD128", true)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, " ")
			for _, want := range []string{"-vaapi_device /dev/dri/renderD128 -i", "scale=-2:720,format=nv12,hwupload", "-c:v h264_vaapi", "-b:v 4M -maxrate 4M -bufsize 8M", "anullsrc=channel_layout=stereo:sample_rate=48000", "-shortest", "-hls_time 6", "-force_key_frames expr:gte(t,n_forced*2)"} {
				if !strings.Contains(joined, want) {
					t.Errorf("missing %q in %s", want, joined)
				}
			}
			if strings.Contains(joined, "-pix_fmt yuv420p") || strings.Contains(joined, "-crf") {
				t.Fatal("software encoder arguments in VAAPI command", joined)
			}
			if strings.Contains(joined, "tonemap=tonemap=hable") != (transfer != "bt709") {
				t.Fatal("HDR mapping changed", joined)
			}
		})
	}
}

const hardwareFFmpeg = `printf '%s\n' "$*" >> "$JUMEDIA_TEST_GPU_ARGS"
case "$*" in
  *color=c=black:s=*)
    if [ "$JUMEDIA_TEST_PROBE_FAIL" = 1 ]; then echo 'driver unavailable' >&2; exit 1; fi
    exit 0 ;;
esac
for output do :; done
directory=$(dirname "$output")
case "$*" in
  *h264_vaapi*)
    if [ "$JUMEDIA_TEST_GPU_FAIL" = 1 ]; then
      echo stale > "$directory/seg99999.ts"
      echo out_time_us=9000000
      echo 'hardware encode failed' >&2
      exit 1
    fi ;;
esac
if [ -f "$directory/seg99999.ts" ]; then echo 'stale GPU output' >&2; exit 1; fi
if [ "$JUMEDIA_TEST_SOFTWARE_FAIL" = 1 ]; then echo 'software encode failed' >&2; exit 1; fi
echo '#EXTM3U' > "$output"
echo segment > "$directory/seg00000.ts"`

func TestLiveCacheHardwareSelectionAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name, encoder                    string
		missing, probeFail, gpuFail      bool
		softwareFail                     bool
		wantProbe, wantGPU, wantSoftware int
		wantError                        string
	}{
		{name: "usable", encoder: "auto", wantProbe: 1, wantGPU: 1},
		{name: "missing device", encoder: "auto", missing: true, wantSoftware: 1},
		{name: "failed probe", encoder: "auto", probeFail: true, wantProbe: 1, wantSoftware: 1},
		{name: "GPU failure retries software", encoder: "auto", gpuFail: true, wantProbe: 1, wantGPU: 1, wantSoftware: 1},
		{name: "both fail", encoder: "auto", gpuFail: true, softwareFail: true, wantProbe: 1, wantGPU: 1, wantSoftware: 1, wantError: "software encode failed"},
		{name: "explicit software", encoder: "libx264", wantSoftware: 1},
		{name: "explicit GPU fails without fallback", encoder: "h264_vaapi", gpuFail: true, wantGPU: 1, wantError: "hardware encode failed"},
		{name: "explicit QSV", encoder: "h264_qsv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testServers(t, func(t *testing.T, s *Server) {
				u, page, _ := cacheSetup(t, s)
				x := cacheChoose(t, s, u, page, false)
				x.Duration = 10
				liveExec(t, s, "UPDATE live_caches SET status='running' WHERE key=?", x.Key)
				if err := os.MkdirAll(x.Dir+".tmp", 0755); err != nil {
					t.Fatal(err)
				}
				device := filepath.Join(t.TempDir(), "renderD128")
				if !tc.missing {
					if err := os.WriteFile(device, nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				capture := filepath.Join(t.TempDir(), "args")
				t.Setenv("JUMEDIA_TEST_GPU_ARGS", capture)
				for key, value := range map[string]bool{"JUMEDIA_TEST_PROBE_FAIL": tc.probeFail, "JUMEDIA_TEST_GPU_FAIL": tc.gpuFail, "JUMEDIA_TEST_SOFTWARE_FAIL": tc.softwareFail} {
					if value {
						t.Setenv(key, "1")
					} else {
						t.Setenv(key, "0")
					}
				}
				size, err := s.encodeLiveCache(t.Context(), x, hardwareProbe(t, "bt709"), "http://127.0.0.1/input", cacheScript(t, hardwareFFmpeg), tc.encoder, device, false, time.Second)
				if tc.wantError != "" {
					if err == nil || err.Error() != tc.wantError {
						t.Fatalf("wanted %q, got %v", tc.wantError, err)
					}
				} else if err != nil || size <= 0 {
					t.Fatal(size, err)
				}
				raw, err := os.ReadFile(capture)
				if err != nil {
					t.Fatal(err)
				}
				var probes, gpu, software int
				for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
					if strings.Contains(line, "color=c=black:s=") {
						probes++
					} else if strings.Contains(line, "h264_vaapi") {
						gpu++
					} else if strings.Contains(line, "libx264") {
						software++
					}
				}
				if probes != tc.wantProbe || gpu != tc.wantGPU || software != tc.wantSoftware {
					t.Fatalf("probe/GPU/software=%d/%d/%d, commands: %s", probes, gpu, software, raw)
				}
				if tc.encoder == "auto" && tc.gpuFail {
					stored, err := liveCacheByKey(t.Context(), s.Store.DB, x.Key)
					if err != nil || stored.Progress != 0 {
						t.Fatal("retry did not reset progress", stored, err)
					}
				}
			})
		})
	}
}

func TestLiveCacheHardwareProbeCancellation(t *testing.T) {
	device := filepath.Join(t.TempDir(), "device")
	if err := os.WriteFile(device, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := liveCacheVAAPIProbe(ctx, cacheScript(t, "exec sleep 30"), device, 720)
	if err == nil || ctx.Err() == nil || time.Since(started) > 3*time.Second {
		t.Fatal("probe did not cancel promptly", err)
	}
}

func TestLiveCacheAutoCancellationNeverRetries(t *testing.T) {
	for _, duringProbe := range []bool{true, false} {
		name := "encoding"
		if duringProbe {
			name = "probe"
		}
		t.Run(name, func(t *testing.T) {
			s := newTestServer(t)
			x := &liveCache{Height: 720, Key: strings.Repeat("a", 32), Dir: filepath.Join(t.TempDir(), strings.Repeat("a", 32))}
			if err := os.MkdirAll(x.Dir+".tmp", 0755); err != nil {
				t.Fatal(err)
			}
			device := filepath.Join(t.TempDir(), "device")
			if err := os.WriteFile(device, nil, 0600); err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(t.TempDir(), "calls")
			t.Setenv("JUMEDIA_TEST_GPU_ARGS", capture)
			body := `printf '%s\n' "$*" >> "$JUMEDIA_TEST_GPU_ARGS"
exec sleep 30`
			if !duringProbe {
				body = `printf '%s\n' "$*" >> "$JUMEDIA_TEST_GPU_ARGS"
case "$*" in *color=c=black:s=*) exit 0 ;; esac
exec sleep 30`
			}
			binary := cacheScript(t, body)
			wantCalls := 2
			if duringProbe {
				wantCalls = 1
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancelled := make(chan struct{})
			go func() {
				defer close(cancelled)
				defer cancel()
				for started := time.Now(); time.Since(started) < 5*time.Second; {
					raw, _ := os.ReadFile(capture)
					if strings.Count(string(raw), "\n") >= wantCalls {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
			}()
			_, err := s.encodeLiveCache(ctx, x, hardwareProbe(t, "bt709"), "input", binary, "auto", device, false, time.Minute)
			<-cancelled
			if err == nil || ctx.Err() == nil {
				t.Fatal("cancellation was ignored", err)
			}
			raw, err := os.ReadFile(capture)
			if err != nil || strings.Count(string(raw), "\n") != wantCalls || strings.Contains(string(raw), "libx264") {
				t.Fatal("cancelled task retried", string(raw), err)
			}
		})
	}
}

// Opt in on a Linux host with the render device and matching VAAPI driver.
func TestLiveCacheRealVAAPI(t *testing.T) {
	if os.Getenv("JUMEDIA_TEST_VAAPI") != "1" {
		t.Skip("set JUMEDIA_TEST_VAAPI=1 on a VAAPI host")
	}
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	if err := liveCacheVAAPIProbe(t.Context(), binary, liveCacheRenderDevice, 720); err != nil {
		t.Fatal("VAAPI frame probe", err)
	}
	input := filepath.Join(t.TempDir(), "input.mp4")
	if raw, err := exec.CommandContext(t.Context(), binary, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=s=1280x720:r=25", "-t", "7", "-c:v", "libx264", "-pix_fmt", "yuv420p", input).CombinedOutput(); err != nil {
		t.Fatal(err, string(raw))
	}
	s := newTestServer(t)
	x := &liveCache{Height: 720, Duration: 7, Key: strings.Repeat("a", 32), Dir: filepath.Join(t.TempDir(), strings.Repeat("a", 32))}
	if err := os.MkdirAll(x.Dir+".tmp", 0755); err != nil {
		t.Fatal(err)
	}
	size, err := s.encodeLiveCache(t.Context(), x, hardwareProbe(t, "bt709"), input, binary, "h264_vaapi", liveCacheRenderDevice, false, time.Minute)
	if err != nil || size == 0 {
		t.Fatal(size, err)
	}
	raw, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-show_streams", "-of", "json", filepath.Join(x.Dir+".tmp", "index.m3u8")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []struct {
			Codec  string `json:"codec_name"`
			Height int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	video, audio := false, false
	for _, stream := range result.Streams {
		video = video || stream.Codec == "h264" && stream.Height == 720
		audio = audio || stream.Codec == "aac"
	}
	if !video || !audio {
		t.Fatal("expected H.264 720p and generated AAC", string(raw))
	}
}
