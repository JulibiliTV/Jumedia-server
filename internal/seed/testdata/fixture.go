package testdata

import (
	"context"
	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"time"
)

// Seed constructs the fixed-ID catalog used by older offline route contracts.
// It is intentionally separate from production base initialization and import.
func Seed(ctx context.Context, s *store.Store, catalog emby.Catalog) error {
	if err := seed.Seed(ctx, s); err != nil {
		return err
	}
	return s.DB.WithTx(ctx, func(tx *db.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		t0 := time.Now().Unix()
		if err := seed.GenerateDemoTx(ctx, tx, t0, catalog, 0); err != nil {
			return err
		}
		// Contract fixtures normalize timestamps against this log row.
		return store.InsertLog(ctx, tx, &store.Log{ID: 1, MID: 1, Action: "系统初始化", Target: "演示数据", Ctime: t0})
	})
}
