package store_test

import (
	"context"
	"testing"

	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestT14RawMediaUpgrade(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		if err := store.InsertUser(ctx, s.DB, &store.User{MID: 1, Username: "upgrade"}); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertArchive(ctx, s.DB, &store.Archive{AID: 100001, BVID: store.AV2BV(100001), MID: 1}); err != nil {
			t.Fatal(err)
		}
		cases := []struct{ before, after string }{
			{"embyds:12:item:src:mp4", "embyds:12:item:src:direct"},
			{"embyds:12:item:src:hls", "embyds:12:item:src:raw-mkv"},
			{"embyds:12:item:src:raw-mp4", "embyds:12:item:src:raw-mp4"},
			{"embyds:12:item:src:direct", "embyds:12:item:src:direct"},
			{"emby:item:mp4", "emby:item:mp4"}, {"/upload/file.mp4", "/upload/file.mp4"},
			{"https://example.test/embyds:12:item:src:hls", "https://example.test/embyds:12:item:src:hls"},
		}
		for i, tc := range cases {
			if err := store.InsertPage(ctx, s.DB, &store.Page{CID: int64(i + 1), AID: 100001, Position: i, Media: tc.before}); err != nil {
				t.Fatal(err)
			}
		}
		for run := 0; run < 3; run++ {
			// Reapply the actual script as well as exercising normal version skips.
			if run < 2 {
				if _, err := s.DB.ExecContext(ctx, "DELETE FROM schema_versions WHERE version=?", 3); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			pages, err := s.ListPage(ctx, "WHERE aid=? ORDER BY position", 100001)
			if err != nil || len(pages) != len(cases) {
				t.Fatal(pages, err)
			}
			for i, p := range pages {
				if p.Media != cases[i].after || p.CID != int64(i+1) {
					t.Fatal(run, p, cases[i])
				}
			}
		}
	})
}
