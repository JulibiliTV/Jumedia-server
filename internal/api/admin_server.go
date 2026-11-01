package api

import (
	"context"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"jumedia-server/internal/config"
	"jumedia-server/internal/emby"
)

type serverValues struct {
	publicOrigin             string
	ffmpeg, ffprobe, encoder string
}

type serverRuntime struct {
	mu           sync.RWMutex // protects effective runtime values
	startup      serverValues
	boot         config.Config
	sourceKeySet bool
	current      serverValues
	initErr      error
}

func (s *Server) initServerSettings() {
	cfg := s.Config
	startup := serverValues{publicOrigin: cfg.PublicOrigin,
		ffmpeg: adminDefault(os.Getenv("JUMEDIA_FFMPEG"), "ffmpeg"), ffprobe: adminDefault(os.Getenv("JUMEDIA_FFPROBE"), "ffprobe"), encoder: adminDefault(os.Getenv("JUMEDIA_FFMPEG_VIDEO_ENCODER"), "libx264")}
	s.runtime = &serverRuntime{startup: startup, boot: cfg, sourceKeySet: os.Getenv("JUMEDIA_DATASOURCE_KEY") != "", current: startup}
	settings, err := s.Store.Settings(context.Background())
	s.runtime.initErr = err
	if err == nil {
		s.applyServerSettings(settings)
	}
}

// The caller serializes settings transactions with liveCaches.mu. Publish only
// committed values; no database reads or network work while holding this lock.
func (s *Server) applyServerSettings(settings map[string]any) {
	r := s.runtime
	next := r.startup
	for key, dst := range map[string]*string{
		"serverPublicOrigin": &next.publicOrigin,
		"serverFfmpeg":       &next.ffmpeg, "serverFfprobe": &next.ffprobe, "serverVideoEncoder": &next.encoder,
	} {
		if v, ok := settings[key].(string); ok && v != "" {
			*dst = v
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = next
}

func (s *Server) serverSnapshot() (serverValues, *emby.Client) {
	r := s.runtime
	r.mu.RLock()
	defer r.mu.RUnlock()
	// The demo Emby client is created once from startup parameters in New.
	return r.current, s.Emby
}
func (s *Server) embyClient() *emby.Client { _, client := s.serverSnapshot(); return client }

var serverCommand = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func serverEncoderValid(value string) bool {
	switch value {
	case "libx264", "h264_videotoolbox", "h264_nvenc", "h264_qsv":
		return true
	}
	return false
}
func serverInput(p map[string]string) error {
	if v, ok := p["serverPublicOrigin"]; ok {
		normalized, err := serverOrigin(v)
		if err != nil {
			return err
		}
		p["serverPublicOrigin"] = normalized
	}
	for _, field := range [][2]string{{"serverFfmpeg", "ffmpeg"}, {"serverFfprobe", "ffprobe"}} {
		key, label := field[0], field[1]
		if v, ok := p[key]; ok {
			v = Trim(v)
			p[key] = v
			absolute := strings.HasPrefix(v, "/") && len(v) > 1 && !strings.ContainsAny(v, "\x00\n\r")
			if v != "" && !absolute && !serverCommand.MatchString(v) {
				return Fail(-400, label+" 路径需为绝对路径或命令名")
			}
		}
	}
	if v := p["serverVideoEncoder"]; v != "" && !serverEncoderValid(v) {
		return Fail(-400, "不支持的视频编码器")
	}
	return nil
}

// pgx accepts all ASCII whitespace (including VT), and a backslash can
// escape even a newline inside a quoted or unquoted keyword value.
var serverDSNSecret = regexp.MustCompile(`(?is)(\b(?:password|sslpassword)[\s\x0b]*=[\s\x0b]*)('(?:\\.|[^'])*'|(?:\\.|[^\s\x0b])*)`)

func serverRedactDSN(dsn string) string {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "已设置（连接信息无法解析）"
		}
		if u.User != nil {
			if _, set := u.User.Password(); set {
				u.User = url.UserPassword(u.User.Username(), "•••")
			}
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "已设置（连接信息无法解析）"
		}
		for k := range q {
			if strings.EqualFold(k, "password") || strings.EqualFold(k, "sslpassword") {
				q.Set(k, "•••")
			}
		}
		u.RawQuery = q.Encode()
		return strings.ReplaceAll(u.String(), "%E2%80%A2", "•")
	}
	return serverDSNSecret.ReplaceAllString(dsn, "${1}'•••'")
}

func (s *Server) adminServer(c *Ctx) (any, error) {
	r := s.runtime
	cfg := r.boot
	v := r.startup
	seed, key := "否", "未设置"
	if cfg.Seed {
		seed = "是"
	}
	if r.sourceKeySet {
		key = "已设置"
	}
	boot := []map[string]string{}
	for _, entry := range [][3]string{
		{"JUMEDIA_ADDR", "监听地址", cfg.Addr}, {"JUMEDIA_DB_DRIVER", "数据库", cfg.DBDriver}, {"JUMEDIA_DB_DSN", "数据库连接", serverRedactDSN(cfg.DBDSN)},
		{"JUMEDIA_DATA_DIR", "数据目录", cfg.DataDir}, {"JUMEDIA_WEB_DIR", "网页目录", cfg.WebDir}, {"JUMEDIA_STATIC_DIR", "静态资源目录", cfg.StaticDir},
		{"JUMEDIA_SEED", "空库写入演示数据", seed}, {"JUMEDIA_DATASOURCE_KEY", "数据源密钥", key},
	} {
		boot = append(boot, map[string]string{"key": entry[0], "label": entry[1], "value": entry[2]})
	}
	data := map[string]any{"boot": boot, "startup": map[string]any{"publicOrigin": v.publicOrigin, "ffmpeg": v.ffmpeg, "ffprobe": v.ffprobe, "videoEncoder": v.encoder}}
	// Configuration paths are literal values, not media URLs for response rewriting.
	body, err := jsonServerResponse(data)
	if err != nil {
		return nil, err
	}
	return Text(body, 200, "application/json; charset=utf-8"), nil
}
func jsonServerResponse(data any) (string, error) {
	return serviceJSON(map[string]any{"code": 0, "message": "0", "ttl": 1, "data": data})
}
