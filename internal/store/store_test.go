package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestMigrateAndTransactions(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_versions").Scan(&n); err != nil || n != 17 {
			t.Fatalf("versions: %d %v", n, err)
		}
		sentinel := errors.New("rollback")
		err := s.DB.WithTx(ctx, func(tx *db.Tx) error {
			if err := store.InsertUser(ctx, tx, &store.User{MID: 1, Username: "rollback"}); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil || n != 0 {
			t.Fatalf("rollback: %d %v", n, err)
		}
		if err = store.InsertSession(ctx, s.DB, &store.Session{Token: "orphan", MID: 99}); err == nil {
			t.Fatal("foreign key not enforced")
		}
		if s.DB.Driver == "sqlite" {
			var mode string
			if err = s.DB.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
				t.Fatalf("WAL %s %v", mode, err)
			}
			for _, p := range []struct {
				q    string
				want int
			}{{"PRAGMA foreign_keys", 1}, {"PRAGMA busy_timeout", 5000}} {
				if err = s.DB.QueryRowContext(ctx, p.q).Scan(&n); err != nil || n != p.want {
					t.Fatalf("%s: %d %v", p.q, n, err)
				}
			}
		}
	})
}
func TestNextIDConcurrent(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		var wg sync.WaitGroup
		ids := make(chan int64, 40)
		errs := make(chan error, 40)
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id, err := s.NextID(context.Background())
				if err != nil {
					errs <- err
				} else {
					ids <- id
				}
			}()
		}
		wg.Wait()
		close(ids)
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		seen := map[int64]bool{}
		for id := range ids {
			if seen[id] {
				t.Fatalf("duplicate %d", id)
			}
			seen[id] = true
		}
		if len(seen) != 40 || !seen[1001] || !seen[1040] {
			t.Fatalf("ids: %v", seen)
		}
	})
}
func TestBVID(t *testing.T) {
	for _, aid := range []int64{1, 100100, 100999, 1000001} {
		got, err := store.BV2AV(store.AV2BV(aid))
		if err != nil || got != aid {
			t.Fatalf("%d: %d %v", aid, got, err)
		}
	}
	if _, err := store.BV2AV("no"); err == nil {
		t.Fatal("invalid BV accepted")
	}
}
