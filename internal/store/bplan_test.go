package store_test

import (
	"context"
	"testing"

	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestBPlanMigration(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
				t.Fatal(err)
			}
		}
		// Reconstruct a v6 database with real data. No 007 column or migration marker
		// is left, so this tests upgrading, not merely skipping a recorded migration.
		for _, q := range []string{"DROP TABLE reservations", "DROP INDEX idx_regions_parent_sort", "DROP INDEX idx_archives_sub_tid", "DROP INDEX idx_banners_channel", "ALTER TABLE regions DROP COLUMN parent", "ALTER TABLE archives DROP COLUMN sub_tid", "ALTER TABLE seasons DROP COLUMN copyright", "ALTER TABLE users DROP COLUMN notice", "ALTER TABLE banners DROP COLUMN loc", "DELETE FROM schema_versions WHERE version=7"} {
			exec(q)
		}
		exec("INSERT INTO users(mid,username) VALUES (?,?)", 1, "migration")
		exec("INSERT INTO regions(tid,name,sort,visible) VALUES (?,?,?,?)", 1001, "影视", 1, 1)
		for i, aid := range []int64{100100, 100107} {
			exec("INSERT INTO archives(aid,bvid,mid,tid,title,pic) VALUES (?,?,?,?,?,?)", aid, store.AV2BV(aid), 1, 1001, "稿件", "/cover")
			exec("INSERT INTO seasons(season_id,media_id,season_type,title,horizontal) VALUES (?,?,?,?,?)", 40000+i, 60000+i, 2, "电影", "/horizontal")
		}
		for i := 0; i < 2; i++ {
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM regions WHERE parent=1001").Scan(&count); err != nil || count != 6 {
			t.Fatalf("children %d %v", count, err)
		}
		// Literal expected values calculated by mock's Math.imul FNV-1a, not the Go implementation.
		for aid, want := range map[int64]int64{100100: 100106, 100107: 100103} {
			var got int64
			if err := s.DB.QueryRowContext(ctx, "SELECT sub_tid FROM archives WHERE aid=?", aid).Scan(&got); err != nil || got != want {
				t.Fatalf("aid %d sub %d want %d: %v", aid, got, want, err)
			}
		}
		for sid, want := range map[int64]int{40000: 3, 40001: 1} {
			var got int
			if err := s.DB.QueryRowContext(ctx, "SELECT copyright FROM seasons WHERE season_id=?", sid).Scan(&got); err != nil || got != want {
				t.Fatalf("copyright %d: %d %v", sid, got, err)
			}
		}
		if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM banners WHERE position=?", "channel").Scan(&count); err != nil || count != 4 {
			t.Fatalf("banners %d %v", count, err)
		}
		exec("UPDATE archives SET sub_tid=? WHERE aid=?", 100101, 100100)
		exec("UPDATE seasons SET copyright=1 WHERE season_id=40000")
		exec("UPDATE regions SET name=?,visible=0 WHERE tid=100101", "自定义")
		for i := 0; i < 2; i++ {
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var sub int64
		if err := s.DB.QueryRowContext(ctx, "SELECT sub_tid FROM archives WHERE aid=100100").Scan(&sub); err != nil || sub != 100101 {
			t.Fatal("restart overwrote edits", sub, err)
		}
		if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM banners WHERE position=?", "channel").Scan(&count); err != nil || count != 4 {
			t.Fatal("duplicate banners", count, err)
		}
	})
}
