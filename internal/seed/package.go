package seed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

type DemoPackage struct {
	Format      string         `json:"format"`
	Version     int            `json:"version"`
	GeneratedAt int64          `json:"generatedAt"`
	Emby        DemoConnection `json:"emby"`
	Catalog     emby.Catalog   `json:"catalog"`
}
type DemoConnection struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

var ErrNotInitial = errors.New("请先重置为初始状态再导入演示数据")

func ParsePackage(raw []byte) (DemoPackage, error) {
	var p DemoPackage
	if json.Unmarshal(raw, &p) != nil || p.Format != "jumedia-demo" {
		return p, errors.New("演示数据包格式无效")
	}
	if p.Version != 1 {
		return p, errors.New("不支持的演示数据包版本")
	}
	if p.Catalog.Series == nil || p.Catalog.Movies == nil || len(p.Catalog.Series)+len(p.Catalog.Movies) == 0 {
		return p, errors.New("演示数据包 catalog 为空或无效")
	}
	var connection struct {
		Emby *struct {
			URL      *string `json:"url"`
			Username *string `json:"username"`
		} `json:"emby"`
	}
	if json.Unmarshal(raw, &connection) != nil || connection.Emby == nil || connection.Emby.URL == nil || connection.Emby.Username == nil {
		return p, errors.New("演示 Emby 连接信息无效")
	}
	u, err := url.Parse(p.Emby.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return p, errors.New("演示 Emby 连接信息无效")
	}
	for _, s := range p.Catalog.Series {
		if len(s.Episodes) == 0 {
			return p, errors.New("演示数据包 catalog 无效")
		}
		for _, e := range s.Episodes {
			if !emby.ValidPair(e.ID, e.SourceID) {
				return p, errors.New("演示数据包 catalog 无效")
			}
		}
	}
	for _, m := range p.Catalog.Movies {
		if !emby.ValidPair(m.ID, m.SourceID) {
			return p, errors.New("演示数据包 catalog 无效")
		}
	}
	return p, nil
}

// ImportTx creates the system source and demo content atomically.
func ImportTx(ctx context.Context, tx *db.Tx, p DemoPackage, encryptedPassword string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE ids SET value=value WHERE name=?", "global"); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role<>?", "admin").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrNotInitial
	}
	// Authentication and audit records do not constitute content: logging in and
	// setting up an administrator must not prevent the first import.
	for _, table := range businessTables {
		switch table {
		case "sessions", "qrcodes", "logs", "regions", "live_areas", "live_area_children":
			continue
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrNotInitial
		}
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM settings WHERE key=?", "_demo_catalog").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrNotInitial
	}
	var offset int64
	if err := tx.QueryRowContext(ctx, "SELECT value FROM ids WHERE name=?", "global").Scan(&offset); err != nil {
		return err
	}
	var maxID int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(mid),0) FROM users").Scan(&maxID); err != nil {
		return err
	}
	offset = max(offset, maxID)
	if err := GenerateDemoTx(ctx, tx, time.Now().Unix(), p.Catalog, offset); err != nil {
		return err
	}
	if err := store.SetSetting(ctx, tx, "_demo_catalog", p.Catalog); err != nil {
		return err
	}
	return createDemoSourceTx(ctx, tx, p, encryptedPassword)
}

func StoredCatalog(ctx context.Context, s *store.Store) (emby.Catalog, error) {
	var catalog emby.Catalog
	v, err := s.Setting(ctx, "_demo_catalog")
	if errors.Is(err, sql.ErrNoRows) {
		return catalog, nil
	}
	if err != nil {
		return catalog, err
	}
	if v == nil {
		return catalog, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return catalog, err
	}
	err = json.Unmarshal(raw, &catalog)
	return catalog, err
}
