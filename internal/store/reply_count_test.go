package store_test

import (
	"testing"

	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestReplyCountMigration014(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		exec := func(query string, args ...any) {
			t.Helper()
			if _, err := s.DB.ExecContext(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
		}
		exec("INSERT INTO users (mid,username) VALUES (?,?)", 1, "counter")
		for i, state := range []int{0, -1, -100} {
			aid := int64(i + 1)
			exec("INSERT INTO archives (aid,bvid,mid,state,stat_reply,stat_view) VALUES (?,?,?,?,?,?)", aid, store.AV2BV(aid), 1, state, 1301, 123)
		}
		for _, r := range []store.Reply{
			{RPID: 1, OID: 1, MID: 1},
			{RPID: 2, OID: 1, MID: 1, Root: 1, Parent: 1},
			{RPID: 3, OID: 2, MID: 1},
		} {
			if err := store.InsertReply(ctx, s.DB, &r); err != nil {
				t.Fatal(err)
			}
		}
		check := func() {
			t.Helper()
			var bad, versions, replies int
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM archives WHERE stat_reply<>(SELECT COUNT(*) FROM replies WHERE oid=archives.aid) OR stat_view<>?", 123).Scan(&bad); err != nil || bad != 0 {
				t.Fatalf("incorrect archive counts: %d, %v", bad, err)
			}
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_versions WHERE version=?", 14).Scan(&versions); err != nil || versions != 1 {
				t.Fatalf("014 recorded %d times: %v", versions, err)
			}
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM replies").Scan(&replies); err != nil || replies != 3 {
				t.Fatalf("migration changed replies: %d, %v", replies, err)
			}
		}
		for i := 0; i < 2; i++ {
			exec("DELETE FROM schema_versions WHERE version=?", 14)
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			check()
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			check()
		}
		// Once recorded, startup must not repeatedly run the data repair.
		exec("UPDATE archives SET stat_reply=? WHERE aid=?", 99, 1)
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := s.DB.QueryRowContext(ctx, "SELECT stat_reply FROM archives WHERE aid=?", 1).Scan(&count); err != nil || count != 99 {
			t.Fatalf("recorded migration reran: %d, %v", count, err)
		}
	})
}
