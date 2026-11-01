package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf16"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

func sourceClip(s string, n int) string {
	r := utf16.Encode([]rune(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(utf16.Decode(r))
}
func (s *Server) registerDataSources() {
	s.registerImageCache()
	for _, admin := range []bool{false, true} {
		prefix := "/x/web/"
		if admin {
			prefix = "/x/admin/"
		}
		s.handle(prefix+"datasources", func(c *Ctx) (any, error) {
			if _, e := s.sourceAuth(c, admin, false); e != nil {
				return nil, e
			}
			return s.sourceList(c, admin)
		})
		for _, action := range []string{"test", "save", "sync", "delete"} {
			s.handle(prefix+"datasource/"+action, func(c *Ctx) (any, error) { return s.sourceAction(c, admin, action) })
		}
	}
	s.handle("/x/web/archive/origin/reset", s.resetArchiveOrigin)
	s.handle("/x/web/season/origin/reset", s.resetCollectionOrigin)
	s.handle("/xlive/app-blink/v1/streamSource/videos", func(c *Ctx) (any, error) {
		u, e := c.Login()
		if e != nil {
			return nil, e
		}
		return s.relayableVideos(c, u.MID)
	})
	s.handle("/x/admin/live/room/videos", func(c *Ctx) (any, error) {
		if _, e := c.Admin(); e != nil {
			return nil, e
		}
		r, e := s.Store.RoomByID(c.Context(), ParseUnicodeID(c.P["room_id"]))
		if errors.Is(e, sql.ErrNoRows) {
			return nil, Fail(19002003, "房间不存在或已被封禁")
		}
		if e != nil {
			return nil, e
		}
		return s.relayableVideos(c, r.UID)
	})
}
func (s *Server) sourceAuth(c *Ctx, admin, write bool) (*store.User, error) {
	if admin {
		return c.Admin()
	}
	if write {
		return c.CSRF()
	}
	return c.Login()
}
func (s *Server) sourceJSON(ctx context.Context, ds store.DataSource) (map[string]any, error) {
	b, _ := json.Marshal(ds)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	u, e := s.Store.UserByID(ctx, ds.MID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	owner := map[string]any{"mid": ds.MID, "name": "已注销用户", "face": ""}
	if u != nil {
		owner["name"] = u.Uname
		owner["face"] = u.Face
	}
	var count int
	e = s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM archives WHERE origin_ds=? AND state<>? AND state<>?", ds.ID, -100, -6).Scan(&count)
	out["owner"] = owner
	out["has_password"] = ds.PasswordEnc != ""
	out["archives"] = count
	return out, e
}
func (s *Server) sourceList(c *Ctx, admin bool) (any, error) {
	if e := s.InitDataSources(c.Context()); e != nil {
		return nil, e
	}
	sources, e := s.Store.ListDataSource(c.Context())
	if e != nil {
		return nil, e
	}
	settings, e := s.Store.Settings(c.Context())
	if e != nil {
		return nil, e
	}
	list := []map[string]any{}
	for _, ds := range sources {
		if !admin && (ds.System || ds.MID != c.Me.MID) {
			continue
		}
		if admin && ((c.P["scope"] == "system" && !ds.System) || (c.P["scope"] == "user" && ds.System)) {
			continue
		}
		row, e := s.sourceJSON(c.Context(), ds)
		if e != nil {
			return nil, e
		}
		if admin {
			owner := row["owner"].(map[string]any)
			if !strings.Contains(strings.ToLower(ds.Name+" "+ds.URL+" "+ds.ServerName+" "+owner["name"].(string)), strings.ToLower(c.P["keyword"])) {
				continue
			}
		}
		list = append(list, row)
	}
	if !admin {
		return map[string]any{"list": list, "enabled": Truthy(settings["userDataSources"]), "max": 5, "sync_hours": settings["dataSourceSyncHours"]}, nil
	}
	pn, ps := NumberGo(c.P["pn"], 1), NumberGo(c.P["ps"], 20)
	mid, _ := settings["officialMid"].(float64)
	official, e := s.Render.Owner(c.Context(), int64(mid))
	if e != nil {
		return nil, e
	}
	return map[string]any{"list": Page(list, pn, ps), "page": map[string]any{"pn": pn, "ps": ps, "total": len(list)}, "official": official}, nil
}
func (s *Server) selectedSource(c *Ctx, admin, optional, systemOnly bool) (*store.DataSource, error) {
	if optional && c.P["id"] == "" {
		return nil, nil
	}
	ds, e := store.DataSourceByID(c.Context(), s.Store.DB, ParseGoID(c.P["id"]))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, Fail(-404, "数据源不存在")
	}
	if e != nil {
		return nil, e
	}
	if (!admin && (ds.MID != c.Me.MID || ds.System)) || (admin && systemOnly && !ds.System) {
		return nil, Fail(-404, "数据源不存在")
	}
	return ds, nil
}
func (s *Server) sourceAction(c *Ctx, admin bool, action string) (any, error) {
	if _, e := s.sourceAuth(c, admin, true); e != nil {
		return nil, e
	}
	if e := s.InitDataSources(c.Context()); e != nil {
		return nil, e
	}
	settings, e := s.Store.Settings(c.Context())
	if e != nil {
		return nil, e
	}
	if !admin && action != "delete" && !Truthy(settings["userDataSources"]) {
		return nil, Fail(-403, "站点未开放个人数据源")
	}
	optional := action == "test" || action == "save"
	ds, e := s.selectedSource(c, admin, optional, optional)
	if e != nil {
		return nil, e
	}
	switch action {
	case "sync":
		v, e := s.startSourceSync(c.Context(), ds.ID)
		if e != nil {
			return nil, e
		}
		return s.sourceJSON(c.Context(), *v)
	case "delete":
		return nil, s.deleteSource(c, ds)
	}
	address, username := strings.TrimSpace(c.P["url"]), strings.TrimSpace(c.P["username"])
	if address == "" {
		return nil, Fail(-400, "请填写 Emby 服务器地址")
	}
	if username == "" {
		return nil, Fail(-400, "请填写 Emby 用户名")
	}
	if _, e = emby.NormalizeBase(address); e != nil {
		return nil, Fail(-400, e.Error())
	}
	password := c.P["password"]
	if password == "" && !BoolParam(c.P["clear_password"]) && ds != nil {
		password, e = s.decryptPassword(ds.PasswordEnc)
		if e != nil {
			return nil, e
		}
	}
	mid := c.Me.MID
	if admin {
		f, _ := settings["officialMid"].(float64)
		mid = int64(f)
	}
	if ds != nil {
		mid = ds.MID
	}
	if action == "save" {
		if ds != nil && ds.Status == "syncing" {
			return nil, Fail(-400, "正在同步中，请稍后再修改")
		}
		if ds == nil && !admin {
			var n int
			if e = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM data_sources WHERE mid=? AND system=0", mid).Scan(&n); e != nil {
				return nil, e
			}
			if n >= 5 {
				return nil, Fail(-400, "最多添加 5 个数据源")
			}
		}
		if _, e = s.Store.UserByID(c.Context(), mid); errors.Is(e, sql.ErrNoRows) {
			return nil, Fail(-400, "官方账号不存在，请先在站点设置中配置")
		}
		if e != nil {
			return nil, e
		}
	}
	client, e := emby.NewDataSource(address, username, password, 0, admin || Truthy(settings["dataSourcePrivateNetwork"]))
	if e != nil {
		return nil, Fail(-400, e.Error())
	}
	defer client.Close()
	info, e := client.Probe(c.Context())
	if e != nil {
		return nil, Fail(-400, e.Error())
	}
	if action == "test" {
		return info, nil
	}
	enc, e := s.encryptPassword(password)
	if e != nil {
		return nil, e
	}
	r := s.sources
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("服务正在退出")
	}
	existing := ds != nil
	e = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		ctx := c.Context()
		if e := LockTx(ctx, tx); e != nil {
			return e
		}
		if existing {
			var e error
			ds, e = store.DataSourceByID(ctx, tx, ds.ID)
			if errors.Is(e, sql.ErrNoRows) {
				return Fail(-404, "数据源不存在")
			}
			if e != nil {
				return e
			}
			if ds.Status == "syncing" {
				return Fail(-400, "正在同步中，请稍后再修改")
			}
		} else {
			var n int
			if !admin {
				if e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM data_sources WHERE mid=? AND system=0", mid).Scan(&n); e != nil {
					return e
				}
				if n >= 5 {
					return Fail(-400, "最多添加 5 个数据源")
				}
			}
			id, e := store.NextID(ctx, tx)
			if e != nil {
				return e
			}
			ds = &store.DataSource{ID: id, MID: mid, System: admin, Type: "emby", AutoSync: true, Ctime: time.Now().Unix()}
		}
		var count int
		if e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM data_sources WHERE mid=? AND system=? AND server_id=? AND username=? AND id<>?", mid, store.BoolInt(admin), info.ServerID, username, ds.ID).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return Fail(-400, "已经添加过这个 Emby 账号")
		}
		libs := []store.DataSourceLibrary{}
		selected, hasSelected := c.P["libraries"]
		for _, l := range info.Libraries {
			lib := store.DataSourceLibrary{ID: l.ID, Name: l.Name, Type: l.Type, Enabled: true}
			for _, old := range ds.Libraries {
				if old.ID == l.ID {
					lib.Enabled = old.Enabled
					lib.Items = old.Items
					lib.CollectionID = old.CollectionID
				}
			}
			if hasSelected {
				lib.Enabled = false
				for _, id := range strings.Split(selected, ",") {
					if id == l.ID {
						lib.Enabled = true
					}
				}
			}
			libs = append(libs, lib)
		}
		name := sourceClip(strings.TrimSpace(c.P["name"]), 30)
		if name == "" {
			name = info.ServerName
		}
		if name == "" {
			name = "Emby"
		}
		ds.Name = name
		ds.URL = address
		ds.Username = username
		ds.PasswordEnc = enc
		ds.ServerName = info.ServerName
		ds.ServerID = info.ServerID
		ds.Libraries = libs
		ds.Status = "syncing"
		ds.Message = "正在同步媒体库…"
		if v, ok := c.P["auto_sync"]; ok {
			ds.AutoSync = BoolParam(v)
		}
		if e := store.SaveDataSource(ctx, tx, ds); e != nil {
			return e
		}
		action := "添加数据源"
		if existing {
			action = "修改数据源"
		}
		target := ds.Name
		if admin {
			target += "（系统）"
		}
		return s.LogTx(ctx, tx, c.Me.MID, action, target)
	})
	if e != nil {
		return nil, e
	}
	if old, ok := r.clients[ds.ID]; ok {
		old.client.Close()
		delete(r.clients, ds.ID)
	}
	s.launchSourceLocked(*ds)
	return s.sourceJSON(c.Context(), *ds)
}
func (s *Server) deleteSource(c *Ctx, ds *store.DataSource) error {
	r := s.sources
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel := r.running[ds.ID]; cancel != nil {
		cancel()
	}
	stopped := []int64{}
	err := s.WriteTx(c, func(tx *db.Tx) error {
		ctx := c.Context()
		rows, e := tx.QueryContext(ctx, "SELECT room_id,source FROM rooms")
		if e != nil {
			return e
		}
		rooms := []store.LiveRoom{}
		for rows.Next() {
			var room store.LiveRoom
			var raw string
			if e = rows.Scan(&room.RoomID, &raw); e != nil {
				rows.Close()
				return e
			}
			if e = json.Unmarshal([]byte(raw), &room.Source); e != nil {
				rows.Close()
				return e
			}
			rooms = append(rooms, room)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, room := range rooms {
			if room.Source == nil || room.Source.Type != "video" {
				continue
			}
			var n int
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM archives WHERE aid=? AND origin_ds=?", room.Source.AID, ds.ID).Scan(&n); e != nil {
				return e
			}
			if n > 0 {
				stopped = append(stopped, room.RoomID)
				if _, e = tx.ExecContext(ctx, "UPDATE rooms SET source=?,live_status=0,pending_start=0 WHERE room_id=?", `{"type":"push","url":"","loop":true}`, room.RoomID); e != nil {
					return e
				}
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE archives SET state=-100 WHERE origin_ds=?", ds.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM collections WHERE origin_ds=?", ds.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM data_sources WHERE id=?", ds.ID); e != nil {
			return e
		}
		return s.LogTx(ctx, tx, c.Me.MID, "删除数据源", ds.Name)
	})
	if err == nil {
		for _, id := range stopped {
			s.liveChat.preparing(id)
		}
		if old, ok := r.clients[ds.ID]; ok {
			old.client.Close()
			delete(r.clients, ds.ID)
		}
	}
	return err
}
func sourceLibraryName(ds *store.DataSource, id string) string {
	if ds != nil {
		for _, l := range ds.Libraries {
			if l.ID == id {
				return l.Name
			}
		}
	}
	return ""
}
func (s *Server) archiveOriginJSON(ctx context.Context, a store.Archive) (any, error) {
	if a.Origin == nil {
		return nil, nil
	}
	ds, e := store.DataSourceByID(ctx, s.Store.DB, a.OriginDS)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	name := ""
	if ds != nil {
		name = ds.Name
	}
	return map[string]any{"type": a.Origin.Type, "datasource_id": a.OriginDS, "datasource_name": name, "item_id": a.OriginItem, "library": sourceLibraryName(ds, a.Origin.Library), "locked": a.Origin.Locked, "meta": a.Origin.Meta, "missing": a.State == -6}, nil
}
func (s *Server) collectionOriginJSON(ctx context.Context, c store.Collection) (any, error) {
	if c.Origin == nil {
		return nil, nil
	}
	ds, e := store.DataSourceByID(ctx, s.Store.DB, c.OriginDS)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	name := ""
	if ds != nil {
		name = ds.Name
	}
	return map[string]any{"type": c.Origin.Type, "datasource_id": c.OriginDS, "datasource_name": name, "library": sourceLibraryName(ds, c.OriginLibrary), "locked": c.Origin.Locked, "meta": c.Origin.Meta}, nil
}
