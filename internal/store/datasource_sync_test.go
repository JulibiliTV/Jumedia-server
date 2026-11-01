package store_test

import (
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
	"testing"
)

func TestT16Migration(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		for _, q := range []string{"DELETE FROM schema_versions WHERE version=5", "DELETE FROM settings WHERE key IN ('dataSourceSyncRate','dataSourceSyncConcurrency')"} {
			if _, err := s.DB.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		settings, err := s.Settings(ctx)
		if err != nil || settings["dataSourceSyncRate"] != float64(5) || settings["dataSourceSyncConcurrency"] != float64(2) {
			t.Fatal(settings, err)
		}
		if err = s.SetSetting(ctx, "dataSourceSyncRate", 17); err != nil {
			t.Fatal(err)
		}
		// Re-execute the SQL as well as the normal already-applied migration path.
		if _, err = s.DB.ExecContext(ctx, "DELETE FROM schema_versions WHERE version=5"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
		}
		v, err := s.Setting(ctx, "dataSourceSyncRate")
		if err != nil || v != float64(17) {
			t.Fatal(v, err)
		}
	})
}
