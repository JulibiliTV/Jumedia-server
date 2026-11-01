package api

import (
	"slices"
	"sort"
	"time"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

var legacyRIDs = map[float64][]int64{1: {1005}, 13: {1005}, 32: {1005}, 33: {1005}, 51: {1005}, 152: {1005}, 167: {1005}, 3: {1003}, 129: {1004}, 4: {1008}, 36: {1010}, 188: {1012}, 234: {1018}, 223: {1013}, 160: {1030, 1031}, 211: {1020}, 217: {1024}, 119: {1007}, 155: {1014}, 5: {1002}, 181: {1001}}

func (s *Server) discoverRegionRanking(c *Ctx) (any, error) {
	rid, day := NumberUnicode(c.P["rid"], 0), NumberUnicode(c.P["day"], 3)
	tids, ok := legacyRIDs[rid]
	regions, err := s.Store.ListRegion(c.Context(), "")
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, r := range regions {
		names[r.TID] = r.Name
		if !ok && float64(r.TID) == rid {
			tids = []int64{r.TID}
		}
	}
	all, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	rows := []store.Archive{}
	for _, a := range all {
		if slices.Contains(tids, a.TID) {
			rows = append(rows, a)
		}
	}
	since := float64(time.Now().Unix()) - day*86400
	score := func(a store.Archive) int64 { return a.Stat.View + a.Stat.Like*10 + a.Stat.Coin*20 }
	sort.SliceStable(rows, func(i, j int) bool {
		x, y := float64(rows[i].Pubdate) >= since, float64(rows[j].Pubdate) >= since
		if x != y {
			return x
		}
		return score(rows[i]) > score(rows[j])
	})
	out := []render.M{}
	for _, a := range rows[:min(100, len(rows))] {
		owner, err := s.Render.Owner(c.Context(), a.MID)
		if err != nil {
			return nil, err
		}
		duration := 0
		for _, p := range a.Pages {
			duration += p.Duration
		}
		out = append(out, render.M{"aid": a.AID, "bvid": a.BVID, "typename": names[a.TID], "title": a.Title, "subtitle": "", "play": a.Stat.View, "review": a.Stat.Reply, "video_review": a.Stat.Danmaku, "favorites": a.Stat.Favorite, "mid": a.MID, "author": owner["name"], "description": a.Desc, "create": time.Unix(a.Pubdate, 0).UTC().Format("2006-01-02 15:04"), "pic": a.Pic, "coins": a.Stat.Coin, "duration": render.FormatDuration(duration), "badgepay": false, "pts": score(a)})
	}
	return out, nil
}
