package seed_test

import (
	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
	"testing"
)

func TestBPlanSeed(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		if err := seed.Seed(t.Context(), s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		a, err := s.ArchiveByID(t.Context(), 100100)
		if err != nil {
			t.Fatal(err)
		}
		if a.SubTID == 0 {
			t.Fatal("missing sub region")
		}
		var parent int64
		if err := s.DB.QueryRowContext(t.Context(), "SELECT parent FROM regions WHERE tid=?", a.SubTID).Scan(&parent); err != nil || parent != a.TID {
			t.Fatal(parent, err)
		}
		for _, sid := range []int64{40000, 40088} {
			v, err := s.SeasonByID(t.Context(), sid)
			if err != nil || v.Copyright != 3 {
				t.Fatal(v, err)
			}
		}
		for _, mid := range []int64{1, 2} {
			u, err := s.UserByID(t.Context(), mid)
			if err != nil || u.Notice == "" {
				t.Fatal(u, err)
			}
		}
		var count int
		if err := s.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM reservations WHERE mid=1 AND stime>ctime").Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
		if _, err := s.DB.ExecContext(t.Context(), "UPDATE users SET notice=? WHERE mid=1", "保留公告"); err != nil {
			t.Fatal(err)
		}
		if err := seed.Seed(t.Context(), s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		u, err := s.UserByID(t.Context(), 1)
		if err != nil || u.Notice != "保留公告" {
			t.Fatal(u, err)
		}
	})
}
