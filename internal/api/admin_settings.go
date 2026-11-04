package api

import (
	"math"
	"regexp"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func (s *Server) adminRegionSave(c *Ctx) (any, error) {
	name := strings.TrimSpace(c.P["name"])
	if name == "" {
		return nil, Fail(-400, "请填写分区名称")
	}
	action := "新建分区"
	err := s.WriteTx(c, func(tx *db.Tx) error {
		write := func() error {
			parent := Number(c.P["parent"], 0)
			if parent != 0 {
				var count int
				if err := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM regions WHERE tid=? AND COALESCE(parent,0)=0", DBID(parent)).Scan(&count); err != nil {
					return err
				}
				if count == 0 {
					return Fail(-400, "上级分区不存在")
				}
			}
			var id int64
			err := tx.QueryRowContext(c.Context(), "SELECT tid FROM regions WHERE tid=?", adminKey(c.P["tid"])).Scan(&id)
			if err == nil {
				action = "编辑分区"
				_, err = tx.ExecContext(c.Context(), "UPDATE regions SET name=?,sort=?,visible=? WHERE tid=?", name, NumberUnicodeGo(c.P["sort"]), store.BoolInt(BoolParam(c.P["visible"])), id)
				return err
			}
			if !adminMissing(err) {
				return err
			}
			if parent != 0 {
				err = tx.QueryRowContext(c.Context(), "SELECT COALESCE(MAX(tid),?)+1 FROM regions WHERE parent=?", int64(parent)*100, int64(parent)).Scan(&id)
				id = max(id, int64(parent)*100+1)
			} else {
				err = tx.QueryRowContext(c.Context(), "SELECT COALESCE(MAX(tid),0)+1 FROM regions WHERE COALESCE(parent,0)=0").Scan(&id)
			}
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(c.Context(), "INSERT INTO regions(tid,name,sort,visible,parent) VALUES (?,?,?,?,?)", id, name, NumberUnicodeGo(c.P["sort"]), store.BoolInt(BoolParam(c.P["visible"])), store.NullableID(int64(parent)))
			return err
		}
		if err := write(); err != nil {
			return err
		}
		return s.LogTx(c.Context(), tx, ActorID(c), action, name)
	})
	if err != nil {
		return nil, err
	}
	return nil, nil
}
func (s *Server) adminRegionDelete(c *Ctx) (any, error) {
	return s.adminWrite(c, "删除分区", c.P["tid"], func(tx *db.Tx) error {
		var n int
		if err := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM archives WHERE tid=? AND state<>?", adminKey(c.P["tid"]), -100).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return Fail(-400, "该分区下仍有稿件，请先迁移")
		}
		if _, err := tx.ExecContext(c.Context(), "UPDATE archives SET sub_tid=0 WHERE sub_tid=? OR sub_tid IN (SELECT tid FROM regions WHERE parent=?)", adminKey(c.P["tid"]), adminKey(c.P["tid"])); err != nil {
			return err
		}
		_, err := tx.ExecContext(c.Context(), "DELETE FROM regions WHERE tid=? OR parent=?", adminKey(c.P["tid"]), adminKey(c.P["tid"]))
		return err
	})
}
func (s *Server) adminAreaSave(c *Ctx) (any, error) {
	action := "新建直播分区"
	err := s.WriteTx(c, func(tx *db.Tx) error {
		write := func() error {
			var id int64
			err := tx.QueryRowContext(c.Context(), "SELECT id FROM live_areas WHERE id=?", adminKey(c.P["id"])).Scan(&id)
			if err == nil {
				action = "编辑直播分区"
				_, err = tx.ExecContext(c.Context(), "UPDATE live_areas SET name=? WHERE id=?", c.P["name"], id)
				return err
			}
			if !adminMissing(err) {
				return err
			}
			var position int
			if err = tx.QueryRowContext(c.Context(), "SELECT COALESCE(MAX(id),0)+1,COALESCE(MAX(position),-1)+1 FROM live_areas").Scan(&id, &position); err != nil {
				return err
			}
			if err = store.InsertLiveArea(c.Context(), tx, &store.LiveArea{ID: id, Name: c.P["name"], Position: position}); err != nil {
				return err
			}
			childID, err := store.NextID(c.Context(), tx)
			if err != nil {
				return err
			}
			return store.InsertLiveAreaChild(c.Context(), tx, &store.LiveAreaChild{ID: childID, ParentID: id, Name: c.P["name"], Position: 0})
		}
		if err := write(); err != nil {
			return err
		}
		return s.LogTx(c.Context(), tx, ActorID(c), action, c.P["name"])
	})
	if err != nil {
		return nil, err
	}
	return nil, nil
}

var adminBannerSeason = regexp.MustCompile(`/ss(\d+)`)

func (s *Server) adminBannerSave(c *Ctx) (any, error) {
	action := "新建轮播"
	err := s.WriteTx(c, func(tx *db.Tx) error {
		write := func() error {
			var id int64
			err := tx.QueryRowContext(c.Context(), "SELECT id FROM banners WHERE id=?", adminKey(c.P["id"])).Scan(&id)
			existing := err == nil
			if err != nil && !adminMissing(err) {
				return err
			}
			if existing {
				action = "编辑轮播"
			} else {
				if id, err = store.NextID(c.Context(), tx); err != nil {
					return err
				}
			}
			var sid any
			if match := adminBannerSeason.FindStringSubmatch(c.P["url"]); match != nil {
				// The URL may point to an absent season. Keep that link intact while respecting
				// the optional FK; adminBanners projects season_id from the URL as the mock does.
				var n int64
				err = tx.QueryRowContext(c.Context(), "SELECT season_id FROM seasons WHERE season_id=?", adminKey(match[1])).Scan(&n)
				if err != nil && !adminMissing(err) {
					return err
				}
				if err == nil {
					sid = n
				}
			}
			position := "home"
			if c.P["position"] == "anime" || c.P["position"] == "channel" {
				position = c.P["position"]
			}
			var loc any
			if position == "channel" {
				n := Number(c.P["loc"], 0)
				if n == 0 || DBID(n) == nil {
					return Fail(-400, "请选择频道")
				}
				loc = DBID(n)
			}
			img := c.P["img"]
			if img == "" {
				return Fail(-400, "请上传轮播图片")
			}
			if existing {
				_, err = tx.ExecContext(c.Context(), "UPDATE banners SET title=?,img=?,url=?,position=?,sort=?,season_id=?,loc=? WHERE id=?", c.P["title"], img, c.P["url"], position, NumberUnicodeGo(c.P["sort"]), sid, loc, id)
			} else {
				_, err = tx.ExecContext(c.Context(), "INSERT INTO banners(id,title,img,url,position,sort,season_id,loc) VALUES (?,?,?,?,?,?,?,?)", id, c.P["title"], img, c.P["url"], position, NumberUnicodeGo(c.P["sort"]), sid, loc)
			}
			return err
		}
		if err := write(); err != nil {
			return err
		}
		return s.LogTx(c.Context(), tx, ActorID(c), action, c.P["title"])
	})
	if err != nil {
		return nil, err
	}
	return nil, nil
}
func (s *Server) adminBannerDelete(c *Ctx) (any, error) {
	return s.adminWrite(c, "删除轮播", c.P["id"], func(tx *db.Tx) error {
		_, err := tx.ExecContext(c.Context(), "DELETE FROM banners WHERE id=?", adminKey(c.P["id"]))
		return err
	})
}
func (s *Server) adminSettingsSave(c *Ctx) (any, error) {
	s.liveCaches.mu.Lock()
	defer s.liveCaches.mu.Unlock()
	s.sources.images.gate.Lock()
	defer s.sources.images.gate.Unlock()
	if c.P["reset"] == "true" {
		imageCfg, err := s.imageCacheSettings(c.Context())
		if err != nil {
			return nil, err
		}
		imageDir := s.imageCacheDirectory(imageCfg)
		s.cancelLiveCache("")
		var caches []liveCache
		err = s.WriteTx(c, func(tx *db.Tx) error {
			var err error
			caches, err = liveCacheList(c.Context(), tx)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(c.Context(), "DELETE FROM live_caches"); err != nil {
				return err
			}
			if err := seed.ResetTx(c.Context(), tx); err != nil {
				return err
			}
			// Administrator accounts survive the reset, so the actor stays valid.
			return s.LogTx(c.Context(), tx, ActorID(c), "重置站点数据", "")
		})
		if err != nil {
			return nil, err
		}
		s.applyServerSettings(nil)
		if _, err = s.clearImageCache(imageDir); err != nil {
			return nil, err
		}
		for _, x := range caches {
			if err = liveCacheRemove(&x); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if err := s.validateLiveCacheSettings(c); err != nil {
		return nil, err
	}
	if err := validateReplySettings(c); err != nil {
		return nil, err
	}
	for _, setting := range []struct {
		key, message string
		max          float64
	}{
		{"dataSourceSyncRate", "每秒请求数应为 1–50 的整数", 50},
		{"dataSourceSyncConcurrency", "并发请求数应为 1–8 的整数", 8},
	} {
		if raw, ok := c.P[setting.key]; ok {
			n := RawNumber(raw)
			if math.IsNaN(n) || n < 1 || n > setting.max || math.Trunc(n) != n {
				return nil, Fail(-400, setting.message)
			}
		}
	}
	if err := s.validateImageCacheSettings(c); err != nil {
		return nil, err
	}
	if err := serverInput(c.P); err != nil {
		return nil, err
	}
	services, err := serviceInput(c.P)
	if err != nil {
		return nil, err
	}
	for k, v := range services {
		c.P[k] = v
	}
	settings, err := s.Store.Settings(c.Context())
	if err != nil {
		return nil, err
	}
	secret, err := s.dandanplayInput(c, settings)
	if err != nil {
		return nil, err
	}
	result, err := s.adminWrite(c, "修改站点设置", "", func(tx *db.Tx) error {
		if secret != serviceString(settings[dandanplaySecretKey]) {
			if err := store.SetSetting(c.Context(), tx, dandanplaySecretKey, secret); err != nil {
				return err
			}
		}
		if v, ok := c.P["officialMid"]; ok {
			var n int
			if err := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM users WHERE mid=?", ParseGoID(v)).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return Fail(-400, "官方账号不存在，请先在站点设置中配置")
			}
		}
		servicesChanged := false
		for k, v := range services {
			if serviceString(settings[k]) != v {
				servicesChanged = true
			}
		}
		for k, v := range c.P {
			cur, ok := settings[k]
			if !ok || (strings.HasPrefix(k, "serverEmby") || strings.HasPrefix(k, "_demo_")) || k == dandanplaySecretKey || k == liveChatSecretKey || k == "csrf" || k == "biliCSRF" || k == "serviceMtime" {
				continue
			}
			var value any = v
			switch cur.(type) {
			case bool:
				value = v == "true"
			case float64:
				n := RawNumber(v)
				value = n
				if math.IsNaN(n) || math.IsInf(n, 0) {
					value = nil
				}
			}
			if err := store.SetSetting(c.Context(), tx, k, value); err != nil {
				return err
			}
			settings[k] = value
		}
		if servicesChanged {
			return store.SetSetting(c.Context(), tx, "serviceMtime", time.Now().Unix())
		}
		return nil
	})
	if err == nil {
		s.applyServerSettings(settings)
		cfg, e := s.imageCacheSettings(c.Context())
		if e != nil {
			return nil, e
		}
		s.imageCacheDirectory(cfg)
	}
	return result, err
}
