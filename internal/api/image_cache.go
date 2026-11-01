package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"jumedia-server/internal/store"
)

type imageCacheRuntime struct {
	// Settings changes and clear wait for in-flight responses, so old requests
	// cannot publish into a previous directory or refill a just-cleared cache.
	gate    sync.RWMutex
	mu      sync.Mutex
	current *imageCacheDirectory
	flights map[string]*imageCacheFlight
	wg      sync.WaitGroup
	closed  bool
	now     func() time.Time
}
type imageCacheDirectory struct {
	dir              string
	max              int64
	enabled          bool
	entries          map[string]imageCacheEntry
	totals           imageCacheCounts
	scanned, working bool
}
type imageCacheConfig struct {
	enabled bool
	dir     string
	maxMB   int64
}
type imageCacheCounts struct {
	Files int64 `json:"files"`
	Bytes int64 `json:"bytes"`
}

func (d *imageCacheDirectory) counts() imageCacheCounts { return d.totals }
func (d *imageCacheDirectory) record(e imageCacheEntry) {
	if old, ok := d.entries[e.name]; ok {
		d.totals.Bytes -= old.size
	} else if len(e.name) == 64 {
		d.totals.Files++
	}
	d.entries[e.name] = e
	d.totals.Bytes += e.size
}
func (d *imageCacheDirectory) forget(name string) {
	if old, ok := d.entries[name]; ok {
		d.totals.Bytes -= old.size
		if len(name) == 64 {
			d.totals.Files--
		}
		delete(d.entries, name)
	}
}
func (s *Server) imageCacheSettings(ctx context.Context) (imageCacheConfig, error) {
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return imageCacheConfig{}, err
	}
	cfg := imageCacheConfig{enabled: Truthy(settings["imageCacheEnabled"]), maxMB: 1024}
	cfg.dir, _ = settings["imageCacheDir"].(string)
	if cfg.dir == "" {
		cfg.dir, err = imageCacheDefaultDir(s.Config.DataDir)
	} else {
		cfg.dir, err = filepath.Abs(cfg.dir)
	}
	if n, ok := settings["imageCacheMaxMB"].(float64); ok && n >= 64 && n <= 1048576 {
		cfg.maxMB = int64(n)
	}
	return cfg, err
}

// Caller holds gate (shared for requests, exclusive for settings and clearing).
func (s *Server) imageCacheDirectory(cfg imageCacheConfig) *imageCacheDirectory {
	rt := s.sources.images
	rt.mu.Lock()
	defer rt.mu.Unlock()
	d := rt.current
	if d == nil || d.dir != cfg.dir {
		d = &imageCacheDirectory{dir: cfg.dir, entries: map[string]imageCacheEntry{}}
		rt.current = d
	}
	d.max = cfg.maxMB << 20
	d.enabled = cfg.enabled
	s.scheduleImageCacheLocked(d)
	return d
}
func (s *Server) scheduleImageCacheLocked(d *imageCacheDirectory) {
	rt := s.sources.images
	if rt.closed || d.working || (d.scanned && (!d.enabled || d.counts().Bytes <= d.max)) {
		return
	}
	d.working = true
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		rt.gate.RLock()
		defer rt.gate.RUnlock()
		rt.mu.Lock()
		defer rt.mu.Unlock()
		defer func() { d.working = false }()
		if rt.current != d || rt.closed {
			return
		}
		if !d.scanned {
			d.entries = map[string]imageCacheEntry{}
			d.totals = imageCacheCounts{}
			err := imageCacheWalk(d.dir, func(_ *os.File, name string, st os.FileInfo) error {
				d.record(imageCacheEntry{name, st.Size(), st.ModTime()})
				return nil
			}, false)
			if err != nil {
				s.Logger.Warn("scan image cache", "error", err)
			}
			d.scanned = true
		}
		if !d.enabled || d.counts().Bytes <= d.max {
			return
		}
		entries := make([]imageCacheEntry, 0, len(d.entries))
		for _, e := range d.entries {
			entries = append(entries, e)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].accessed.Before(entries[j].accessed) })
		total := d.counts().Bytes
		for _, e := range entries {
			if total <= d.max*9/10 {
				break
			}
			if err := imageCacheRemove(d.dir, e.name); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.Logger.Warn("evict image cache", "error", err)
				continue
			}
			d.forget(e.name)
			total -= e.size
		}
	}()
}
func (s *Server) clearImageCache(d *imageCacheDirectory) (imageCacheCounts, error) {
	rt := s.sources.images
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var deleted imageCacheCounts
	err := imageCacheWalk(d.dir, func(shard *os.File, name string, st os.FileInfo) error {
		if err := unix.Unlinkat(int(shard.Fd()), name, 0); err != nil {
			return err
		}
		if len(name) == 64 {
			deleted.Files++
		}
		deleted.Bytes += st.Size()
		d.forget(name)
		return nil
	}, true)
	if err == nil {
		d.entries = map[string]imageCacheEntry{}
		d.totals = imageCacheCounts{}
		d.scanned = true
	}
	return deleted, err
}
func (s *Server) registerImageCache() {
	s.sources.images = &imageCacheRuntime{now: time.Now, flights: map[string]*imageCacheFlight{}}
	s.handle("GET /x/admin/imagecache", s.adminImageCache)
	s.handle("POST /x/admin/imagecache/clear", s.adminImageCacheClear)
	if cfg, err := s.imageCacheSettings(context.Background()); err == nil {
		s.imageCacheDirectory(cfg)
	} else {
		s.Logger.Warn("initialize image cache", "error", err)
	}
}
func (s *Server) adminImageCache(c *Ctx) (any, error) {
	if _, err := c.Admin(); err != nil {
		return nil, err
	}
	rt := s.sources.images
	rt.gate.RLock()
	defer rt.gate.RUnlock()
	cfg, err := s.imageCacheSettings(c.Context())
	if err != nil {
		return nil, err
	}
	d := s.imageCacheDirectory(cfg)
	rt.mu.Lock()
	counts := d.counts()
	rt.mu.Unlock()
	return map[string]any{"enabled": cfg.enabled, "dir": cfg.dir, "max_mb": cfg.maxMB, "files": counts.Files, "bytes": counts.Bytes}, nil
}
func (s *Server) adminImageCacheClear(c *Ctx) (any, error) {
	if _, err := c.Admin(); err != nil {
		return nil, err
	}
	rt := s.sources.images
	rt.gate.Lock()
	defer rt.gate.Unlock()
	cfg, err := s.imageCacheSettings(c.Context())
	if err != nil {
		return nil, err
	}
	counts, err := s.clearImageCache(s.imageCacheDirectory(cfg))
	if err != nil {
		return nil, err
	}
	return counts, s.Log(c, "清空图片缓存", "")
}
func (s *Server) validateImageCacheSettings(c *Ctx) error {
	if raw, ok := c.P["imageCacheDir"]; ok {
		dir := strings.TrimSpace(raw)
		c.P["imageCacheDir"] = dir
		if dir != "" && !filepath.IsAbs(dir) {
			return Fail(-400, "图片缓存目录必须是绝对路径")
		}
		if dir == "" {
			var err error
			dir, err = imageCacheDefaultDir(s.Config.DataDir)
			if err != nil {
				return err
			}
		}
		if err := imageCacheProbe(dir); err != nil {
			return Fail(-400, "图片缓存目录不可写："+err.Error())
		}
	}
	if raw, ok := c.P["imageCacheMaxMB"]; ok {
		n := RawNumber(raw)
		if math.IsNaN(n) || n < 64 || n > 1048576 || math.Trunc(n) != n {
			return Fail(-400, "图片缓存上限应为 64–1048576 MB 的整数")
		}
	}
	return nil
}
func imageCacheKey(ds int64, item, kind, tag string) string {
	raw, _ := json.Marshal([]any{ds, item, kind, tag})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func imageCacheHeaders(w http.ResponseWriter, meta imageCacheMetadata, hit bool) {
	w.Header().Set("Content-Type", meta.Type)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("ETag", meta.ETag)
	if hit {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
	}
}
func (s *Server) imageCacheHit(w http.ResponseWriter, r *http.Request, d *imageCacheDirectory, key, tag string) bool {
	rt := s.sources.images
	rt.mu.Lock()
	f, meta, offset, err := imageCacheRead(d.dir, key)
	if err != nil {
		rt.mu.Unlock()
		return false
	}
	st, err := f.Stat()
	if err != nil || tag == "" && rt.now().Sub(meta.Created) >= 24*time.Hour {
		f.Close()
		rt.mu.Unlock()
		return false
	}
	now := rt.now()
	if now.Sub(st.ModTime()) >= time.Hour {
		if err := imageCacheTouch(f, now); err != nil {
			s.Logger.Warn("touch image cache", "error", err)
		} else {
			d.record(imageCacheEntry{key, st.Size(), now})
		}
	}
	rt.mu.Unlock()
	defer f.Close()
	imageCacheHeaders(w, meta, true)
	http.ServeContent(w, r, key, meta.Modified, io.NewSectionReader(f, offset, st.Size()-offset))
	return true
}

type imageCacheFetch struct {
	body  []byte
	meta  imageCacheMetadata
	spool *os.File
	size  int64
}
type imageCacheFlight struct {
	done   chan struct{}
	refs   int
	result *imageCacheFetch
	err    error
}

// Each participant owns a reference until it finishes serving the response.
// Oversized responses use an unlinked, request-lifetime spool outside the cache,
// keeping memory bounded while still fetching only once for concurrent misses.
func (rt *imageCacheRuntime) flight(ctx context.Context, key string, fetch func() (*imageCacheFetch, error)) (*imageCacheFetch, error, func()) {
	rt.mu.Lock()
	f, exists := rt.flights[key]
	if !exists {
		f = &imageCacheFlight{done: make(chan struct{})}
		rt.flights[key] = f
	}
	f.refs++
	rt.mu.Unlock()
	release := func() {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		f.refs--
		if f.refs == 0 {
			delete(rt.flights, key)
			if f.result != nil && f.result.spool != nil {
				f.result.spool.Close()
			}
		}
	}
	if !exists {
		f.result, f.err = fetch()
		close(f.done)
	} else {
		select {
		case <-f.done:
		case <-ctx.Done():
			return nil, ctx.Err(), release
		}
	}
	return f.result, f.err, release
}

// Called only AFTER the data source and sourceMediaAllowed checks. Disabled
// requests return false and use the original streaming proxy unchanged.
func (s *Server) serveCachedSourceImage(w http.ResponseWriter, r *http.Request, ds *store.DataSource, item, kind string) bool {
	rt := s.sources.images
	rt.gate.RLock()
	defer rt.gate.RUnlock()
	cfg, err := s.imageCacheSettings(r.Context())
	if err != nil {
		http.Error(w, "cache settings unavailable", 500)
		return true
	}
	d := s.imageCacheDirectory(cfg)
	if !cfg.enabled {
		return false
	}
	tag := r.URL.Query().Get("tag")
	key := imageCacheKey(ds.ID, item, kind, tag)
	if s.imageCacheHit(w, r, d, key, tag) {
		return true
	}
	// A flight shares the response even when persisting the cache fails.
	streamed := false
	value, err, release := rt.flight(r.Context(), key, func() (*imageCacheFetch, error) {
		// Another flight may have published between our lookup and joining it.
		rt.mu.Lock()
		f, meta, offset, readErr := imageCacheRead(d.dir, key)
		if readErr == nil {
			if tag != "" || rt.now().Sub(meta.Created) < 24*time.Hour {
				st, e := f.Stat()
				if e == nil {
					body, e := io.ReadAll(io.NewSectionReader(f, offset, st.Size()-offset))
					f.Close()
					rt.mu.Unlock()
					return &imageCacheFetch{body: body, meta: meta}, e
				}
			}
			f.Close()
		}
		rt.mu.Unlock()
		client, e := s.sourceClient(r.Context(), ds)
		if e != nil {
			return nil, e
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		res, e := client.Image(ctx, item, kind, tag)
		if e != nil {
			return nil, e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 || !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "image/") {
			return nil, errors.New("Emby image unavailable")
		}
		body, e := io.ReadAll(io.LimitReader(res.Body, imageCacheLimit+1))
		if e != nil {
			return nil, e
		}

		now := rt.now()
		modified, e := http.ParseTime(res.Header.Get("Last-Modified"))
		if e != nil {
			modified = now
		}
		sum := sha256.Sum256(body)
		meta = imageCacheMetadata{Type: res.Header.Get("Content-Type"), Created: now, Modified: modified, ETag: `"` + hex.EncodeToString(sum[:]) + `"`}
		if len(body) > imageCacheLimit {
			// Temporary transport storage, never part of the persistent cache.
			spool, e := os.CreateTemp("", "jumedia-image-response-")
			if e != nil {
				s.Logger.Warn("spool image response", "error", e)
				streamed = true
				imageCacheHeaders(w, meta, false)
				w.Header().Del("ETag")
				_, _ = w.Write(body)
				_, _ = io.Copy(w, res.Body)
				return nil, nil
			}
			if e = os.Remove(spool.Name()); e != nil {
				spool.Close()
				return nil, e
			}
			n, e := spool.Write(body)
			if e == nil {
				var rest int64
				rest, e = io.Copy(spool, res.Body)
				n += int(rest)
			}
			if e != nil {
				spool.Close()
				return nil, e
			}
			meta.ETag = res.Header.Get("ETag")
			return &imageCacheFetch{meta: meta, spool: spool, size: int64(n)}, nil
		}

		rt.mu.Lock()
		size, writeErr := imageCacheWrite(d.dir, key, meta, body, now)
		if writeErr == nil {
			d.record(imageCacheEntry{key, size, now})
			s.scheduleImageCacheLocked(d)
		}
		rt.mu.Unlock()
		if writeErr != nil {
			s.Logger.Warn("write image cache", "error", writeErr)
		}
		return &imageCacheFetch{body: body, meta: meta}, nil
	})
	defer release()
	if err != nil {
		w.Header().Set("X-Cache", "MISS")
		http.Error(w, "Emby image unavailable", 502)
		return true
	}
	if value == nil {
		if !streamed {
			w.Header().Set("X-Cache", "MISS")
		}
		return streamed
	}
	result := value
	imageCacheHeaders(w, result.meta, false)
	if result.spool != nil {
		http.ServeContent(w, r, key, result.meta.Modified, io.NewSectionReader(result.spool, 0, result.size))
	} else {
		http.ServeContent(w, r, key, result.meta.Modified, bytes.NewReader(result.body))
	}
	return true
}
