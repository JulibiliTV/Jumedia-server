package api

import (
	"database/sql"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) spaceFollows(c *Ctx, mid, fid int64) (bool, error) {
	n, err := Count(c, s.Store.DB, "SELECT COUNT(*) FROM relations WHERE mid=? AND fid=?", mid, fid)
	return n > 0, err
}
func (s *Server) spaceFollowings(c *Ctx) (any, error) {
	mid := ParseWideID(c.P["vmid"])
	// T1 has no relation insertion position; equal-second ties use fid instead
	// of the Mock array's stable insertion order.
	rows, err := s.Store.ListRelation(c.Context(), "WHERE mid=? ORDER BY mtime DESC,fid", mid)
	if err != nil {
		return nil, err
	}
	list := []render.M{}
	for _, row := range Page(rows, NumberWide(c.P["pn"], 1), NumberWide(c.P["ps"], 50)) {
		u, err := s.Store.UserByID(c.Context(), row.FID)
		if errors.Is(err, sql.ErrNoRows) {
			u = &store.User{}
		} else if err != nil {
			return nil, err
		}
		mutual, err := s.spaceFollows(c, row.FID, mid)
		if err != nil {
			return nil, err
		}
		attribute := 2
		if mutual {
			attribute = 6
		}
		list = append(list, render.M{"mid": row.FID, "uname": u.Uname, "face": render.Face(row.FID, u.Face), "sign": u.Sign, "mtime": row.Mtime, "attribute": attribute})
	}
	return render.M{"total": len(rows), "list": list}, nil
}
func (s *Server) spaceInfo(c *Ctx) (any, error) {
	u, err := s.spaceUser(c, ParseWideID(c.P["mid"]))
	if err != nil {
		return nil, err
	}
	followed, err := s.spaceFollows(c, ActorID(c), u.MID)
	if err != nil {
		return nil, err
	}
	rooms, err := s.Store.ListLiveRoom(c.Context(), "WHERE uid=? AND banned=0 ORDER BY room_id LIMIT 1", u.MID)
	if err != nil {
		return nil, err
	}
	var room any
	if len(rooms) > 0 {
		r := rooms[0]
		room = render.M{"roomStatus": 1, "liveStatus": r.LiveStatus, "title": r.Title, "cover": r.Cover, "roomid": r.RoomID, "url": "/live/" + strconv.FormatInt(r.RoomID, 10), "watched_show": render.M{"text_large": render.CompactCount(r.Online) + "人看过"}}
	}
	birthday := ""
	if len(u.Birthday) > 5 {
		birthday = u.Birthday[5:]
	}
	label, title := "", ""
	role := 0
	if u.VIP != 0 {
		label = "年度大会员"
	}
	if u.Role == "admin" {
		role = 1
		title = "Jumedia 官方账号"
	}
	return render.M{"mid": u.MID, "name": u.Uname, "sex": u.Sex, "face": render.Face(u.MID, u.Face), "sign": u.Sign, "level": u.Level, "birthday": birthday, "top_photo": u.TopPhoto, "is_followed": followed, "silence": u.Status, "jointime": u.Ctime, "vip": render.M{"status": u.VIP, "label": render.M{"text": label}}, "official": render.M{"role": role, "title": title}, "live_room": room}, nil
}
func (s *Server) spaceRelation(c *Ctx) (any, error) {
	mid, me := ParseWideID(c.P["mid"]), ActorID(c)
	a, err := s.spaceFollows(c, me, mid)
	if err != nil {
		return nil, err
	}
	b, err := s.spaceFollows(c, mid, me)
	if err != nil {
		return nil, err
	}
	attrA, attrB := 0, 0
	if a {
		attrA = 2
	}
	if b {
		attrB = 2
	}
	if a && b {
		attrA, attrB = 6, 6
	}
	blocked, err := Count(c, s.Store.DB, "SELECT COUNT(*) FROM user_blocks WHERE mid=? AND fid=?", me, mid)
	if err != nil {
		return nil, err
	}
	if blocked > 0 {
		attrA = 128
	}
	blockedBy, err := Count(c, s.Store.DB, "SELECT COUNT(*) FROM user_blocks WHERE mid=? AND fid=?", mid, me)
	if err != nil {
		return nil, err
	}
	if blockedBy > 0 {
		attrB = 128
	}
	return render.M{"relation": render.M{"mid": NumberWide(c.P["mid"], 0), "attribute": attrA, "mtime": 0}, "be_relation": render.M{"mid": me, "attribute": attrB}}, nil
}
func (s *Server) spaceStat(c *Ctx) (any, error) {
	mid := ParseWideID(c.P["vmid"])
	following, err := s.Store.FollowingCount(c.Context(), mid)
	if err != nil {
		return nil, err
	}
	follower, err := Count(c, s.Store.DB, "SELECT COUNT(*) FROM relations WHERE fid=?", mid)
	if err != nil {
		return nil, err
	}
	number := NumberWide(c.P["vmid"], 0)
	count := float64(follower) + math.Mod(number*9871, 50000)
	var encodedCount any = count
	if math.IsNaN(count) || math.IsInf(count, 0) {
		encodedCount = nil // JSON.stringify encodes nonfinite Numbers as null.
	}
	black, err := Count(c, s.Store.DB, "SELECT COUNT(*) FROM user_blocks WHERE mid=?", mid)
	if err != nil {
		return nil, err
	}
	return render.M{"mid": number, "following": following, "whisper": 0, "black": black, "follower": encodedCount}, nil
}
func (s *Server) spaceCard(c *Ctx) (any, error) {
	u, err := s.spaceUser(c, ParseWideID(c.P["mid"]))
	if err != nil {
		return nil, err
	}
	following, err := s.spaceFollows(c, ActorID(c), u.MID)
	if err != nil {
		return nil, err
	}
	fans, err := s.Store.FollowerCount(c.Context(), u.MID)
	if err != nil {
		return nil, err
	}
	attention, err := s.Store.FollowingCount(c.Context(), u.MID)
	if err != nil {
		return nil, err
	}
	likes, err := Count(c, s.Store.DB, "SELECT COALESCE(SUM(stat_like),0) FROM archives WHERE mid=?", u.MID)
	if err != nil {
		return nil, err
	}
	count, err := Count(c, s.Store.DB, "SELECT COUNT(*) FROM archives WHERE mid=? AND state=0", u.MID)
	if err != nil {
		return nil, err
	}
	return render.M{"card": render.M{"mid": strconv.FormatInt(u.MID, 10), "name": u.Uname, "face": render.Face(u.MID, u.Face), "sign": u.Sign, "sex": u.Sex, "fans": fans, "attention": attention, "level_info": render.LevelInfo(*u)}, "space": render.M{"s_img": u.TopPhoto, "l_img": u.TopPhoto}, "following": following, "archive_count": count, "follower": fans, "like_num": likes}, nil
}

// Public space listings filter only state, not the uploader's status (unlike
// PublishedArchives). Pages are loaded before using the shared renderers.
func (s *Server) spaceArchives(c *Ctx, mid int64) ([]store.Archive, error) {
	rows, err := s.Store.ListArchive(c.Context(), "WHERE mid=? AND state=0 ORDER BY aid", mid)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Pages, err = s.Store.ListPage(c.Context(), "WHERE aid=? ORDER BY position", rows[i].AID)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
func spaceSort(rows []store.Archive, order string) {
	sort.SliceStable(rows, func(i, j int) bool {
		switch order {
		case "click":
			return rows[i].Stat.View > rows[j].Stat.View
		case "stow":
			return rows[i].Stat.Favorite > rows[j].Stat.Favorite
		case "like":
			return rows[i].Stat.Like > rows[j].Stat.Like
		default:
			return rows[i].Pubdate > rows[j].Pubdate
		}
	})
}
func (s *Server) spaceRenderArcs(c *Ctx, rows []store.Archive, full bool) ([]render.M, error) {
	out := []render.M{}
	for _, a := range rows {
		var v render.M
		var err error
		if full {
			v, err = s.Render.ArcView(c.Context(), a)
		} else {
			v, err = s.Render.SpaceArc(c.Context(), a)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Server) spaceSearch(c *Ctx) (any, error) {
	rows, err := s.spaceArchives(c, ParseWideID(c.P["mid"]))
	if err != nil {
		return nil, err
	}
	list := []store.Archive{}
	tid := NumberWide(c.P["tid"], 0)
	kw := strings.ToLower(c.P["keyword"])
	for _, a := range rows {
		if (tid == 0 || float64(a.TID) == tid) && strings.Contains(strings.ToLower(a.Title), kw) {
			list = append(list, a)
		}
	}
	order := c.P["order"]
	if order != "click" && order != "stow" {
		order = "pubdate"
	}
	spaceSort(list, order)
	tlist := render.M{}
	for _, a := range list {
		key := strconv.FormatInt(a.TID, 10)
		if v, ok := tlist[key].(render.M); ok {
			v["count"] = v["count"].(int) + 1
		} else {
			name, err := s.Store.RegionName(c.Context(), a.TID)
			if err != nil {
				return nil, err
			}
			tlist[key] = render.M{"tid": a.TID, "name": name, "count": 1}
		}
	}
	pn, ps := NumberWide(c.P["pn"], 1), NumberWide(c.P["ps"], 30)
	vlist, err := s.spaceRenderArcs(c, Page(list, pn, ps), false)
	if err != nil {
		return nil, err
	}
	return render.M{"list": render.M{"vlist": vlist, "tlist": tlist}, "page": render.M{"pn": pn, "ps": ps, "count": len(list)}}, nil
}
func (s *Server) spaceCursor(c *Ctx) (any, error) {
	rows, err := s.spaceArchives(c, ParseWideID(c.P["vmid"]))
	if err != nil {
		return nil, err
	}
	order := "pubdate"
	if c.P["order"] == "click" {
		order = "click"
	}
	spaceSort(rows, order)
	start := 0
	aid := ParseWideID(c.P["aid"])
	if aid != 0 {
		for i, a := range rows {
			if a.AID == aid {
				start = i + 1
				break
			}
		}
	}
	items := []render.M{}
	for _, a := range Slice(rows, float64(start), float64(start+20)) {
		v, err := s.Render.SpaceArc(c.Context(), a)
		if err != nil {
			return nil, err
		}
		v["param"] = strconv.FormatInt(a.AID, 10)
		v["goto"] = "av"
		v["cover"] = a.Pic
		v["ctime"] = a.Pubdate
		items = append(items, v)
	}
	return render.M{"item": items, "has_next": start+20 < len(rows), "count": len(rows)}, nil
}
func (s *Server) spaceTop(c *Ctx) (any, error) {
	rows, err := s.spaceArchives(c, ParseWideID(c.P["vmid"]))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, Fail(53016, "没有置顶视频")
	}
	spaceSort(rows, "click")
	return s.Render.ArcView(c.Context(), rows[0], render.M{"reason": "代表作"})
}
func (s *Server) spaceMasterpiece(c *Ctx) (any, error) {
	rows, err := s.spaceArchives(c, ParseWideID(c.P["vmid"]))
	if err != nil {
		return nil, err
	}
	spaceSort(rows, "like")
	return s.spaceRenderArcs(c, Slice(rows, 1, 4), true)
}
