package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	mathrand "math/rand/v2"
	"strings"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func (l *liveAPI) areas(c *Ctx) (any, error) {
	areas, e := l.s.Store.LiveAreas(c.Context())
	if e != nil {
		return nil, e
	}
	out := []liveMap{}
	for _, a := range areas {
		children := []liveMap{}
		for _, v := range a.Children {
			children = append(children, liveMap{"id": liveStringID(v.ID), "name": v.Name, "parent_id": liveStringID(a.ID), "parent_name": a.Name})
		}
		out = append(out, liveMap{"id": a.ID, "name": a.Name, "list": children})
	}
	return liveMap{"data": out}, nil
}
func (l *liveAPI) rooms(c *Ctx) (any, error) {
	clause := "WHERE live_status=1 AND banned=0 AND (CAST(? AS DOUBLE PRECISION)=0 OR parent_area_id=CAST(? AS DOUBLE PRECISION)) AND (CAST(? AS DOUBLE PRECISION)=0 OR area_id=CAST(? AS DOUBLE PRECISION)) ORDER BY online DESC, room_id"
	if c.P["sort_type"] == "live_time" {
		clause = "WHERE live_status=1 AND banned=0 AND (CAST(? AS DOUBLE PRECISION)=0 OR parent_area_id=CAST(? AS DOUBLE PRECISION)) AND (CAST(? AS DOUBLE PRECISION)=0 OR area_id=CAST(? AS DOUBLE PRECISION)) ORDER BY live_time DESC, room_id"
	}
	parent, area := NumberUnicodeGoFloat(c.P["parent_area_id"], 0), NumberUnicodeGoFloat(c.P["area_id"], 0)
	rows, e := l.s.Store.ListLiveRoom(c.Context(), clause, parent, parent, area, area)
	if e != nil {
		return nil, e
	}
	return l.cards(c, Page(rows, NumberUnicodeGoFloat(c.P["page"], 1), NumberUnicodeGoFloat(c.P["page_size"], 20)))
}
func (l *liveAPI) recommend(c *Ctx) (any, error) {
	rows, e := l.s.Store.ListLiveRoom(c.Context(), "WHERE live_status=1 AND banned=0 ORDER BY online DESC, room_id")
	if e != nil {
		return nil, e
	}
	pn, ps := NumberUnicodeGoFloat(c.P["page"], 1), NumberUnicodeGoFloat(c.P["page_size"], 30)
	cards, e := l.cards(c, Page(rows, pn, ps))
	return liveMap{"list": cards, "has_more": store.BoolInt(pn*ps < float64(len(rows)))}, e
}
func (l *liveAPI) following(c *Ctx) (any, error) {
	u, e := c.Login()
	if e != nil {
		return nil, e
	}
	rows, e := l.s.Store.ListLiveRoom(c.Context(), "WHERE uid IN (SELECT fid FROM relations WHERE mid=?) ORDER BY live_status DESC, online DESC, room_id", u.MID)
	if e != nil {
		return nil, e
	}
	pn, ps := NumberUnicodeGoFloat(c.P["page"], 1), NumberUnicodeGoFloat(c.P["page_size"], 20)
	cards, e := l.cards(c, Page(rows, pn, ps))
	if e != nil {
		return nil, e
	}
	for _, v := range cards {
		v["area_name_v2"] = v["area_v2_name"]
	}
	liveCount := 0
	for _, r := range rows {
		if r.LiveStatus == 1 {
			liveCount++
		}
	}
	var pages any = math.Max(1, math.Ceil(float64(len(rows))/ps))
	if n := pages.(float64); math.IsNaN(n) || math.IsInf(n, 0) {
		pages = nil
	}
	return liveMap{"title": "我的关注", "pageSize": ps, "totalPage": pages, "count": len(rows), "live_count": liveCount, "list": cards}, nil
}
func (l *liveAPI) info(c *Ctx) (any, error) {
	r, e := l.room(c, ParseUnicodeID(c.P["room_id"]))
	if e != nil {
		return nil, e
	}
	card, e := l.s.Render.RoomCard(c.Context(), *r)
	if e != nil {
		return nil, e
	}
	fans, e := l.s.Store.FollowerCount(c.Context(), r.UID)
	if e != nil {
		return nil, e
	}
	when := "0000-00-00 00:00:00"
	if r.LiveTime != 0 {
		when = liveDate(r.LiveTime)
	}
	return liveMap{"uid": r.UID, "room_id": r.RoomID, "short_id": 0, "title": r.Title, "user_cover": r.Cover, "keyframe": r.Cover, "live_status": r.LiveStatus, "online": r.Online, "area_id": r.AreaID, "area_name": card["area_v2_name"], "parent_area_id": r.ParentAreaID, "parent_area_name": card["area_v2_parent_name"], "description": r.Description, "tags": r.Tags, "live_time": when, "attention": fans}, nil
}
func (l *liveAPI) init(c *Ctx) (any, error) {
	r, e := l.room(c, ParseUnicodeID(c.P["id"]))
	if e != nil {
		return nil, e
	}
	return liveMap{"room_id": r.RoomID, "short_id": 0, "uid": r.UID, "live_status": r.LiveStatus, "live_time": r.LiveTime, "is_locked": r.Banned}, nil
}
func (l *liveAPI) master(c *Ctx) (any, error) {
	u, e := l.s.Store.UserByID(c.Context(), ParseUnicodeID(c.P["uid"]))
	if errors.Is(e, sql.ErrNoRows) {
		u = &store.User{}
	} else if e != nil {
		return nil, e
	}
	fans, e := l.s.Store.FollowerCount(c.Context(), u.MID)
	if e != nil {
		return nil, e
	}
	rooms, e := l.s.Store.ListLiveRoom(c.Context(), "WHERE uid=? ORDER BY room_id LIMIT 1", u.MID)
	if e != nil {
		return nil, e
	}
	var rid int64
	if len(rooms) > 0 {
		rid = rooms[0].RoomID
	}
	return liveMap{"info": liveMap{"uid": u.MID, "uname": u.Uname, "face": render.Face(ParseUnicodeID(c.P["uid"]), u.Face), "gender": 0, "official_verify": liveMap{"type": -1, "desc": ""}}, "follower_num": fans, "room_id": rid, "room_news": liveMap{"content": ""}}, nil
}
func liveToken() string { b := make([]byte, 12); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func (l *liveAPI) appendMessage(c *Ctx, rid, uid int64, text string) error {
	_, err := l.insertMessage(c, rid, uid, text)
	return err
}
func (l *liveAPI) insertMessage(c *Ctx, rid, uid int64, text string, feedRUID ...int64) (*store.LiveMessage, error) {
	m := &store.LiveMessage{RoomID: rid, UID: uid, Text: text, TS: l.now().Unix()}
	err := l.s.WriteTx(c, func(tx *db.Tx) error {
		id, e := store.NextID(c.Context(), tx)
		if e != nil {
			return e
		}
		m.ID = id
		if e = store.InsertLiveMessage(c.Context(), tx, m); e != nil {
			return e
		}
		if len(feedRUID) > 0 {
			return feedMedal(c.Context(), tx, uid, feedRUID[0], l.now())
		}
		return nil
	})
	return m, err
}
func (l *liveAPI) history(c *Ctx) (any, error) {
	r, e := l.room(c, ParseUnicodeID(c.P["roomid"]))
	if e != nil {
		return nil, e
	}
	if r.LiveStatus == 1 && mathrand.Float64() < 0.7 {
		var count int
		if e = l.s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM users").Scan(&count); e != nil {
			return nil, e
		}
		uid := int64(1)
		if count > 0 {
			uid += int64(mathrand.IntN(count))
		}
		if e = l.appendMessage(c, r.RoomID, uid, seed.LiveChatter[mathrand.IntN(len(seed.LiveChatter))]); e != nil {
			return nil, e
		}
	}
	rows, e := l.s.Store.ListLiveMessage(c.Context(), "WHERE room_id=? AND (price IS NULL OR price=0) ORDER BY id DESC LIMIT 30", r.RoomID)
	if e != nil {
		return nil, e
	}
	out := []liveMap{}
	for i := len(rows) - 1; i >= 0; i-- {
		m := rows[i]
		name, face, e := l.messageUser(c, m.UID, "观众")
		if e != nil {
			return nil, e
		}
		medal, e := l.s.wornMedal(c.Context(), m.UID)
		if e != nil {
			return nil, e
		}
		out = append(out, liveMap{"text": m.Text, "uid": m.UID, "nickname": name, "timeline": liveDate(m.TS), "id_str": liveStringID(m.ID), "check_info": liveMap{"ts": m.TS}, "user": liveMap{"uid": m.UID, "base": liveMap{"name": name, "face": face}, "medal": medal}, "medal_info": nil})
	}
	return liveMap{"admin": []any{}, "room": out}, nil
}
func (l *liveAPI) messageUser(c *Ctx, uid int64, fallback string) (string, string, error) {
	u, e := l.s.Store.UserByID(c.Context(), uid)
	if errors.Is(e, sql.ErrNoRows) {
		return fallback, render.Face(uid, ""), nil
	}
	if e != nil {
		return "", "", e
	}
	return u.Uname, render.Face(u.MID, u.Face), nil
}
func (l *liveAPI) superChat(c *Ctx) (any, error) {
	rows, e := l.s.Store.ListLiveMessage(c.Context(), "WHERE room_id=? AND price IS NOT NULL AND price<>0 ORDER BY id", ParseUnicodeID(c.P["room_id"]))
	if e != nil {
		return nil, e
	}
	out := []liveMap{}
	for _, m := range rows {
		name, face, e := l.messageUser(c, m.UID, "")
		if e != nil {
			return nil, e
		}
		out = append(out, liveChatSuperChat(m, name, face))
	}
	return liveMap{"list": out}, nil
}
func (l *liveAPI) send(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	r, e := l.room(c, ParseUnicodeID(c.P["roomid"]))
	if e != nil {
		return nil, e
	}
	msg := strings.TrimSpace(c.P["msg"])
	if msg == "" || UTF16Length(msg) > 40 {
		return nil, Fail(-400, "弹幕需在 1–40 字之间")
	}
	message, e := l.insertMessage(c, r.RoomID, u.MID, msg, r.UID)
	if e != nil {
		return nil, e
	}
	medal, legacy, e := l.s.liveChatMedals(c.Context(), u.MID)
	if e != nil {
		return nil, e
	}
	l.s.liveChat.broadcast(r.RoomID, liveChatDanmu(message, u, l.s.Origin(c.R), medal, legacy), false)
	return liveMap{"mode_info": liveMap{"mode": 0}}, nil
}
func (l *liveAPI) creatorInfo(c *Ctx) (any, error) {
	u, e := c.Login()
	if e != nil {
		return nil, e
	}
	r, addr, e := l.streamRoom(c, u, false)
	if e != nil {
		return nil, e
	}
	card, e := l.s.Render.RoomCard(c.Context(), *r)
	if e != nil {
		return nil, e
	}
	card["rtmp"] = addr
	card["banned"] = r.Banned
	card["pending_start"] = r.PendingStart
	card["source"], e = l.s.liveSourceJSON(c, r)
	if e != nil {
		return nil, e
	}
	return card, nil
}
func (l *liveAPI) update(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	r, e := l.ownRoom(c, u.MID)
	if e != nil {
		return nil, e
	}
	if c.P["title"] != "" {
		r.Title = UTF16PrefixNormalized(c.P["title"], 40)
	}
	if c.P["cover"] != "" {
		r.Cover = c.P["cover"]
	}
	e = l.s.WriteTx(c, func(tx *db.Tx) error {
		if c.P["area_id"] != "" {
			r.AreaID = ParseUnicodeID(c.P["area_id"])
			err := tx.QueryRowContext(c.Context(), "SELECT parent_id FROM live_area_children WHERE id=?", r.AreaID).Scan(&r.ParentAreaID)
			if errors.Is(err, sql.ErrNoRows) {
				return Fail(-400, "分区不存在")
			}
			if err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(c.Context(), "UPDATE rooms SET title=?,cover=?,area_id=?,parent_area_id=? WHERE room_id=?", r.Title, r.Cover, r.AreaID, r.ParentAreaID, r.RoomID)
		return err
	})
	return nil, e
}
func (l *liveAPI) start(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	r, e := l.ownRoom(c, u.MID)
	if e != nil {
		return nil, e
	}
	if r.Banned {
		return nil, Fail(60009, "直播间已被封禁")
	}
	if r.Source == nil || (r.Source.Type != "m3u8" && r.Source.Type != "video") || r.Source.URL == "" {
		return nil, Fail(-400, "暂未接入 RTMP 推流服务，请先配置 m3u8 直播源或选择转播视频")
	}
	if r.Source.Type == "video" {
		x, err := l.s.roomCache(c.Context(), l.s.Store.DB, r)
		if err != nil {
			return nil, err
		}
		if why := liveCacheNotReady(x); why != "" {
			return nil, Fail(-400, why)
		}
	}
	if id := ParseUnicodeID(c.P["area_v2"]); id != 0 {
		r.AreaID = id
	}
	e = l.s.WriteTx(c, func(tx *db.Tx) error {
		if err := RequireReference(c, tx, "SELECT COUNT(*) FROM live_area_children WHERE id=?", r.AreaID, -400, "分区不存在"); err != nil {
			return err
		}
		_, err := tx.ExecContext(c.Context(), "UPDATE rooms SET live_status=1,pending_start=0,live_time=?,online=1,area_id=? WHERE room_id=?", l.now().Unix(), r.AreaID, r.RoomID)
		return err
	})
	return liveMap{"change": 1, "status": "LIVE"}, e
}
func (l *liveAPI) stop(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	r, e := l.ownRoom(c, u.MID)
	var f *Failure
	if errors.As(e, &f) && f.Code == -404 {
		return liveMap{"change": 1, "status": "PREPARING"}, nil
	}
	if e != nil {
		return nil, e
	}
	_, e = l.s.Store.DB.ExecContext(c.Context(), "UPDATE rooms SET live_status=0,pending_start=0 WHERE room_id=?", r.RoomID)
	if e == nil {
		l.s.liveChat.preparing(r.RoomID)
	}
	return liveMap{"change": 1, "status": "PREPARING"}, e
}
