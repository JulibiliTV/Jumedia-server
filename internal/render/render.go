// Package render translates stored records into the exact client-facing shapes
// from mock/render.ts. Database failures are returned, never rendered as success.
package render

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

type M = map[string]any
type Renderer struct{ Store *store.Store }

func New(s *store.Store) *Renderer { return &Renderer{Store: s} }

// Face preserves existing avatars and assigns a stable default to empty ones.
// API responses apply their normal public-origin conversion after rendering.
func Face(mid int64, face string) string {
	if face == "" {
		return seed.Avatar(mid)
	}
	return face
}

func LevelInfo(u store.User) M {
	var next any = (u.Level + 1) * 2000
	if u.Level >= 6 {
		next = "--"
	}
	return M{"current_level": u.Level, "current_min": 0, "current_exp": u.Exp, "next_exp": next}
}

// CompactCount follows JavaScript toFixed(1), including half-up ties.
func CompactCount(n int64) string {
	if n < 10000 {
		return strconv.FormatInt(n, 10)
	}
	ratio := new(big.Rat).SetFloat64(float64(n) / 10000)
	scaled := new(big.Int).Mul(ratio.Num(), big.NewInt(10))
	whole, rem := new(big.Int), new(big.Int)
	whole.QuoRem(scaled, ratio.Denom(), rem)
	if rem.Lsh(rem, 1).Cmp(ratio.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	digits := whole.String()
	if len(digits) < 2 {
		digits = "0" + digits
	}
	value := digits[:len(digits)-1] + "." + digits[len(digits)-1:]
	return strings.TrimSuffix(value, ".0") + "万"
}
func FormatDuration(seconds int) string {
	s := max(0, seconds)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}
func TotalDuration(a store.Archive) int {
	n := 0
	for _, p := range a.Pages {
		n += p.Duration
	}
	return n
}
func SeasonBadge(s store.Season) string {
	badges := []string{"", "番剧", "电影", "纪录片", "国创", "电视剧", "", "综艺"}
	if s.SeasonType < 0 || s.SeasonType >= len(badges) {
		return ""
	}
	return badges[s.SeasonType]
}
func IndexShow(s store.Season) string {
	if s.SeasonType == 2 {
		return "全片"
	}
	unit := "话"
	if s.SeasonType == 5 {
		unit = "集"
	} else if s.SeasonType == 7 {
		unit = "期"
	}
	if s.IsFinish {
		return fmt.Sprintf("全%d%s", len(s.Episodes), unit)
	}
	return fmt.Sprintf("更新至第%d%s", len(s.Episodes), unit)
}
func merge(v M, extra []M) M {
	for _, m := range extra {
		for k, x := range m {
			v[k] = x
		}
	}
	return v
}
func (r *Renderer) Owner(ctx context.Context, mid int64) (M, error) {
	u, err := r.Store.UserByID(ctx, mid)
	if errors.Is(err, sql.ErrNoRows) {
		return M{"mid": mid, "name": "已注销用户", "face": Face(mid, "")}, nil
	}
	if err != nil {
		return nil, err
	}
	return M{"mid": mid, "name": u.Uname, "face": Face(mid, u.Face)}, nil
}
func (r *Renderer) MemberJSON(ctx context.Context, mid int64) (M, error) {
	u, err := r.Store.UserByID(ctx, mid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	v := M{"mid": strconv.FormatInt(mid, 10), "uname": "已注销用户", "avatar": Face(mid, ""), "sex": "保密", "sign": "", "level_info": M{"current_level": 0}, "vip": M{"vipStatus": 0, "vipType": 0}}
	if u != nil {
		v["uname"] = u.Uname
		v["avatar"] = Face(mid, u.Face)
		v["sex"] = u.Sex
		v["sign"] = u.Sign
		v["level_info"] = LevelInfo(*u)
		v["vip"] = M{"vipStatus": u.VIP, "vipType": store.BoolInt(u.VIP != 0) * 2}
	}
	return v, nil
}
func (r *Renderer) ArcView(ctx context.Context, a store.Archive, extra ...M) (M, error) {
	owner, err := r.Owner(ctx, a.MID)
	if err != nil {
		return nil, err
	}
	region, err := r.Store.RegionName(ctx, a.TID)
	if err != nil {
		return nil, err
	}
	dim := func() M { return M{"width": 1280, "height": 720, "rotate": 0} }
	pages := []M{}
	var cid int64
	for i, p := range a.Pages {
		if i == 0 {
			cid = p.CID
		}
		pages = append(pages, M{"cid": p.CID, "page": i + 1, "from": "vupload", "part": p.Part, "duration": p.Duration, "dimension": dim()})
	}
	v := M{"aid": a.AID, "bvid": a.BVID, "videos": len(a.Pages), "tid": a.TID, "tname": region, "copyright": a.Copyright, "pic": a.Pic, "title": a.Title, "pubdate": a.Pubdate, "ctime": a.Ctime, "desc": a.Desc, "state": a.State, "duration": TotalDuration(a), "owner": owner, "stat": M{"aid": a.AID, "view": a.Stat.View, "danmaku": a.Stat.Danmaku, "reply": a.Stat.Reply, "favorite": a.Stat.Favorite, "coin": a.Stat.Coin, "share": a.Stat.Share, "like": a.Stat.Like, "his_rank": 0, "now_rank": 0, "evaluation": "", "vt": 0}, "dynamic": "", "cid": cid, "dimension": dim(), "is_upower_exclusive": false, "tag": a.Tag, "pages": pages}
	v["sub_tid"], v["sub_tname"] = a.SubTID, ""
	if a.SubTID != 0 {
		name, err := r.Store.RegionName(ctx, a.SubTID)
		if err != nil {
			return nil, err
		}
		v["sub_tname"] = name
	}
	var collectionID int64
	err = r.Store.DB.QueryRowContext(ctx, "SELECT id FROM collections WHERE kind=? AND id IN (SELECT collection_id FROM collection_items WHERE aid=?) ORDER BY id LIMIT 1", "season", a.AID).Scan(&collectionID)
	if err == nil {
		c, e := r.Store.CollectionByID(ctx, collectionID)
		if e != nil {
			return nil, e
		}
		season, e := r.UgcSeason(ctx, *c)
		if e != nil {
			return nil, e
		}
		v["ugc_season"] = season
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return merge(v, extra), nil
}
func (r *Renderer) UgcSeason(ctx context.Context, c store.Collection) (M, error) {
	episodes := []M{}
	for _, aid := range c.AIDs {
		a, err := r.Store.ArchiveByID(ctx, aid)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if a.State != 0 {
			continue
		}
		owner, err := r.Owner(ctx, a.MID)
		if err != nil {
			return nil, err
		}
		var cid int64
		if len(a.Pages) > 0 {
			cid = a.Pages[0].CID
		}
		episodes = append(episodes, M{"id": a.AID, "aid": a.AID, "cid": cid, "bvid": a.BVID, "title": a.Title, "arc": M{"aid": a.AID, "pic": a.Pic, "title": a.Title, "duration": TotalDuration(*a), "ctime": a.Ctime, "pubdate": a.Pubdate, "stat": M{"view": a.Stat.View, "danmaku": a.Stat.Danmaku}, "author": owner}})
	}
	return M{"id": c.ID, "title": c.Title, "cover": c.Cover, "mid": c.MID, "intro": c.Intro, "ep_count": len(episodes), "sections": []M{{"id": c.ID * 10, "title": "正片", "season_id": c.ID, "episodes": episodes}}}, nil
}
func (r *Renderer) SpaceArc(ctx context.Context, a store.Archive) (M, error) {
	o, err := r.Owner(ctx, a.MID)
	if err != nil {
		return nil, err
	}
	v := M{"aid": a.AID, "bvid": a.BVID, "title": a.Title, "pic": a.Pic, "description": a.Desc, "play": a.Stat.View, "video_review": a.Stat.Danmaku, "comment": a.Stat.Reply, "length": FormatDuration(TotalDuration(a)), "duration": TotalDuration(a), "created": a.Pubdate, "pubdate": a.Pubdate, "mid": a.MID, "author": o["name"], "typeid": a.TID, "stat": M{"view": a.Stat.View, "danmaku": a.Stat.Danmaku}}
	if len(a.Pages) > 0 {
		v["cid"] = a.Pages[0].CID
	}
	return v, nil
}
func (r *Renderer) FeedCard(ctx context.Context, a store.Archive) (M, error) {
	o, err := r.Owner(ctx, a.MID)
	if err != nil {
		return nil, err
	}
	region, err := r.Store.RegionName(ctx, a.TID)
	if err != nil {
		return nil, err
	}
	player := M{"aid": a.AID, "type": "av", "duration": TotalDuration(a)}
	if len(a.Pages) > 0 {
		player["cid"] = a.Pages[0].CID
	}
	return M{"card_type": "small_cover_v2", "card_goto": "av", "goto": "av", "param": strconv.FormatInt(a.AID, 10), "cover": a.Pic, "title": a.Title, "uri": "/video/" + a.BVID, "cover_left_text_2": CompactCount(a.Stat.View), "cover_left_text_3": CompactCount(a.Stat.Danmaku), "cover_right_text": FormatDuration(TotalDuration(a)), "args": M{"up_id": a.MID, "up_name": o["name"], "rid": a.TID, "rname": region, "aid": a.AID}, "player_args": player, "bvid": a.BVID, "pubdate": a.Pubdate, "owner": o, "three_point": M{"dislike_reasons": []M{{"id": 1, "name": "不感兴趣"}, {"id": 4, "name": fmt.Sprint("UP主：", o["name"])}, {"id": 3, "name": "分区：" + region}}}}, nil
}
func SeasonCard(s store.Season, extra ...M) M {
	copyright, duration := s.Copyright, 0
	if copyright == 0 {
		copyright = 1
	}
	if s.SeasonType == 2 && len(s.Episodes) > 0 {
		duration = s.Episodes[0].Duration
	}
	first, last := M{}, M{"index_show": IndexShow(s)}
	if len(s.Episodes) > 0 {
		f, l := s.Episodes[0], s.Episodes[len(s.Episodes)-1]
		first["ep_id"] = f.EpID
		first["cover"] = f.Cover
		last["id"] = l.EpID
		last["cover"] = l.Cover
	}
	score := strconv.FormatFloat(s.Score, 'f', -1, 64)
	link := fmt.Sprintf("/bangumi/play/ss%d", s.SeasonID)
	return merge(M{"copyright": copyright, "season_status": 1, "duration": duration, "year": time.Unix(s.Ctime, 0).In(time.Local).Year(), "season_id": s.SeasonID, "media_id": s.MediaID, "season_type": s.SeasonType, "title": s.Title, "cover": s.Cover, "ss_horizontal_cover": s.Horizontal, "badge": SeasonBadge(s), "badge_info": M{"text": SeasonBadge(s)}, "index_show": IndexShow(s), "is_finish": store.BoolInt(s.IsFinish), "rating": score + "分", "score": score, "order": CompactCount(s.Views) + "次播放", "stat": M{"view": s.Views, "follow": s.Follow, "danmaku": s.Danmakus}, "new_ep": last, "first_ep": first, "evaluate": s.Evaluate, "areas": strings.Join(s.Areas, " "), "styles": strings.Join(s.Styles, " / "), "link": link, "url": link}, extra)
}
func (r *Renderer) RoomCard(ctx context.Context, room store.LiveRoom) (M, error) {
	o, err := r.Owner(ctx, room.UID)
	if err != nil {
		return nil, err
	}
	areas, err := r.Store.LiveAreas(ctx)
	if err != nil {
		return nil, err
	}
	area, child := "", ""
	for _, a := range areas {
		if a.ID == room.ParentAreaID {
			area = a.Name
			for _, c := range a.Children {
				if c.ID == room.AreaID {
					child = c.Name
				}
			}
		}
	}
	count := CompactCount(room.Online)
	return M{"roomid": room.RoomID, "room_id": room.RoomID, "uid": room.UID, "title": room.Title, "uname": o["name"], "face": o["face"], "online": room.Online, "user_cover": room.Cover, "system_cover": room.Cover, "cover": room.Cover, "room_cover": room.Cover, "area_v2_name": child, "area_name": child, "area_v2_parent_name": area, "area_v2_id": room.AreaID, "area_v2_parent_id": room.ParentAreaID, "live_status": room.LiveStatus, "text_small": count, "watched_show": M{"num": room.Online, "text_large": count + "人看过", "text_small": count}, "live_time": room.LiveTime}, nil
}
func (r *Renderer) ReplyJSON(ctx context.Context, reply store.Reply, me int64, withChildren ...bool) (M, error) {
	include := len(withChildren) == 0 || withChildren[0]
	children := []store.Reply{}
	var err error
	if include {
		children, err = r.Store.ListReply(ctx, "WHERE root=? ORDER BY ctime,rpid", reply.RPID)
		if err != nil {
			return nil, err
		}
		children, err = r.Store.VisibleReplies(ctx, me, children)
		if err != nil {
			return nil, err
		}
	}
	prefix := ""
	if reply.Parent != 0 && reply.Parent != reply.Root {
		p, e := r.Store.ReplyByID(ctx, reply.Parent)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if p != nil {
			u, e := r.Store.UserByID(ctx, p.MID)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			name := "用户"
			if u != nil {
				name = u.Uname
			}
			prefix = "回复 @" + name + " :"
		}
	}
	member, err := r.MemberJSON(ctx, reply.MID)
	if err != nil {
		return nil, err
	}
	action := 0
	if me != 0 {
		for _, id := range reply.Haters {
			if id == me {
				action = 2
			}
		}
		for _, id := range reply.Likers {
			if id == me {
				action = 1
			}
		}
	}
	replies := []M{}
	for _, c := range children[:min(3, len(children))] {
		full, e := r.Store.ReplyByID(ctx, c.RPID)
		if e != nil {
			return nil, e
		}
		v, e := r.ReplyJSON(ctx, *full, me, false)
		if e != nil {
			return nil, e
		}
		replies = append(replies, v)
	}
	return M{"rpid": reply.RPID, "rpid_str": strconv.FormatInt(reply.RPID, 10), "oid": reply.OID, "type": 1, "mid": reply.MID, "root": reply.Root, "parent": reply.Parent, "count": len(children), "rcount": len(children), "ctime": reply.Ctime, "like": len(reply.Likers), "action": action, "member": member, "content": M{"message": prefix + reply.Message, "members": []any{}, "pictures": []any{}}, "replies": replies, "up_action": M{"like": false, "reply": false}}, nil
}
