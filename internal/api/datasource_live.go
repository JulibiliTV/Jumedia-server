package api

import (
	"database/sql"
	"errors"
	"strings"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func (s *Server) relayableVideos(c *Ctx, mid int64) (any, error) {
	archives, err := s.Store.ListArchive(c.Context(), "WHERE mid=? AND state<>-100 AND state<>-6 ORDER BY ctime DESC,aid DESC", mid)
	if err != nil {
		return nil, err
	}
	list := []map[string]any{}
	for _, a := range archives {
		if !strings.Contains(strings.ToLower(a.Title), strings.ToLower(c.P["keyword"])) {
			continue
		}
		pages, err := s.Store.ListPage(c.Context(), "WHERE aid=? ORDER BY position", a.AID)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, p := range pages {
			if liveRelayableMedia(p.Media) {
				out = append(out, map[string]any{"cid": p.CID, "part": p.Part, "duration": p.Duration})
			}
		}
		if len(out) == 0 {
			continue
		}
		name := ""
		if a.Origin != nil {
			ds, e := store.DataSourceByID(c.Context(), s.Store.DB, a.OriginDS)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			if ds != nil {
				name = ds.Name
			}
		}
		list = append(list, map[string]any{"aid": a.AID, "bvid": a.BVID, "title": a.Title, "pic": a.Pic, "state": a.State, "datasource": name, "pages": out})
	}
	pn, ps := NumberGo(c.P["pn"], 1), NumberGo(c.P["ps"], 20)
	return map[string]any{"list": Page(list, pn, ps), "page": map[string]any{"pn": pn, "ps": ps, "total": len(list)}}, nil
}
func (s *Server) liveSourceJSON(c *Ctx, r *store.LiveRoom) (any, error) {
	src := liveSource(r)
	if src.Type != "video" {
		return src, nil
	}
	out := map[string]any{"type": src.Type, "url": src.URL, "loop": src.Loop, "format": src.Format, "kind": src.Kind, "aid": src.AID, "cid": src.CID, "video": nil}
	cache, err := s.roomCache(c.Context(), s.Store.DB, r)
	if err != nil {
		return nil, err
	}
	out["cache"] = liveCacheJSON(cache)
	a, err := s.Store.ArchiveByID(c.Context(), src.AID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, p := range a.Pages {
		if p.CID == src.CID {
			out["video"] = map[string]any{"aid": a.AID, "bvid": a.BVID, "cid": p.CID, "title": a.Title, "part": p.Part, "pic": a.Pic, "duration": p.Duration}
			break
		}
	}
	return out, nil
}
func (s *Server) videoSource(c *Ctx, q db.Querier, r *store.LiveRoom) (*store.StreamSource, error) {
	aid, cid := ParseUnicodeID(c.P["aid"]), ParseUnicodeID(c.P["cid"])
	if aid == 0 || cid == 0 {
		return nil, Fail(-400, "请选择要转播的视频")
	}
	a, err := store.ArchiveFrom(c.Context(), q, aid)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (a.MID != r.UID || a.State == -100 || a.State == -6) {
		return nil, Fail(-404, "视频不存在或不属于该直播间的主播")
	}
	if err != nil {
		return nil, err
	}
	for _, p := range a.Pages {
		if p.CID != cid {
			continue
		}
		if !liveRelayableMedia(p.Media) {
			return nil, Fail(-400, "只能转播数据源中的视频")
		}
		cache, err := s.liveCacheEnsure(c.Context(), q, p, r.LiveStatus == 1)
		if err != nil {
			return nil, err
		}
		return &store.StreamSource{Type: "video", Cache: cache.Key, URL: liveCachePath(cache.Key), Loop: c.P["loop"] != "false", Format: "ts", Kind: "vod", AID: aid, CID: cid}, nil
	}
	return nil, Fail(-404, "视频不存在或不属于该直播间的主播")
}
