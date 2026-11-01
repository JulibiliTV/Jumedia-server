package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

type liveCacheProbe struct {
	Streams []struct {
		Index       int    `json:"index"`
		Type        string `json:"codec_type"`
		Height      int    `json:"height"`
		Transfer    string `json:"color_transfer"`
		Disposition struct {
			Default int `json:"default"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

var liveCacheLocalURL = regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+/[^\s'"\]]+`)

func liveCacheError(message string) string {
	lines := strings.Split(strings.TrimSpace(message), "\n")
	message = strings.TrimSpace(lines[len(lines)-1])
	message = liveCacheLocalURL.ReplaceAllString(message, "[本地媒体]")
	r := []rune(message)
	if len(r) > 200 {
		message = string(r[:200])
	}
	return message
}
func liveRelayableMedia(media string) bool {
	_, _, _, _, ok := emby.ParseDSRef(media)
	if ok {
		return true
	}
	_, _, ok = emby.ParseRef(media)
	return ok
}
func (s *Server) liveCacheInput(ctx context.Context, media string) (string, func(), error) {
	return s.liveCacheInputClient(ctx, media, s.embyClient())
}
func (s *Server) liveCacheInputClient(ctx context.Context, media string, client *emby.Client) (string, func(), error) {
	item, source, ok := emby.ParseRef(media)
	container := "mkv"
	if ds, it, src, mode, valid := emby.ParseDSRef(media); valid {
		record, e := store.DataSourceByID(ctx, s.Store.DB, ds)
		if e != nil {
			return "", nil, errors.New("数据源不存在")
		}
		client, e = s.sourceClient(ctx, record)
		if e != nil {
			return "", nil, errors.New("无法连接数据源")
		}
		item, source, ok = it, src, true
		if strings.HasPrefix(mode, "raw-") {
			container = strings.TrimPrefix(mode, "raw-")
		} else if mode == "direct" {
			container = "mp4"
		}
	}
	if !ok {
		return "", nil, errors.New("视频原始文件不存在")
	}
	token := make([]byte, 24)
	if _, e := rand.Read(token); e != nil {
		return "", nil, e
	}
	path := "/" + hex.EncodeToString(token)
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return "", nil, e
	}
	proxy := &http.Server{ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path || (r.Method != "GET" && r.Method != "HEAD") {
			http.NotFound(w, r)
			return
		}
		res, e := client.RawStream(r.Context(), item, source, container, r.Header)
		if e != nil {
			http.Error(w, "原始文件读取失败", 502)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
			http.Error(w, "原始文件读取失败", 502)
			return
		}
		for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
			if v := res.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.WriteHeader(res.StatusCode)
		if r.Method != "HEAD" {
			_, _ = io.Copy(w, res.Body)
		}
	})}
	done := make(chan struct{})
	go func() { defer close(done); _ = proxy.Serve(listener) }()
	return "http://" + listener.Addr().String() + path, func() { _ = proxy.Close(); <-done }, nil
}
func liveCacheArgs(x *liveCache, p liveCacheProbe, input, encoder string, toneMap bool) ([]string, error) {
	height := 0
	transfer := ""
	audio := -1
	for _, stream := range p.Streams {
		if stream.Type == "video" && height == 0 {
			height = stream.Height
			transfer = stream.Transfer
		}
		if stream.Type == "audio" && (audio < 0 || stream.Disposition.Default == 1) {
			audio = stream.Index
			if stream.Disposition.Default == 1 {
				break
			}
		}
	}
	// Audio can precede video in arbitrary containers.
	if height == 0 {
		for _, stream := range p.Streams {
			if stream.Type == "video" {
				height = stream.Height
				transfer = stream.Transfer
				break
			}
		}
	}
	if height <= 0 {
		return nil, errors.New("原始文件没有视频流")
	}
	height = min(height, x.Height)
	height = max(2, height/2*2)
	args := []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-i", input}
	if audio < 0 {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000")
	}
	args = append(args, "-map", "0:v:0")
	if audio >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", audio))
	} else {
		args = append(args, "-map", "1:a:0", "-shortest")
	}
	vf := fmt.Sprintf("scale=-2:%d,format=yuv420p", height)
	if toneMap && (transfer == "smpte2084" || transfer == "arib-std-b67") {
		vf = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv," + vf
	}
	args = append(args, "-vf", vf, "-c:v", encoder, "-pix_fmt", "yuv420p")
	if encoder == "libx264" {
		args = append(args, "-preset", "veryfast", "-crf", "21", "-profile:v", "high")
	}
	rate := 4
	if height > 720 {
		rate = 8
	}
	if height > 1080 {
		rate = 12
	}
	if height > 1440 {
		rate = 20
	}
	args = append(args, "-maxrate", fmt.Sprintf("%dM", rate), "-bufsize", fmt.Sprintf("%dM", rate*2), "-force_key_frames", "expr:gte(t,n_forced*2)", "-sc_threshold", "0", "-c:a", "aac", "-b:a", "160k", "-ar", "48000", "-ac", "2", "-sn", "-dn", "-f", "hls", "-hls_time", "6", "-hls_playlist_type", "vod", "-hls_segment_type", "mpegts", "-hls_segment_filename", filepath.Join(x.Dir+".tmp", "seg%05d.ts"), "-progress", "pipe:1", filepath.Join(x.Dir+".tmp", "index.m3u8"))
	return args, nil
}

// Bounded stderr avoids retaining unbounded diagnostics from an unhealthy process.
type liveCacheTail struct{ data []byte }

func (w *liveCacheTail) Write(p []byte) (int, error) {
	n := len(p)
	w.data = append(w.data, p...)
	if len(w.data) > 8192 {
		w.data = append([]byte(nil), w.data[len(w.data)-8192:]...)
	}
	return n, nil
}

type liveCacheProgress struct {
	mu        sync.Mutex
	buf       string
	last      time.Time
	lastWrite time.Time
	value     int64
	duration  float64
	update    func(int)
}

func (w *liveCacheProgress) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(p)
	for {
		i := strings.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(w.buf[:i])
		w.buf = w.buf[i+1:]
		if strings.HasPrefix(line, "out_time_us=") {
			n, e := strconv.ParseInt(strings.TrimPrefix(line, "out_time_us="), 10, 64)
			if e == nil && n > w.value {
				w.value = n
				w.last = time.Now()
				if time.Since(w.lastWrite) >= 2*time.Second && w.duration > 0 {
					w.update(min(99, max(0, int(float64(n)/1e6/w.duration*100))))
					w.lastWrite = time.Now()
				}
			}
		}
	}
	if len(w.buf) > 8192 {
		w.buf = ""
	}
	return len(p), nil
}
func (s *Server) transcodeLiveCache(ctx context.Context, x *liveCache, cfg serverValues, client *emby.Client, stallTimeout time.Duration) (int64, error) {
	binary, e := exec.LookPath(cfg.ffmpeg)
	if e != nil {
		return 0, errors.New("服务器未安装 ffmpeg")
	}
	probe, e := exec.LookPath(cfg.ffprobe)
	if e != nil {
		return 0, errors.New("服务器未安装 ffprobe")
	}
	if !liveCacheSafeDir(x) {
		return 0, errors.New("缓存目录无效")
	}
	if e = liveCacheRemove(x); e != nil {
		return 0, e
	}
	if e = os.MkdirAll(x.Dir+".tmp", 0755); e != nil {
		return 0, e
	}
	input, closeInput, e := s.liveCacheInputClient(ctx, x.Media, client)
	if e != nil {
		return 0, e
	}
	defer closeInput()
	probeCtx, cancelProbe := context.WithTimeout(ctx, max(30*time.Second, stallTimeout))
	defer cancelProbe()
	cmd := exec.CommandContext(probeCtx, probe, "-v", "error", "-show_streams", "-show_format", "-of", "json", input)
	cmd.WaitDelay = time.Second
	var stderr liveCacheTail
	cmd.Stderr = &stderr
	raw, e := cmd.Output()
	if e != nil {
		if len(stderr.data) > 0 {
			return 0, errors.New(liveCacheError(string(stderr.data)))
		}
		return 0, errors.New("无法读取原始视频信息")
	}
	var p liveCacheProbe
	if e = json.Unmarshal(raw, &p); e != nil {
		return 0, errors.New("无法读取原始视频信息")
	}
	if duration, e := strconv.ParseFloat(p.Format.Duration, 64); e == nil && duration > 0 {
		x.Duration = duration
	}
	hdr := false
	for _, stream := range p.Streams {
		if stream.Type == "video" {
			hdr = stream.Transfer == "smpte2084" || stream.Transfer == "arib-std-b67"
			break
		}
	}
	toneMap := false
	if hdr {
		filterCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		filters, e := exec.CommandContext(filterCtx, binary, "-hide_banner", "-filters").Output()
		cancel()
		toneMap = e == nil && bytes.Contains(filters, []byte(" zscale ")) && bytes.Contains(filters, []byte(" tonemap "))
		if !toneMap {
			s.Logger.Warn("HDR 转码缺少 zscale/tonemap，直接转换", "key", x.Key)
		}
	}
	args, e := liveCacheArgs(x, p, input, cfg.encoder, toneMap)
	if e != nil {
		return 0, e
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd = exec.CommandContext(work, binary, args...)
	cmd.WaitDelay = time.Second
	stderr = liveCacheTail{}
	cmd.Stderr = &stderr
	progress := &liveCacheProgress{last: time.Now(), duration: x.Duration, update: func(n int) {
		_, e := s.Store.DB.ExecContext(work, "UPDATE live_caches SET progress=?,mtime=? WHERE key=? AND status='running'", n, time.Now().Unix(), x.Key)
		if e != nil && work.Err() == nil {
			s.Logger.Error("live cache progress", "key", x.Key, "error", e)
		}
	}}
	cmd.Stdout = progress
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	interval := min(time.Second, stallTimeout/4)
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	stalled := false
	for {
		select {
		case e = <-done:
			if stalled {
				return 0, errors.New("转码超过 10 分钟没有进度，请重试")
			}
			if e != nil {
				if len(stderr.data) > 0 {
					return 0, errors.New(liveCacheError(string(stderr.data)))
				}
				return 0, e
			}
			if _, e = os.Stat(filepath.Join(x.Dir+".tmp", "index.m3u8")); e != nil {
				return 0, errors.New("转码未生成播放列表")
			}
			return liveCacheSize(x.Dir + ".tmp")
		case <-ticker.C:
			progress.mu.Lock()
			last := progress.last
			progress.mu.Unlock()
			if time.Since(last) >= stallTimeout {
				stalled = true
				cancel()
			}
		}
	}
}
