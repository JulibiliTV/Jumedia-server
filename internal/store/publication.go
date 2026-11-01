package store

import (
	"context"
	"encoding/json"
	"time"

	"jumedia-server/internal/db"
)

// migratePublication adds columns conditionally so replaying migration 009 is safe
// on both engines. All identifiers below are fixed, never request input.
func migratePublication(ctx context.Context, tx *db.Tx) error {
	for _, table := range []struct {
		name    string
		columns []struct{ name, ddl string }
	}{
		{"archives", []struct{ name, ddl string }{{"dtime", "BIGINT NOT NULL DEFAULT 0"}, {"no_disturbance", "INTEGER NOT NULL DEFAULT 0"}, {"no_tv", "INTEGER NOT NULL DEFAULT 0"}}},
		{"rooms", []struct{ name, ddl string }{{"stream_key", "TEXT NOT NULL DEFAULT ''"}}},
		{"episodes", []struct{ name, ddl string }{{"op", "TEXT NOT NULL DEFAULT 'null'"}, {"points", "TEXT NOT NULL DEFAULT 'null'"}}},
	} {
		// Read the column list from the catalog: a `SELECT *` probe would be a
		// cached pgx plan whose result type changes after ALTER TABLE.
		query := "SELECT name FROM pragma_table_info(?)"
		if tx.Driver != "sqlite" {
			query = "SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=?"
		}
		rows, err := tx.QueryContext(ctx, query, table.name)
		if err != nil {
			return err
		}
		found := map[string]bool{}
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			found[name] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, col := range table.columns {
			if !found[col.name] {
				if _, err = tx.ExecContext(ctx, "ALTER TABLE "+table.name+" ADD COLUMN "+col.name+" "+col.ddl); err != nil {
					return err
				}
			}
		}
	}
	return ExtendPublication(ctx, tx, time.Now())
}

// ExtendPublication is Mock extend13, used only by migration 009 and seed/reset.
// The caller owns the transaction; existing settings and episode extras survive.
func ExtendPublication(ctx context.Context, tx *db.Tx, at time.Time) error {
	raw, _ := json.Marshal("加微信\n加V\n代刷\n免费领取\n私聊我\n刷粉")
	if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO NOTHING", "sensitiveWords", string(raw)); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT medal_id FROM fan_medals WHERE day=0")
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		d := at.Add(-time.Duration(id%20) * 24 * time.Hour)
		day := d.Year()*10000 + int(d.Month())*100 + d.Day()
		if _, err = tx.ExecContext(ctx, "UPDATE fan_medals SET day=? WHERE medal_id=? AND day=0", day, id); err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, "SELECT ep_id,duration,long_title FROM episodes WHERE season_id IN (SELECT season_id FROM seasons WHERE season_type IN (1,4)) AND duration>=600 AND (op='null' OR op='')")
	if err != nil {
		return err
	}
	type episode struct {
		id       int64
		duration int
		title    string
	}
	episodes := []episode{}
	for rows.Next() {
		var e episode
		if err = rows.Scan(&e.id, &e.duration, &e.title); err != nil {
			rows.Close()
			return err
		}
		episodes = append(episodes, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range episodes {
		start := 30 + int(e.id%4)*15
		title := e.title
		if title == "" {
			title = "正片开始"
		}
		op, _ := json.Marshal([]int{start, start + 89})
		points, _ := json.Marshal([]map[string]any{{"from": 0, "content": "前情回顾"}, {"from": start + 90, "content": title}, {"from": e.duration * 55 / 100, "content": "转折"}, {"from": e.duration * 86 / 100, "content": "片尾与下集预告"}})
		if _, err = tx.ExecContext(ctx, "UPDATE episodes SET op=?,points=? WHERE ep_id=?", string(op), string(points), e.id); err != nil {
			return err
		}
	}
	return nil
}
