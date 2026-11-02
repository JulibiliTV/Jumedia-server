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

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) adminUsers(c *Ctx) (any, error) {
	users, err := s.Store.ListUser(c.Context(), "ORDER BY mid")
	if err != nil {
		return nil, err
	}
	list := []store.User{}
	for _, u := range users {
		if !adminMatch(u.Uname+u.Username+strconv.FormatInt(u.MID, 10), c.P["keyword"]) || (c.P["role"] != "" && u.Role != c.P["role"]) || (c.P["status"] != "" && float64(u.Status) != NumberUnicodeGo(c.P["status"])) {
			continue
		}
		list = append(list, u)
	}
	selected, page := PageResult(list, c, 20)
	out := []map[string]any{}
	for _, u := range selected {
		v := adminObject(u)
		v["face"] = render.Face(u.MID, u.Face)
		var n int
		if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM archives WHERE mid=?", u.MID).Scan(&n); err != nil {
			return nil, err
		}
		v["archives"] = n
		v["follower"], err = s.Store.FollowerCount(c.Context(), u.MID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return map[string]any{"list": out, "page": page}, nil
}

func (s *Server) adminArchives(c *Ctx) (any, error) {
	archives, err := s.Store.ListArchive(c.Context(), "ORDER BY ctime DESC,aid")
	if err != nil {
		return nil, err
	}
	list := []store.Archive{}
	pending, rejected := 0, 0
	for _, a := range archives {
		if a.State == -1 {
			pending++
		}
		if a.State == -2 {
			rejected++
		}
		state := c.P["state"]
		if (state == "" && (a.State == -100 || a.State == -20)) || (state != "" && float64(a.State) != NumberUnicodeGo(state)) || (NumberUnicodeGo(c.P["tid"]) != 0 && float64(a.TID) != NumberUnicodeGo(c.P["tid"])) {
			continue
		}
		owner, e := s.Render.Owner(c.Context(), a.MID)
		if e != nil {
			return nil, e
		}
		if !adminMatch(a.Title+a.BVID+owner["name"].(string), c.P["keyword"]) {
			continue
		}
		list = append(list, a)
	}
	selected, page := PageResult(list, c, 20)
	out := []map[string]any{}
	for _, a := range selected {
		a.Pages, err = s.Store.ListPage(c.Context(), "WHERE aid=? ORDER BY position", a.AID)
		if err != nil {
			return nil, err
		}
		v, e := s.Render.ArcView(c.Context(), a)
		if e != nil {
			return nil, e
		}
		v["reason"] = a.Reason
		if len(a.Pages) > 0 {
			v["media"] = a.Pages[0].Media
		}
		out = append(out, v)
	}
	return map[string]any{"list": out, "page": page, "counts": map[string]any{"pending": pending, "rejected": rejected}}, nil
}
func (s *Server) adminSeasons(c *Ctx) (any, error) {
	seasons, err := s.Store.ListSeason(c.Context(), "ORDER BY ctime DESC,season_id")
	if err != nil {
		return nil, err
	}
	list := []store.Season{}
	counts := map[string]int{"0": 0}
	for _, v := range seasons {
		if !adminMatch(v.Title, c.P["keyword"]) {
			continue
		}
		counts["0"]++
		counts[strconv.Itoa(v.SeasonType)]++
		if NumberUnicodeGo(c.P["season_type"]) == 0 || float64(v.SeasonType) == NumberUnicodeGo(c.P["season_type"]) {
			list = append(list, v)
		}
	}
	selected, page := PageResult(list, c, 20)
	out := []map[string]any{}
	for _, season := range selected {
		episodes, err := s.Store.ListEpisode(c.Context(), "WHERE season_id=? ORDER BY position", season.SeasonID)
		if err != nil {
			return nil, err
		}
		row := adminObject(season)
		eps := []map[string]any{}
		for _, e := range episodes {
			ep, err := s.adminEpisode(c, e)
			if err != nil {
				return nil, err
			}
			eps = append(eps, ep)
		}
		row["episodes"] = eps
		out = append(out, row)
	}
	return map[string]any{"list": out, "page": page, "counts": counts}, nil
}
func (s *Server) adminRooms(c *Ctx) (any, error) {
	rooms, err := s.Store.ListLiveRoom(c.Context(), "ORDER BY live_status DESC,online DESC,room_id")
	if err != nil {
		return nil, err
	}
	list := []store.LiveRoom{}
	for _, r := range rooms {
		owner, e := s.Render.Owner(c.Context(), r.UID)
		if e != nil {
			return nil, e
		}
		if adminMatch(r.Title+owner["name"].(string)+strconv.FormatInt(r.RoomID, 10), c.P["keyword"]) {
			list = append(list, r)
		}
	}
	selected, page := PageResult(list, c, 20)
	out := []map[string]any{}
	for _, r := range selected {
		v, e := s.Render.RoomCard(c.Context(), r)
		if e != nil {
			return nil, e
		}
		v["banned"] = r.Banned
		v["pending_start"] = r.PendingStart
		v["source"], err = s.liveSourceJSON(c, &r)
		if err != nil {
			return nil, err
		}
		if r.Source == nil {
			v["source"] = map[string]any{"type": "push", "url": "", "loop": true}
		}
		out = append(out, v)
	}
	return map[string]any{"list": out, "page": page}, nil
}
func (s *Server) adminReplies(c *Ctx) (any, error) {
	words, err := s.sensitiveWords(c)
	if err != nil {
		return nil, err
	}
	stat, err := s.moderationStat(c)
	if err != nil {
		return nil, err
	}
	replies, err := s.Store.ListReply(c.Context(), "ORDER BY ctime DESC,rpid")
	if err != nil {
		return nil, err
	}
	list := []store.Reply{}
	for _, r := range replies {
		if BoolParam(c.P["reported"]) {
			count, _, err := s.commentReportSummary(c, r.RPID)
			if err != nil {
				return nil, err
			}
			if count == 0 {
				continue
			}
		}
		name := ""
		u, e := s.Store.UserByID(c.Context(), r.MID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if u != nil {
			name = u.Uname
		}
		if adminMatch(r.Message+name, c.P["keyword"]) && (!BoolParam(c.P["flagged"]) || flagOf(words, r.Message) != "") {
			list = append(list, r)
		}
	}
	selected, page := PageResult(list, c, 20)
	out := []map[string]any{}
	for _, r := range selected {
		full, e := s.Store.ReplyByID(c.Context(), r.RPID)
		if e != nil {
			return nil, e
		}
		v, e := s.Render.ReplyJSON(c.Context(), *full, 0, false)
		if e != nil {
			return nil, e
		}
		v["flag"] = flagOf(words, r.Message)
		count, reasons, err := s.commentReportSummary(c, r.RPID)
		if err != nil {
			return nil, err
		}
		v["report_count"], v["report_reasons"] = count, reasons
		v["archive"] = nil
		a, e := s.Store.ArchiveByID(c.Context(), r.OID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if a != nil {
			v["archive"] = map[string]any{"aid": r.OID, "bvid": a.BVID, "title": a.Title}
		}
		out = append(out, v)
	}
	return map[string]any{"list": out, "page": page, "stat": stat}, nil
}
func (s *Server) adminDanmaku(c *Ctx) (any, error) {
	words, err := s.sensitiveWords(c)
	if err != nil {
		return nil, err
	}
	stat, err := s.moderationStat(c)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.ListDanmaku(c.Context(), "ORDER BY ctime DESC,id")
	if err != nil {
		return nil, err
	}
	list := []store.Danmaku{}
	for _, d := range all {
		if (d.Weight == 10 || c.P["keyword"] != "") && adminMatch(d.Content, c.P["keyword"]) && (!BoolParam(c.P["flagged"]) || flagOf(words, d.Content) != "") {
			list = append(list, d)
		}
	}
	selected, page := PageResult(list, c, 20)
	out := []map[string]any{}
	for _, d := range selected {
		v := adminObject(d)
		v["flag"] = flagOf(words, d.Content)
		v["member"], err = s.Render.Owner(c.Context(), d.MID)
		if err != nil {
			return nil, err
		}
		title := ""
		err = s.Store.DB.QueryRowContext(c.Context(), "SELECT a.title FROM archives a JOIN archive_pages p ON p.aid=a.aid WHERE p.cid=?", d.CID).Scan(&title)
		if errors.Is(err, sql.ErrNoRows) {
			err = s.Store.DB.QueryRowContext(c.Context(), "SELECT s.title FROM seasons s JOIN episodes e ON e.season_id=s.season_id WHERE e.cid=?", d.CID).Scan(&title)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		v["target"] = title
		out = append(out, v)
	}
	return map[string]any{"list": out, "page": page, "stat": stat}, nil
}
func (s *Server) adminRegions(c *Ctx) (any, error) {
	regions, err := s.Store.ListRegion(c.Context(), "ORDER BY sort,tid")
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range regions {
		v := adminObject(r)
		var n int
		if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM archives WHERE state=0 AND ((?=0 AND tid=?) OR (?<>0 AND sub_tid=?))", r.Parent, r.TID, r.Parent, r.TID).Scan(&n); err != nil {
			return nil, err
		}
		v["count"] = n
		out = append(out, v)
	}
	return map[string]any{"list": out}, nil
}
func (s *Server) adminAreas(c *Ctx) (any, error) {
	areas, err := s.Store.LiveAreas(c.Context())
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, a := range areas {
		v := adminObject(a)
		var n int
		if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM rooms WHERE parent_area_id=?", a.ID).Scan(&n); err != nil {
			return nil, err
		}
		v["rooms"] = n
		out = append(out, v)
	}
	return map[string]any{"list": out}, nil
}
func (s *Server) adminBanners(c *Ctx) (any, error) {
	list, err := s.Store.ListBanner(c.Context(), "ORDER BY position,sort,id")
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, b := range list {
		v := adminObject(b)
		if match := adminBannerSeason.FindStringSubmatch(b.URL); match != nil {
			v["season_id"] = NumberUnicodeGo(match[1])
		}
		out = append(out, v)
	}
	return map[string]any{"list": out}, nil
}
func (s *Server) adminSettings(c *Ctx) (any, error) {
	settings, err := s.Store.Settings(c.Context())
	if err != nil {
		return nil, err
	}
	delete(settings, liveChatSecretKey)
	for key := range settings {
		if strings.HasPrefix(key, "serverEmby") || strings.HasPrefix(key, "_demo_") {
			delete(settings, key)
		}
	}
	body, err := jsonServerResponse(settings)
	if err != nil {
		return nil, err
	}
	return Text(body, 200, "application/json; charset=utf-8"), nil
}
func (s *Server) adminLogs(c *Ctx) (any, error) {
	logs, err := s.Store.ListLog(c.Context(), "ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	operators := []map[string]any{}
	seen := map[int64]bool{}
	filtered := []store.Log{}
	days, mid := NumberWide(c.P["days"], 0), NumberWide(c.P["mid"], 0)
	cutoff := float64(time.Now().Unix()) - days*86400
	kinds := map[string][]string{"archive": {"稿件", "投稿", "审核", "番剧", "合集", "评论", "弹幕"}, "user": {"用户", "封禁", "登录", "密码", "资料"}, "live": {"直播", "转码", "分区"}, "datasource": {"数据源", "元信息", "图片缓存"}, "settings": {"设置", "轮播", "重置", "初始化"}}
	for _, entry := range logs {
		if !seen[entry.MID] {
			owner, e := s.Render.Owner(c.Context(), entry.MID)
			if e != nil {
				return nil, e
			}
			operators = append(operators, owner)
			seen[entry.MID] = true
		}
		words := kinds[c.P["kind"]]
		matches := len(words) == 0
		for _, word := range words {
			if strings.Contains(entry.Action, word) {
				matches = true
				break
			}
		}
		if matches && (days == 0 || float64(entry.Ctime) >= cutoff) && (mid == 0 || float64(entry.MID) == mid) {
			filtered = append(filtered, entry)
		}
	}
	pn, ps := NumberWide(c.P["pn"], 1), NumberWide(c.P["ps"], 30)
	selected := Page(filtered, pn, ps)
	page := map[string]any{"pn": pn, "ps": ps, "total": len(filtered)}
	out := []map[string]any{}
	for _, l := range selected {
		v := adminObject(l)
		v["member"], err = s.Render.Owner(c.Context(), l.MID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return map[string]any{"list": out, "page": page, "operators": operators}, nil
}
func (s *Server) adminDashboard(c *Ctx) (any, error) {
	users, err := s.Store.ListUser(c.Context(), "ORDER BY mid")
	if err != nil {
		return nil, err
	}
	archives, err := s.Store.ListArchive(c.Context(), "ORDER BY aid")
	if err != nil {
		return nil, err
	}
	rooms, err := s.Store.ListLiveRoom(c.Context(), "ORDER BY room_id")
	if err != nil {
		return nil, err
	}
	regions, err := s.Store.ListRegion(c.Context(), "WHERE visible=1 AND COALESCE(parent,0)=0 ORDER BY sort,tid")
	if err != nil {
		return nil, err
	}
	recent, err := s.Store.ListLog(c.Context(), "ORDER BY id DESC LIMIT 8")
	if err != nil {
		return nil, err
	}
	stat, err := s.moderationStat(c)
	if err != nil {
		return nil, err
	}
	v := map[string]any{"users": len(users), "rooms": len(rooms), "recent": recent, "reply_today": stat["reply_today"], "danmaku_today": stat["danmaku_today"]}
	for key, query := range map[string]string{"seasons": "SELECT COUNT(*) FROM seasons", "episodes": "SELECT COUNT(*) FROM episodes", "replies": "SELECT COUNT(*) FROM replies", "danmaku": "SELECT COUNT(*) FROM danmaku"} {
		var n int
		if err = s.Store.DB.QueryRowContext(c.Context(), query).Scan(&n); err != nil {
			return nil, err
		}
		v[key] = n
	}
	banned, published, pending, live := 0, 0, 0, 0
	var views int64
	for _, u := range users {
		if u.Status != 0 {
			banned++
		}
	}
	for _, a := range archives {
		if a.State == 0 {
			published++
		}
		if a.State == -1 {
			pending++
		}
		views += a.Stat.View
	}
	for _, r := range rooms {
		if r.LiveStatus == 1 {
			live++
		}
	}
	v["banned"], v["archives"], v["pending"], v["live_now"], v["views"] = banned, published, pending, live, views
	now := publicationNow(c.Context())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	trend := []map[string]any{}
	for i := 0; i < 14; i++ {
		day := today - int64(13-i)*86400
		uploads, nusers := 0, 0
		for _, a := range archives {
			if a.Ctime >= day && a.Ctime < day+86400 {
				uploads++
			}
		}
		for _, u := range users {
			if u.Ctime >= day && u.Ctime < day+86400 {
				nusers++
			}
		}
		date := time.Unix(day, 0).In(now.Location())
		trend = append(trend, map[string]any{"date": fmt.Sprintf("%d/%d", date.Month(), date.Day()), "uploads": uploads, "users": nusers, "views": math.Floor(float64(views) / 300 * (0.7 + math.Abs(math.Sin(float64(i)))*0.6))})
	}
	v["trend"] = trend
	regionRows := []map[string]any{}
	for _, r := range regions {
		n := 0
		for _, a := range archives {
			if a.TID == r.TID && a.State == 0 {
				n++
			}
		}
		regionRows = append(regionRows, map[string]any{"tid": r.TID, "name": r.Name, "count": n})
	}
	v["regions"] = regionRows
	top, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(top, func(i, j int) bool { return top[i].Stat.View > top[j].Stat.View })
	cards := []map[string]any{}
	for _, a := range top[:min(6, len(top))] {
		card, e := s.Render.ArcView(c.Context(), a)
		if e != nil {
			return nil, e
		}
		cards = append(cards, card)
	}
	v["top"] = cards
	return v, nil
}
