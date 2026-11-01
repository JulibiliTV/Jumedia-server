package seed_test

import (
	seedfixture "jumedia-server/internal/seed/testdata"
	"testing"

	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestServiceSeedAndReset(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		defaults := map[string]any{"serviceApi": "", "serviceLive": "", "serviceApp": "", "servicePassport": "", "serviceCdns": "[]", "serviceMtime": float64(0)}
		// Require seed to add the fields itself, independently of migration 011.
		for k := range defaults {
			if _, err := s.DB.ExecContext(ctx, "DELETE FROM settings WHERE key=?", k); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.SetSetting(ctx, "serviceApi", "https://keep.test"); err != nil {
			t.Fatal(err)
		}
		if err := seedfixture.Seed(ctx, s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		defaults["serviceApi"] = "https://keep.test"
		check := func() {
			t.Helper()
			got, err := s.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range defaults {
				if got[k] != v {
					t.Errorf("%s = %#v, want %#v", k, got[k], v)
				}
			}
		}
		check()
		if err := seedfixture.Seed(ctx, s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		check()
		if err := seed.Reset(ctx, s); err != nil {
			t.Fatal(err)
		}
		defaults["serviceApi"] = ""
		check()
	})
}
