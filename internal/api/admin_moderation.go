package api

import (
	"database/sql"
	"errors"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func (s *Server) adminUserUpdate(c *Ctx) (any, error) {
	// Compare numeric IDs before converting, so fractional IDs cannot target a user.
	users, err := s.Store.ListUser(c.Context(), "WHERE mid=?", adminKey(c.P["mid"]))
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, Fail(-404, "用户不存在")
	}
	u := users[0]
	role, status := u.Role, u.Status
	if c.P["role"] != "" {
		role = "user"
		if c.P["role"] == "admin" {
			role = "admin"
		}
	}
	if v, ok := c.P["status"]; ok {
		status = store.BoolInt(NumberUnicodeGo(v) != 0)
	}
	// T9 requires self-protection for every spelling that normalizes to a ban or demotion.
	if u.MID == c.Me.MID && (status != 0 || role != "admin") {
		return nil, Fail(-400, "不能修改自己的管理权限或封禁自己")
	}
	coins := u.Coins
	if v, ok := c.P["coins"]; ok {
		coins = NumberUnicodeGo(v)
	}
	action := "更新用户"
	if status != 0 {
		action = "封禁用户"
	}
	return s.adminWrite(c, action, u.Uname, func(tx *db.Tx) error {
		if _, err := tx.ExecContext(c.Context(), "UPDATE users SET role=?,status=?,coins=? WHERE mid=?", role, status, coins, u.MID); err != nil {
			return err
		}
		if status != 0 {
			_, err := tx.ExecContext(c.Context(), "DELETE FROM sessions WHERE mid=?", u.MID)
			return err
		}
		return nil
	})
}
func (s *Server) adminArchiveAudit(c *Ctx) (any, error) {
	archives, err := s.Store.ListArchive(c.Context(), "WHERE aid=?", adminKey(c.P["aid"]))
	if err != nil {
		return nil, err
	}
	if len(archives) == 0 {
		return nil, Fail(-404, "稿件不存在")
	}
	a := archives[0]
	state := NumberUnicodeGo(c.P["state"])
	reason := ""
	if state == -2 {
		reason = adminDefault(c.P["reason"], "内容不符合社区规范")
	}
	action := "审核通过"
	if state == -2 {
		action = "退回稿件"
	}

	return s.adminWrite(c, action, a.Title, func(tx *db.Tx) error {
		var dtime int64
		if err := tx.QueryRowContext(c.Context(), "SELECT dtime,pubdate FROM archives WHERE aid=?", a.AID).Scan(&dtime, &a.Pubdate); err != nil {
			return err
		}
		if state == 0 {
			a.Pubdate = publicationNow(c.Context()).Unix()
			if dtime > a.Pubdate {
				state = -40
				a.Pubdate = dtime
			}
		}

		_, err := tx.ExecContext(c.Context(), "UPDATE archives SET state=?,reason=?,pubdate=? WHERE aid=?", state, reason, a.Pubdate, a.AID)
		return err
	})
}
func (s *Server) adminArchiveDelete(c *Ctx) (any, error) {
	archives, err := s.Store.ListArchive(c.Context(), "WHERE aid=?", adminKey(c.P["aid"]))
	if err != nil {
		return nil, err
	}
	if len(archives) == 0 {
		return nil, nil
	}
	a := archives[0]
	return s.adminWrite(c, "删除稿件", a.Title, func(tx *db.Tx) error {
		_, err := tx.ExecContext(c.Context(), "UPDATE archives SET state=? WHERE aid=?", -100, a.AID)
		return err
	})
}
func (s *Server) adminRoomBan(c *Ctx) (any, error) {
	s.liveCaches.mu.Lock()
	defer s.liveCaches.mu.Unlock()
	rooms, err := s.Store.ListLiveRoom(c.Context(), "WHERE room_id=?", adminKey(c.P["room_id"]))
	if err != nil {
		return nil, err
	}
	if len(rooms) == 0 {
		return nil, Fail(19002003, "房间不存在或已被封禁")
	}
	r := rooms[0]
	banned := BoolParam(c.P["banned"])
	action := "解封直播间"
	if banned {
		r.LiveStatus = 0
		action = "封禁直播间"
	}
	result, err := s.adminWrite(c, action, r.Title, func(tx *db.Tx) error {
		_, err := tx.ExecContext(c.Context(), "UPDATE rooms SET banned=?,live_status=? WHERE room_id=?", store.BoolInt(banned), r.LiveStatus, r.RoomID)
		return err
	})
	if err == nil && banned {
		s.liveChat.preparing(r.RoomID)
	}
	return result, err
}
func (s *Server) adminReplyDelete(c *Ctx) (any, error) {
	replies, err := s.Store.ListReply(c.Context(), "WHERE rpid=?", adminKey(c.P["rpid"]))
	if err != nil {
		return nil, err
	}
	if len(replies) == 0 {
		return nil, nil
	}
	r := replies[0]
	target := UTF16PrefixNormalized(r.Message, 20)
	return s.adminWrite(c, "删除评论", target, func(tx *db.Tx) error {
		return communityDeleteReplies(c, tx, r.RPID)
	})
}
func (s *Server) adminDanmakuDelete(c *Ctx) (any, error) {
	return s.adminWrite(c, "删除弹幕", c.P["id"], func(tx *db.Tx) error {
		_, err := tx.ExecContext(c.Context(), "DELETE FROM danmaku WHERE id=?", adminKey(c.P["id"]))
		return err
	})
}
func (s *Server) adminSeasonDelete(c *Ctx) (any, error) {
	seasons, err := s.Store.ListSeason(c.Context(), "WHERE season_id=?", adminKey(c.P["season_id"]))
	if err != nil {
		return nil, err
	}
	if len(seasons) == 0 {
		return nil, nil
	}
	season := seasons[0]
	return s.adminWrite(c, "删除番剧", season.Title, func(tx *db.Tx) error {
		// Mock keeps banner links even after a season is deleted.
		if _, err := tx.ExecContext(c.Context(), "UPDATE banners SET season_id=NULL WHERE season_id=?", season.SeasonID); err != nil {
			return err
		}
		// Episodes and their dependent records follow the T1 schema's FK actions.
		_, err := tx.ExecContext(c.Context(), "DELETE FROM seasons WHERE season_id=?", season.SeasonID)
		return err
	})
}

func adminMissing(err error) bool { return errors.Is(err, sql.ErrNoRows) }
