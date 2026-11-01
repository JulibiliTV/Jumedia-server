package api

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"strings"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func discoverMatch(text, keyword string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(keyword))
}
func discoverEm(text, keyword string) string {
	if keyword == "" {
		return text
	}
	return strings.ReplaceAll(text, keyword, `<em class="keyword">`+keyword+`</em>`)
}
func (s *Server) discoverSearchVideo(c *Ctx, a store.Archive, kw string) (render.M, error) {
	o, err := s.Render.Owner(c.Context(), a.MID)
	if err != nil {
		return nil, err
	}
	region, err := s.Store.RegionName(c.Context(), a.TID)
	if err != nil {
		return nil, err
	}
	return render.M{"type": "video", "id": a.AID, "aid": a.AID, "bvid": a.BVID, "title": discoverEm(a.Title, kw), "pic": a.Pic, "author": o["name"], "mid": a.MID, "upic": o["face"], "play": a.Stat.View, "video_review": a.Stat.Danmaku, "favorites": a.Stat.Favorite, "duration": render.FormatDuration(render.TotalDuration(a)), "pubdate": a.Pubdate, "description": a.Desc, "tag": a.Tag, "typename": region}, nil
}
func (s *Server) discoverSearchUser(c *Ctx, u store.User) (render.M, error) {
	fans, err := s.Store.FollowerCount(c.Context(), u.MID)
	if err != nil {
		return nil, err
	}
	var videos int
	if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM archives WHERE mid=? AND state=0", u.MID).Scan(&videos); err != nil {
		return nil, err
	}
	return render.M{"type": "bili_user", "mid": u.MID, "uname": u.Uname, "usign": u.Sign, "upic": u.Face, "fans": fans, "videos": videos, "level": u.Level}, nil
}

// allSearch differs deliberately: all searches season styles and only user
// names, whereas typed searches season titles and user signatures.
func (s *Server) discoverSearchRows(c *Ctx, kind, kw string, allSearch bool) ([]render.M, error) {
	rows := []render.M{}
	switch kind {
	case "video":
		list, err := s.Store.PublishedArchives(c.Context())
		if err != nil {
			return nil, err
		}
		if !allSearch {
			bplanSortArcs(list, c.P["order"])
		}
		for _, a := range list {
			if !allSearch && ((NumberUnicode(c.P["tids"], 0) != 0 && float64(a.TID) != NumberUnicode(c.P["tids"], 0)) || !bplanDurationIn(render.TotalDuration(a), NumberUnicode(c.P["duration"], 0))) {
				continue
			}
			if discoverMatch(a.Title+a.Desc+a.Tag, kw) {
				v, e := s.discoverSearchVideo(c, a, kw)
				if e != nil {
					return nil, e
				}
				rows = append(rows, v)
			}
		}
	case "media_bangumi", "media_ft":
		list, err := s.Store.ListSeason(c.Context(), "WHERE state=0 ORDER BY season_id")
		if err != nil {
			return nil, err
		}
		for _, a := range list {
			text := a.Title
			if allSearch {
				text += strings.Join(a.Styles, "")
			}
			bangumi := a.SeasonType == 1 || a.SeasonType == 4
			if bangumi != (kind == "media_bangumi") || !discoverMatch(text, kw) {
				continue
			}
			a.Episodes, err = s.Store.ListEpisode(c.Context(), "WHERE season_id=? ORDER BY position", a.SeasonID)
			if err != nil {
				return nil, err
			}
			rows = append(rows, render.SeasonCard(a, render.M{"title": discoverEm(a.Title, kw)}))
		}
	case "live_room":
		list, err := s.Store.ListLiveRoom(c.Context(), "WHERE banned=0 ORDER BY room_id")
		if err != nil {
			return nil, err
		}
		for _, a := range list {
			o, e := s.Render.Owner(c.Context(), a.UID)
			if e != nil {
				return nil, e
			}
			if discoverMatch(a.Title+o["name"].(string), kw) {
				v, e := s.Render.RoomCard(c.Context(), a)
				if e != nil {
					return nil, e
				}
				rows = append(rows, v)
			}
		}
	case "bili_user":
		list, err := s.Store.ListUser(c.Context(), "WHERE status=0 ORDER BY mid")
		if err != nil {
			return nil, err
		}
		for _, u := range list {
			text := u.Uname
			if !allSearch {
				text += u.Sign
			}
			if discoverMatch(text, kw) {
				v, e := s.discoverSearchUser(c, u)
				if e != nil {
					return nil, e
				}
				rows = append(rows, v)
			}
		}
	}
	return rows, nil
}
func (s *Server) discoverSearchAll(c *Ctx) (any, error) {
	kw := strings.TrimSpace(c.P["keyword"])
	counts := render.M{}
	result := []render.M{}
	count := 0
	for _, kind := range []string{"media_bangumi", "media_ft", "bili_user", "live_room", "video"} {
		rows, err := s.discoverSearchRows(c, kind, kw, true)
		if err != nil {
			return nil, err
		}
		counts[kind] = len(rows)
		switch kind {
		case "bili_user":
			rows = rows[:min(1, len(rows))]
		case "live_room":
			rows = rows[:min(4, len(rows))]
		case "video":
			count = len(rows)
			rows = rows[:min(20, len(rows))]
		}
		result = append(result, render.M{"result_type": kind, "data": rows})
	}
	var id [6]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	return render.M{"seid": hex.EncodeToString(id[:]), "page": 1, "pagesize": 20, "numResults": count, "numPages": math.Ceil(float64(count) / 20), "top_tlist": counts, "result": result}, nil
}
func (s *Server) discoverSearchType(c *Ctx) (any, error) {
	kind := c.P["search_type"]
	pn, ps := NumberUnicode(c.P["page"], 1), NumberUnicode(c.P["page_size"], 20)
	rows, err := s.discoverSearchRows(c, kind, strings.TrimSpace(c.P["keyword"]), false)
	if err != nil {
		return nil, err
	}
	// JSON.stringify serializes NaN and Infinity (page_size=0) as null.
	pages := math.Max(1, math.Ceil(float64(len(rows))/ps))
	var numPages any = pages
	if math.IsNaN(pages) || math.IsInf(pages, 0) {
		numPages = nil
	}
	result := Page(rows, pn, ps)
	out := render.M{"page": pn, "pagesize": ps, "numResults": len(rows), "numPages": numPages, "result": result}
	if kind == "live_room" {
		out["result"] = render.M{"live_room": result}
		out["pageinfo"] = render.M{"live_room": render.M{"numResults": len(rows), "numPages": numPages}}
	}
	return out, nil
}
func (s *Server) discoverSuggest(c *Ctx) (any, error) {
	kw := strings.TrimSpace(c.P["term"])
	tags := []render.M{}
	if kw != "" {
		archives, err := s.Store.PublishedArchives(c.Context())
		if err != nil {
			return nil, err
		}
		seasons, err := s.Store.ListSeason(c.Context(), "ORDER BY season_id")
		if err != nil {
			return nil, err
		}
		users, err := s.Store.ListUser(c.Context(), "ORDER BY mid")
		if err != nil {
			return nil, err
		}
		pool := []string{}
		for _, a := range archives {
			pool = append(pool, a.Title)
		}
		for _, a := range seasons {
			pool = append(pool, a.Title)
		}
		for _, u := range users {
			pool = append(pool, u.Uname)
		}
		for _, title := range pool {
			if discoverMatch(title, kw) {
				tags = append(tags, render.M{"value": title, "term": title, "ref": len(tags), "name": discoverEm(title, kw), "spid": 0})
				if len(tags) == 10 {
					break
				}
			}
		}
	}
	return Root(render.M{"code": 0, "result": render.M{"tag": tags}}), nil
}
