// Package testutil provides isolated databases for the shared SQLite/PG suite.
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func Drivers(t *testing.T, fn func(*testing.T, *store.Store)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, Open(t, "sqlite")) })
	t.Run("postgres", func(t *testing.T) {
		if os.Getenv("JUMEDIA_TEST_PG_DSN") == "" {
			t.Skip("JUMEDIA_TEST_PG_DSN is not set")
		}
		fn(t, Open(t, "postgres"))
	})
}
func Open(t *testing.T, driver string) *store.Store {
	t.Helper()
	ctx := context.Background()
	var d *db.DB
	if driver == "postgres" {
		cfg, err := pgx.ParseConfig(os.Getenv("JUMEDIA_TEST_PG_DSN"))
		if err != nil {
			t.Fatal(err)
		}
		admin := stdlib.OpenDB(*cfg)
		t.Cleanup(func() { _ = admin.Close() })
		schema := fmt.Sprintf("jumedia_test_%d", time.Now().UnixNano())
		if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
				t.Error(err)
			}
		})
		cfg.RuntimeParams["search_path"] = schema
		d = &db.DB{DB: stdlib.OpenDB(*cfg), Driver: "postgres"}
	} else {
		var err error
		d, err = db.Open(ctx, "sqlite", filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = d.Close() })
	s := store.New(d)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}
