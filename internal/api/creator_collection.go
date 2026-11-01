package api

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func (s *Server) creatorSeasons(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	collections, err := s.Store.ListCollection(c.Context(), "WHERE mid=? ORDER BY id", u.MID)
	if err != nil {
		return nil, err
	}
	seasons := []map[string]any{}
	for _, collection := range collections {
		items, err := s.Store.ListCollectionItem(c.Context(), "WHERE collection_id=? ORDER BY position", collection.ID)
		if err != nil {
			return nil, err
		}
		aids := []int64{}
		archives := []map[string]any{}
		for _, item := range items {
			aids = append(aids, item.AID)
			a, err := s.Store.ArchiveByID(c.Context(), item.AID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			archives = append(archives, map[string]any{"aid": a.AID, "title": a.Title, "pic": a.Pic, "state": a.State})
		}
		origin, err := s.collectionOriginJSON(c.Context(), collection)
		if err != nil {
			return nil, err
		}
		seasons = append(seasons, map[string]any{"origin": origin, "id": collection.ID, "kind": collection.Kind, "title": collection.Title, "intro": collection.Intro, "cover": collection.Cover, "aids": aids, "ep_count": len(aids), "ctime": collection.Ctime, "archives": archives})
	}
	return map[string]any{"seasons": seasons}, nil
}

func (s *Server) creatorSeasonSave(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	title := Trim(c.P["title"])
	if title == "" {
		return nil, Fail(-400, "请填写名称")
	}
	err = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		ctx := c.Context()
		if err := LockTx(ctx, tx); err != nil {
			return err
		}
		existingCollection, e := store.CollectionFrom(ctx, tx, ParseGoID(c.P["season_id"]))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if existingCollection != nil && existingCollection.MID == u.MID && existingCollection.Origin != nil {
			return updateCollectionOrigin(c, tx, existingCollection, title)
		}
		aids := []int64{}
		seen := map[int64]bool{}
		firstCover := ""
		for _, value := range strings.Split(c.P["aids"], ",") {
			var aid int64
			var cover string
			err := tx.QueryRowContext(ctx, "SELECT aid,pic FROM archives WHERE aid=? AND mid=? AND (?<>? OR origin_ds=0)", ParseGoID(value), u.MID, c.P["kind"], "season").Scan(&aid, &cover)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			// collection_items represents membership as a set, with explicit order.
			if seen[aid] {
				continue
			}
			seen[aid] = true
			if len(aids) == 0 {
				firstCover = cover
			}
			aids = append(aids, aid)
		}
		var id int64
		var oldCover string
		err := tx.QueryRowContext(ctx, "SELECT id,cover FROM collections WHERE id=? AND mid=?", ParseGoID(c.P["season_id"]), u.MID).Scan(&id, &oldCover)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		existing := err == nil
		cover := c.P["cover"]
		if existing {
			if cover == "" {
				cover = oldCover
			}
			if _, err = tx.ExecContext(ctx, "UPDATE collections SET title=?,intro=?,cover=? WHERE id=?", title, c.P["intro"], cover, id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM collection_items WHERE collection_id=?", id); err != nil {
				return err
			}
		} else {
			id, err = store.NextID(ctx, tx)
			if err != nil {
				return err
			}
			if cover == "" {
				cover = firstCover
			}
			kind := "season"
			if c.P["kind"] == "series" {
				kind = "series"
			}
			if err = store.InsertCollection(ctx, tx, &store.Collection{ID: id, MID: u.MID, Kind: kind, Title: title, Intro: c.P["intro"], Cover: cover, Ctime: time.Now().Unix()}); err != nil {
				return err
			}
		}
		for i, aid := range aids {
			// Match the mock: uniqueness is controlled by the supplied kind, while
			// updating an existing collection never changes its stored kind.
			if c.P["kind"] == "season" {
				if _, err = tx.ExecContext(ctx, "DELETE FROM collection_items WHERE aid=? AND collection_id IN (SELECT id FROM collections WHERE kind=? AND id<>?)", aid, "season", id); err != nil {
					return err
				}
			}
			if err = store.InsertCollectionItem(ctx, tx, &store.CollectionItem{CollectionID: id, AID: aid, Position: i}); err != nil {
				return err
			}
		}
		return nil
	})
	return nil, err
}

func (s *Server) creatorSeasonDelete(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	err = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		if err := LockTx(c.Context(), tx); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM collections WHERE id=? AND mid=? AND origin_ds<>0", ParseGoID(c.P["season_id"]), u.MID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return Fail(-400, "媒体库合集由数据源管理，请在数据源中停用对应媒体库")
		}
		// The FK cascade removes items together with their collection.
		_, err := tx.ExecContext(c.Context(), "DELETE FROM collections WHERE id=? AND mid=?", ParseGoID(c.P["season_id"]), u.MID)
		return err
	})
	return nil, err
}
