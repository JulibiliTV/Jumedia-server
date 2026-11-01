package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

type fanMedal struct{ ID, MID, RUID, Intimacy, Today, Day, Wearing, Ctime int64 }

var medalLevels = []int64{0, 100, 200, 300, 500, 700, 1000, 1300, 1600, 2000, 2500, 3000, 4000, 5000, 6500, 8000, 10000, 13000, 16000, 20000}

// Decay counts complete 24-hour periods since local midnight of the last day.
func medalIntimacy(m fanMedal, at time.Time) int64 {
	start := m.Ctime
	if m.Day != 0 {
		start = time.Date(int(m.Day/10000), time.Month(m.Day/100%100), int(m.Day%100), 0, 0, 0, 0, at.Location()).Unix()
	}
	idle := int64(math.Floor(float64(at.Unix()-start)/86400)) - 30
	if idle > 0 {
		return max(0, m.Intimacy-idle*20)
	}
	return m.Intimacy
}

func medalLevel(intimacy int64) int {
	level := 0
	for _, n := range medalLevels {
		if intimacy >= n {
			level++
		}
	}
	return level
}
func medalColor(level int) int {
	switch {
	case level >= 17:
		return 0xc79d24
	case level >= 13:
		return 0xbe6686
	case level >= 9:
		return 0x8d7ca6
	case level >= 5:
		return 0x5d7b9e
	default:
		return 0x5c968e
	}
}
func medalDay(at time.Time) int64 { return int64(at.Year()*10000 + int(at.Month())*100 + at.Day()) }
func (s *Server) medalName(ctx context.Context, ruid int64) (string, error) {
	u, err := s.Store.UserByID(ctx, ruid)
	if errors.Is(err, sql.ErrNoRows) {
		return "主播团", nil
	}
	if err != nil {
		return "", err
	}
	name := []rune(u.Uname)
	return string(name[:min(2, len(name))]) + "团", nil
}
func readFanMedals(ctx context.Context, q db.Querier, query string, args ...any) ([]fanMedal, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []fanMedal{}
	for rows.Next() {
		var m fanMedal
		if err = rows.Scan(&m.ID, &m.MID, &m.RUID, &m.Intimacy, &m.Today, &m.Day, &m.Wearing, &m.Ctime); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const medalColumns = "medal_id,mid,ruid,intimacy,today,day,wearing,ctime"

func (s *Server) medalJSON(ctx context.Context, m fanMedal, at time.Time) (liveMap, error) {
	name, err := s.medalName(ctx, m.RUID)
	if err != nil {
		return nil, err
	}
	intimacy := medalIntimacy(m, at)
	level := medalLevel(intimacy)
	base := medalLevels[level-1]
	next := base + 5000
	if level < len(medalLevels) {
		next = medalLevels[level]
	}
	today := int64(0)
	if m.Day == medalDay(at) {
		today = m.Today
	}
	color := medalColor(level)
	return liveMap{"medal_id": m.ID, "uid": m.MID, "target_id": m.RUID, "medal_name": name, "level": level, "intimacy": intimacy - base, "next_intimacy": next - base, "today_feed": today, "day_limit": 100, "wearing_status": m.Wearing, "is_lighted": 1, "medal_color": color, "medal_color_start": color, "medal_color_end": color, "medal_color_border": color, "guard_level": 0, "total_intimacy": intimacy}, nil
}
func (s *Server) wornMedal(ctx context.Context, mid int64) (liveMap, error) {
	medals, err := readFanMedals(ctx, s.Store.DB, "SELECT "+medalColumns+" FROM fan_medals WHERE mid=? AND wearing=1", mid)
	if err != nil || len(medals) == 0 {
		return nil, err
	}
	m := medals[0]
	name, err := s.medalName(ctx, m.RUID)
	if err != nil {
		return nil, err
	}
	level := medalLevel(medalIntimacy(m, publicationNow(ctx)))
	color := medalColor(level)
	hex := fmt.Sprintf("#%06x", color)
	return liveMap{"name": name, "level": level, "ruid": m.RUID, "color_start": color, "color_end": color, "color_border": color, "v2_medal_color_start": hex, "v2_medal_color_text": hex, "guard_level": 0, "is_light": 1}, nil
}

// feedMedal uses the message transaction and its write lock, so failed sends
// never grant intimacy and concurrent sends cannot bypass the daily cap.
func feedMedal(ctx context.Context, tx *db.Tx, mid, ruid int64, at time.Time) error {
	medals, err := readFanMedals(ctx, tx, "SELECT "+medalColumns+" FROM fan_medals WHERE mid=? AND ruid=?", mid, ruid)
	if err != nil || len(medals) == 0 {
		return err
	}
	m := medals[0]
	day := medalDay(at)
	if m.Day != day {
		m.Intimacy = medalIntimacy(m, at)
		m.Today = 0
	}
	add := max(int64(0), min(int64(5), 100-m.Today))
	_, err = tx.ExecContext(ctx, "UPDATE fan_medals SET intimacy=?,today=?,day=? WHERE medal_id=?", m.Intimacy+add, m.Today+add, day, m.ID)
	return err
}
func (l *liveAPI) medalPanel(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	pn, ps, target := NumberWide(c.P["page"], 1), NumberWide(c.P["pageSize"], 10), NumberWide(c.P["target_id"], 0)
	mine, err := readFanMedals(c.Context(), l.s.Store.DB, "SELECT "+medalColumns+" FROM fan_medals WHERE mid=? ORDER BY medal_id", u.MID)
	if err != nil {
		return nil, err
	}
	at := l.now()
	sort.SliceStable(mine, func(i, j int) bool {
		if mine[i].Wearing != mine[j].Wearing {
			return mine[i].Wearing > mine[j].Wearing
		}
		return medalIntimacy(mine[i], at) > medalIntimacy(mine[j], at)
	})
	others, wearing := []fanMedal{}, []fanMedal{}
	for _, m := range mine {
		if target != 0 && float64(m.RUID) != target {
			continue
		}
		if m.Wearing == 1 {
			wearing = append(wearing, m)
		} else {
			others = append(others, m)
		}
	}
	total := len(others) + len(wearing)
	rows := func(ms []fanMedal) ([]liveMap, error) {
		out := []liveMap{}
		for _, m := range ms {
			medal, e := l.s.medalJSON(c.Context(), m, l.now())
			if e != nil {
				return nil, e
			}
			owner, e := l.s.Render.Owner(c.Context(), m.RUID)
			if e != nil {
				return nil, e
			}
			rooms, e := l.s.Store.ListLiveRoom(c.Context(), "WHERE uid=? ORDER BY room_id LIMIT 1", m.RUID)
			if e != nil {
				return nil, e
			}
			room := liveMap{"room_id": 0, "living_status": 0, "url": ""}
			if len(rooms) > 0 {
				r := rooms[0]
				room["room_id"] = r.RoomID
				room["url"] = fmt.Sprintf("/live/%d", r.RoomID)
				if r.LiveStatus == 1 && !r.Banned {
					room["living_status"] = 1
				}
			}
			out = append(out, liveMap{"medal": medal, "anchor_info": liveMap{"nick_name": owner["name"], "avatar": owner["face"], "verify": 0}, "room_info": room})
		}
		return out, nil
	}
	list, err := rows(Page(others, pn, ps))
	if err != nil {
		return nil, err
	}
	if pn != 1 {
		wearing = nil
	}
	special, err := rows(wearing)
	if err != nil {
		return nil, err
	}
	pages := math.Max(1, math.Ceil(float64(total)/ps))
	var totalPages any = pages
	if math.IsNaN(pages) || math.IsInf(pages, 0) {
		totalPages = nil
	}
	return liveMap{"list": list, "special_list": special, "total_number": total, "page_info": liveMap{"number": total, "current_page": pn, "has_more": pn*ps < float64(total), "total_page": totalPages}}, nil
}
func (l *liveAPI) medalRank(c *Ctx) (any, error) {
	r, err := l.room(c, ParseID(c.P["roomid"]))
	if err != nil {
		return nil, err
	}
	fans, err := readFanMedals(c.Context(), l.s.Store.DB, "SELECT "+medalColumns+" FROM fan_medals WHERE ruid=? ORDER BY medal_id", r.UID)
	if err != nil {
		return nil, err
	}
	at := l.now()
	sort.SliceStable(fans, func(i, j int) bool { return medalIntimacy(fans[i], at) > medalIntimacy(fans[j], at) })
	name, err := l.s.medalName(c.Context(), r.UID)
	if err != nil {
		return nil, err
	}
	list := []liveMap{}
	var own any
	for i, m := range fans {
		self := c.Me != nil && c.Me.MID == m.MID
		if i >= 50 && !self {
			continue
		}
		o, e := l.s.Render.Owner(c.Context(), m.MID)
		if e != nil {
			return nil, e
		}
		isSelf := 0
		if self {
			isSelf = 1
		}
		level := medalLevel(medalIntimacy(m, at))
		row := liveMap{"uid": m.MID, "uname": o["name"], "face": o["face"], "rank": i + 1, "medal_name": name, "level": level, "color": medalColor(level), "target_id": r.UID, "special": "", "isSelf": isSelf}
		if i < 50 {
			list = append(list, row)
		}
		if self {
			own = row
		}
	}
	return liveMap{"list": list, "own": own, "medal_name": name, "total": len(fans)}, nil
}
func (l *liveAPI) medalWear(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	id := ParseID(c.P["medal_id"])
	err = l.s.WriteTx(c, func(tx *db.Tx) error {
		var count int
		if e := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM fan_medals WHERE mid=? AND medal_id=?", u.MID, id).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			return Fail(-404, "勋章不存在")
		}
		if _, e := tx.ExecContext(c.Context(), "UPDATE fan_medals SET wearing=0 WHERE mid=?", u.MID); e != nil {
			return e
		}
		_, e := tx.ExecContext(c.Context(), "UPDATE fan_medals SET wearing=1 WHERE mid=? AND medal_id=?", u.MID, id)
		return e
	})
	return nil, err
}
func (l *liveAPI) medalTakeOff(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	err = l.s.WriteTx(c, func(tx *db.Tx) error {
		_, e := tx.ExecContext(c.Context(), "UPDATE fan_medals SET wearing=0 WHERE mid=?", u.MID)
		return e
	})
	return nil, err
}
func (l *liveAPI) medalJoin(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	r, err := l.room(c, ParseID(c.P["roomid"]))
	if err != nil {
		return nil, err
	}
	if r.UID == u.MID {
		return nil, Fail(-400, "不能加入自己的粉丝团")
	}
	var m fanMedal
	err = l.s.WriteTx(c, func(tx *db.Tx) error {
		medals, e := readFanMedals(c.Context(), tx, "SELECT "+medalColumns+" FROM fan_medals WHERE mid=? AND ruid=?", u.MID, r.UID)
		if e != nil {
			return e
		}
		if len(medals) > 0 {
			m = medals[0]
			return nil
		}
		var wearing int
		if e = tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM fan_medals WHERE mid=? AND wearing=1", u.MID).Scan(&wearing); e != nil {
			return e
		}
		id, e := store.NextID(c.Context(), tx)
		if e != nil {
			return e
		}
		m = fanMedal{ID: id, MID: u.MID, RUID: r.UID, Ctime: l.now().Unix()}
		if wearing == 0 {
			m.Wearing = 1
		}
		_, e = tx.ExecContext(c.Context(), "INSERT INTO fan_medals("+medalColumns+") VALUES (?,?,?,?,?,?,?,?)", m.ID, m.MID, m.RUID, m.Intimacy, m.Today, m.Day, m.Wearing, m.Ctime)
		return e
	})
	if err != nil {
		return nil, err
	}
	medal, err := l.s.medalJSON(c.Context(), m, l.now())
	return liveMap{"medal": medal}, err
}

func (s *Server) liveChatMedals(ctx context.Context, mid int64) (liveMap, []any, error) {
	medal, err := s.wornMedal(ctx, mid)
	legacy := []any{}
	if err != nil || medal == nil {
		return medal, legacy, err
	}
	ruid := medal["ruid"].(int64)
	owner, err := s.Render.Owner(ctx, ruid)
	if err != nil {
		return nil, nil, err
	}
	rooms, err := s.Store.ListLiveRoom(ctx, "WHERE uid=? ORDER BY room_id LIMIT 1", ruid)
	if err != nil {
		return nil, nil, err
	}
	var roomID int64
	if len(rooms) > 0 {
		roomID = rooms[0].RoomID
	}
	legacy = []any{medal["level"], medal["name"], owner["name"], roomID, medal["color_start"]}
	return medal, legacy, nil
}
