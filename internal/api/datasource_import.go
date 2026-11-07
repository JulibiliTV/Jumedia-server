package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

func (s *Server) sourceImportContext(c *Ctx, write bool) (*store.DataSource, store.DataSourceLibrary, map[string]any, error) {
	var lib store.DataSourceLibrary
	if _, err := s.sourceAuth(c, false, write); err != nil {
		return nil, lib, nil, err
	}
	settings, err := s.Store.Settings(c.Context())
	if err != nil {
		return nil, lib, nil, err
	}
	if !Truthy(settings["userDataSources"]) {
		return nil, lib, nil, Fail(-403, "站点未开放个人数据源")
	}
	ds, err := s.selectedSource(c, false, false)
	if err != nil {
		return nil, lib, nil, err
	}
	for _, l := range ds.Libraries {
		if l.ID == c.P["library"] && l.Enabled {
			return ds, l, settings, nil
		}
	}
	return nil, lib, nil, Fail(-400, "请选择已接入的媒体库")
}

func sourceImportable(item emby.Item) bool {
	if item.LocationType == "Virtual" {
		return false
	}
	switch item.Type {
	case "Series":
		return true
	case "Movie", "Video", "MusicVideo":
		return len(item.MediaSources) > 0
	}
	return false
}

// Imported catalogs use library names as local identifiers. Resolve only that
// legacy shape; keep local identifiers so existing origins still match.
func sourceImportLibraryID(ctx context.Context, client *sourceSyncClient, lib store.DataSourceLibrary) (string, error) {
	if lib.Type != "" || lib.ID != lib.Name {
		return lib.ID, nil
	}
	libraries, err := client.Libraries(ctx)
	if err != nil {
		return "", err
	}
	for _, candidate := range libraries {
		if candidate.ID == lib.ID {
			return candidate.ID, nil
		}
	}
	id, matches := "", 0
	for _, candidate := range libraries {
		if candidate.Name == lib.Name {
			id = candidate.ID
			matches++
		}
	}
	if matches != 1 {
		return "", errors.New("媒体库名称无法唯一匹配，请重新连接并保存数据源")
	}
	return id, nil
}

func (s *Server) sourceBrowse(c *Ctx) (any, error) {
	ds, lib, settings, err := s.sourceImportContext(c, false)
	if err != nil {
		return nil, err
	}
	client, err := s.sourceSyncClient(c.Context(), ds, settings)
	if err != nil {
		return nil, err
	}
	defer client.close()
	libraryID, err := sourceImportLibraryID(c.Context(), client, lib)
	if err != nil {
		return nil, Fail(-400, err.Error())
	}
	pn, ps := max(1, int(NumberGo(c.P["pn"], 1))), max(1, min(100, int(NumberGo(c.P["ps"], 20))))
	page, err := client.browsePage(c.Context(), libraryID, strings.TrimSpace(c.P["keyword"]), (pn-1)*ps, ps, nil)
	if err != nil {
		return nil, Fail(-400, err.Error())
	}
	list := []map[string]any{}
	for _, item := range page.Items {
		var aid int64
		var state int
		err = s.Store.DB.QueryRowContext(c.Context(), "SELECT aid,state FROM archives WHERE origin_ds=? AND origin_item=?", ds.ID, item.ID).Scan(&aid, &state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		var archive any
		if err == nil {
			archive = map[string]any{"aid": aid, "state": state}
		}
		list = append(list, map[string]any{"id": item.ID, "title": item.Name, "type": item.Type, "pic": sourceItemImage(ds.ID, item), "importable": sourceImportable(item), "archive": archive})
	}
	return map[string]any{"list": list, "page": map[string]any{"pn": pn, "ps": ps, "total": page.TotalRecordCount}}, nil
}

func (s *Server) sourceImport(c *Ctx) (any, error) {
	ds, lib, settings, err := s.sourceImportContext(c, true)
	if err != nil {
		return nil, err
	}
	if ds.Status == "syncing" {
		return nil, Fail(-400, "正在同步中，请稍后")
	}
	var input []string
	if json.Unmarshal([]byte(c.P["item_ids"]), &input) != nil || len(input) == 0 || len(input) > 20 {
		return nil, Fail(-400, "每次请选择 1–20 个视频")
	}
	ids, seen := []string{}, map[string]bool{}
	for _, id := range input {
		if strings.TrimSpace(id) == "" || strings.ContainsAny(id, ",:") {
			return nil, Fail(-400, "视频 ID 无效")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	client, err := s.sourceSyncClient(c.Context(), ds, settings)
	if err != nil {
		return nil, err
	}
	defer client.close()
	libraryID, err := sourceImportLibraryID(c.Context(), client, lib)
	if err != nil {
		return nil, Fail(-400, err.Error())
	}
	page, err := client.browsePage(c.Context(), libraryID, "", 0, len(ids), ids)
	if err != nil {
		return nil, Fail(-400, err.Error())
	}
	items := map[string]emby.Item{}
	for _, item := range page.Items {
		items[item.ID] = item
	}
	regions, err := s.Store.ListRegion(c.Context(), "WHERE COALESCE(parent,0)=0 ORDER BY sort,tid")
	if err != nil {
		return nil, err
	}
	mirrors := []sourceSyncMirror{}
	for _, id := range ids {
		item, ok := items[id]
		if !ok || !sourceImportable(item) {
			return nil, Fail(-400, "所选视频不存在或没有可用媒体，请刷新列表")
		}
		if client.provider == emby.Jellyfin {
			// Jellyfin ignores ParentId when /Items is filtered by explicit IDs.
			inLibrary, err := client.hasAncestor(c.Context(), item.ID, libraryID)
			if err != nil {
				return nil, Fail(-400, err.Error())
			}
			if !inLibrary {
				return nil, Fail(-400, "所选视频不在该媒体库")
			}
		}
		mirror, err := mirrorSource(c.Context(), client, ds, regions, lib, item)
		if err != nil {
			return nil, Fail(-400, err.Error())
		}
		if mirror == nil {
			return nil, Fail(-400, fmt.Sprintf("「%s」没有可用的视频", item.Name))
		}
		mirrors = append(mirrors, sourceSyncMirror{item: id, mirror: mirror})
	}
	archives := []map[string]any{}
	err = s.WriteTx(c, func(tx *db.Tx) error {
		ctx := c.Context()
		current, err := store.DataSourceByID(ctx, tx, ds.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return Fail(-404, "数据源不存在")
		}
		if err != nil {
			return err
		}
		if current.Status == "syncing" {
			return Fail(-400, "正在同步中，请稍后")
		}
		for _, result := range mirrors {
			var aid int64
			var state int
			err := tx.QueryRowContext(ctx, "SELECT aid,state FROM archives WHERE origin_ds=? AND origin_item=?", ds.ID, result.item).Scan(&aid, &state)
			created := errors.Is(err, sql.ErrNoRows)
			if err != nil && !created {
				return err
			}
			if !created && state == -100 {
				return Fail(-400, "所选视频已在 Jumedia 删除，不能重复导入")
			}
			if created {
				aid, err = saveSourceSyncArchive(ctx, tx, ds, &lib, result, true, nil, Truthy(settings["dandanplayEnabled"]))
				if err != nil {
					return err
				}
				state = -20
				if _, err = tx.ExecContext(ctx, "UPDATE archives SET state=-20,ctime=?,pubdate=0 WHERE aid=?", time.Now().Unix(), aid); err != nil {
					return err
				}
			}
			archives = append(archives, map[string]any{"aid": aid, "bvid": store.AV2BV(aid), "state": state, "created": created})
		}
		return s.LogTx(ctx, tx, c.Me.MID, "数据源导入草稿", ds.Name)
	})
	return map[string]any{"archives": archives}, err
}
