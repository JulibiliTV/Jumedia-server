package api

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) bplanReply(c *Ctx) (any, error) {
	oid, mode, ps := ParseID(c.P["oid"]), Number(c.P["mode"], 3), math.Min(30, Number(c.P["ps"], 20))
	next := Number(c.P["next"], 1)
	if next == 0 {
		next = 1
	}
	all, err := s.Store.ListReply(c.Context(), "WHERE oid=? ORDER BY rpid", oid)
	if err != nil {
		return nil, err
	}
	top := []store.Reply{}
	for _, r := range all {
		if r.Root == 0 {
			full, err := s.Store.ReplyByID(c.Context(), r.RPID)
			if err != nil {
				return nil, err
			}
			top = append(top, *full)
		}
	}
	sort.SliceStable(top, func(i, j int) bool {
		if mode != 2 && len(top[i].Likers) != len(top[j].Likers) {
			return len(top[i].Likers) > len(top[j].Likers)
		}
		return top[i].Ctime > top[j].Ctime
	})
	replies := []render.M{}
	for _, r := range Page(top, next, ps) {
		v, err := s.Render.ReplyJSON(c.Context(), r, ActorID(c))
		if err != nil {
			return nil, err
		}
		replies = append(replies, v)
	}
	var mid int64
	a, err := s.Store.ArchiveByID(c.Context(), oid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if a != nil {
		mid = a.MID
	}
	label := "最热评论"
	if mode == 2 {
		label = "最新评论"
	}
	return render.M{"cursor": render.M{"is_begin": next == 1, "prev": next - 1, "next": next + 1, "is_end": next*ps >= float64(len(top)), "mode": mode, "mode_text": label, "all_count": len(all), "name": label}, "replies": replies, "top_replies": []any{}, "upper": render.M{"mid": mid}}, nil
}
func (s *Server) bplanGold(c *Ctx) (any, error) {
	r, err := (&liveAPI{s: s}).room(c, ParseID(c.P["roomId"]))
	if err != nil {
		return nil, err
	}
	pn, ps := Number(c.P["page"], 1), math.Min(50, Number(c.P["pageSize"], 20))
	messages, err := s.Store.ListLiveMessage(c.Context(), "WHERE room_id=? ORDER BY id", r.RoomID)
	if err != nil {
		return nil, err
	}
	scores := map[int64]float64{}
	users := []int64{}
	for _, m := range messages {
		if m.UID == r.UID {
			continue
		}
		if _, ok := scores[m.UID]; !ok {
			users = append(users, m.UID)
		}
		scores[m.UID]++
		if m.Price != nil {
			scores[m.UID] += *m.Price * 10
		}
	}
	sort.SliceStable(users, func(i, j int) bool { return scores[users[i]] > scores[users[j]] })
	rows := []render.M{}
	for i, uid := range Page(users, pn, ps) {
		o, err := s.Render.Owner(c.Context(), uid)
		if err != nil {
			return nil, err
		}
		rows = append(rows, render.M{"userRank": (pn-1)*ps + float64(i) + 1, "uid": uid, "name": o["name"], "face": o["face"], "score": scores[uid], "medalInfo": nil, "guard_level": 0})
	}
	return render.M{"onlineNum": len(users), "OnlineRankItem": rows, "ownInfo": nil, "tips_text": ""}, nil
}
func (s *Server) bplanGuards(c *Ctx) (any, error) {
	r, err := (&liveAPI{s: s}).room(c, ParseID(c.P["roomid"]))
	if err != nil {
		return nil, err
	}
	pn, ps := Number(c.P["page"], 1), math.Min(30, Number(c.P["page_size"], 20))
	fans, err := s.Store.ListRelation(c.Context(), "WHERE fid=? ORDER BY mtime,mid", r.UID)
	if err != nil {
		return nil, err
	}
	rows := []render.M{}
	for i, f := range fans {
		o, err := s.Render.Owner(c.Context(), f.MID)
		if err != nil {
			return nil, err
		}
		level := 3
		if i == 0 {
			level = 2
		}
		rows = append(rows, render.M{"uid": f.MID, "ruid": r.UID, "rank": i + 1, "username": o["name"], "face": o["face"], "is_alive": 1, "guard_level": level, "guard_sub_level": 0})
	}
	top := []render.M{}
	if pn == 1 {
		top = rows[:min(3, len(rows))]
	}
	pages := math.Max(1, math.Ceil(float64(len(rows))/ps))
	var page any = pages
	if math.IsNaN(pages) || math.IsInf(pages, 0) {
		page = nil
	}
	return render.M{"info": render.M{"num": len(rows), "page": page, "now": pn, "achievement_level": 1}, "top3": top, "list": Page(rows[min(3, len(rows)):], pn, ps)}, nil
}
func (s *Server) bplanReservations(c *Ctx) (any, error) {
	rows, err := s.Store.DB.QueryContext(c.Context(), "SELECT sid,mid,title,stime,ctime FROM reservations WHERE mid=? AND stime>? ORDER BY stime,sid", DBID(Number(c.P["vmid"], 0)), time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []render.M{}
	for rows.Next() {
		var sid, mid, stime, ctime int64
		var title string
		if err = rows.Scan(&sid, &mid, &title, &stime, &ctime); err != nil {
			return nil, err
		}
		at := time.Unix(stime, 0).In(time.Local)
		out = append(out, render.M{"sid": sid, "name": title, "total": 0, "stime": stime, "etime": stime + 7200, "is_follow": 0, "state": 100, "oid": "", "type": 2, "up_mid": mid, "reserve_record_ctime": ctime, "live_plan_start_time": stime, "lottery_type": 0, "lottery_prize_info": nil, "show_total": true, "subtitle": fmt.Sprintf("%d/%d %02d:%02d 直播", at.Month(), at.Day(), at.Hour(), at.Minute()), "attached_badge_text": "", "ugc_info": nil, "desc": ""})
	}
	return out, rows.Err()
}
func (s *Server) bplanReservationAdd(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	title := Trim(c.P["title"])
	stime := Number(c.P["stime"], 0)
	if title == "" || UTF16Length(title) > 40 {
		return nil, Fail(-400, "标题需为 1–40 个字符")
	}
	if stime <= float64(time.Now().Unix()) {
		return nil, Fail(-400, "开播时间需晚于现在")
	}
	// Persistent seconds are BIGINT in both drivers. Avoid silent truncation.
	timestamp := DBID(stime)
	if timestamp == nil {
		return nil, Fail(-400, "开播时间需晚于现在")
	}
	var sid int64
	err = s.WriteTx(c, func(tx *db.Tx) error {
		var err error
		sid, err = store.NextID(c.Context(), tx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(c.Context(), "INSERT INTO reservations(sid,mid,title,stime,ctime) VALUES (?,?,?,?,?)", sid, u.MID, title, timestamp, time.Now().Unix())
		return err
	})
	if err != nil {
		return nil, err
	}
	return render.M{"sid": sid}, nil
}
func (s *Server) bplanReservationDelete(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	result, err := s.Store.DB.ExecContext(c.Context(), "DELETE FROM reservations WHERE sid=? AND mid=?", DBID(Number(c.P["sid"], 0)), u.MID)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, Fail(-404, "预约不存在")
	}
	return nil, nil
}
