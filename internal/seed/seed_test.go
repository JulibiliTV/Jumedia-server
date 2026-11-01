package seed_test

import (
	"context"
	"encoding/json"
	seedfixture "jumedia-server/internal/seed/testdata"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestSeedContract(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		if err := seedfixture.Seed(ctx, s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		var counts map[string]int
		if err := json.Unmarshal([]byte(seedfixture.Counts), &counts); err != nil {
			t.Fatal(err)
		}
		counts["regions"] = 67
		counts["reservations"] = 13
		counts["seq"] = 7017
		counts["fan_medals"] = 17
		counts["banners"] = 22
		counts["settings"] = 29 // T24 retains four server runtime settings, dropping three Emby overrides.
		check := func() {
			t.Helper()
			var mismatches, empty int
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM archives WHERE stat_reply<>(SELECT COUNT(*) FROM replies WHERE oid=archives.aid)").Scan(&mismatches); err != nil || mismatches != 0 {
				t.Fatalf("seed/reset reply count mismatches: %d, %v", mismatches, err)
			}
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM archives WHERE stat_reply=0").Scan(&empty); err != nil || empty == 0 {
				t.Fatalf("expected archives without replies: %d, %v", empty, err)
			}
			for table, want := range counts {
				var got int
				q := "SELECT COUNT(*) FROM " + table
				if table == "seq" {
					q = "SELECT value FROM ids WHERE name='global'"
				}
				if err := s.DB.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
					t.Fatalf("%s got %d want %d: %v", table, got, want, err)
				}
			}
		}
		check()
		for _, v := range []struct{ name, pw string }{{"jumedia", "demo123"}, {"lumen", "demo123"}} {
			u, e := s.UserByUsername(ctx, v.name)
			if e != nil {
				t.Fatal(e)
			}
			if e = bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(v.pw)); e != nil {
				t.Fatal(e)
			}
		}
		a, err := s.ArchiveByID(ctx, 100100)
		if err != nil || len(a.Pages) != 1 || a.Pages[0].CID != 50001 || a.MID != 2 {
			t.Fatalf("archive %+v %v", a, err)
		}
		season, err := s.SeasonByID(ctx, 40000)
		if err != nil || len(season.Episodes) != 6 || season.Episodes[0].EpID != 300001 {
			t.Fatalf("season %v", err)
		}
		room, err := s.RoomByID(ctx, 20000)
		if err != nil || room.Source == nil || room.Source.Type != "push" || room.Source.URL != "" || room.LiveStatus != 0 || room.Online != 0 || room.PendingStart || !room.Source.Loop {
			t.Fatalf("source %+v %v", room, err)
		}
		published, err := s.PublishedArchives(ctx)
		if err != nil || len(published) != 40 {
			t.Fatalf("published %d %v", len(published), err)
		}
		fans, err := s.FollowerCount(ctx, 1)
		if err != nil || fans != 9875 {
			t.Fatalf("followers %d %v", fans, err)
		}
		if err = seedfixture.Seed(ctx, s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		check()
		if err = seed.SeedDanmaku(ctx, s, 50001, a.Pages[0].Duration); err != nil {
			t.Fatal(err)
		}
		if err = seed.SeedDanmaku(ctx, s, 50001, a.Pages[0].Duration); err != nil {
			t.Fatal(err)
		}
		var n int
		if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM danmaku WHERE cid=?", 50001).Scan(&n); err != nil || n != 46 {
			t.Fatalf("danmaku %d %v", n, err)
		}
		if err = s.SetSetting(ctx, "siteName", "changed"); err != nil {
			t.Fatal(err)
		}
		if err = seed.Reset(ctx, s); err != nil {
			t.Fatal(err)
		}
		if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		v, err := s.Setting(ctx, "siteName")
		if err != nil || v != "Jumedia" {
			t.Fatalf("reset setting %v %v", v, err)
		}
	})
}

func TestBasePreservesExistingRegions(t *testing.T) {
	s := testutil.Open(t, "sqlite")
	ctx := t.Context()
	if err := store.InsertRegion(ctx, s.DB, &store.Region{TID: 1005, Name: "preexisting"}); err != nil {
		t.Fatal(err)
	}
	if err := seed.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := s.DB.QueryRowContext(ctx, "SELECT name FROM regions WHERE tid=?", 1005).Scan(&name); err != nil || name != "preexisting" {
		t.Fatal(name, err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
