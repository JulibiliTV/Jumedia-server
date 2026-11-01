package api

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) registerBPlan() {
	for _, r := range []struct {
		path    string
		handler Handler
	}{
		{"/x/player/online/total", s.bplanWatching}, {"/x/space/upstat", s.bplanUpstat}, {"/x/space/notice", s.bplanNotice}, {"/x/space/notice/set", s.bplanNoticeSet},
		{"/main/hotword", s.bplanHotword}, {"/x/web-interface/wbi/search/default", s.bplanSearchDefault}, {"/x/polymer/web-dynamic/v1/portal", s.bplanPortal},
		{"/x/web-interface/online", s.bplanOnline}, {"/x/tag/hots", s.bplanTags}, {"/x/web-interface/dynamic/region", s.bplanRegion}, {"/x/web-show/res/locs", s.bplanLocs},
		{"/x/v2/reply/wbi/main", s.bplanReply}, {"/pgc/season/index/condition", s.bplanCondition}, {"/xlive/general-interface/v1/rank/getOnlineGoldRank", s.bplanGold},
		{"/xlive/app-room/v1/guardTab/topList", s.bplanGuards}, {"/x/space/reservation", s.bplanReservations}, {"/x/web/reservation/add", s.bplanReservationAdd}, {"/x/web/reservation/delete", s.bplanReservationDelete},
	} {
		s.handle(r.path, r.handler)
	}
}
func (s *Server) bplanWatching(c *Ctx) (any, error) {
	var a *store.Archive
	var err error
	if c.P["bvid"] != "" {
		a, err = s.Store.ArchiveByID(c.Context(), discoverBV2AV(c.P["bvid"]))
	} else {
		a, err = s.Store.ArchiveByID(c.Context(), ParseID(c.P["aid"]))
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "啥都木有")
	}
	if err != nil {
		return nil, err
	}
	n := math.Max(1, math.Floor(float64(a.Stat.View)/4000+float64((a.AID*7+time.Now().Unix()/60)%23)+0.5))
	text := strconv.FormatFloat(n, 'f', 0, 64)
	if n >= 1000 {
		text = strconv.FormatFloat(math.Floor(n/1000)*1000, 'f', 0, 64) + "+"
	}
	return render.M{"total": text, "count": strconv.FormatFloat(math.Max(1, math.Floor(n*.6+.5)), 'f', 0, 64), "show_switch": render.M{"total": true, "count": true}, "abtest": render.M{"group": "b"}}, nil
}
func (s *Server) bplanUpstat(c *Ctx) (any, error) {
	arcs, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	var views, likes int64
	for _, a := range arcs {
		if float64(a.MID) == Number(c.P["mid"], 0) {
			views += a.Stat.View
			likes += a.Stat.Like
		}
	}
	return render.M{"archive": render.M{"view": views, "vt": 0, "enable_vt": 0}, "article": render.M{"view": 0}, "likes": likes}, nil
}
func (s *Server) bplanNotice(c *Ctx) (any, error) {
	u, err := s.Store.UserByID(c.Context(), ParseID(c.P["mid"]))
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return nil, err
	}
	return u.Notice, nil
}
func (s *Server) bplanNoticeSet(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	notice := Trim(c.P["notice"])
	if UTF16Length(notice) > 150 {
		return nil, Fail(-400, "公告最多 150 个字符")
	}
	_, err = s.Store.DB.ExecContext(c.Context(), "UPDATE users SET notice=? WHERE mid=?", notice, u.MID)
	return nil, err
}
func (s *Server) bplanHotword(c *Ctx) (any, error) {
	arcs, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	bplanSortArcs(arcs, "click")
	words := []render.M{}
	seen := map[string]bool{}
	replacer := strings.NewReplacer("《", " ", "》", " ", "【", " ", "】", " ", "「", " ", "」", " ")
	for _, a := range arcs[:min(10, len(arcs))] {
		fields := strings.Fields(Trim(replacer.Replace(a.Title)))
		word := ""
		if len(fields) > 0 {
			word = string(utf16.Decode(utf16.Encode([]rune(fields[0]))[:min(16, UTF16Length(fields[0]))]))
		}
		if seen[word] {
			continue
		}
		seen[word] = true
		i := len(words) + 1
		kind := 1
		if i <= 3 {
			kind = 4
		}
		words = append(words, render.M{"id": i, "pos": i, "keyword": word, "show_name": word, "word_type": kind, "icon": "", "hot_id": i, "is_commercial": "0"})
	}
	return Root(render.M{"code": 0, "exp_str": "", "list": words, "top_list": []any{}, "hotword_egg_info": "", "seid": "", "timestamp": time.Now().Unix(), "total_count": 10}), nil
}
func (s *Server) bplanSearchDefault(c *Ctx) (any, error) {
	arcs, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	bplanSortArcs(arcs, "click")
	out := render.M{"seid": "", "id": 0, "type": 0, "show_name": "", "name": "", "goto_type": 0, "goto_value": "", "url": ""}
	if len(arcs) > 0 {
		pool := arcs[:min(8, len(arcs))]
		a := pool[(time.Now().Unix()/600)%int64(len(pool))]
		out["id"], out["show_name"], out["name"], out["goto_type"], out["goto_value"], out["url"] = a.AID, a.Title, a.Title, 1, a.BVID, "/video/"+a.BVID
	}
	return out, nil
}
func (s *Server) bplanPortal(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	ups, err := s.Store.ListUser(c.Context(), "WHERE status=0 AND mid IN (SELECT fid FROM relations WHERE mid=?) ORDER BY mid", u.MID)
	if err != nil {
		return nil, err
	}
	arcs, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	rooms, err := s.Store.ListLiveRoom(c.Context(), "WHERE live_status=1 AND banned=0 ORDER BY room_id")
	if err != nil {
		return nil, err
	}
	followers, err := s.Store.FollowerCount(c.Context(), u.MID)
	if err != nil {
		return nil, err
	}
	following, err := s.Store.FollowingCount(c.Context(), u.MID)
	if err != nil {
		return nil, err
	}
	dyns := 0
	updates := map[int64]bool{}
	for _, a := range arcs {
		if a.MID == u.MID {
			dyns++
		}
		if a.Pubdate >= time.Now().Unix()-86400*3 {
			updates[a.MID] = true
		}
	}
	upRows, liveRows := []render.M{}, []render.M{}
	for _, up := range ups {
		upRows = append(upRows, render.M{"mid": up.MID, "uname": up.Uname, "face": up.Face, "has_update": updates[up.MID], "is_reserve_recall": false})
		for _, r := range rooms {
			if r.UID == up.MID {
				liveRows = append(liveRows, render.M{"mid": r.UID, "uname": up.Uname, "face": up.Face, "room_id": r.RoomID, "title": r.Title, "jump_url": fmt.Sprintf("/live/%d", r.RoomID), "is_reserve_recall": false})
				break
			}
		}
	}
	return render.M{"live_users": render.M{"count": len(liveRows), "group": "", "items": liveRows}, "my_info": render.M{"mid": u.MID, "name": u.Uname, "face": u.Face, "follower": followers, "following": following, "dyns": dyns}, "up_list": render.M{"has_more": false, "items": upRows}}, nil
}
func (s *Server) bplanOnline(c *Ctx) (any, error) {
	regions, err := s.Store.ListRegion(c.Context(), "ORDER BY sort,tid")
	if err != nil {
		return nil, err
	}
	arcs, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	counts := map[string]int{}
	for _, r := range regions {
		if r.Parent == 0 && !r.Visible {
			continue
		}
		n := 0
		for _, a := range arcs {
			if a.Pubdate >= since && ((r.Parent == 0 && a.TID == r.TID) || (r.Parent != 0 && a.SubTID == r.TID)) {
				n++
			}
		}
		counts[strconv.FormatInt(r.TID, 10)] = n
	}
	var sessions, online int64
	if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil {
		return nil, err
	}
	if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COALESCE(SUM(online),0) FROM rooms WHERE live_status=1").Scan(&online); err != nil {
		return nil, err
	}
	return render.M{"region_count": counts, "all_count": len(arcs), "web_online": sessions, "play_online": online}, nil
}
func (s *Server) bplanRegionArcs(c *Ctx, rid float64) ([]store.Archive, bool, error) {
	regions, err := s.Store.ListRegion(c.Context(), "WHERE tid=?", DBID(rid))
	if err != nil {
		return nil, false, err
	}
	arcs, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, false, err
	}
	out := []store.Archive{}
	child := len(regions) > 0 && regions[0].Parent != 0
	for _, a := range arcs {
		if (!child && float64(a.TID) == rid) || (child && float64(a.SubTID) == rid) {
			out = append(out, a)
		}
	}
	return out, len(regions) > 0, nil
}
func bplanHashTag(name string) uint32 {
	h := uint32(7)
	for _, r := range name {
		u := uint32(r)
		if r > 0xffff {
			u = uint32(utf16.Encode([]rune{r})[0])
		}
		h = h*31 + u
	}
	return h % 1000000
}
func (s *Server) bplanTags(c *Ctx) (any, error) {
	rid := Number(c.P["rid"], 0)
	arcs, _, err := s.bplanRegionArcs(c, rid)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	names := []string{}
	for _, a := range arcs {
		for _, tag := range strings.Split(a.Tag, ",") {
			tag = Trim(tag)
			if tag == "" {
				continue
			}
			if _, ok := counts[tag]; !ok {
				names = append(names, tag)
			}
			counts[tag] += a.Stat.View
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return counts[names[i]] > counts[names[j]] })
	tags := []render.M{}
	for _, name := range names[:min(20, len(names))] {
		tags = append(tags, render.M{"tag_id": bplanHashTag(name), "tag_name": name, "highlight": 0, "is_atten": 0, "count": render.M{"view": counts[name], "use": 0, "atten": 0}})
	}
	return []render.M{{"rid": rid, "tags": tags}}, nil
}
func (s *Server) bplanRegion(c *Ctx) (any, error) {
	rid, pn, ps := Number(c.P["rid"], 0), Number(c.P["pn"], 1), math.Min(50, Number(c.P["ps"], 20))
	arcs, exists, err := s.bplanRegionArcs(c, rid)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, Fail(-400, "分区不存在")
	}
	rows := []store.Archive{}
	for _, a := range arcs {
		if bplanDurationIn(render.TotalDuration(a), Number(c.P["duration"], 0)) {
			rows = append(rows, a)
		}
	}
	order := c.P["order"]
	if !bplanSortArcs(rows, order) {
		bplanSortArcs(rows, "pubdate")
	}
	out := []render.M{}
	for _, a := range Page(rows, pn, ps) {
		v, err := s.Render.ArcView(c.Context(), a)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return render.M{"archives": out, "page": render.M{"count": len(rows), "num": pn, "size": ps}}, nil
}
func (s *Server) bplanLocs(c *Ctx) (any, error) {
	banners, err := s.Store.ListBanner(c.Context(), "WHERE position=? ORDER BY sort,id", "channel")
	if err != nil {
		return nil, err
	}
	out := render.M{}
	for _, v := range strings.Split(c.P["ids"], ",") {
		id := Number(v, 0)
		if id == 0 {
			continue
		}
		key := strconv.FormatFloat(id, 'f', -1, 64)
		items := []render.M{}
		for _, b := range banners {
			if b.Loc == nil || float64(*b.Loc) != id {
				continue
			}
			var sid int64
			if b.SeasonID != nil {
				sid = *b.SeasonID
			}
			if m := adminBannerSeason.FindStringSubmatch(b.URL); m != nil {
				sid = ParseID(m[1])
			}
			items = append(items, render.M{"id": b.ID, "contract_id": "", "pos_num": len(items) + 1, "name": b.Title, "pic": b.Img, "litpic": "", "url": b.URL, "style": 0, "agency": "", "label": "", "intro": "", "creative_type": 0, "request_id": "", "src_id": id, "area": 0, "is_ad_loc": false, "is_ad": false, "server_type": 0, "cm_mark": 0, "stime": 0, "mid": "", "activity_type": 0, "epid": 0, "season_id": sid, "sub_title": "", "ad_cb": "", "null_frame": false})
		}
		out[key] = items
	}
	return out, nil
}
