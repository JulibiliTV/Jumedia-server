package store

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf16"

	"jumedia-server/internal/db"
)

// ExtendDemoLive implements mock v14 extend14 for migration 010 and seed/reset.
// The caller owns the transaction. A video source without a cache is picked up
// by the existing live-cache startup recovery before HTTP starts serving.
func ExtendDemoLive(ctx context.Context, tx *db.Tx) error {
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
		var src *StreamSource
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
		if r.id == 21000 || r.id == 21018 {
			a, err = firstDemoLiveArchive(ctx, tx, r.uid)
			if err != nil {
				return err
			}
		}
		src := StreamSource{Type: "push", Loop: true}
		if a != nil {
			src = StreamSource{Type: "video", Loop: true, Format: "ts", Kind: "vod", AID: a.aid, CID: a.cid}
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
