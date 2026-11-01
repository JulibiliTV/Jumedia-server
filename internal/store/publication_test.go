package store_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestPublicationMigration009(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		if err := seed.Seed(t.Context(), s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := s.DB.ExecContext(t.Context(), q, args...); err != nil {
				t.Fatal(err)
			}
		}
		// Simulate a v12 database without any of the new columns/settings.
		exec("DROP INDEX idx_archives_scheduled")
		for _, q := range []string{"ALTER TABLE archives DROP COLUMN dtime", "ALTER TABLE archives DROP COLUMN no_tv", "ALTER TABLE archives DROP COLUMN no_disturbance", "ALTER TABLE rooms DROP COLUMN stream_key", "ALTER TABLE episodes DROP COLUMN op", "ALTER TABLE episodes DROP COLUMN points"} {
			exec(q)
		}
		exec("DELETE FROM settings WHERE key=?", "sensitiveWords")
		exec("UPDATE fan_medals SET day=0")
		exec("DELETE FROM schema_versions WHERE version=9")
		if err := s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		setting, err := s.Setting(t.Context(), "sensitiveWords")
		if err != nil || setting != "加微信\n加V\n代刷\n免费领取\n私聊我\n刷粉" {
			t.Fatal(setting, err)
		}
		var dtime, noTV, noDisturb int
		var key string
		if err = s.DB.QueryRowContext(t.Context(), "SELECT dtime,no_tv,no_disturbance FROM archives ORDER BY aid LIMIT 1").Scan(&dtime, &noTV, &noDisturb); err != nil || dtime != 0 || noTV != 0 || noDisturb != 0 {
			t.Fatal(dtime, noTV, noDisturb, err)
		}
		if err = s.DB.QueryRowContext(t.Context(), "SELECT stream_key FROM rooms ORDER BY room_id LIMIT 1").Scan(&key); err != nil || key != "" {
			t.Fatal(key, err)
		}
		rows, err := s.DB.QueryContext(t.Context(), "SELECT e.ep_id,e.duration,e.long_title,e.op,e.points,s.season_type FROM episodes e JOIN seasons s ON s.season_id=e.season_id")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var id int64
			var duration, typ int
			var title, opRaw, pointsRaw string
			if err = rows.Scan(&id, &duration, &title, &opRaw, &pointsRaw, &typ); err != nil {
				t.Fatal(err)
			}
			var op []int
			var points []struct {
				From    int    `json:"from"`
				Content string `json:"content"`
			}
			if err = json.Unmarshal([]byte(opRaw), &op); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(pointsRaw), &points); err != nil {
				t.Fatal(err)
			}
			if (typ == 1 || typ == 4) && duration >= 600 {
				start := 30 + int(id%4)*15
				if title == "" {
					title = "正片开始"
				}
				if !reflect.DeepEqual(op, []int{start, start + 89}) || len(points) != 4 || points[0].Content != "前情回顾" || points[1].From != start+90 || points[1].Content != title || points[2].From != duration*55/100 || points[3].From != duration*86/100 {
					t.Fatal(id, op, points)
				}
				n++
			} else if op != nil || points != nil {
				t.Fatal(id, op, points)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil || n == 0 {
			t.Fatal(n, err)
		}
		var day int64
		if err = s.DB.QueryRowContext(t.Context(), "SELECT day FROM fan_medals WHERE medal_id=7001").Scan(&day); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-24 * time.Hour)
		if day != int64(at.Year()*10000+int(at.Month())*100+at.Day()) {
			t.Fatal(day)
		}
		// Replay the actual migration, preserving user values and existing OP/points.
		exec("UPDATE episodes SET op='[1,2]',points='[{\"from\":1,\"content\":\"保留\"}]' WHERE ep_id=300001")
		exec("UPDATE fan_medals SET day=20200101 WHERE medal_id=7001")
		if err = s.SetSetting(t.Context(), "sensitiveWords", "自定义"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			exec("DELETE FROM schema_versions WHERE version=9")
			if err = s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		var opRaw, pointsRaw string
		if err = s.DB.QueryRowContext(t.Context(), "SELECT op,points FROM episodes WHERE ep_id=300001").Scan(&opRaw, &pointsRaw); err != nil || opRaw != "[1,2]" || pointsRaw != "[{\"from\":1,\"content\":\"保留\"}]" {
			t.Fatal(opRaw, pointsRaw, err)
		}
		if err = s.DB.QueryRowContext(t.Context(), "SELECT day FROM fan_medals WHERE medal_id=7001").Scan(&day); err != nil || day != 20200101 {
			t.Fatal(day, err)
		}
		setting, err = s.Setting(t.Context(), "sensitiveWords")
		if err != nil || setting != "自定义" {
			t.Fatal(setting, err)
		}
	})
}
