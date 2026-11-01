package api

import (
	"fmt"
	"jumedia-server/internal/render"
	"math"
	"sort"
)

func (s *Server) discoverWebFeed(c *Ctx) (any, error) {
	ps := NumberWide(c.P["ps"], 12)
	idx := math.Max(0, NumberWide(c.P["fresh_idx"], 1)-1) * ps
	list, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool {
		return math.Mod(float64(list[i].AID)*2654435761+idx, 1000) < math.Mod(float64(list[j].AID)*2654435761+idx, 1000)
	})
	mid := int64(0)
	follows := map[int64]bool{}
	if c.Me != nil {
		mid = c.Me.MID
		follows, err = s.discoverFollows(c, mid)
		if err != nil {
			return nil, err
		}
	}
	item := []render.M{}
	for i := 0; i < int(math.Max(0, math.Min(ps, float64(len(list))))); i++ {
		n := math.Mod(idx+float64(i), float64(len(list)))
		if math.IsNaN(n) || n < 0 || math.Trunc(n) != n {
			return nil, fmt.Errorf("feed index has no archive")
		}
		a := list[int(n)]
		card, err := s.Render.ArcView(c.Context(), a)
		if err != nil {
			return nil, err
		}
		card["id"], card["goto"], card["uri"], card["is_followed"] = a.AID, "av", "/video/"+a.BVID, 0
		if follows[a.MID] {
			card["is_followed"] = 1
		}
		item = append(item, card)
	}
	return render.M{"item": item, "mid": mid}, nil
}
