package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/dm"
	"jumedia-server/internal/render"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func (s *Server) communityDMSegment(c *Ctx) (any, error) {
	cid := ParseID(c.P["oid"])
	pending, err := s.requestDandanplay(c.Context(), cid)
	if err != nil {
		s.Logger.Warn("danmaku on-demand request failed", "error", err)
	}
	c.W.Header().Set("Cache-Control", "no-store")
	c.W.Header().Set("Access-Control-Expose-Headers", "X-Jumedia-Danmaku-Pending")
	c.W.Header().Set("X-Jumedia-Danmaku-Pending", strconv.FormatBool(pending))
	if e := s.communitySeedDanmaku(c, cid); e != nil {
		return nil, e
	}
	rows, e := s.Store.ListDanmaku(c.Context(), "WHERE cid=? ORDER BY id", cid)
	if e != nil {
		return nil, e
	}
	external, e := s.Store.DandanplayComments(c.Context(), cid)
	if e != nil {
		return nil, e
	}
	rows = append(rows, external...)
	return Bytes(dm.Segment(rows, cid, int(Int(c.P["segment_index"], 1))), "application/octet-stream"), nil
}
func (s *Server) communitySeedDanmaku(c *Ctx, cid int64) error {
	duration := 24
	e := s.Store.DB.QueryRowContext(c.Context(), "SELECT duration FROM archive_pages WHERE cid=?", cid).Scan(&duration)
	if errors.Is(e, sql.ErrNoRows) {
		e = s.Store.DB.QueryRowContext(c.Context(), "SELECT duration FROM episodes WHERE cid=?", cid).Scan(&duration)
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	return seed.SeedDanmaku(c.Context(), s.Store, cid, duration)
}
func (s *Server) communityDMView(c *Ctx) (any, error) {
	settings := map[string]string{}
	if c.Me != nil {
		rows, e := s.Store.ListDMConfig(c.Context(), "WHERE mid=?", c.Me.MID)
		if e != nil {
			return nil, e
		}
		if len(rows) > 0 {
			settings = rows[0].Values
		}
	}
	return Bytes(dm.WebView(settings), "application/octet-stream"), nil
}
func (s *Server) communityDMConfig(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	e = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		// Ensure a row exists, then lock it through a portable no-op UPDATE before
		// reading so concurrent partial updates merge rather than overwrite.
		if _, e := tx.ExecContext(c.Context(), "INSERT INTO dm_config (mid,values_json) VALUES (?,?) ON CONFLICT (mid) DO NOTHING", u.MID, "{}"); e != nil {
			return e
		}
		if _, e := tx.ExecContext(c.Context(), "UPDATE dm_config SET mid=mid WHERE mid=?", u.MID); e != nil {
			return e
		}
		var encoded string
		if e := tx.QueryRowContext(c.Context(), "SELECT values_json FROM dm_config WHERE mid=?", u.MID).Scan(&encoded); e != nil {
			return e
		}
		prev := map[string]string{}
		if e := json.Unmarshal([]byte(encoded), &prev); e != nil {
			return e
		}
		if prev == nil {
			prev = map[string]string{}
		}
		keys := []string{"dm_switch", "blockscroll", "blocktop", "blockbottom", "blockcolor", "opacity", "fontsize", "speedplus", "dmarea", "preventshade", "ai_switch", "ai_level", "dmask"}
		same := true
		for _, k := range keys {
			v, ok := c.P[k]
			old, exists := prev[k]
			if ok != exists || v != old {
				same = false
			}
		}
		if same {
			return Fail(23004, "设置未变更")
		}
		for _, k := range keys {
			if v, ok := c.P[k]; ok {
				prev[k] = v
			}
		}
		b, e := json.Marshal(prev)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(c.Context(), "UPDATE dm_config SET values_json=? WHERE mid=?", string(b), u.MID)
		return e
	})
	return nil, e
}
func (s *Server) communityDMFilter(c *Ctx) (any, error) {
	u, e := c.Login()
	if e != nil {
		return nil, e
	}
	rows, e := s.Store.ListDMFilter(c.Context(), "WHERE mid=?", u.MID)
	if e != nil {
		return nil, e
	}
	if len(rows) == 0 {
		return render.M{"rule": []string{}, "keyword": []string{}, "regex": []string{}}, nil
	}
	f := rows[0]
	return render.M{"rule": f.Rule, "keyword": f.Keyword, "regex": f.Regex}, nil
}
func (s *Server) communityDMPost(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	msg := Trim(c.P["msg"])
	if msg == "" || UTF16Length(msg) > 100 {
		return nil, Fail(36700, "弹幕内容需在 1–100 字之间")
	}
	d := store.Danmaku{CID: ParseID(c.P["oid"]), Progress: int(Int(c.P["progress"])), Mode: int(Int(c.P["mode"], 1)), Fontsize: int(Int(c.P["fontsize"], 25)), Color: int(Int(c.P["color"], 0xffffff)), MID: u.MID, Content: msg, Ctime: time.Now().Unix(), Weight: 10}
	e = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		var e error
		d.ID, e = store.NextID(c.Context(), tx)
		if e != nil {
			return e
		}
		if e = store.InsertDanmaku(c.Context(), tx, &d); e != nil {
			return e
		}
		_, e = tx.ExecContext(c.Context(), "UPDATE archives SET stat_danmaku=stat_danmaku+1 WHERE aid=?", ParseID(c.P["aid"]))
		return e
	})
	if e != nil {
		return nil, e
	}
	return render.M{"dmid": d.ID, "dmid_str": strconv.FormatInt(d.ID, 10), "action": ""}, nil
}
