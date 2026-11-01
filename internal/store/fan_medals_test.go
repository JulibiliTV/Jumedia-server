package store_test

import (
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
	"testing"
)

func TestFanMedalsMigration(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := s.DB.ExecContext(t.Context(), q, args...); err != nil {
				t.Fatal(err)
			}
		}
		exec("DROP TABLE fan_medals")
		exec("DELETE FROM schema_versions WHERE version=8")
		exec("INSERT INTO live_areas(id) VALUES (0)")
		exec("INSERT INTO live_area_children(id,parent_id) VALUES (0,0)")
		for i := 1; i <= 8; i++ {
			exec("INSERT INTO users(mid,username) VALUES (?,?)", i, string(rune('a'+i)))
			exec("INSERT INTO rooms(room_id,uid) VALUES (?,?)", 100+i, i)
		}
		for i := 0; i < 2; i++ {
			if err := s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := s.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM fan_medals WHERE mid=1").Scan(&count); err != nil || count != 6 {
			t.Fatal(count, err)
		}
		exec("UPDATE fan_medals SET intimacy=9999 WHERE medal_id=7001")
		// Replay the SQL and data extension, not just skip a recorded version.
		exec("DELETE FROM schema_versions WHERE version=8")
		if err := s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRowContext(t.Context(), "SELECT intimacy FROM fan_medals WHERE medal_id=7001").Scan(&count); err != nil || count != 9999 {
			t.Fatal(count, err)
		}
		if _, err := s.DB.ExecContext(t.Context(), "UPDATE fan_medals SET wearing=1 WHERE medal_id=7002"); err == nil {
			t.Fatal("missing wearing uniqueness")
		}
		if _, err := s.DB.ExecContext(t.Context(), "INSERT INTO fan_medals(medal_id,mid,ruid) VALUES (9000,1,1)"); err == nil {
			t.Fatal("missing membership uniqueness")
		}
		id, err := s.NextID(t.Context())
		if err != nil || id <= 7006 {
			t.Fatal(id, err)
		}
	})
}
