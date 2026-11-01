package api

import (
	"database/sql"
	"errors"
	"slices"
	"strings"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func originLock(locked []string, field string, equal bool) []string {
	out := []string{}
	found := false
	for _, f := range locked {
		if f == field {
			found = true
			if equal {
				continue
			}
		}
		out = append(out, f)
	}
	if !equal && !found {
		out = append(out, field)
	}
	return out
}

func (s *Server) editArchiveOrigin(c *Ctx, title string) (any, error) {
	var a *store.Archive
	err := s.WriteTx(c, func(tx *db.Tx) error {
		var e error
		a, e = store.ArchiveFrom(c.Context(), tx, ParseGoID(c.P["aid"]))
		if errors.Is(e, sql.ErrNoRows) || e == nil && (a.MID != c.Me.MID || a.State == -100) {
			return Fail(-404, "稿件不存在")
		}
		if e != nil {
			return e
		}
		if a.Origin == nil {
			return Fail(-400, "该稿件不是数据源中的视频")
		}
		pic := sourceLocalImage(c, c.P["cover"])
		if pic == "" {
			pic = a.Pic
		}
		next := store.ArchiveMeta{Title: title, Desc: c.P["desc"], TID: int64(NumberGo(c.P["tid"], 0)), Tag: c.P["tag"], Pic: pic}
		m := a.Origin.Meta
		oldValues := []any{a.Title, a.Desc, a.TID, a.Tag, a.Pic}
		newValues := []any{next.Title, next.Desc, next.TID, next.Tag, next.Pic}
		metaValues := []any{m.Title, m.Desc, m.TID, m.Tag, m.Pic}
		for i, f := range archiveFields {
			if oldValues[i] != newValues[i] {
				a.Origin.Locked = originLock(a.Origin.Locked, f, newValues[i] == metaValues[i])
			}
		}
		applyArchiveMeta(a, next, archiveFields)
		var audit string
		if e = tx.QueryRowContext(c.Context(), "SELECT value FROM settings WHERE key=?", "auditRequired").Scan(&audit); e != nil {
			return e
		}
		ds, e := store.DataSourceByID(c.Context(), tx, a.OriginDS)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if a.State != -6 {
			a.State = 0
			if audit == "true" && c.Me.Role != "admin" && (ds == nil || !ds.System) {
				a.State = -1
			}
		}
		a.Reason = ""
		if e = store.SaveArchiveOrigin(c.Context(), tx, a); e != nil {
			return e
		}
		if _, e = tx.ExecContext(c.Context(), "UPDATE archives SET sub_tid=? WHERE aid=?", int64(Number(c.P["sub_tid"], 0)), a.AID); e != nil {
			return e
		}
		return s.LogTx(c.Context(), tx, c.Me.MID, "编辑稿件", title)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"aid": a.AID, "bvid": a.BVID, "state": a.State}, nil
}
func (s *Server) resetArchiveOrigin(c *Ctx) (any, error) {
	if _, e := c.CSRF(); e != nil {
		return nil, e
	}
	var a *store.Archive
	err := s.WriteTx(c, func(tx *db.Tx) error {
		var e error
		a, e = store.ArchiveFrom(c.Context(), tx, ParseGoID(c.P["aid"]))
		if errors.Is(e, sql.ErrNoRows) || e == nil && (a.MID != c.Me.MID || a.State == -100) {
			return Fail(-404, "稿件不存在")
		}
		if e != nil {
			return e
		}
		if a.Origin == nil {
			return Fail(-400, "该稿件不是数据源中的视频")
		}
		fields := a.Origin.Locked
		if c.P["fields"] != "" {
			fields = strings.Split(c.P["fields"], ",")
		}
		valid := []string{}
		for _, f := range fields {
			if slices.Contains(archiveFields, f) {
				valid = append(valid, f)
			}
		}
		applyArchiveMeta(a, a.Origin.Meta, valid)
		a.Origin.Locked = unlocked(a.Origin.Locked, valid)
		if e = store.SaveArchiveOrigin(c.Context(), tx, a); e != nil {
			return e
		}
		return s.LogTx(c.Context(), tx, c.Me.MID, "恢复数据源元信息", a.Title)
	})
	if err != nil {
		return nil, err
	}
	return s.creatorRow(c, *a)
}
func updateCollectionOrigin(c *Ctx, tx *db.Tx, x *store.Collection, title string) error {
	next := store.CollectionMeta{Title: title, Intro: c.P["intro"], Cover: sourceLocalImage(c, c.P["cover"])}
	if next.Cover == "" {
		next.Cover = x.Cover
	}
	meta := x.Origin.Meta
	x.Origin.Locked = originLock(x.Origin.Locked, "title", next.Title == meta.Title)
	x.Origin.Locked = originLock(x.Origin.Locked, "intro", next.Intro == meta.Intro)
	x.Origin.Locked = originLock(x.Origin.Locked, "cover", next.Cover == meta.Cover)
	x.Title = next.Title
	x.Intro = next.Intro
	x.Cover = next.Cover
	return store.SaveCollectionOrigin(c.Context(), tx, x)
}
func (s *Server) resetCollectionOrigin(c *Ctx) (any, error) {
	if _, e := c.CSRF(); e != nil {
		return nil, e
	}
	err := s.WriteTx(c, func(tx *db.Tx) error {
		x, e := store.CollectionFrom(c.Context(), tx, ParseGoID(c.P["season_id"]))
		if errors.Is(e, sql.ErrNoRows) || e == nil && x.MID != c.Me.MID {
			return Fail(-404, "合集不存在")
		}
		if e != nil {
			return e
		}
		if x.Origin == nil {
			return Fail(-400, "该合集不是数据源媒体库")
		}
		for _, f := range x.Origin.Locked {
			switch f {
			case "title":
				x.Title = x.Origin.Meta.Title
			case "intro":
				x.Intro = x.Origin.Meta.Intro
			case "cover":
				x.Cover = x.Origin.Meta.Cover
			}
		}
		x.Origin.Locked = []string{}
		return store.SaveCollectionOrigin(c.Context(), tx, x)
	})
	return nil, err
}

// JSON resources are absolute externally, but source metadata stays portable.
// Normalize an unchanged image returned by this server before comparing locks.
func sourceLocalImage(c *Ctx, image string) string {
	prefix := c.Server.Origin(c.R)
	if strings.HasPrefix(image, prefix+"/") {
		path := strings.TrimPrefix(image, prefix)
		for _, p := range []string{"/media/", "/mock/", "/upload/"} {
			if strings.HasPrefix(path, p) {
				return path
			}
		}
	}
	return image
}
