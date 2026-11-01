package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

type liveCache struct {
	Key, Media                         string
	Height                             int
	AID, CID                           int64
	Duration                           float64
	Status                             string
	Progress                           int
	Message                            string
	Size                               int64
	Dir                                string
	Ctime, Mtime, Started, UnusedSince int64
}

const liveCacheColumns = "key,media,height,aid,cid,duration,status,progress,message,size,dir,ctime,mtime,started,unused_since"

var liveCacheKeyPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var liveCacheFilePattern = regexp.MustCompile(`^(index\.m3u8|seg[0-9]{5}\.ts)$`)

func liveCacheScan(row interface{ Scan(...any) error }) (*liveCache, error) {
	var x liveCache
	err := row.Scan(&x.Key, &x.Media, &x.Height, &x.AID, &x.CID, &x.Duration, &x.Status, &x.Progress, &x.Message, &x.Size, &x.Dir, &x.Ctime, &x.Mtime, &x.Started, &x.UnusedSince)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &x, err
}
func liveCacheByKey(ctx context.Context, q db.Querier, key string) (*liveCache, error) {
	return liveCacheScan(q.QueryRowContext(ctx, "SELECT "+liveCacheColumns+" FROM live_caches WHERE key=?", key))
}
func liveCacheList(ctx context.Context, q db.Querier) ([]liveCache, error) {
	rows, e := q.QueryContext(ctx, "SELECT "+liveCacheColumns+" FROM live_caches ORDER BY ctime DESC,key")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []liveCache{}
	for rows.Next() {
		x, e := liveCacheScan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}
func liveCacheJSON(x *liveCache) any {
	if x == nil {
		return nil
	}
	return map[string]any{"key": x.Key, "status": x.Status, "progress": x.Progress, "message": x.Message, "height": x.Height, "size": x.Size}
}
func liveCacheNotReady(x *liveCache) string {
	if x == nil {
		return "转播视频的转码缓存不存在，请重新选择视频"
	}
	if x.Status == "failed" {
		return "转播视频转码失败：" + x.Message
	}
	if x.Status != "ready" {
		p := "排队中"
		if x.Status == "running" {
			p = fmt.Sprintf("%d%%", x.Progress)
		}
		return "转播视频正在转码缓存（" + p + "），完成后才能开播"
	}
	return ""
}
func liveCachePath(key string) string { return "/live-cache/" + key + "/index.m3u8" }
func (s *Server) roomCache(ctx context.Context, q db.Querier, r *store.LiveRoom) (*liveCache, error) {
	if r.Source == nil || r.Source.Type != "video" {
		return nil, nil
	}
	return liveCacheByKey(ctx, q, r.Source.Cache)
}
func (s *Server) liveCacheSettings(ctx context.Context, q db.Querier) (string, int, error) {
	var rawDir, rawHeight string
	if e := q.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", "liveCacheDir").Scan(&rawDir); e != nil {
		return "", 0, e
	}
	if e := q.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", "liveTranscodeHeight").Scan(&rawHeight); e != nil {
		return "", 0, e
	}
	var dir string
	var height int
	if e := json.Unmarshal([]byte(rawDir), &dir); e != nil {
		return "", 0, e
	}
	if e := json.Unmarshal([]byte(rawHeight), &height); e != nil {
		return "", 0, e
	}
	if dir == "" {
		dir = filepath.Join(s.Config.DataDir, "live-cache")
	}
	dir, e := filepath.Abs(dir)
	return dir, height, e
}

// Caller holds the cache operation lock and its database transaction.
func (s *Server) liveCacheEnsure(ctx context.Context, q db.Querier, p store.Page, live bool) (*liveCache, error) {
	dir, height, e := s.liveCacheSettings(ctx, q)
	if e != nil {
		return nil, e
	}
	x, e := liveCacheScan(q.QueryRowContext(ctx, "SELECT "+liveCacheColumns+" FROM live_caches WHERE media=? AND height=?", p.Media, height))
	if e != nil {
		return nil, e
	}
	if live && liveCacheNotReady(x) != "" {
		return nil, Fail(-400, "直播中只能切换到已完成转码缓存的视频，请先下播或等待转码完成")
	}
	if x != nil {
		return x, nil
	}
	b := make([]byte, 16)
	if _, e = rand.Read(b); e != nil {
		return nil, e
	}
	key := hex.EncodeToString(b)
	now := time.Now().Unix()
	x = &liveCache{Key: key, Media: p.Media, Height: height, AID: p.AID, CID: p.CID, Duration: float64(p.Duration), Status: "pending", Dir: filepath.Join(dir, key), Ctime: now, Mtime: now}
	_, e = q.ExecContext(ctx, "INSERT INTO live_caches ("+liveCacheColumns+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", x.Key, x.Media, x.Height, x.AID, x.CID, x.Duration, x.Status, 0, "", 0, x.Dir, now, now, 0, 0)
	return x, e
}
func liveCacheRetry(ctx context.Context, q db.Querier, x *liveCache) error {
	if x.Status != "failed" {
		return nil
	}
	_, e := q.ExecContext(ctx, "UPDATE live_caches SET status='pending',progress=0,message='',mtime=? WHERE key=? AND status='failed'", time.Now().Unix(), x.Key)
	if e == nil {
		x.Status = "pending"
		x.Progress = 0
		x.Message = ""
	}
	return e
}
func liveCacheSafeDir(x *liveCache) bool {
	return liveCacheKeyPattern.MatchString(x.Key) && filepath.IsAbs(x.Dir) && filepath.Clean(x.Dir) == x.Dir && filepath.Base(x.Dir) == x.Key
}
func liveCacheRemove(x *liveCache) error {
	if !liveCacheSafeDir(x) {
		return errors.New("拒绝删除不安全的缓存目录")
	}
	// RemoveAll removes symlinks themselves, never their targets. Neither parent nor current settings are deletion inputs.
	return errors.Join(os.RemoveAll(x.Dir), os.RemoveAll(x.Dir+".tmp"))
}

// OpenRoot confines access even if a cache file or directory has been replaced by a symlink.
func liveCacheRead(x *liveCache, name string) ([]byte, error) {
	if !liveCacheSafeDir(x) || !liveCacheFilePattern.MatchString(name) {
		return nil, os.ErrNotExist
	}
	root, e := os.OpenRoot(x.Dir)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, livePlaylistLimit+1))
}
