package api

import (
	"fmt"
	"math"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
)

// pgcFollowList: type 1 = 追番 (番剧 / 国创), 2 = 追剧 (other season types);
// a missing or other type, and ps outside 1–30 (default 15), answer -400.
func (s *Server) pgcFollowList(c *Ctx) (any, error) {
	kind := c.P["type"]
	if kind != "1" && kind != "2" {
		return nil, Fail(-400, "请求错误")
	}
	mid, status := NumberUnicode(c.P["vmid"], 0), NumberUnicode(c.P["follow_status"], 0)
	pn, ps := NumberUnicode(c.P["pn"], 1), NumberUnicode(c.P["ps"], 15)
	if pn < 1 || ps < 1 || ps > 30 {
		return nil, Fail(-400, "请求错误")
	}
	statusID := DBID(status)
	if status < math.MinInt32 || status > math.MaxInt32 {
		statusID = nil
	}
	rows, err := s.Store.ListBangumiFollow(c.Context(), "WHERE mid=? AND (?=0 OR status=?) ORDER BY mtime DESC, season_id", DBID(mid), statusID, statusID)
	if err != nil {
		return nil, err
	}
	filtered := rows[:0]
	for _, f := range rows {
		season, err := s.Store.SeasonByID(c.Context(), f.SeasonID)
		if err != nil {
			return nil, err
		}
		if anime := season.SeasonType == 1 || season.SeasonType == 4; anime == (kind == "1") {
			filtered = append(filtered, f)
		}
	}
	rows = filtered
	cards := []render.M{}
	for _, f := range Page(rows, pn, ps) {
		season, err := s.Store.SeasonByID(c.Context(), f.SeasonID)
		if err != nil {
			return nil, err
		}
		last, err := s.pgcLastHistory(c, f.MID, f.SeasonID)
		if err != nil {
			return nil, err
		}
		progress, renewal := "尚未观看", ""
		var lastEP render.M
		hasUpdate := false
		if last != nil {
			for i, ep := range season.Episodes {
				if ep.EpID == last.EpID {
					progress = fmt.Sprintf("看到第%s话", ep.Title)
					lastEP = render.M{"ep_id": ep.EpID, "index": ep.Title, "title": ep.LongTitle, "position": i + 1, "time": last.Progress, "view_at": last.ViewAt}
					hasUpdate = i < len(season.Episodes)-1 && season.Episodes[len(season.Episodes)-1].PubTime > last.ViewAt
					break
				}
			}
		}
		if !season.IsFinish {
			renewal = fmt.Sprintf("每周%s %s", pgcWeekday(season.Weekday), season.PubTime)
		}
		card := render.SeasonCard(*season, render.M{
			"follow_status": f.Status, "progress": progress, "renewal_time": renewal,
			"total_count": len(season.Episodes), "follow_mtime": f.Mtime,
			"last_ep": lastEP, "has_update": hasUpdate,
		})
		// Extend the existing new_ep map so its legacy fields remain unchanged.
		newEP := card["new_ep"].(render.M)
		newEP["title"], newEP["pub_time"] = "", int64(0)
		if len(season.Episodes) > 0 {
			latest := season.Episodes[len(season.Episodes)-1]
			newEP["title"], newEP["pub_time"] = latest.Title, latest.PubTime
		}
		cards = append(cards, card)
	}
	return render.M{"pn": pn, "ps": ps, "total": len(rows), "list": cards}, nil
}
func (s *Server) pgcUserStatus(c *Ctx) (any, error) {
	result := render.M{"follow": 0, "follow_status": 0, "pay": 0, "sponsor": 0, "vip": 0}
	if c.Me != nil {
		result["vip"] = c.Me.VIP
		follows, err := s.Store.ListBangumiFollow(c.Context(), "WHERE mid=? AND season_id=?", c.Me.MID, DBID(NumberUnicode(c.P["season_id"], 0)))
		if err != nil {
			return nil, err
		}
		if len(follows) > 0 {
			result["follow"], result["follow_status"] = 1, follows[0].Status
		}
	}
	return Result(result), nil
}
func (s *Server) pgcFollowAdd(c *Ctx) (any, error) {
	user, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	sid := DBID(NumberUnicode(c.P["season_id"], 0))
	err = s.WriteTx(c, func(tx *db.Tx) error {
		// The shared schema requires a real season. The browser mock permits a
		// dangling follow; reject it explicitly instead of exposing a SQL error.
		var exists int
		if err := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM seasons WHERE season_id=?", sid).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return Fail(-404, "番剧不存在")
		}
		result, err := tx.ExecContext(c.Context(), "INSERT INTO bangumi_follow(mid,season_id,status,mtime) VALUES (?,?,?,?) ON CONFLICT(mid,season_id) DO NOTHING", user.MID, sid, 1, time.Now().Unix())
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count > 0 {
			_, err = tx.ExecContext(c.Context(), "UPDATE seasons SET follow=follow+1 WHERE season_id=?", sid)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return Result(render.M{"toast": "追番成功", "relation": true, "status": 2}), nil
}
func (s *Server) pgcFollowDel(c *Ctx) (any, error) {
	user, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	if _, err = s.Store.DB.ExecContext(c.Context(), "DELETE FROM bangumi_follow WHERE mid=? AND season_id=?", user.MID, DBID(NumberUnicode(c.P["season_id"], 0))); err != nil {
		return nil, err
	}
	// The mock deliberately does not decrement the season's cumulative count.
	return Result(render.M{"toast": "已取消追番", "relation": false, "status": 0}), nil
}
func (s *Server) pgcFollowUpdate(c *Ctx) (any, error) {
	user, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	status := NumberUnicode(c.P["status"], 1)
	// Shared status storage is INTEGER, including PostgreSQL's int32 range.
	// Reject unrepresentable values before SQLite can persist an unreadable REAL.
	if math.Trunc(status) != status || status < math.MinInt32 || status > math.MaxInt32 {
		return nil, Fail(-400, "请求参数错误")
	}
	result, err := s.Store.DB.ExecContext(c.Context(), "UPDATE bangumi_follow SET status=?, mtime=? WHERE mid=? AND season_id=?", int64(status), time.Now().Unix(), user.MID, DBID(NumberUnicode(c.P["season_id"], 0)))
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, Fail(-404, "尚未追番")
	}
	return Result(render.M{"toast": "已更新"}), nil
}
