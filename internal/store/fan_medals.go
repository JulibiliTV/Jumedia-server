package store

import (
	"context"
	"fmt"

	"jumedia-server/internal/db"
)

// ExtendFanMedals ports Mock extend12 for both an existing v11 database and a
// freshly seeded one. The caller owns the transaction and startup/write lock.
func ExtendFanMedals(ctx context.Context, tx *db.Tx, at int64) error {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM fan_medals").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	readIDs := func(query string) ([]int64, error) {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	anchors, err := readIDs("SELECT uid FROM rooms GROUP BY uid ORDER BY MIN(room_id)")
	if err != nil {
		return err
	}
	users, err := readIDs("SELECT mid FROM users ORDER BY mid")
	if err != nil {
		return err
	}
	if len(anchors) == 0 || len(users) == 0 {
		return nil
	}
	id := int64(7000)
	insert := func(mid, ruid, intimacy, today, wearing, ctime int64) error {
		id++
		_, err := tx.ExecContext(ctx, "INSERT INTO fan_medals(medal_id,mid,ruid,intimacy,today,day,wearing,ctime) VALUES (?,?,?,?,?,0,?,?)", id, mid, ruid, intimacy, today, wearing, ctime)
		return err
	}
	if users[0] == 1 {
		for i, ruid := range anchors[:min(6, len(anchors))] {
			if err = insert(1, ruid, []int64{2480, 1820, 1240, 820, 260, 80}[i], []int64{45, 20, 0, 35, 5, 0}[i], int64(BoolInt(i == 0)), at-86400*int64(60-i*7)); err != nil {
				return err
			}
		}
	}
	for _, mid := range users {
		if mid <= 1 {
			continue
		}
		ruid := anchors[(mid*7)%int64(len(anchors))]
		if ruid == mid {
			continue
		}
		h := uint32(2166136261)
		for _, c := range fmt.Sprintf("m%d", mid) {
			h = (h ^ uint32(c)) * 16777619
		}
		if err = insert(mid, ruid, int64(h%5000)+50, 0, int64(BoolInt(mid%3 != 0)), at-86400*(mid%40)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE ids SET value=CASE WHEN value<? THEN ? ELSE value END WHERE name=?", id, id, "global")
	return err
}
