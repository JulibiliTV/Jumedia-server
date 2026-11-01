package seed

import (
	"context"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func seedBPlan(ctx context.Context, tx *db.Tx, t0 int64) error {
	if err := store.ExtendBPlan(ctx, tx); err != nil {
		return err
	}
	for mid, notice := range map[int64]string{1: "欢迎来到 Jumedia！每周五晚 8 点官方放映厅不见不散。", 2: "新一期光影实验周三更新，评论区留下你想看的题目。"} {
		if _, err := tx.ExecContext(ctx, "UPDATE users SET notice=? WHERE mid=?", notice, mid); err != nil {
			return err
		}
	}
	now := time.Unix(t0, 0).In(time.Local)
	day := func(n, h int) int64 {
		return time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location()).Unix() + int64(n)*86400
	}
	type room struct {
		uid   int64
		title string
	}
	rooms := []room{}
	rows, err := tx.QueryContext(ctx, "SELECT uid,title FROM rooms WHERE uid>1 ORDER BY room_id LIMIT 6")
	if err != nil {
		return err
	}
	for rows.Next() {
		var r room
		if err = rows.Scan(&r.uid, &r.title); err != nil {
			rows.Close()
			return err
		}
		rooms = append(rooms, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for i, r := range rooms {
		for j, title := range []string{strings.TrimPrefix(r.title, "【放映厅】") + " 续集放映", "周末一起看：观众点播场"} {
			stime := day(2+i%4, 20)
			if j == 1 {
				stime = day(5+i%3, 19)
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO reservations(sid,mid,title,stime,ctime) VALUES (?,?,?,?,?)", 5000+i*2+j, r.uid, title, stime, t0); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO reservations(sid,mid,title,stime,ctime) VALUES (?,?,?,?,?)", 4999, 1, "官方放映厅 · 周五电影夜", day(3, 20), t0); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE ids SET value=CASE WHEN value<? THEN ? ELSE value END WHERE name=?", 5011, 5011, "global")
	return err
}
