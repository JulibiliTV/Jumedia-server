package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

// ExtendFanMedals ports Mock extend12 for both an existing v11 database and a
// freshly seeded one. The caller owns the transaction and startup/write lock.
func demoFanMedals(ctx context.Context, tx *db.Tx, at, offset int64) error {
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
	users, err := readIDs("SELECT mid FROM users WHERE role='user' ORDER BY mid")
	if err != nil {
		return err
	}
	if len(anchors) == 0 || len(users) == 0 {
		return nil
	}
	id := offset + int64(7000)
	insert := func(mid, ruid, intimacy, today, wearing, ctime int64) error {
		id++
		_, err := tx.ExecContext(ctx, "INSERT INTO fan_medals(medal_id,mid,ruid,intimacy,today,day,wearing,ctime) VALUES (?,?,?,?,?,0,?,?)", id, mid, ruid, intimacy, today, wearing, ctime)
		return err
	}
	if users[0] == offset+1 {
		for i, ruid := range anchors[:min(6, len(anchors))] {
			if err = insert(offset+1, ruid, []int64{2480, 1820, 1240, 820, 260, 80}[i], []int64{45, 20, 0, 35, 5, 0}[i], int64(store.BoolInt(i == 0)), at-86400*int64(60-i*7)); err != nil {
				return err
			}
		}
	}
	for _, mid := range users {
		if mid <= offset+1 {
			continue
		}
		ruid := anchors[((mid-offset)*7)%int64(len(anchors))]
		if ruid == mid {
			continue
		}
		h := uint32(2166136261)
		for _, c := range fmt.Sprintf("m%d", mid-offset) {
			h = (h ^ uint32(c)) * 16777619
		}
		if err = insert(mid, ruid, int64(h%5000)+50, 0, int64(store.BoolInt((mid-offset)%3 != 0)), at-86400*((mid-offset)%40)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE ids SET value=CASE WHEN value<? THEN ? ELSE value END WHERE name=?", id, id, "global")
	return err
}

func demoLive(ctx context.Context, tx *db.Tx, offset int64) error {
	rows, err := tx.QueryContext(ctx, "SELECT room_id,uid,source FROM rooms ORDER BY room_id")
	if err != nil {
		return err
	}
	type room struct{ id, uid int64 }
	rooms := []room{}
	for rows.Next() {
		var r room
		var raw string
		if err = rows.Scan(&r.id, &r.uid, &raw); err != nil {
			rows.Close()
			return err
		}
		var src *store.StreamSource
		if err = json.Unmarshal([]byte(raw), &src); err != nil {
			rows.Close()
			return err
		}
		if src != nil && src.Type == "m3u8" && strings.HasPrefix(src.URL, "/media/emby/") {
			rooms = append(rooms, r)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range rooms {
		var a *demoLiveArchive
		if r.id == offset+21000 || r.id == offset+21018 {
			a, err = firstDemoLiveArchive(ctx, tx, r.uid)
			if err != nil {
				return err
			}
		}
		src := store.StreamSource{Type: "push", Loop: true}
		if a != nil {
			src = store.StreamSource{Type: "video", Loop: true, Format: "ts", Kind: "vod", AID: a.aid, CID: a.cid}
		}
		raw, err := json.Marshal(src)
		if err != nil {
			return err
		}
		if a == nil {
			_, err = tx.ExecContext(ctx, "UPDATE rooms SET source=?,live_status=0,online=0,pending_start=0 WHERE room_id=?", string(raw), r.id)
		} else {
			// Match JS slice(0,40), using the project's valid UTF-8 normalization
			// when the boundary cuts a surrogate pair.
			title := utf16.Encode([]rune("【放映厅】" + a.title))
			title = title[:min(40, len(title))]
			_, err = tx.ExecContext(ctx, "UPDATE rooms SET source=?,title=?,cover=?,description=?,live_status=1 WHERE room_id=?", string(raw), string(utf16.Decode(title)), a.pic, "循环转播《"+a.title+"》，欢迎一起看～", r.id)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type demoLiveArchive struct {
	aid, cid   int64
	title, pic string
}

func firstDemoLiveArchive(ctx context.Context, tx *db.Tx, uid int64) (*demoLiveArchive, error) {
	// position represents pages[0], not the smallest cid. Filtering media in Go
	// keeps prefix matching case-sensitive on both SQLite and PostgreSQL.
	rows, err := tx.QueryContext(ctx, `SELECT a.aid,p.cid,a.title,a.pic,p.media
 FROM archives a JOIN archive_pages p ON p.aid=a.aid
 WHERE a.mid=? AND a.state=0 AND p.position=(SELECT MIN(first.position) FROM archive_pages first WHERE first.aid=a.aid)
 ORDER BY a.aid,p.cid`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a demoLiveArchive
		var media string
		if err = rows.Scan(&a.aid, &a.cid, &a.title, &a.pic, &media); err != nil {
			return nil, err
		}
		if strings.HasPrefix(media, "emby:") {
			return &a, nil
		}
	}
	return nil, rows.Err()
}
