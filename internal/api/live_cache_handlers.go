package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"jumedia-server/internal/db"
)

func (s *Server) registerLiveCaches() {
	s.Mux.HandleFunc("GET /live-cache/{key}/{file}", s.serveLiveCache)
	s.handle("/x/admin/live/caches", s.adminLiveCaches)
	for path, h := range map[string]Handler{"/x/admin/live/cache/retry": s.adminLiveCacheRetry, "/x/admin/live/cache/delete": s.adminLiveCacheDelete} {
		s.handle(path, func(c *Ctx) (any, error) { s.liveCaches.mu.Lock(); defer s.liveCaches.mu.Unlock(); return h(c) })
	}
}
func (s *Server) serveLiveCache(w http.ResponseWriter, r *http.Request) {
	key, name := r.PathValue("key"), r.PathValue("file")
	if !liveCacheKeyPattern.MatchString(key) || !liveCacheFilePattern.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	x, e := liveCacheByKey(r.Context(), s.Store.DB, key)
	if e != nil {
		http.Error(w, "cache lookup failed", 500)
		return
	}
	if x == nil || x.Status != "ready" || !liveCacheSafeDir(x) {
		http.NotFound(w, r)
		return
	}
	root, e := os.OpenRoot(x.Dir)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	f, e := root.Open(name)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if name == "index.m3u8" {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	}
	http.ServeContent(w, r, name, st.ModTime(), f)
}
func (l *liveAPI) sourceRetry(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	r, e := l.ownRoom(c, u.MID)
	if e != nil {
		if adminMissing(e) {
			return nil, Fail(-404, "当前直播源没有转码任务")
		}
		if f, ok := e.(*Failure); ok && f.Code == -404 {
			return nil, Fail(-404, "当前直播源没有转码任务")
		}
		return nil, e
	}
	x, e := l.s.roomCache(c.Context(), l.s.Store.DB, r)
	if e != nil {
		return nil, e
	}
	if x == nil {
		return nil, Fail(-404, "当前直播源没有转码任务")
	}
	if e = liveCacheRetry(c.Context(), l.s.Store.DB, x); e != nil {
		return nil, e
	}
	src, e := l.s.liveSourceJSON(c, r)
	return liveMap{"source": src}, e
}
func (s *Server) adminLiveCaches(c *Ctx) (any, error) {
	if _, e := c.Admin(); e != nil {
		return nil, e
	}
	xs, e := liveCacheList(c.Context(), s.Store.DB)
	if e != nil {
		return nil, e
	}
	rooms, e := s.Store.ListLiveRoom(c.Context(), "")
	if e != nil {
		return nil, e
	}
	dir, height, e := s.liveCacheSettings(c.Context(), s.Store.DB)
	if e != nil {
		return nil, e
	}
	pn, ps := NumberUnicodeGo(c.P["pn"], 1), NumberUnicodeGo(c.P["ps"], 20)
	out := []map[string]any{}
	for _, x := range Page(xs, pn, ps) {
		v := liveCacheJSON(&x).(map[string]any)
		v["aid"], v["cid"], v["title"], v["part"], v["pic"] = x.AID, x.CID, "", "", ""
		v["duration"], v["dir"], v["ctime"], v["mtime"], v["unused_since"] = x.Duration, x.Dir, x.Ctime, x.Mtime, x.UnusedSince
		a, e := s.Store.ArchiveByID(c.Context(), x.AID)
		if e != nil && !adminMissing(e) {
			return nil, e
		}
		if a != nil {
			v["title"], v["pic"] = a.Title, a.Pic
			for _, p := range a.Pages {
				if p.CID == x.CID {
					v["part"] = p.Part
				}
			}
		}
		refs := []map[string]any{}
		for _, r := range rooms {
			if r.Source != nil && r.Source.Cache == x.Key {
				refs = append(refs, map[string]any{"room_id": r.RoomID, "title": r.Title})
			}
		}
		v["rooms"] = refs
		out = append(out, v)
	}
	var bytes int64
	for _, x := range xs {
		bytes += x.Size
	}
	return map[string]any{"bytes": bytes, "list": out, "page": map[string]any{"pn": pn, "ps": ps, "total": len(xs)}, "dir": dir, "height": height, "keep_hours": 24}, nil
}
func (s *Server) adminLiveCacheRetry(c *Ctx) (any, error) {
	if _, e := c.Admin(); e != nil {
		return nil, e
	}
	var out any
	e := s.WriteTx(c, func(tx *db.Tx) error {
		x, e := liveCacheByKey(c.Context(), tx, c.P["key"])
		if e != nil {
			return e
		}
		if x == nil {
			return Fail(-404, "缓存不存在")
		}
		if e = liveCacheRetry(c.Context(), tx, x); e != nil {
			return e
		}
		out = liveCacheJSON(x)
		return s.LogTx(c.Context(), tx, ActorID(c), "重试转码", fmt.Sprintf("av%d", x.AID))
	})
	return out, e
}
func (s *Server) adminLiveCacheDelete(c *Ctx) (any, error) {
	if _, e := c.Admin(); e != nil {
		return nil, e
	}
	x, e := liveCacheByKey(c.Context(), s.Store.DB, c.P["key"])
	if e != nil {
		return nil, e
	}
	if x == nil {
		return nil, Fail(-404, "缓存不存在")
	}
	used, e := liveCacheReferences(c.Context(), s.Store.DB)
	if e != nil {
		return nil, e
	}
	if used[x.Key] {
		return nil, Fail(-400, "仍有直播间在使用该缓存，请先更换它们的直播源")
	}
	s.cancelLiveCache(x.Key)
	var deleted *liveCache
	e = s.WriteTx(c, func(tx *db.Tx) error {
		x, e := liveCacheByKey(c.Context(), tx, c.P["key"])
		if e != nil {
			return e
		}
		if x == nil {
			return Fail(-404, "缓存不存在")
		}
		used, e := liveCacheReferences(c.Context(), tx)
		if e != nil {
			return e
		}
		if used[x.Key] {
			return Fail(-400, "仍有直播间在使用该缓存，请先更换它们的直播源")
		}
		// Recheck references in the deletion transaction.
		deleted = x
		if _, e = tx.ExecContext(c.Context(), "DELETE FROM live_caches WHERE key=?", x.Key); e != nil {
			return e
		}
		return s.LogTx(c.Context(), tx, ActorID(c), "删除转码缓存", fmt.Sprintf("av%d", x.AID))
	})
	if e != nil {
		return nil, e
	}
	return nil, liveCacheRemove(deleted)
}
func (s *Server) validateLiveCacheSettings(c *Ctx) error {
	if value, ok := c.P["liveCacheDir"]; ok {
		dir := strings.TrimSpace(value)
		c.P["liveCacheDir"] = dir
		if dir != "" && !filepath.IsAbs(dir) {
			return Fail(-400, "缓存目录必须是绝对路径")
		}
		if dir == "" {
			dir = filepath.Join(s.Config.DataDir, "live-cache")
		}
		e := os.MkdirAll(dir, 0755)
		if e == nil {
			var f *os.File
			f, e = os.CreateTemp(dir, ".jumedia-write-")
			if e == nil {
				_, e = f.WriteString("probe")
				closeErr := f.Close()
				removeErr := os.Remove(f.Name())
				if e == nil {
					e = closeErr
				}
				if e == nil {
					e = removeErr
				}
			}
		}
		if e != nil {
			return Fail(-400, "缓存目录不可写："+e.Error())
		}
	}
	if value, ok := c.P["liveTranscodeHeight"]; ok {
		switch RawNumber(value) {
		case 720, 1080, 1440, 2160:
		default:
			return Fail(-400, "转码分辨率无效")
		}
	}
	return nil
}
