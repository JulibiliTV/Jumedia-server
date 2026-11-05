package api

import (
	"net/http"

	"jumedia-server/internal/db"
)

func (s *Server) adminUserPassword(c *Ctx) (any, error) {
	if c.R.Method != http.MethodPost {
		return nil, Fail(-405, "请使用 POST 请求")
	}
	users, err := s.Store.ListUser(c.Context(), "WHERE mid=?", adminKey(c.P["mid"]))
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, Fail(-404, "用户不存在")
	}
	if UTF16Length(c.P["new_password"]) < 6 {
		return nil, Fail(-400, "新密码至少 6 位")
	}
	hash, err := accountHashPassword(c.P["new_password"])
	if err != nil {
		return nil, err
	}
	u := users[0]
	result, err := s.adminWrite(c, "修改用户密码", u.Uname, func(tx *db.Tx) error {
		if _, err := tx.ExecContext(c.Context(), "UPDATE users SET password=? WHERE mid=?", hash, u.MID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(c.Context(), "DELETE FROM sessions WHERE mid=?", u.MID); err != nil {
			return err
		}
		_, err := tx.ExecContext(c.Context(), "DELETE FROM qrcodes WHERE mid=?", u.MID)
		return err
	})
	if err == nil && u.MID == c.Me.MID {
		c.ClearCookies()
	}
	return result, err
}
