package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

type sourceClient struct {
	key    string
	client sourceMediaClient
}
type sourceRuntime struct {
	images  *imageCacheRuntime
	mu      sync.Mutex
	init    sync.Once
	err     error
	aead    cipher.AEAD
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running map[int64]context.CancelFunc
	clients map[int64]sourceClient
	slots   chan struct{}
	closed  bool
}

func newSourceRuntime() *sourceRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	return &sourceRuntime{ctx: ctx, cancel: cancel, running: map[int64]context.CancelFunc{}, clients: map[int64]sourceClient{}, slots: make(chan struct{}, 2)}
}
func sourceKey(dir string) ([]byte, error) {
	if raw := os.Getenv("JUMEDIA_DATASOURCE_KEY"); raw != "" {
		b, e := base64.StdEncoding.DecodeString(raw)
		if e != nil || len(b) != 32 {
			return nil, errors.New("JUMEDIA_DATASOURCE_KEY 必须是 base64 编码的 32 字节密钥")
		}
		return b, nil
	}
	if dir == "" {
		dir = "data"
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "datasource.key")
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		b = make([]byte, 32)
		if _, e = rand.Read(b); e != nil {
			return nil, e
		}
		// Publish a complete key atomically without replacing another process's key.
		f, e := os.CreateTemp(dir, ".datasource-key-")
		if e != nil {
			return nil, e
		}
		defer os.Remove(f.Name())
		if _, e = f.Write(b); e != nil {
			f.Close()
			return nil, e
		}
		if e = f.Close(); e != nil {
			return nil, e
		}
		if e = os.Link(f.Name(), path); e != nil && !errors.Is(e, os.ErrExist) {
			return nil, e
		}
		b, e = os.ReadFile(path)
	}
	if e != nil {
		return nil, e
	}
	if len(b) != 32 {
		return nil, errors.New("datasource.key 必须为 32 字节")
	}
	if e = os.Chmod(path, 0600); e != nil {
		return nil, e
	}
	return b, nil
}
func (s *Server) InitDataSources(ctx context.Context) error {
	r := s.sources
	r.init.Do(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed {
			r.err = errors.New("服务正在退出")
			return
		}
		key, e := sourceKey(s.Config.DataDir)
		if e != nil {
			r.err = e
			return
		}
		block, e := aes.NewCipher(key)
		if e != nil {
			r.err = e
			return
		}
		r.aead, e = cipher.NewGCM(block)
		if e != nil {
			r.err = e
			return
		}
		_, e = s.Store.DB.ExecContext(ctx, "UPDATE data_sources SET status=?,message=? WHERE status=?", "error", "同步被中断，请重新同步", "syncing")
		if e != nil {
			r.err = e
			return
		}
		if _, e = s.Store.DB.ExecContext(ctx, "UPDATE dandanplay_sync SET status='pending' WHERE status='fetching'"); e != nil {
			r.err = e
			return
		}
		r.wg.Add(1)
		go func() { defer r.wg.Done(); s.runDandanplay(r.ctx) }()
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-r.ctx.Done():
					return
				case <-ticker.C:
					s.autoSyncSources(r.ctx)
				}
			}
		}()
	})
	return r.err
}
func (s *Server) CloseDataSources() {
	images := s.sources.images
	images.mu.Lock()
	images.closed = true
	images.mu.Unlock()
	images.wg.Wait()
	r := s.sources
	r.mu.Lock()
	r.closed = true
	r.cancel()
	for _, cancel := range r.running {
		cancel()
	}
	for _, c := range r.clients {
		c.client.Close()
	}
	r.mu.Unlock()
	r.wg.Wait()
}
func (s *Server) encryptPassword(password string) (string, error) {
	if password == "" {
		return "", nil
	}
	a := s.sources.aead
	n := make([]byte, a.NonceSize())
	if _, e := rand.Read(n); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(a.Seal(n, n, []byte(password), nil)), nil
}
func (s *Server) decryptPassword(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	a := s.sources.aead
	b, e := base64.StdEncoding.DecodeString(enc)
	if e != nil || len(b) < a.NonceSize() {
		return "", errors.New("数据源密码无法解密")
	}
	p, e := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], nil)
	if e != nil {
		return "", errors.New("数据源密码无法解密")
	}
	return string(p), nil
}
func (s *Server) sourceClient(ctx context.Context, ds *store.DataSource) (sourceMediaClient, error) {
	if e := s.InitDataSources(ctx); e != nil {
		return nil, e
	}
	settings, e := s.Store.Settings(ctx)
	if e != nil {
		return nil, e
	}
	allow := ds.System || Truthy(settings["dataSourcePrivateNetwork"])
	key := fmt.Sprintf("%s\n%s\n%s\n%s\n%t", ds.Type, ds.URL, ds.Username, ds.PasswordEnc, allow)
	r := s.sources
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("服务正在退出")
	}
	if old, ok := r.clients[ds.ID]; ok {
		if old.key == key {
			return old.client, nil
		}
		old.client.Close()
		delete(r.clients, ds.ID)
	}
	password, e := s.decryptPassword(ds.PasswordEnc)
	if e != nil {
		return nil, e
	}
	client, e := newSourceMediaClient(emby.Provider(ds.Type), ds.URL, ds.Username, password, ds.ID, allow)
	if e == nil {
		r.clients[ds.ID] = sourceClient{key, client}
	}
	return client, e
}
func (s *Server) autoSyncSources(ctx context.Context) {
	settings, e := s.Store.Settings(ctx)
	if e != nil {
		return
	}
	hours, _ := settings["dataSourceSyncHours"].(float64)
	if hours <= 0 {
		return
	}
	sources, e := s.Store.ListDataSource(ctx)
	if e != nil {
		return
	}
	for _, ds := range sources {
		if ds.AutoSync && (ds.System || Truthy(settings["userDataSources"])) && float64(time.Now().Unix()-ds.LastSync) >= hours*3600 {
			_, _ = s.startSourceSync(ctx, ds.ID)
		}
	}
}
func (s *Server) startSourceSync(ctx context.Context, id int64) (*store.DataSource, error) {
	r := s.sources
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("服务正在退出")
	}
	if _, ok := r.running[id]; ok {
		return nil, Fail(-400, "正在同步中，请稍后")
	}
	var ds *store.DataSource
	err := s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if e := LockTx(ctx, tx); e != nil {
			return e
		}
		var e error
		ds, e = store.DataSourceByID(ctx, tx, id)
		if e != nil {
			return e
		}
		if ds.Status == "syncing" {
			return Fail(-400, "正在同步中，请稍后")
		}
		ds.Status = "syncing"
		ds.Message = "正在同步媒体库…"
		return store.SaveDataSource(ctx, tx, ds)
	})
	if err != nil {
		return nil, err
	}
	s.launchSourceLocked(*ds)
	return ds, nil
}

// Caller holds runtime.mu and has committed status=syncing before launching.
func (s *Server) launchSourceLocked(ds store.DataSource) {
	r := s.sources
	// Snapshot when accepted, including runs queued behind the two global slots.
	settings, settingsErr := s.Store.Settings(r.ctx)
	ctx, cancel := context.WithCancel(r.ctx)
	r.running[ds.ID] = cancel
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer cancel()
		var n int
		var err error
		select {
		case r.slots <- struct{}{}:
			if settingsErr != nil {
				err = settingsErr
			} else {
				n, err = s.syncDataSource(ctx, &ds, settings)
			}
			<-r.slots
		case <-ctx.Done():
			err = ctx.Err()
		}
		status, message := "ok", fmt.Sprintf("已同步 %d 个视频", n)
		if err != nil {
			status = "error"
			message = "同步失败：" + err.Error()
			if errors.Is(err, emby.ErrPrivate) || errors.Is(err, emby.ErrNetwork) || strings.HasPrefix(err.Error(), emby.Provider(ds.Type).Name()+" ") {
				message = err.Error()
			}
			var partial *sourceSyncPartialError
			if errors.As(err, &partial) {
				message = partial.Error()
			}
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				message = "同步被中断，请重新同步"
			}
		}
		finish, cancelFinish := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelFinish()
		// Never recreate a source deleted while a fetch was in progress.
		_, e := s.Store.DB.ExecContext(finish, "UPDATE data_sources SET status=?,message=?,last_sync=? WHERE id=?", status, message, time.Now().Unix(), ds.ID)
		if e != nil {
			s.Logger.Error("finish datasource sync", "id", ds.ID, "error", e)
		}
		r.mu.Lock()
		delete(r.running, ds.ID)
		r.mu.Unlock()
	}()
}
