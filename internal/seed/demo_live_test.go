package seed_test

import (
	seedfixture "jumedia-server/internal/seed/testdata"
	"reflect"
	"strings"
	"testing"

	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestDemoLiveSeedAndReset(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		catalog := testdata.Catalog()
		for _, populate := range []func() error{func() error { return seedfixture.Seed(ctx, s, catalog) }, func() error {
			if err := seed.Reset(ctx, s); err != nil {
				return err
			}
			if n, err := s.ListLiveRoom(ctx, ""); err != nil || len(n) != 0 {
				t.Fatal("reset retained rooms", err)
			}
			return seedfixture.Seed(ctx, s, catalog)
		}} {
			if err := populate(); err != nil {
				t.Fatal(err)
			}
			rooms, err := s.ListLiveRoom(ctx, "ORDER BY room_id")
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, r := range rooms {
				if r.RoomID != 21000 && r.RoomID != 21018 {
					if r.Source.Type != "push" || r.LiveStatus != 0 || r.Online != 0 || r.PendingStart {
						t.Fatal(r)
					}
					continue
				}
				count++
				archives, e := s.ListArchive(ctx, "WHERE mid=? AND state=0 ORDER BY aid", r.UID)
				if e != nil {
					t.Fatal(e)
				}
				var want *store.Archive
				for _, a := range archives {
					full, e := s.ArchiveByID(ctx, a.AID)
					if e != nil {
						t.Fatal(e)
					}
					if len(full.Pages) > 0 && strings.HasPrefix(full.Pages[0].Media, "emby:") {
						want = full
						break
					}
				}
				if want == nil || r.Source.Type != "video" || r.Source.AID != want.AID || r.Source.CID != want.Pages[0].CID || r.LiveStatus != 1 || r.Cover != want.Pic {
					t.Fatalf("room=%+v source=%+v archive=%+v", r, r.Source, want)
				}
			}
			if count != 2 {
				t.Fatal(count)
			}
			if err = seed.MigrateMedia(ctx, s, catalog); err != nil {
				t.Fatal(err)
			}
			after, err := s.ListLiveRoom(ctx, "ORDER BY room_id")
			if err != nil || !reflect.DeepEqual(rooms, after) {
				t.Fatal("startup media migration overwrote video source", err)
			}
		}
	})
}
