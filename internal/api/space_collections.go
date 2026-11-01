package api

import (
	"database/sql"
	"errors"
	"slices"
	"sort"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) spaceCollectionArcs(c *Ctx, x *store.Collection) ([]store.Archive, error) {
	arcs := []store.Archive{}
	for _, aid := range x.AIDs {
		a, err := s.Store.ArchiveByID(c.Context(), aid)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if a.State == 0 {
			arcs = append(arcs, *a)
		}
	}
	return arcs, nil
}
func (s *Server) spaceCollections(c *Ctx) (any, error) {
	mid := ParseWideID(c.P["mid"])
	pn, ps := NumberWide(c.P["page_num"], 1), NumberWide(c.P["page_size"], 10)
	rows, err := s.Store.ListCollection(c.Context(), "WHERE mid=? ORDER BY id", mid)
	if err != nil {
		return nil, err
	}
	seasons, series := []render.M{}, []render.M{}
	for _, row := range Page(rows, pn, ps) {
		if row.Kind != "season" && row.Kind != "series" {
			continue
		}
		x, err := s.Store.CollectionByID(c.Context(), row.ID)
		if err != nil {
			return nil, err
		}
		arcs, err := s.spaceCollectionArcs(c, x)
		if err != nil {
			return nil, err
		}
		recent, err := s.spaceRenderArcs(c, Slice(arcs, 0, 4), false)
		if err != nil {
			return nil, err
		}
		meta := render.M{"name": x.Title, "cover": x.Cover, "description": x.Intro, "total": len(arcs), "mid": mid}
		item := render.M{"meta": meta, "recent_aids": Slice(x.AIDs, 0, 4), "archives": recent}
		if x.Kind == "season" {
			meta["season_id"] = x.ID
			meta["ptime"] = x.Ctime
			seasons = append(seasons, item)
		} else {
			meta["series_id"] = x.ID
			meta["mtime"] = x.Ctime
			series = append(series, item)
		}
	}
	return render.M{"items_lists": render.M{"page": render.M{"page_num": pn, "page_size": ps, "total": len(rows)}, "seasons_list": seasons, "series_list": series}}, nil
}
func (s *Server) spaceCollectionVideos(c *Ctx, id int64, pn float64, reverse bool) (any, error) {
	x, err := s.Store.CollectionByID(c.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "合集不存在")
	}
	if err != nil {
		return nil, err
	}
	arcs, err := s.spaceCollectionArcs(c, x)
	if err != nil {
		return nil, err
	}
	if x.Kind == "season" {
		if reverse {
			slices.Reverse(arcs)
		}
	} else {
		sort.SliceStable(arcs, func(i, j int) bool {
			if reverse {
				return arcs[i].Pubdate < arcs[j].Pubdate
			}
			return arcs[i].Pubdate > arcs[j].Pubdate
		})
	}
	aids := []int64{}
	for _, a := range arcs {
		aids = append(aids, a.AID)
	}
	out, err := s.spaceRenderArcs(c, Page(arcs, pn, 30), false)
	if err != nil {
		return nil, err
	}
	return render.M{"aids": aids, "archives": out, "meta": render.M{"season_id": x.ID, "series_id": x.ID, "name": x.Title, "cover": x.Cover, "description": x.Intro, "total": len(arcs), "mid": x.MID}, "page": render.M{"page_num": pn, "num": pn, "page_size": 30, "size": 30, "total": len(arcs)}}, nil
}
