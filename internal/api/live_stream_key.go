package api

import (
	"crypto/rand"
	"encoding/hex"
	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

// Creation, first-use key generation and reset share the database write lock,
// including across multiple server instances. Reads never rotate an existing key.
func (l *liveAPI) streamRoom(c *Ctx, u *store.User, reset bool) (*store.LiveRoom, liveMap, error) {
	var id int64
	var key, title string
	err := l.s.WriteTx(c, func(tx *db.Tx) error {
		err := tx.QueryRowContext(c.Context(), "SELECT room_id,title,stream_key FROM rooms WHERE uid=? ORDER BY room_id LIMIT 1", u.MID).Scan(&id, &title, &key)
		if adminMissing(err) {
			r := &store.LiveRoom{RoomID: 30000 + u.MID, UID: u.MID, Title: u.Uname + "的直播间", ParentAreaID: 10, AreaID: 646, Source: &store.StreamSource{Type: "push", Loop: true}}
			if err = store.InsertLiveRoom(c.Context(), tx, r); err != nil {
				return err
			}
			id, title = r.RoomID, r.Title
		} else if err != nil {
			return err
		}
		if key == "" || reset {
			b := make([]byte, 12)
			if _, err = rand.Read(b); err != nil {
				return err
			}
			key = hex.EncodeToString(b)
			if _, err = tx.ExecContext(c.Context(), "UPDATE rooms SET stream_key=? WHERE room_id=?", key, id); err != nil {
				return err
			}
		}
		if reset {
			return l.s.LogTx(c.Context(), tx, u.MID, "重置推流码", title)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	r, err := l.s.Store.RoomByID(c.Context(), id)
	addr := liveMap{"addr": "rtmp://" + liveHost(c) + "/live-jumedia/", "code": "?streamname=live_" + liveStringID(u.MID) + "_" + liveStringID(id) + "&key=" + key}
	return r, addr, err
}
func (l *liveAPI) webUpStreamAddr(c *Ctx) (any, error) {
	reset := BoolParam(c.P["reset_key"])
	u, err := c.Login()
	if reset {
		u, err = c.CSRF()
	}
	if err != nil {
		return nil, err
	}
	_, addr, err := l.streamRoom(c, u, reset)
	if err != nil {
		return nil, err
	}
	return liveMap{"addr": addr, "line_list": []any{}, "srt_addr": nil}, nil
}
