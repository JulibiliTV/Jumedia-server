// Package api provides the shared request pipeline and domain registration hooks.
package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"jumedia-server/internal/config"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

type Server struct {
	runtime    *serverRuntime
	liveCaches *liveCacheRuntime
	sources    *sourceRuntime
	liveChat   *liveChatHub
	Store      *store.Store
	Config     config.Config
	Render     *render.Renderer
	Mux        *http.ServeMux
	Logger     *slog.Logger
}
type Ctx struct {
	W       http.ResponseWriter
	R       *http.Request
	P       map[string]string
	Form    *multipart.Form
	Me      *store.User
	Session *store.Session
	Server  *Server
}

func (c *Ctx) Context() context.Context { return c.R.Context() }

type Handler func(*Ctx) (any, error)

func New(s *store.Store, cfg config.Config) *Server {
	srv := &Server{Store: s, Config: cfg, Render: render.New(s), Mux: http.NewServeMux(), Logger: slog.Default()}
	srv.liveCaches = newLiveCacheRuntime()
	srv.initServerSettings()
	srv.registerLiveCaches()
	srv.sources = newSourceRuntime()
	srv.registerDataSources()
	srv.registerSourceMedia()
	srv.registerFoundation()
	srv.registerBPlan()
	srv.registerAccount()
	srv.registerDiscover()
	srv.registerSpace()
	srv.registerCommunity()
	srv.registerPgc()
	srv.liveChat = newLiveChatHub(srv)
	srv.registerLive()
	srv.registerCreator()
	srv.registerAdmin()
	if cfg.StaticDir != "" {
		srv.Mux.Handle("/mock/", http.StripPrefix("/mock/", http.FileServer(http.Dir(filepath.Join(cfg.StaticDir, "mock")))))
	}
	srv.Mux.Handle("/upload/", http.StripPrefix("/upload/", http.FileServer(http.Dir(cfg.UploadDir()))))
	srv.Mux.HandleFunc("/", srv.fallback)
	return srv
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if allowedOrigin(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.promoteScheduled(r.Context()); err != nil {
		writeResponse(w, nil, err)
		return
	}
	s.Mux.ServeHTTP(w, r)
}
func allowedOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "http" && u.Hostname() == "localhost" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func (s *Server) handle(path string, h Handler) {
	s.Mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Logger.Error("request panic", "path", r.URL.Path, "error", fmt.Sprint(v))
				writeResponse(w, nil, fmt.Errorf("panic: %v", v))
			}
		}()
		c := &Ctx{W: w, R: r, P: map[string]string{}, Server: s}
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				c.P[k] = v[len(v)-1]
			}
		}
		content, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		var err error
		if content == "multipart/form-data" {
			err = r.ParseMultipartForm(32 << 20)
			c.Form = r.MultipartForm
			if c.Form != nil {
				defer c.Form.RemoveAll()
				for k, v := range c.Form.Value {
					if len(v) > 0 {
						c.P[k] = v[len(v)-1]
					}
				}
			}
		} else if content == "application/x-www-form-urlencoded" {
			err = r.ParseForm()
			for k, v := range r.PostForm {
				if len(v) > 0 {
					c.P[k] = v[len(v)-1]
				}
			}
		}
		if err != nil {
			writeResponse(w, nil, Fail(-400, "请求参数错误"))
			return
		}
		token := CookieValue(r, "SESSDATA")
		if token != "" || c.P["access_key"] != "" {
			c.Session, err = s.Store.SessionByCredentials(r.Context(), token, c.P["access_key"])
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
			}
		}
		if err == nil && c.Session != nil {
			c.Me, err = s.Store.UserByID(r.Context(), c.Session.MID)
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
			}
		}
		if err != nil {
			s.Logger.Error("session lookup", "error", err)
			writeResponse(w, nil, err)
			return
		}
		if c.Me != nil && hasInlineResourceParams(c.P) {
			writeResponse(w, nil, Fail(-400, "不支持内嵌资源，请上传文件"), s.Origin(r))
			return
		}
		value, err := h(c)
		if err != nil {
			var f *Failure
			if !errors.As(err, &f) {
				s.Logger.Error("request failed", "path", r.URL.Path, "error", err)
			}
		}
		writeResponse(w, value, err, s.Origin(r))
	})
}
func (s *Server) Origin(r *http.Request) string {
	if s.runtime != nil {
		cfg := s.serverSnapshot()
		if cfg.publicOrigin != "" {
			return strings.TrimRight(cfg.publicOrigin, "/")
		}
	}
	if s.Config.PublicOrigin != "" {
		return strings.TrimRight(s.Config.PublicOrigin, "/")
	}
	first := func(v string) string { return strings.TrimSpace(strings.Split(v, ",")[0]) }
	proto := "http"
	if r.TLS != nil {
		proto = "https"
	}
	if p := first(r.Header.Get("X-Forwarded-Proto")); p == "http" || p == "https" {
		proto = p
	}
	host := first(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host
}
func (s *Server) Log(c *Ctx, action, target string) error {
	var mid int64
	if c.Me != nil {
		mid = c.Me.MID
	}
	return s.Store.AddLog(c.Context(), mid, action, target)
}
func (c *Ctx) Login() (*store.User, error) {
	if c.Me == nil {
		return nil, Fail(-101, "账号未登录")
	}
	if c.Me.Status != 0 {
		return nil, Fail(-403, "账号已被封禁")
	}
	return c.Me, nil
}
func (c *Ctx) CSRF() (*store.User, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	csrf := c.P["csrf"]
	if csrf == "" {
		csrf = c.P["biliCSRF"]
	}
	if csrf == "" {
		return nil, Fail(-111, "csrf 校验失败")
	}
	var n int
	if err = c.Server.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM sessions WHERE mid=? AND csrf=?", u.MID, csrf).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, Fail(-111, "csrf 校验失败")
	}
	return u, nil
}
func (c *Ctx) Admin() (*store.User, error) {
	var u *store.User
	var err error
	if c.R.Method == http.MethodGet {
		u, err = c.Login()
	} else {
		u, err = c.CSRF()
	}
	if err != nil {
		return nil, err
	}
	if u.Role != "admin" {
		return nil, Fail(-403, "需要管理员权限")
	}
	return u, nil
}
func (s *Server) fallback(w http.ResponseWriter, r *http.Request) {
	for _, prefix := range []string{"/x/", "/pgc/", "/bfs/", "/mock/", "/upload/", "/live-relay/", "/media/", "/login/", "/api/"} {
		if strings.HasPrefix(r.URL.Path, prefix) {
			writeResponse(w, nil, Fail(-404, "接口不存在"))
			return
		}
	}
	if s.Config.WebDir == "" {
		writeResponse(w, nil, Fail(-404, "接口不存在"))
		return
	}
	// os.Root prevents both traversal and symlinks escaping the configured SPA.
	root, err := os.OpenRoot(s.Config.WebDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	f, err := root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		f, err = root.Open("index.html")
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	// Documents and worker scripts need matching isolation policies for the
	// player's shared-memory pipelines. Public cross-origin images remain usable.
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Embedder-Policy", "credentialless")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
