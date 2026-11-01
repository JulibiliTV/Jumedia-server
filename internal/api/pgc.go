package api

import (
	"strconv"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) registerPgc() {
	s.handle("/pgc/web/rank/list", s.pgcWebRank)
	s.handle("/pgc/page/web/v3/feed", s.pgcFeed)
	s.handle("/pgc/page/web/feed", s.pgcFeed)
	s.handle("/pgc/page/web/v3", s.pgcPage)
	s.handle("/pgc/web/timeline", s.pgcTimeline)
	s.handle("/pgc/season/rank/index", s.pgcRankIndex)
	s.handle("/pgc/season/rank/list", s.pgcRankList)
	s.handle("/pgc/view/web/season", s.pgcSeason)
	s.handle("/pgc/season/index/result", s.pgcIndex)
	s.handle("/pgc/player/web/playurl", s.pgcPlayURL)
	s.handle("/x/space/bangumi/follow/list", s.pgcFollowList)
	s.handle("/pgc/view/web/season/user/status", s.pgcUserStatus)
	s.handle("/pgc/web/follow/add", s.pgcFollowAdd)
	s.handle("/pgc/web/follow/del", s.pgcFollowDel)
	s.handle("/pgc/web/follow/status/update", s.pgcFollowUpdate)
}

// ListSeason intentionally leaves Episodes unloaded. Load them in one query so
// every shared SeasonCard has its first_ep/new_ep and correct episode count.
func (s *Server) pgcSeasons(c *Ctx) ([]store.Season, error) {
	seasons, err := s.Store.ListSeason(c.Context(), "ORDER BY season_id")
	if err != nil {
		return nil, err
	}
	episodes, err := s.Store.ListEpisode(c.Context(), "ORDER BY season_id, position")
	if err != nil {
		return nil, err
	}
	bySeason := make(map[int64][]store.Episode)
	for _, ep := range episodes {
		bySeason[ep.SeasonID] = append(bySeason[ep.SeasonID], ep)
	}
	for i := range seasons {
		seasons[i].Episodes = bySeason[seasons[i].SeasonID]
	}
	return seasons, nil
}
func pgcStyles(seasons []store.Season) []string {
	styles := []string{}
	seen := map[string]bool{}
	for _, season := range seasons {
		if season.SeasonType != 1 {
			continue
		}
		for _, style := range season.Styles {
			if !seen[style] {
				styles = append(styles, style)
				seen[style] = true
			}
		}
	}
	return styles
}
func pgcCards(seasons []store.Season, ranked bool) []render.M {
	cards := []render.M{}
	for i, season := range seasons {
		extra := render.M{}
		if ranked {
			extra["rank"] = i + 1
		}
		cards = append(cards, render.SeasonCard(season, extra))
	}
	return cards
}
func pgcWeekday(day int) string {
	days := []rune("一二三四五六日")
	if day < 1 || day > len(days) {
		return "undefined"
	}
	return string(days[day-1])
}
func pgcScore(score float64) string { return strconv.FormatFloat(score, 'f', -1, 64) }
