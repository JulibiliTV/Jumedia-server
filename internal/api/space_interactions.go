package api

import (
	"database/sql"
	"math"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func (s *Server) spaceHasLike(c *Ctx) (any, error) {
	if c.Me == nil {
		return 0, nil
	}
	return Count(c, s.Store.DB, "SELECT COUNT(*) FROM likes WHERE mid=? AND aid=?", c.Me.MID, ParseWideID(c.P["aid"]))
}
func spaceSetLike(c *Ctx, tx *db.Tx, mid, aid int64, add bool) error {
	var query string
	if add {
		query = "INSERT INTO likes(mid,aid) VALUES (?,?) ON CONFLICT(mid,aid) DO NOTHING"
	} else {
		query = "DELETE FROM likes WHERE mid=? AND aid=?"
	}
	result, err := tx.ExecContext(c.Context(), query, mid, aid)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if !add {
		n = -n
	}
	_, err = tx.ExecContext(c.Context(), "UPDATE archives SET stat_like=stat_like+? WHERE aid=?", n, aid)
	return err
}
func (s *Server) spaceLike(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	a, err := s.spaceArchive(c, false)
	if err != nil {
		return nil, err
	}
	if c.P["like"] != "1" && c.P["like"] != "2" {
		return nil, nil
	}
	return nil, s.WriteTx(c, func(tx *db.Tx) error { return spaceSetLike(c, tx, u.MID, a.AID, c.P["like"] == "1") })
}
func (s *Server) spaceDislike(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	aid := ParseWideID(c.P["aid"])
	return nil, s.WriteTx(c, func(tx *db.Tx) error {
		if err := RequireReference(c, tx, "SELECT COUNT(*) FROM archives WHERE aid=?", aid, -404, "啥都木有"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(c.Context(), "DELETE FROM dislikes WHERE mid=? AND aid=?", u.MID, aid); err != nil {
			return err
		}
		if c.P["dislike"] != "0" {
			return nil
		}
		_, err := tx.ExecContext(c.Context(), "INSERT INTO dislikes(mid,aid) SELECT ?,aid FROM archives WHERE aid=?", u.MID, aid)
		return err
	})
}
func (s *Server) spaceCoin(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	a, err := s.spaceArchive(c, false)
	if err != nil {
		return nil, err
	}
	n := NumberWide(c.P["multiply"], 1)
	if a.MID == u.MID {
		return nil, Fail(34002, "不能给自己投币哦")
	}
	err = s.WriteTx(c, func(tx *db.Tx) error {
		var given, coins float64
		if err := tx.QueryRowContext(c.Context(), "SELECT COALESCE(SUM(n),0) FROM coins WHERE mid=? AND aid=?", u.MID, a.AID).Scan(&given); err != nil {
			return err
		}
		if given+n > 2 {
			return Fail(34005, "超过投币上限啦")
		}
		if err := tx.QueryRowContext(c.Context(), "SELECT coins FROM users WHERE mid=?", u.MID).Scan(&coins); err != nil {
			return err
		}
		if coins < n {
			return Fail(-104, "硬币不足")
		}
		// coins.n and archives.stat_coin are integer columns in T1. Reject values
		// which cannot be represented identically on SQLite and PostgreSQL.
		if n != math.Trunc(n) || n < -math.Exp2(31) || n >= math.Exp2(31) || given+n < -math.Exp2(31) {
			return Fail(-400, "投币数量需为整数")
		}
		if _, err := tx.ExecContext(c.Context(), "UPDATE users SET coins=coins-? WHERE mid=?", n, u.MID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(c.Context(), "UPDATE users SET coins=coins+? WHERE mid=?", n/10, a.MID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(c.Context(), "UPDATE archives SET stat_coin=stat_coin+? WHERE aid=?", int64(n), a.AID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(c.Context(), "INSERT INTO coins(mid,aid,n) VALUES (?,?,?) ON CONFLICT(mid,aid) DO UPDATE SET n=coins.n+excluded.n", u.MID, a.AID, int64(n)); err != nil {
			return err
		}
		if c.P["select_like"] == "1" {
			return spaceSetLike(c, tx, u.MID, a.AID, true)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"like": c.P["select_like"] == "1"}, nil
}
func (s *Server) spaceFavoured(c *Ctx) (any, error) {
	n := int64(0)
	var err error
	if c.Me != nil {
		n, err = Count(c, s.Store.DB, "SELECT COUNT(*) FROM fav_items WHERE aid=? AND folder_id IN (SELECT id FROM fav_folders WHERE mid=?)", ParseWideID(c.P["aid"]), c.Me.MID)
	}
	return map[string]any{"count": 1, "favoured": n > 0}, err
}
func (s *Server) spaceFavorite(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	a, err := s.spaceArchive(c, true)
	if err != nil {
		return nil, err
	}
	err = s.WriteTx(c, func(tx *db.Tx) error {
		for _, key := range []string{"add_media_ids", "del_media_ids"} {
			for _, raw := range strings.Split(c.P[key], ",") {
				if raw == "" {
					continue
				}
				id := ParseWideID(raw)
				n, err := Count(c, tx, "SELECT COUNT(*) FROM fav_folders WHERE id=? AND mid=?", id, u.MID)
				if err != nil {
					return err
				}
				if n == 0 {
					continue
				}
				var result sql.Result
				if key == "add_media_ids" {
					result, err = tx.ExecContext(c.Context(), "INSERT INTO fav_items(folder_id,aid,mtime) VALUES (?,?,?) ON CONFLICT(folder_id,aid) DO NOTHING", id, a.AID, time.Now().Unix())
				} else {
					result, err = tx.ExecContext(c.Context(), "DELETE FROM fav_items WHERE folder_id=? AND aid=?", id, a.AID)
				}
				if err != nil {
					return err
				}
				delta, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if key == "del_media_ids" {
					delta = -delta
				}
				if _, err = tx.ExecContext(c.Context(), "UPDATE archives SET stat_favorite=stat_favorite+? WHERE aid=?", delta, a.AID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"prompt": false}, nil
}
func (s *Server) spaceShare(c *Ctx) (any, error) {
	a, err := s.spaceArchive(c, false)
	if err != nil {
		return nil, err
	}
	var count int64
	err = s.Store.DB.QueryRowContext(c.Context(), "UPDATE archives SET stat_share=stat_share+1 WHERE aid=? RETURNING stat_share", a.AID).Scan(&count)
	return map[string]any{"share_url": s.Origin(c.R) + "/video/" + a.BVID, "count": count}, err
}
func (s *Server) spaceModify(c *Ctx) (any, error) {
	if c.P["act"] == "5" || c.P["act"] == "6" {
		return s.communityBlock(c)
	}
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	fid := ParseWideID(c.P["fid"])
	if fid == u.MID {
		return nil, Fail(22001, "不能关注自己")
	}
	n, err := Count(c, s.Store.DB, "SELECT COUNT(*) FROM users WHERE mid=?", fid)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, Fail(22003, "用户不存在")
	}
	if c.P["act"] == "1" {
		blocked, err := Count(c, s.Store.DB, "SELECT COUNT(*) FROM user_blocks WHERE mid=? AND fid=?", u.MID, fid)
		if err != nil {
			return nil, err
		}
		if blocked > 0 {
			return nil, Fail(-403, "请先解除拉黑")
		}
	}
	return nil, s.WriteTx(c, func(tx *db.Tx) error {
		if _, err := tx.ExecContext(c.Context(), "DELETE FROM relations WHERE mid=? AND fid=?", u.MID, fid); err != nil {
			return err
		}
		if c.P["act"] == "1" {
			return store.InsertRelation(c.Context(), tx, &store.Relation{MID: u.MID, FID: fid, Mtime: time.Now().Unix()})
		}
		return nil
	})
}
