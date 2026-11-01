package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

type liveCacheRuntime struct {
	mu           sync.Mutex // serializes source changes, publication, deletion and reset
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	activeKey    string
	activeCancel context.CancelFunc
	exited       chan struct{} // closed before publication needs mu
	stallTimeout time.Duration
	initialized  bool
}

func newLiveCacheRuntime() *liveCacheRuntime {
	return &liveCacheRuntime{stallTimeout: 10 * time.Minute}
}
func (s *Server) InitLiveCaches(ctx context.Context) error {
	r := s.liveCaches
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.initialized {
		return nil
	}
	if s.runtime.initErr != nil {
		return s.runtime.initErr
	}
	cfg, _ := s.serverSnapshot()
	if !serverEncoderValid(cfg.encoder) {
		return errors.New("不支持的 JUMEDIA_FFMPEG_VIDEO_ENCODER")
	}
	if e := s.recoverLiveCaches(ctx); e != nil {
		return e
	}
	work, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.initialized = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				s.runNextLiveCache(work)
			}
		}
	}()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				r.mu.Lock()
				e := s.collectLiveCaches(work, time.Now().Unix())
				r.mu.Unlock()
				if e != nil && work.Err() == nil {
					s.Logger.Error("live cache cleanup", "error", e)
				}
			}
		}
	}()
	return nil
}
func (s *Server) CloseLiveCaches() {
	r := s.liveCaches
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// Caller holds mu. The process exits before trying to acquire mu to publish.
func (s *Server) cancelLiveCache(key string) {
	r := s.liveCaches
	if r.activeCancel != nil && (key == "" || key == r.activeKey) {
		r.activeCancel()
		<-r.exited
	}
}
func (s *Server) recoverLiveCaches(ctx context.Context) error {
	caches, e := liveCacheList(ctx, s.Store.DB)
	if e != nil {
		return e
	}

	rooms, e := s.Store.ListLiveRoom(ctx, "")
	if e != nil {
		return e
	}
	return s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if e := LockTx(ctx, tx); e != nil {
			return e
		}
		for _, x := range caches {
			if liveCacheSafeDir(&x) {
				if e = os.RemoveAll(x.Dir + ".tmp"); e != nil {
					return e
				}
			}
			if x.Status == "running" {
				_, e = tx.ExecContext(ctx, "UPDATE live_caches SET status='pending',progress=0,message='',mtime=? WHERE key=?", time.Now().Unix(), x.Key)
			}
			if x.Status == "ready" {
				if _, err := liveCacheRead(&x, "index.m3u8"); err != nil {
					_, e = tx.ExecContext(ctx, "UPDATE live_caches SET status='failed',message=?,mtime=? WHERE key=?", "缓存文件丢失，请重试", time.Now().Unix(), x.Key)
				}
			}
			if e != nil {
				return e
			}
		}
		for _, r := range rooms {
			if r.Source == nil || r.Source.Type != "video" || r.Source.Cache != "" {
				continue
			}
			var page *store.Page
			a, err := store.ArchiveFrom(ctx, tx, r.Source.AID)
			if err != nil && !adminMissing(err) {
				return err
			}
			if a != nil {
				for _, p := range a.Pages {
					if p.CID == r.Source.CID && liveRelayableMedia(p.Media) {
						v := p
						page = &v
						break
					}
				}
			}
			if page == nil {
				r.Source = &store.StreamSource{Type: "push", Loop: true}
				r.LiveStatus = 0
				r.PendingStart = false
			} else {
				x, e := s.liveCacheEnsure(ctx, tx, *page, false)
				if e != nil {
					return e
				}
				r.Source.Cache = x.Key
				r.Source.URL = liveCachePath(x.Key)
				r.Source.Format = "ts"
				r.Source.Kind = "vod"
				if r.LiveStatus == 1 {
					r.LiveStatus = 0
					r.PendingStart = true
				}
				// No worker will finish an already-ready shared cache again.
				// Fulfil the pending start here, using the same ban policy as
				// finishLiveCache, instead of leaving this room waiting forever.
				if r.PendingStart && x.Status == "ready" {
					r.PendingStart = false
					r.LiveStatus = 0
					if !r.Banned {
						r.LiveStatus = 1
						r.LiveTime = time.Now().Unix()
						r.Online = max(1, r.Online)
					}
				}
			}
			raw, e := json.Marshal(r.Source)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "UPDATE rooms SET source=?,live_status=?,pending_start=?,live_time=?,online=? WHERE room_id=?", string(raw), r.LiveStatus, store.BoolInt(r.PendingStart), r.LiveTime, r.Online, r.RoomID); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Server) runNextLiveCache(ctx context.Context) {
	r := s.liveCaches
	r.mu.Lock()
	if ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	var x *liveCache
	e := s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if e := LockTx(ctx, tx); e != nil {
			return e
		}
		var err error
		x, err = liveCacheScan(tx.QueryRowContext(ctx, "SELECT "+liveCacheColumns+" FROM live_caches WHERE status='pending' ORDER BY ctime,key LIMIT 1"))
		if err != nil || x == nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE live_caches SET status='running',progress=0,started=?,mtime=? WHERE key=? AND status='pending'", time.Now().Unix(), time.Now().Unix(), x.Key)
		return err
	})
	if e != nil || x == nil {
		r.mu.Unlock()
		if e != nil && ctx.Err() == nil {
			s.Logger.Error("claim live cache", "error", e)
		}
		return
	}

	job, cancel := context.WithCancel(ctx)
	r.activeKey = x.Key
	r.activeCancel = cancel
	r.exited = make(chan struct{})
	exited := r.exited
	cfg, client := s.serverSnapshot()
	timeout := r.stallTimeout
	r.mu.Unlock()
	size, err := s.transcodeLiveCache(job, x, cfg, client, timeout)
	close(exited)
	r.mu.Lock()
	defer r.mu.Unlock()
	defer cancel()
	r.activeCancel = nil
	r.activeKey = ""
	finish, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if job.Err() != nil {
		if liveCacheSafeDir(x) {
			_ = os.RemoveAll(x.Dir + ".tmp")
		}
		_, e = s.Store.DB.ExecContext(finish, "UPDATE live_caches SET status='pending',progress=0,message='',mtime=? WHERE key=? AND status='running'", time.Now().Unix(), x.Key)
	} else if err != nil {
		if liveCacheSafeDir(x) {
			_ = os.RemoveAll(x.Dir + ".tmp")
		}
		_, e = s.Store.DB.ExecContext(finish, "UPDATE live_caches SET status='failed',message=?,mtime=? WHERE key=? AND status='running'", liveCacheError(err.Error()), time.Now().Unix(), x.Key)
	} else {
		// A delete/reset may have removed this job while it was finishing.
		current, lookup := liveCacheByKey(finish, s.Store.DB, x.Key)
		if lookup != nil {
			e = lookup
		} else if current != nil && current.Status == "running" {
			e = os.Rename(x.Dir+".tmp", x.Dir)
			if e == nil {
				e = s.finishLiveCache(finish, x, size)
			}
			if e != nil {
				_, _ = s.Store.DB.ExecContext(finish, "UPDATE live_caches SET status='failed',message=?,mtime=? WHERE key=?", liveCacheError(e.Error()), time.Now().Unix(), x.Key)
			}
		}
	}
	if e != nil {
		s.Logger.Error("finish live cache", "key", x.Key, "error", e)
	}
}
func (s *Server) finishLiveCache(ctx context.Context, x *liveCache, size int64) error {
	return s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if e := LockTx(ctx, tx); e != nil {
			return e
		}
		now := time.Now().Unix()
		if _, e := tx.ExecContext(ctx, "UPDATE live_caches SET status='ready',progress=100,message='',size=?,duration=?,mtime=? WHERE key=?", size, x.Duration, now, x.Key); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, "SELECT room_id,source FROM rooms WHERE pending_start=1")
		if e != nil {
			return e
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			var raw string
			if e = rows.Scan(&id, &raw); e != nil {
				rows.Close()
				return e
			}
			var src store.StreamSource
			if json.Unmarshal([]byte(raw), &src) == nil && src.Cache == x.Key && src.Type == "video" {
				ids = append(ids, id)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			if _, e = tx.ExecContext(ctx, "UPDATE rooms SET live_status=CASE WHEN banned=0 THEN 1 ELSE live_status END,live_time=CASE WHEN banned=0 THEN ? ELSE live_time END,online=CASE WHEN banned=0 AND online<1 THEN 1 ELSE online END,pending_start=0 WHERE room_id=? AND pending_start=1", now, id); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Server) collectLiveCaches(ctx context.Context, now int64) error {
	var removed []liveCache
	e := s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if e := LockTx(ctx, tx); e != nil {
			return e
		}
		used, e := liveCacheReferences(ctx, tx)
		if e != nil {
			return e
		}
		xs, e := liveCacheList(ctx, tx)
		if e != nil {
			return e
		}
		for _, x := range xs {
			since := x.UnusedSince
			if used[x.Key] {
				since = 0
			} else if since == 0 {
				since = now
			}
			if since > 0 && now-since >= 86400 && x.Status != "running" {
				if _, e = tx.ExecContext(ctx, "DELETE FROM live_caches WHERE key=?", x.Key); e != nil {
					return e
				}
				removed = append(removed, x)
			} else if since != x.UnusedSince {
				if _, e = tx.ExecContext(ctx, "UPDATE live_caches SET unused_since=? WHERE key=?", since, x.Key); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	for _, x := range removed {
		if e = liveCacheRemove(&x); e != nil {
			return e
		}
	}
	return nil
}
func liveCacheReferences(ctx context.Context, q db.Querier) (map[string]bool, error) {
	rows, e := q.QueryContext(ctx, "SELECT source FROM rooms")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	used := map[string]bool{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		var src *store.StreamSource
		if e = json.Unmarshal([]byte(raw), &src); e != nil {
			return nil, e
		}
		if src != nil && src.Cache != "" {
			used[src.Cache] = true
		}
	}
	return used, rows.Err()
}
func liveCacheSize(dir string) (int64, error) {
	var size int64
	e := filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			st, e := d.Info()
			if e != nil {
				return e
			}
			size += st.Size()
		}
		return nil
	})
	return size, e
}
