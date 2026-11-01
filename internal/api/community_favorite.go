package api

import (
	"database/sql"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) communityFolders(c *Ctx) (any, error) {
	mid, rid := ParseID(c.P["up_mid"]), ParseID(c.P["rid"])
	folders, e := s.Store.ListFavFolder(c.Context(), "WHERE mid=? AND (privacy=0 OR mid=?) ORDER BY ctime,id", mid, ActorID(c))
	if e != nil {
		return nil, e
	}
	list := []render.M{}
	for _, f := range folders {
		items, e := s.Store.ListFavItem(c.Context(), "WHERE folder_id=? ORDER BY mtime DESC,aid", f.ID)
		if e != nil {
			return nil, e
		}
		fav := 0
		for _, i := range items {
			if rid != 0 && i.AID == rid {
				fav = 1
			}
		}
		cover := ""
		if len(items) > 0 {
			a, e := s.Store.ArchiveByID(c.Context(), items[0].AID)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			if a != nil {
				cover = a.Pic
			}
		}
		list = append(list, render.M{"id": f.ID, "fid": f.ID, "mid": f.MID, "attr": f.Attr, "title": f.Title, "intro": f.Intro, "privacy": f.Privacy, "media_count": len(items), "fav_state": fav, "cover": cover, "ctime": f.Ctime})
	}
	return render.M{"count": len(list), "list": list}, nil
}
func (s *Server) communityResources(c *Ctx) (any, error) {
	folders, e := s.Store.ListFavFolder(c.Context(), "WHERE id=?", ParseID(c.P["media_id"]))
	if e != nil {
		return nil, e
	}
	if len(folders) == 0 || folders[0].Privacy == 1 && folders[0].MID != ActorID(c) {
		return nil, Fail(-403, "收藏夹不存在或已设为私密")
	}
	f := folders[0]
	items, e := s.Store.ListFavItem(c.Context(), "WHERE folder_id=? ORDER BY mtime DESC,aid", f.ID)
	if e != nil {
		return nil, e
	}
	type resource struct {
		item    store.FavItem
		archive store.Archive
	}
	filtered := []resource{}
	keyword := strings.ToLower(c.P["keyword"])
	for _, i := range items {
		a, e := s.Store.ArchiveByID(c.Context(), i.AID)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if strings.Contains(strings.ToLower(a.Title), keyword) {
			filtered = append(filtered, resource{i, *a})
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		switch c.P["order"] {
		case "view":
			return filtered[i].archive.Stat.View > filtered[j].archive.Stat.View
		case "pubtime":
			return filtered[i].archive.Pubdate > filtered[j].archive.Pubdate
		default:
			return filtered[i].item.Mtime > filtered[j].item.Mtime
		}
	})
	cover := ""
	if len(filtered) > 0 {
		cover = filtered[0].archive.Pic
	}
	u, e := s.Store.UserByID(c.Context(), f.MID)
	if e != nil {
		return nil, e
	}
	pn, ps := Number(c.P["pn"], 1), Number(c.P["ps"], 20)
	medias := []render.M{}
	for _, r := range Page(filtered, pn, ps) {
		a := r.archive
		title, attr := a.Title, 0
		if a.State != 0 {
			title = "已失效视频"
			attr = 9
		}
		owner, e := s.Render.Owner(c.Context(), a.MID)
		if e != nil {
			return nil, e
		}
		ugc := render.M{}
		if len(a.Pages) > 0 {
			ugc["first_cid"] = a.Pages[0].CID
		}
		medias = append(medias, render.M{"id": a.AID, "type": 2, "title": title, "cover": a.Pic, "intro": a.Desc, "duration": render.TotalDuration(a), "upper": owner, "attr": attr, "cnt_info": render.M{"collect": a.Stat.Favorite, "play": a.Stat.View, "danmaku": a.Stat.Danmaku}, "bvid": a.BVID, "bv_id": a.BVID, "pubtime": a.Pubdate, "fav_time": r.item.Mtime, "ugc": ugc})
	}
	return render.M{"info": render.M{"id": f.ID, "fid": f.ID, "mid": f.MID, "title": f.Title, "intro": f.Intro, "cover": cover, "media_count": len(items), "privacy": f.Privacy, "upper": render.M{"mid": u.MID, "name": u.Uname, "face": u.Face}, "ctime": f.Ctime}, "medias": medias, "has_more": pn*ps < float64(len(filtered))}, nil
}
func (s *Server) communityFolderAdd(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	title := Trim(c.P["title"])
	if title == "" {
		return nil, Fail(-400, "请填写收藏夹名称")
	}
	f := store.FavFolder{MID: u.MID, Title: title, Intro: c.P["intro"], Privacy: int(Int(c.P["privacy"])), Ctime: time.Now().Unix()}
	e = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		var e error
		f.ID, e = store.NextID(c.Context(), tx)
		if e != nil {
			return e
		}
		return store.InsertFavFolder(c.Context(), tx, &f)
	})
	if e != nil {
		return nil, e
	}
	return render.M{"id": f.ID, "title": f.Title}, nil
}
func (s *Server) communityFolderEdit(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	id := ParseID(c.P["media_id"])
	result, e := s.Store.DB.ExecContext(c.Context(), "UPDATE fav_folders SET title=?,intro=?,privacy=? WHERE id=? AND mid=?", c.P["title"], c.P["intro"], Int(c.P["privacy"]), id, u.MID)
	if e != nil {
		return nil, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return nil, e
	}
	if n == 0 {
		return nil, Fail(-404, "收藏夹不存在")
	}
	return render.M{"id": id}, nil
}
func (s *Server) communityFolderDelete(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	ids := []int64{}
	for _, v := range strings.Split(c.P["media_ids"], ",") {
		n := Number(v, math.NaN())
		if v == "" {
			n = 0
		}
		if !math.IsNaN(n) && n == math.Trunc(n) {
			ids = append(ids, int64(n))
		}
	}
	e = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		var def int64
		e := tx.QueryRowContext(c.Context(), "SELECT id FROM fav_folders WHERE mid=? ORDER BY ctime,id LIMIT 1", u.MID).Scan(&def)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		for _, id := range ids {
			if e == nil && id == def {
				return Fail(-400, "默认收藏夹不能删除")
			}
		}
		for _, id := range ids {
			if _, e := tx.ExecContext(c.Context(), "DELETE FROM fav_folders WHERE mid=? AND id=?", u.MID, id); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return 0, nil
}
