package seed_test

import (
	"strings"
	"testing"

	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestServerSeedAndReset(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		defaults := map[string]any{"serverPublicOrigin": "", "serverFfmpeg": "", "serverFfprobe": "", "serverVideoEncoder": ""}
		// Require seed to add the fields itself, independently of migration 012.
		for k := range defaults {
			if _, err := s.DB.ExecContext(ctx, "DELETE FROM settings WHERE key=?", k); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.SetSetting(ctx, "serverPublicOrigin", "https://keep.test"); err != nil {
			t.Fatal(err)
		}
		if err := seed.Seed(ctx, s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		defaults["serverPublicOrigin"] = "https://keep.test"
		check := func() {
			t.Helper()
			got, err := s.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for k := range got {
				if strings.HasPrefix(k, "serverEmby") {
					t.Fatalf("retired seed setting: %s", k)
				}
			}
			for k, v := range defaults {
				if got[k] != v {
					t.Errorf("%s = %#v, want %#v", k, got[k], v)
				}
			}
		}
		check()
		if err := seed.Seed(ctx, s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		check()
		if err := s.SetSetting(ctx, "serverEmbyPassword", "legacy"); err != nil {
			t.Fatal(err)
		}
		if err := seed.Reset(ctx, s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		defaults["serverPublicOrigin"] = ""
		check()
	})
}
