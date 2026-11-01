package api

import (
	"database/sql"
	"errors"

	"jumedia-server/internal/store"
)

func (s *Server) registerSpace() {
	s.handle("/x/web-interface/archive/has/like", s.spaceHasLike)
	s.handle("/x/web-interface/archive/like", s.spaceLike)
	s.handle("/x/v2/view/dislike", s.spaceDislike)
	s.handle("/x/web-interface/coin/add", s.spaceCoin)
	s.handle("/x/v2/fav/video/favoured", s.spaceFavoured)
	s.handle("/x/v3/fav/resource/deal", s.spaceFavorite)
	s.handle("/x/web-interface/share/add", s.spaceShare)
	s.handle("/x/relation/modify", s.spaceModify)
	s.handle("/x/relation/followings", s.spaceFollowings)
	s.handle("/x/space/wbi/acc/info", s.spaceInfo)
	s.handle("/x/space/wbi/acc/relation", s.spaceRelation)
	s.handle("/x/relation/stat", s.spaceStat)
	s.handle("/x/web-interface/card", s.spaceCard)
	s.handle("/x/space/wbi/arc/search", s.spaceSearch)
	s.handle("/x/v2/space/archive/cursor", s.spaceCursor)
	s.handle("/x/space/top/arc", s.spaceTop)
	s.handle("/x/space/masterpiece", s.spaceMasterpiece)
	s.handle("/x/polymer/web-space/seasons_series_list", s.spaceCollections)
	s.handle("/x/polymer/web-space/seasons_archives_list", func(c *Ctx) (any, error) {
		return s.spaceCollectionVideos(c, ParseWideID(c.P["season_id"]), NumberWide(c.P["page_num"], 1), c.P["sort_reverse"] == "1" || c.P["sort_reverse"] == "true")
	})
	s.handle("/x/series/archives", func(c *Ctx) (any, error) {
		return s.spaceCollectionVideos(c, ParseWideID(c.P["series_id"]), NumberWide(c.P["pn"], 1), c.P["sort"] == "asc")
	})
}

func (s *Server) spaceUser(c *Ctx, id int64) (*store.User, error) {
	u, err := s.Store.UserByID(c.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "啥都木有")
	}
	return u, err
}
func (s *Server) spaceArchive(c *Ctx, onlyRID bool) (*store.Archive, error) {
	key := "rid"
	if !onlyRID {
		for _, k := range []string{"aid", "oid", "rid", "avid"} {
			if _, ok := c.P[k]; ok {
				key = k
				break
			}
		}
	}
	a, err := s.Store.ArchiveByID(c.Context(), ParseWideID(c.P[key]))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "啥都木有")
	}
	if err != nil {
		return nil, err
	}
	if a.State != 0 && (c.Me == nil || (a.MID != c.Me.MID && c.Me.Role != "admin")) {
		return nil, Fail(-404, "啥都木有")
	}
	return a, nil
}
