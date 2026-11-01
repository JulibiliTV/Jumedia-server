package api

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) communityHeartbeat(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	e = s.WriteTx(c, func(tx *db.Tx) error {
		var paused int
		if e := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM history_paused WHERE mid=?", u.MID).Scan(&paused); e != nil {
			return e
		}
		if paused > 0 {
			return nil
		}
		h := store.History{MID: u.MID, AID: ParseID(c.P["aid"]), CID: ParseID(c.P["cid"]), EpID: ParseID(c.P["epid"]), SID: ParseID(c.P["sid"]), ViewAt: time.Now().Unix(), Progress: int(Int(c.P["played_time"]))}
		if h.EpID != 0 {
			if err := RequireReference(c, tx, "SELECT COUNT(*) FROM episodes WHERE ep_id=?", h.EpID, -404, "剧集不存在"); err != nil {
				return err
			}
		}
		if h.SID != 0 {
			if err := RequireReference(c, tx, "SELECT COUNT(*) FROM seasons WHERE season_id=?", h.SID, -404, "番剧不存在"); err != nil {
				return err
			}
		}

		_, e := tx.ExecContext(c.Context(), "INSERT INTO history (mid,aid,cid,epid,sid,view_at,progress) VALUES (?,?,?,?,?,?,?) ON CONFLICT (mid,aid) DO UPDATE SET cid=excluded.cid,epid=excluded.epid,sid=excluded.sid,view_at=excluded.view_at,progress=excluded.progress", h.MID, h.AID, h.CID, store.NullableID(h.EpID), store.NullableID(h.SID), h.ViewAt, h.Progress)
		return e
	})
	return nil, e
}
func (s *Server) communityHistory(c *Ctx) (any, error) {
	u, e := c.Login()
	if e != nil {
		return nil, e
	}
	ps := Number(c.P["ps"], 20)
	maxID, viewAt := Int(c.P["max"]), Int(c.P["view_at"])
	var rows []store.History
	if viewAt == 0 {
		rows, e = s.Store.ListHistory(c.Context(), "WHERE mid=? ORDER BY view_at DESC,aid DESC", u.MID)
	} else {
		rows, e = s.Store.ListHistory(c.Context(), "WHERE mid=? AND (view_at<? OR (view_at=? AND aid<?)) ORDER BY view_at DESC,aid DESC", u.MID, viewAt, viewAt, maxID)
	}
	if e != nil {
		return nil, e
	}
	list := []render.M{}
	var lastID, lastAt int64
	for _, h := range Slice(rows, 0, ps) {
		v, e := s.communityHistoryRow(c, h)
		if e != nil {
			return nil, e
		}
		if v != nil {
			list = append(list, v)
			lastID = v["history"].(render.M)["oid"].(int64)
			lastAt = h.ViewAt
		}
	}
	return render.M{"cursor": render.M{"max": lastID, "view_at": lastAt, "business": "", "ps": ps}, "tab": []render.M{{"type": "archive", "name": "视频"}}, "list": list}, nil
}
func (s *Server) communityHistoryRow(c *Ctx, h store.History) (render.M, error) {
	if h.EpID != 0 {
		season, e := s.Store.SeasonByID(c.Context(), h.SID)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		for _, ep := range season.Episodes {
			if ep.EpID == h.EpID {
				return render.M{"title": season.Title, "long_title": "第" + ep.Title + "话 " + ep.LongTitle, "cover": ep.Cover, "author_name": "番剧", "author_face": "", "author_mid": 0, "view_at": h.ViewAt, "progress": h.Progress, "duration": ep.Duration, "badge": render.SeasonBadge(*season), "kid": season.SeasonID, "uri": fmt.Sprintf("/bangumi/play/ep%d", ep.EpID), "history": render.M{"oid": ep.AID, "epid": ep.EpID, "bvid": "", "cid": ep.CID, "page": 1, "part": ep.LongTitle, "business": "pgc", "dt": 2}}, nil
			}
		}
		return nil, nil
	}
	a, e := s.Store.ArchiveByID(c.Context(), h.AID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if len(a.Pages) == 0 {
		return nil, fmt.Errorf("archive %d has no history page", a.AID)
	}
	index := 0
	for i, p := range a.Pages {
		if p.CID == h.CID {
			index = i
			break
		}
	}
	p := a.Pages[index]
	o, e := s.Render.Owner(c.Context(), a.MID)
	if e != nil {
		return nil, e
	}
	return render.M{"title": a.Title, "long_title": "", "cover": a.Pic, "author_name": o["name"], "author_face": o["face"], "author_mid": a.MID, "view_at": h.ViewAt, "progress": h.Progress, "duration": p.Duration, "videos": len(a.Pages), "badge": "", "kid": a.AID, "uri": "/video/" + a.BVID, "history": render.M{"oid": a.AID, "epid": 0, "bvid": a.BVID, "cid": p.CID, "page": index + 1, "part": p.Part, "business": "archive", "dt": 2}}, nil
}
func (s *Server) communityShadow(c *Ctx) (any, error) {
	u, e := c.Login()
	if e != nil {
		return nil, e
	}
	var n int
	e = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM history_paused WHERE mid=?", u.MID).Scan(&n)
	return n > 0, e
}
func (s *Server) communityShadowSet(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	if c.P["switch"] == "true" || c.P["switch"] == "1" {
		_, e = s.Store.DB.ExecContext(c.Context(), "INSERT INTO history_paused (mid) VALUES (?) ON CONFLICT (mid) DO NOTHING", u.MID)
	} else {
		_, e = s.Store.DB.ExecContext(c.Context(), "DELETE FROM history_paused WHERE mid=?", u.MID)
	}
	return nil, e
}
func (s *Server) communityHistoryClear(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	_, e = s.Store.DB.ExecContext(c.Context(), "DELETE FROM history WHERE mid=?", u.MID)
	return nil, e
}
func (s *Server) communityToView(c *Ctx) (any, error) {
	u, e := c.Login()
	if e != nil {
		return nil, e
	}
	clause := "WHERE mid=? ORDER BY add_at DESC,aid"
	web := c.R.URL.Path == "/x/v2/history/toview/web"
	if web && c.P["asc"] == "true" {
		clause = "WHERE mid=? ORDER BY add_at,aid"
	}
	rows, e := s.Store.ListToView(c.Context(), clause, u.MID)
	if e != nil {
		return nil, e
	}
	list := []render.M{}
	for _, row := range rows {
		a, e := s.Store.ArchiveByID(c.Context(), row.AID)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return nil, e
		}
		progress := 0
		e = s.Store.DB.QueryRowContext(c.Context(), "SELECT progress FROM history WHERE mid=? AND aid=?", u.MID, row.AID).Scan(&progress)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		v, e := s.Render.ArcView(c.Context(), *a, render.M{"add_at": row.AddAt, "progress": progress})
		if e != nil {
			return nil, e
		}
		list = append(list, v)
	}
	count := len(list)
	if web {
		list = Page(list, Number(c.P["pn"], 1), Number(c.P["ps"], 20))
	}
	return render.M{"count": count, "list": list}, nil
}
func (s *Server) communityToViewAdd(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	a, e := s.communityArchive(c)
	if e != nil {
		return nil, e
	}
	result, e := s.Store.DB.ExecContext(c.Context(), "INSERT INTO toview (mid,aid,add_at) VALUES (?,?,?) ON CONFLICT (mid,aid) DO NOTHING", u.MID, a.AID, time.Now().Unix())
	if e != nil {
		return nil, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return nil, e
	}
	if n == 0 {
		return nil, Fail(90001, "已经在稍后再看里了")
	}
	return nil, nil
}
func (s *Server) communityToViewDelete(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	e = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		for _, resource := range strings.Split(c.P["resources"], ",") {
			id := ParseID(strings.SplitN(resource, ":", 2)[0])
			if _, e := tx.ExecContext(c.Context(), "DELETE FROM toview WHERE mid=? AND aid=?", u.MID, id); e != nil {
				return e
			}
		}
		return nil
	})
	return nil, e
}
