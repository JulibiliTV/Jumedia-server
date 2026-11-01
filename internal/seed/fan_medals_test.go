package seed_test

import (
	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	seedfixture "jumedia-server/internal/seed/testdata"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
	"testing"
	"time"
)

func TestFanMedalsSeedAndReset(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		check := func() {
			t.Helper()
			rows, err := s.DB.QueryContext(t.Context(), "SELECT medal_id,ruid,intimacy,today,day,wearing,ctime FROM fan_medals WHERE mid=1 ORDER BY medal_id")
			if err != nil {
				t.Fatal(err)
			}
			i := 0
			var firstTime int64
			for rows.Next() {
				var id, ruid, intimacy, today, day, wearing, ctime int64
				if err = rows.Scan(&id, &ruid, &intimacy, &today, &day, &wearing, &ctime); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					firstTime = ctime
				}
				worn := int64(0)
				if i == 0 {
					worn = 1
				}
				active := time.Unix(firstTime+60*86400, 0).Add(-time.Duration(id%20) * 24 * time.Hour)
				expectedDay := int64(active.Year()*10000 + int(active.Month())*100 + active.Day())
				if i >= 6 || id != int64(7001+i) || ruid != int64(i+1) || intimacy != []int64{2480, 1820, 1240, 820, 260, 80}[i] || today != []int64{45, 20, 0, 35, 5, 0}[i] || day != expectedDay || wearing != worn || ctime != firstTime+int64(i)*7*86400 {
					t.Fatal(id, ruid, intimacy, today, day, wearing, ctime)
				}
				i++
			}
			err = rows.Err()
			rows.Close()
			if err != nil || i != 6 {
				t.Fatal(i, err)
			}
			var count, seq int64
			if err = s.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM fan_medals").Scan(&count); err != nil || count != 17 {
				t.Fatal(count, err)
			}
			if err = s.DB.QueryRowContext(t.Context(), "SELECT value FROM ids WHERE name=?", "global").Scan(&seq); err != nil || seq != 7017 {
				t.Fatal(seq, err)
			}
			// m2 uses FNV-1a and chooses anchor (2*7)%6 = 2 (uid 3).
			var ruid, intimacy, wearing int64
			if err = s.DB.QueryRowContext(t.Context(), "SELECT ruid,intimacy,wearing FROM fan_medals WHERE mid=2").Scan(&ruid, &intimacy, &wearing); err != nil || ruid != 3 || intimacy != 3944 || wearing != 1 {
				t.Fatal(ruid, intimacy, wearing, err)
			}
		}
		if err := seedfixture.Seed(t.Context(), s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		check()
		if _, err := s.DB.ExecContext(t.Context(), "UPDATE fan_medals SET intimacy=99 WHERE medal_id=7001"); err != nil {
			t.Fatal(err)
		}
		if err := seedfixture.Seed(t.Context(), s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := s.DB.QueryRowContext(t.Context(), "SELECT intimacy FROM fan_medals WHERE medal_id=7001").Scan(&n); err != nil || n != 99 {
			t.Fatal(n, err)
		}
		if err := seed.Reset(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM fan_medals").Scan(&n); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	})
}
