package store_test

import (
	"reflect"
	"testing"

	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestServiceMigration011(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		defaults := map[string]any{"serviceApi": "", "serviceLive": "", "serviceApp": "", "servicePassport": "", "serviceCdns": "[]", "serviceMtime": float64(0)}
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
		check(defaults)
		// Simulate a v14 database with both missing and preexisting extension keys.
		for k := range defaults {
			if _, err := s.DB.ExecContext(ctx, "DELETE FROM settings WHERE key=?", k); err != nil {
				t.Fatal(err)
			}
		}
		saved := map[string]any{"serviceApi": "https://custom.test/path", "serviceCdns": `[{"name":"自定义","url":"https://cdn.test"}]`, "serviceMtime": float64(123)}
		for k, v := range saved {
			if err := s.SetSetting(ctx, k, v); err != nil {
				t.Fatal(err)
			}
			defaults[k] = v
		}
		for i := 0; i < 2; i++ {
			if _, err := s.DB.ExecContext(ctx, "DELETE FROM schema_versions WHERE version=?", 11); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			check(defaults)
			var n int
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_versions WHERE version=?", 11).Scan(&n); err != nil || n != 1 {
				t.Fatalf("version 011: %d %v", n, err)
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			check(defaults)
		}
	})
}
