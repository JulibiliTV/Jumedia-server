package store_test

import (
	"reflect"
	"testing"

	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestServerMigration012(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		defaults := map[string]any{"serverPublicOrigin": "", "serverEmbyUrl": "", "serverEmbyUsername": "", "serverEmbyPassword": "", "serverFfmpeg": "", "serverFfprobe": "", "serverVideoEncoder": ""}
		check := func(want map[string]any) {
			t.Helper()
			got, err := s.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range want {
				if !reflect.DeepEqual(got[k], v) {
					t.Errorf("%s = %#v, want %#v", k, got[k], v)
				}
			}
		}
		// Replay 012 in isolation: 013 is already recorded by Store.Open.
		// Simulate a v15 database with both missing and preexisting extension keys.
		for k := range defaults {
			if _, err := s.DB.ExecContext(ctx, "DELETE FROM settings WHERE key=?", k); err != nil {
				t.Fatal(err)
			}
		}
		saved := map[string]any{"serverPublicOrigin": "https://custom.test", "serverEmbyPassword": "kept-secret", "serverVideoEncoder": "h264_nvenc"}
		for k, v := range saved {
			if err := s.SetSetting(ctx, k, v); err != nil {
				t.Fatal(err)
			}
			defaults[k] = v
		}
		for i := 0; i < 2; i++ {
			if _, err := s.DB.ExecContext(ctx, "DELETE FROM schema_versions WHERE version=?", 12); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			check(defaults)
			var n int
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_versions WHERE version=?", 12).Scan(&n); err != nil || n != 1 {
				t.Fatalf("version 012: %d %v", n, err)
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			check(defaults)
		}
	})
}

func TestServerMigration013(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		removed := []string{"serverEmbyUrl", "serverEmbyUsername", "serverEmbyPassword"}
		check := func(want map[string]any) {
			t.Helper()
			got, err := s.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range removed {
				if _, ok := got[k]; ok {
					t.Errorf("retired key still exists: %s", k)
				}
			}
			for k, v := range want {
				if !reflect.DeepEqual(got[k], v) {
					t.Errorf("%s = %#v, want %#v", k, got[k], v)
				}
			}
			var n int
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_versions WHERE version=?", 13).Scan(&n); err != nil || n != 1 {
				t.Fatalf("version 013: %d %v", n, err)
			}
		}
		check(map[string]any{"serverPublicOrigin": "", "serverFfmpeg": "", "serverFfprobe": "", "serverVideoEncoder": ""})
		// Simulate an existing 012 database with user overrides.
		for _, k := range removed {
			if err := s.SetSetting(ctx, k, "old-value"); err != nil {
				t.Fatal(err)
			}
		}
		kept := map[string]any{"serverPublicOrigin": "https://kept.test", "serverFfmpeg": "/bin/custom", "serverFfprobe": "probe", "serverVideoEncoder": "h264_nvenc", "unrelated": true}
		for k, v := range kept {
			if err := s.SetSetting(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 2; i++ {
			if _, err := s.DB.ExecContext(ctx, "DELETE FROM schema_versions WHERE version=?", 13); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			check(kept)
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			check(kept)
		}
	})
}
