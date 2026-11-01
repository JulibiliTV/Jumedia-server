package api

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) pgcRanked(c *Ctx, filterStyle bool) (render.M, error) {
	seasons, err := s.pgcSeasons(c)
	if err != nil {
		return nil, err
	}
	style := ""
	styleID := NumberUnicode(c.P["style_id"], 0)
	kind := NumberUnicode(c.P["season_type"], 1)
	if filterStyle && styleID >= 2 && styleID < 210 {
		styles, index := pgcStyles(seasons), NumberUnicode(c.P["style_id"], 0)-2
		for i, name := range styles {
			if float64(i) == index {
				style = name
				break
			}
		}
	}
	rows := []store.Season{}
	for _, season := range seasons {
		if season.State == 0 && (float64(season.SeasonType) == kind || (filterStyle && kind == 0)) && (style == "" || slices.Contains(season.Styles, style)) {
			rows = append(rows, season)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if filterStyle && styleID == 430 {
			return rows[i].Follow > rows[j].Follow
		}
		return rows[i].Views > rows[j].Views
	})
	if filterStyle {
		rows = rows[:min(100, len(rows))]
	}
	return render.M{"list": pgcCards(rows, true)}, nil
}
func (s *Server) pgcWebRank(c *Ctx) (any, error) {
	result, err := s.pgcRanked(c, false)
	if err != nil {
		return nil, err
	}
	result["note"] = "根据播放、追番、互动数据综合计算"
	return Result(result), nil
}
func (s *Server) pgcRankList(c *Ctx) (any, error) { return s.pgcRanked(c, true) }
func (s *Server) pgcRankIndex(c *Ctx) (any, error) {
	seasons, err := s.pgcSeasons(c)
	if err != nil {
		return nil, err
	}
	link := func(id int) string {
		return fmt.Sprintf("/pgc/season/rank/list?season_type=1&style_id=%d&day=3", id)
	}
	tabs := []render.M{{"tab_id": 1, "title": "综合", "url": link(0)}}
	for i, style := range pgcStyles(seasons) {
		tabs = append(tabs, render.M{"tab_id": i + 2, "title": style, "url": link(i + 2)})
	}
	return render.M{"index": tabs}, nil
}
func pgcFeedPayload(seasons []store.Season, name string, cursor float64) render.M {
	kind, ok := map[string]int{"anime": 1, "guochuang": 4, "documentary": 3, "movie": 2, "tv": 5, "variety": 7}[name]
	if !ok {
		kind = 1
	}
	rows := []store.Season{}
	for _, season := range seasons {
		if season.State == 0 && season.SeasonType == kind {
			rows = append(rows, season)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Follow > rows[j].Follow })
	sub := []render.M{}
	for _, season := range Slice(rows, cursor, cursor+12) {
		card := render.M{"season_id": season.SeasonID, "title": season.Title, "cover": season.Cover, "sub_title": render.IndexShow(season), "rating": pgcScore(season.Score), "stat": render.M{"view": render.CompactCount(season.Views), "follow": render.CompactCount(season.Follow)}, "hover": render.M{"text": []string{pgcScore(season.Score) + "分", strings.Join(season.Styles, " ")}}, "link": fmt.Sprintf("/bangumi/play/ss%d", season.SeasonID), "badge_info": render.M{"text": render.SeasonBadge(season)}}
		if len(season.Episodes) > 0 {
			card["episode_id"] = season.Episodes[0].EpID
		}
		sub = append(sub, card)
	}
	items := sub
	if kind == 1 || kind == 4 {
		items = []render.M{{"card_style": "v_card", "sub_items": sub}}
	}
	return render.M{"items": items, "coursor": cursor + float64(len(sub)), "has_next": cursor+float64(len(sub)) < float64(len(rows))}
}
func (s *Server) pgcFeed(c *Ctx) (any, error) {
	seasons, err := s.pgcSeasons(c)
	if err != nil {
		return nil, err
	}
	return pgcFeedPayload(seasons, c.P["name"], NumberUnicode(c.P["coursor"], 0)), nil
}
func (s *Server) pgcPage(c *Ctx) (any, error) {
	seasons, err := s.pgcSeasons(c)
	if err != nil {
		return nil, err
	}
	byID := map[int64]store.Season{}
	for _, season := range seasons {
		byID[season.SeasonID] = season
	}
	banners, err := s.Store.ListBanner(c.Context(), "WHERE position=? ORDER BY sort, id", "anime")
	if err != nil {
		return nil, err
	}
	bannerItems := []render.M{}
	for _, banner := range banners {
		var sid int64
		if banner.SeasonID != nil {
			sid = *banner.SeasonID
		}
		title, evaluate := "", ""
		if season, ok := byID[sid]; ok {
			title, evaluate = render.IndexShow(season), season.Evaluate
		}
		bannerItems = append(bannerItems, render.M{"season_id": sid, "title": banner.Title, "cover": banner.Img, "big_cover": banner.Img, "play_title": title, "evaluate": evaluate, "link": banner.URL})
	}
	followItems := []render.M{}
	if c.Me != nil {
		follows, err := s.Store.ListBangumiFollow(c.Context(), "WHERE mid=? ORDER BY season_id", c.Me.MID)
		if err != nil {
			return nil, err
		}
		for _, follow := range follows {
			item := render.M{"follow_item_type": "user", "season_id": follow.SeasonID, "progress": "尚未观看", "link": fmt.Sprintf("/bangumi/play/ss%d", follow.SeasonID)}
			if season, ok := byID[follow.SeasonID]; ok {
				item["title"], item["cover"] = season.Title, season.Cover
			}
			followItems = append(followItems, item)
		}
	}
	name := "anime"
	if c.P["name"] == "guochuang" {
		name = "guochuang"
	}
	feed := pgcFeedPayload(seasons, name, 0)
	feed["style"], feed["title"] = "web_feed_v3", "猜你喜欢"
	return render.M{"modules": []render.M{{"style": "web_banner_v3", "title": "", "items": bannerItems}, {"style": "web_follow_v3", "title": "我的追番", "items": followItems}, feed}}, nil
}
func (s *Server) pgcIndex(c *Ctx) (any, error) {
	seasons, err := s.pgcSeasons(c)
	if err != nil {
		return nil, err
	}
	order, kind := NumberUnicode(c.P["order"], 2), NumberUnicode(c.P["season_type"], 1)
	key := func(season store.Season) float64 {
		switch order {
		case 4:
			return season.Score
		case 3:
			return float64(season.Follow)
		case 0, 5:
			return float64(season.Ctime)
		default:
			return float64(season.Views)
		}
	}
	styles := bplanStyles(seasons, int(kind))
	rows := []store.Season{}
	for _, season := range seasons {
		if season.State == 0 && float64(season.SeasonType) == kind && bplanIndexFilter(c.P, season, styles) {
			rows = append(rows, season)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if c.P["sort"] == "1" {
			return key(rows[i]) < key(rows[j])
		}
		return key(rows[i]) > key(rows[j])
	})
	pn, ps := NumberUnicode(c.P["page"], 1), NumberUnicode(c.P["pagesize"], 24)
	cards := []render.M{}
	for _, season := range Page(rows, pn, ps) {
		label := render.CompactCount(season.Views) + "次播放"
		if order == 4 {
			label = pgcScore(season.Score) + "分"
		} else if order == 3 {
			label = render.CompactCount(season.Follow) + "追番"
		}
		cards = append(cards, render.SeasonCard(season, render.M{"order": label}))
	}
	return render.M{"list": cards, "has_next": store.BoolInt(pn*ps < float64(len(rows))), "num": pn, "size": ps, "total": len(rows)}, nil
}
func (s *Server) pgcTimeline(c *Ctx) (any, error) {
	seasons, err := s.pgcSeasons(c)
	if err != nil {
		return nil, err
	}
	followed := map[int64]bool{}
	if c.Me != nil {
		rows, err := s.Store.ListBangumiFollow(c.Context(), "WHERE mid=?", c.Me.MID)
		if err != nil {
			return nil, err
		}
		for _, f := range rows {
			followed[f.SeasonID] = true
		}
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	before, after, kind := NumberUnicode(c.P["before"], 6), NumberUnicode(c.P["after"], 6), NumberUnicode(c.P["types"], 1)
	days := []render.M{}
	for d := -before; d <= after; d++ {
		// The mock adds 86400000 milliseconds, rather than calendar days (DST).
		date := time.UnixMilli(today.UnixMilli() + int64(d*86400000)).In(today.Location())
		weekday := (int(date.Weekday())+6)%7 + 1
		episodes := []render.M{}
		for _, season := range seasons {
			if season.State != 0 || float64(season.SeasonType) != kind || season.Weekday != weekday {
				continue
			}
			count := len(season.Episodes)
			if d > 0 {
				count++
			}
			index := fmt.Sprintf("第%d话", count)
			if season.IsFinish {
				index = "已完结"
			}
			ep := render.M{"season_id": season.SeasonID, "title": season.Title, "cover": season.Cover, "square_cover": season.Cover, "pub_time": season.PubTime, "pub_index": index, "published": store.BoolInt(d <= 0), "follow": store.BoolInt(followed[season.SeasonID])}
			// ep_cover (web only): latest episode cover, else the wide still, else the poster.
			epCover := season.Horizontal
			if len(season.Episodes) > 0 {
				last := season.Episodes[len(season.Episodes)-1]
				ep["episode_id"] = last.EpID
				if last.Cover != "" {
					epCover = last.Cover
				}
			}
			if epCover == "" {
				epCover = season.Cover
			}
			ep["ep_cover"] = epCover
			episodes = append(episodes, ep)
		}
		days = append(days, render.M{"date": fmt.Sprintf("%d-%d", date.Month(), date.Day()), "date_ts": date.Unix(), "day_of_week": weekday, "is_today": store.BoolInt(d == 0), "episodes": episodes})
	}
	return Result(days), nil
}
