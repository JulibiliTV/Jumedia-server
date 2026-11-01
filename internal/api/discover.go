package api

import (
	"fmt"
	"math"

	"sort"
	"strconv"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) registerDiscover() {
	s.handle("/x/web-interface/popular/series/list", s.discoverWeeklyList)
	s.handle("/x/web-interface/popular/series/one", s.discoverWeeklyOne)
	s.handle("/x/web-interface/ranking/region", s.discoverRegionRanking)
	s.handle("/x/web-interface/feedback/dislike", func(c *Ctx) (any, error) { _, err := c.CSRF(); return nil, err })
	s.handle("/x/v2/feed/index", s.discoverFeed)
	s.handle("/x/web-interface/wbi/index/top/feed/rcmd", s.discoverWebFeed)
	s.handle("/x/feed/dislike", func(c *Ctx) (any, error) { return nil, nil })
	s.handle("/x/web-interface/popular", s.discoverPopular)
	s.handle("/x/web-interface/ranking/v2", s.discoverRanking)
	s.handle("/x/web-interface/region/feed/rcmd", s.discoverRegion)
	s.handle("/x/polymer/web-dynamic/v1/feed/all", s.discoverDynamic)
	s.handle("/x/web-interface/search/all/v2", s.discoverSearchAll)
	s.handle("/x/web-interface/wbi/search/type", s.discoverSearchType)
	s.handle("/x/web-interface/suggest", s.discoverSuggest)
	s.handle("/x/web-interface/view", s.discoverView)
	s.handle("/x/web-interface/view/detail", s.discoverDetail)
	s.handle("/x/player/wbi/v2", s.discoverPlayer)
	s.handle("/x/player/videoshot", func(c *Ctx) (any, error) {
		return render.M{"pvdata": "", "img_x_len": 0, "img_y_len": 0, "img_x_size": 0, "img_y_size": 0, "image": []any{}, "index": []any{}}, nil
	})
	s.handle("/x/player/wbi/playurl", s.discoverPlayURL)
	s.handle("/bfs/subtitle/{file}", s.discoverSubtitle)
}

func discoverMap[T any](xs []T, fn func(T) (render.M, error)) ([]render.M, error) {
	out := make([]render.M, 0, len(xs))
	for _, x := range xs {
		v, err := fn(x)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Server) discoverFollows(c *Ctx, mid int64) (map[int64]bool, error) {
	rs, err := s.Store.ListRelation(c.Context(), "WHERE mid=?", mid)
	out := map[int64]bool{}
	for _, r := range rs {
		out[r.FID] = true
	}
	return out, err
}
func (s *Server) discoverFeed(c *Ctx) (any, error) {
	list, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	list, err = s.publicationFilter(c, list, true)
	if err != nil {
		return nil, err
	}
	idx := NumberUnicode(c.P["idx"], 0)
	sort.SliceStable(list, func(i, j int) bool {
		return math.Mod(float64(list[i].AID)*2654435761+idx, 1000) < math.Mod(float64(list[j].AID)*2654435761+idx, 1000)
	})
	follows := map[int64]bool{}
	if c.Me != nil {
		follows, err = s.discoverFollows(c, c.Me.MID)
		if err != nil {
			return nil, err
		}
	}
	items := make([]render.M, 0, 20)
	for i := 0; i < 20; i++ {
		n := math.Mod(idx+float64(i), float64(len(list)))
		if math.IsNaN(n) || n < 0 || math.Trunc(n) != n {
			return nil, fmt.Errorf("feed index has no archive")
		}
		a := list[int(n)]
		card, e := s.Render.FeedCard(c.Context(), a)
		if e != nil {
			return nil, e
		}
		if follows[a.MID] {
			card["rcmd_reason"] = render.M{"content": "已关注"}
		}
		items = append(items, card)
	}
	return render.M{"items": items, "config": render.M{"column": 4}}, nil
}
func (s *Server) discoverPopular(c *Ctx) (any, error) {
	list, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Stat.View > list[j].Stat.View })
	pn, ps := NumberUnicode(c.P["pn"], 1), NumberUnicode(c.P["ps"], 20)
	rows, err := discoverMap(Page(list, pn, ps), func(a store.Archive) (render.M, error) {
		reason := ""
		if a.Stat.View > 500000 {
			reason = "百万播放"
		}
		return s.Render.ArcView(c.Context(), a, render.M{"rcmd_reason": render.M{"content": reason}})
	})
	if err != nil {
		return nil, err
	}
	return render.M{"list": rows, "no_more": pn*ps >= float64(len(list))}, nil
}
func (s *Server) discoverRanking(c *Ctx) (any, error) {
	all, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	rid := NumberUnicode(c.P["rid"], 0)
	list := []store.Archive{}
	for _, a := range all {
		if rid == 0 || float64(a.TID) == rid {
			list = append(list, a)
		}
	}
	score := func(a store.Archive) int64 { return a.Stat.View + a.Stat.Like*10 + a.Stat.Coin*20 }
	sort.SliceStable(list, func(i, j int) bool { return score(list[i]) > score(list[j]) })
	rows, err := discoverMap(list[:min(100, len(list))], func(a store.Archive) (render.M, error) {
		return s.Render.ArcView(c.Context(), a, render.M{"score": score(a)})
	})
	if err != nil {
		return nil, err
	}
	return render.M{"note": "根据稿件内容质量、近期的数据综合展示，动态更新", "list": rows}, nil
}
func (s *Server) discoverRegion(c *Ctx) (any, error) {
	all, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	rid := NumberUnicode(c.P["from_region"], 0)
	list := []store.Archive{}
	for _, a := range all {
		if float64(a.TID) == rid {
			list = append(list, a)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Pubdate > list[j].Pubdate })
	list = Page(list, NumberUnicode(c.P["display_id"], 1), NumberUnicode(c.P["request_cnt"], 15))
	if len(list) == 0 {
		return nil, Fail(62013, "没有更多了")
	}
	rows, err := discoverMap(list, func(a store.Archive) (render.M, error) { return s.Render.ArcView(c.Context(), a) })
	if err != nil {
		return nil, err
	}
	return render.M{"archives": rows}, nil
}
func (s *Server) discoverDynamic(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	follows, err := s.discoverFollows(c, u.MID)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	all, err = s.publicationFilter(c, all, false)
	if err != nil {
		return nil, err
	}
	list := []store.Archive{}
	for _, a := range all {
		if follows[a.MID] {
			list = append(list, a)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Pubdate > list[j].Pubdate })
	offset := NumberUnicode(c.P["offset"], 0)
	items, err := discoverMap(Slice(list, offset, offset+12), func(a store.Archive) (render.M, error) {
		o, err := s.Render.Owner(c.Context(), a.MID)
		if err != nil {
			return nil, err
		}
		return render.M{"id_str": strconv.FormatInt(a.AID*31, 10), "type": "DYNAMIC_TYPE_AV", "visible": true, "modules": render.M{
			"module_author":  render.M{"mid": a.MID, "name": o["name"], "face": o["face"], "pub_ts": a.Pubdate, "pub_time": "", "pub_action": "投稿了视频", "jump_url": fmt.Sprintf("/space/%d", a.MID)},
			"module_dynamic": render.M{"desc": render.M{"text": a.Desc}, "major": render.M{"type": "MAJOR_TYPE_ARCHIVE", "archive": render.M{"aid": strconv.FormatInt(a.AID, 10), "bvid": a.BVID, "title": a.Title, "cover": a.Pic, "desc": a.Desc, "duration_text": render.FormatDuration(render.TotalDuration(a)), "stat": render.M{"play": render.CompactCount(a.Stat.View), "danmaku": render.CompactCount(a.Stat.Danmaku)}, "jump_url": "/video/" + a.BVID}}},
			"module_stat":    render.M{"comment": render.M{"count": a.Stat.Reply}, "like": render.M{"count": a.Stat.Like}, "forward": render.M{"count": a.Stat.Share}},
		}}, nil
	})
	if err != nil {
		return nil, err
	}
	next := offset + float64(len(items))
	return render.M{"items": items, "offset": strconv.FormatFloat(next, 'f', -1, 64), "has_more": next < float64(len(list)), "update_num": 0}, nil
}
