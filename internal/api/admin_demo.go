package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"jumedia-server/internal/db"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func (s *Server) adminDemoImport(c *Ctx) (any, error) {
	if _, err := c.CSRF(); err != nil {
		return nil, err
	}
	if c.R.Method != http.MethodPost {
		return nil, Fail(-400, "请使用 POST 上传演示数据包")
	}
	if c.Form == nil || len(c.Form.File["file"]) == 0 {
		return nil, Fail(-400, "请选择演示数据包文件")
	}
	f, err := c.Form.File["file"][0].Open()
	if err != nil {
		return nil, Fail(-400, "演示数据包格式无效")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil || len(raw) > 32<<20 {
		return nil, Fail(-400, "演示数据包格式无效")
	}
	p, err := seed.ParsePackage(raw)
	if err != nil {
		return nil, Fail(-400, err.Error())
	}
	if err = s.ImportDemo(c.Context(), p); errors.Is(err, seed.ErrNotInitial) {
		return nil, Fail(-400, err.Error())
	} else if err != nil {
		return nil, err
	}
	return nil, nil
}

// ImportDemo is shared by the administrator upload and the offline CLI.
func (s *Server) ImportDemo(ctx context.Context, p seed.DemoPackage) error {
	if err := s.InitDataSources(ctx); err != nil {
		return err
	}
	password, err := s.encryptPassword(p.Emby.Password)
	if err != nil {
		return err
	}
	s.liveCaches.mu.Lock()
	defer s.liveCaches.mu.Unlock()
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if err := seed.ImportTx(ctx, tx, p, password); err != nil {
			return err
		}
		return s.prepareDemoCachesTx(ctx, tx)
	})
	if err != nil {
		return err
	}
	return nil
}

// ResetApplication shares the HTTP reset transaction and cache cleanup with CLI.
func (s *Server) ResetApplication(ctx context.Context) error {
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/x/admin/settings/save", nil)
	_, err := s.adminSettingsSave(&Ctx{R: r, P: map[string]string{"reset": "true"}, Server: s})
	return err
}

// Imported demo video rooms join the same transaction as their content. The
// existing worker will pick up pending caches after the import lock is released.
func (s *Server) prepareDemoCachesTx(ctx context.Context, tx *db.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT room_id,source,live_status FROM rooms ORDER BY room_id")
	if err != nil {
		return err
	}
	type room struct {
		id   int64
		raw  string
		live int
	}
	var rooms []room
	for rows.Next() {
		var r room
		if err = rows.Scan(&r.id, &r.raw, &r.live); err != nil {
			rows.Close()
			return err
		}
		rooms = append(rooms, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range rooms {
		var source *store.StreamSource
		if err = json.Unmarshal([]byte(r.raw), &source); err != nil {
			return err
		}
		if source == nil || source.Type != "video" {
			continue
		}
		a, err := store.ArchiveFrom(ctx, tx, source.AID)
		if err != nil {
			return err
		}
		for _, page := range a.Pages {
			if page.CID != source.CID {
				continue
			}
			cache, err := s.liveCacheEnsure(ctx, tx, page, false)
			if err != nil {
				return err
			}
			source.Cache = cache.Key
			source.URL = liveCachePath(cache.Key)
			raw, err := json.Marshal(source)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE rooms SET source=?,live_status=0,pending_start=? WHERE room_id=?", string(raw), store.BoolInt(r.live == 1), r.id); err != nil {
				return err
			}
		}
	}
	return nil
}
