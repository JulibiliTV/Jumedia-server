package store_test

import (
	"os"
	"strings"
	"testing"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestDandanplayMigrationPreservesCache(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		script, err := os.ReadFile("migrations/016_dandanplay.sql")
		if err != nil {
			t.Fatal(err)
		}
		err = s.DB.WithTx(t.Context(), func(tx *db.Tx) error {
			for _, query := range append([]string{"DROP TABLE dandanplay_comments", "DROP TABLE dandanplay_sync", "DELETE FROM schema_versions WHERE version=17"}, strings.Split(string(script), ";")...) {
				if strings.TrimSpace(query) != "" {
					if _, err := tx.ExecContext(t.Context(), query); err != nil {
						return err
					}
				}
			}
			for _, query := range []string{
				"INSERT INTO users(mid,username) VALUES (1,'fixture')",
				"INSERT INTO archives(aid,mid) VALUES (1,1)",
				"INSERT INTO dandanplay_sync(cid,aid,media,filename,duration,status,count,updated_at) VALUES (10,1,'fixture','fixture',120,'ready',1,123)",
				"INSERT INTO dandanplay_comments(id,cid,progress,mode,fontsize,color,content,ctime,weight) VALUES (11,10,1000,1,25,1,'cached',123,5)",
			} {
				if _, err := tx.ExecContext(t.Context(), query); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		comments, err := s.DandanplayComments(t.Context(), 10)
		if err != nil || len(comments) != 1 || comments[0].ID != 11 || comments[0].Content != "cached" {
			t.Fatalf("cache changed: %+v %v", comments, err)
		}
		var owner, count, updated int64
		if err = s.DB.QueryRowContext(t.Context(), "SELECT archive_aid,count,updated_at FROM dandanplay_sync WHERE cid=10").Scan(&owner, &count, &updated); err != nil || owner != 1 || count != 1 || updated != 123 {
			t.Fatal("metadata changed", err)
		}
		if _, err = s.DB.ExecContext(t.Context(), "DELETE FROM archives WHERE aid=1"); err != nil {
			t.Fatal(err)
		}
		comments, err = s.DandanplayComments(t.Context(), 10)
		if err != nil || len(comments) != 0 {
			t.Fatal("owner deletion did not remove cache", err)
		}
	})
}
