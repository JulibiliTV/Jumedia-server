package store_test

import (
	"context"
	"testing"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestT12DatabaseUpgrade(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		// Reconstruct the exact pre-002 columns/version, then retain real business rows.
		for _, query := range []string{
			"DROP TABLE data_sources", "DROP INDEX archives_origin", "DROP INDEX collections_origin",
			"ALTER TABLE archives DROP COLUMN origin_ds", "ALTER TABLE archives DROP COLUMN origin_item", "ALTER TABLE archives DROP COLUMN origin",
			"ALTER TABLE collections DROP COLUMN origin_ds", "ALTER TABLE collections DROP COLUMN origin_library", "ALTER TABLE collections DROP COLUMN origin",
			"DELETE FROM schema_versions WHERE version=2", "DELETE FROM settings WHERE key IN ('userDataSources','dataSourcePrivateNetwork','dataSourceSyncHours','officialMid')",
		} {
			if _, e := s.DB.ExecContext(ctx, query); e != nil {
				t.Fatal(query, e)
			}
		}
		if e := s.DB.WithTx(ctx, func(tx *db.Tx) error {
			if e := store.InsertUser(ctx, tx, &store.User{MID: 1, Username: "upgrade"}); e != nil {
				return e
			}
			return store.InsertArchive(ctx, tx, &store.Archive{AID: 100001, BVID: store.AV2BV(100001), MID: 1, Title: "existing T12", Pages: []store.Page{}, Chapters: []store.Chapter{}, Subtitles: []store.Subtitle{}})
		}); e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 2; i++ {
			if e := s.Migrate(ctx); e != nil {
				t.Fatal(e)
			}
		}
		a, e := s.ArchiveByID(ctx, 100001)
		if e != nil || a.Title != "existing T12" || a.Origin != nil || a.OriginDS != 0 || a.OriginItem != "" {
			t.Fatal(a, e)
		}
		settings, e := s.Settings(ctx)
		if e != nil || settings["userDataSources"] != true || settings["dataSourcePrivateNetwork"] != false || settings["dataSourceSyncHours"] != float64(6) || settings["officialMid"] != float64(1) {
			t.Fatal(settings, e)
		}
		// Multiple uploads retain origin_ds=0, but external item identities are unique.
		if e = store.InsertArchive(ctx, s.DB, &store.Archive{AID: 100002, BVID: store.AV2BV(100002), MID: 1}); e != nil {
			t.Fatal(e)
		}
		if _, e = s.DB.ExecContext(ctx, "UPDATE archives SET origin_ds=?,origin_item=? WHERE aid=?", 1, "item", 100001); e != nil {
			t.Fatal(e)
		}
		if _, e = s.DB.ExecContext(ctx, "UPDATE archives SET origin_ds=?,origin_item=? WHERE aid=?", 1, "item", 100002); e == nil {
			t.Fatal("duplicate origin accepted")
		}
	})
}
