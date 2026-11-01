package store_test

import (
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
	"testing"
)

func TestT17Migration(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		for _, q := range []string{"DELETE FROM schema_versions WHERE version=6", "DELETE FROM settings WHERE key IN ('imageCacheEnabled','imageCacheDir','imageCacheMaxMB')"} {
			if _, err := s.DB.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		settings, err := s.Settings(ctx)
		if err != nil || settings["imageCacheEnabled"] != true || settings["imageCacheDir"] != "" || settings["imageCacheMaxMB"] != float64(1024) {
			t.Fatal(settings, err)
		}
		if err := s.SetSetting(ctx, "imageCacheMaxMB", 128); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, "DELETE FROM schema_versions WHERE version=6"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
		}
		value, err := s.Setting(ctx, "imageCacheMaxMB")
		if err != nil || value != float64(128) {
			t.Fatal(value, err)
		}
	})
}
