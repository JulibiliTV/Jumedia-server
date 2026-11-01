package api

import (
	"database/sql"
	"errors"
	"regexp"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func (s *Server) accountLogout(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	// Match Mock: access_key can authenticate, but logout only removes the
	// session identified by the browser's SESSDATA cookie.
	if _, err = s.Store.DB.ExecContext(c.Context(), "DELETE FROM sessions WHERE mid=? AND token=?", u.MID, CookieValue(c.R, "SESSDATA")); err != nil {
		return nil, err
	}
	c.ClearCookies()
	return Root(map[string]any{"code": 0, "status": true, "ts": time.Now().Unix(), "data": map[string]any{"redirectUrl": "/"}}), nil
}

func (s *Server) accountLogin(c *Ctx) (any, error) {
	// Preserve Mock's generic error for a missing username.trim() operand.
	if _, ok := c.P["username"]; !ok {
		return nil, errors.New("missing login username")
	}
	u, err := s.Store.UserByUsername(c.Context(), Trim(c.P["username"]))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-629, "账号或密码错误")
	}
	if err != nil {
		return nil, err
	}
	if !accountCheckPassword(u.Password, c.P["password"]) {
		return nil, Fail(-629, "账号或密码错误")
	}
	if u.Status != 0 {
		return nil, Fail(-403, "账号已被封禁，请联系管理员")
	}
	var session *store.Session
	err = s.WriteTx(c, func(tx *db.Tx) error {
		var e error
		session, e = accountNewSession(c.Context(), tx, u.MID)
		if e != nil {
			return e
		}
		return s.LogTx(c.Context(), tx, u.MID, "登录", u.Uname)
	})
	if err != nil {
		return nil, err
	}
	c.SetCookies(session)
	return map[string]any{"mid": u.MID, "status": 0, "message": "", "url": "/", "refresh_token": session.Refresh}, nil
}

var accountUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{4,20}$`)

func (s *Server) accountRegister(c *Ctx) (any, error) {
	allowed, err := s.Store.Setting(c.Context(), "allowRegister")
	if err != nil {
		return nil, err
	}
	if allowed != true {
		return nil, Fail(-403, "当前暂停开放注册")
	}
	username, uname := Trim(c.P["username"]), Trim(c.P["uname"])
	if !accountUsernamePattern.MatchString(username) {
		return nil, Fail(-400, "用户名需为 4–20 位字母、数字或下划线")
	}
	if UTF16Length(c.P["password"]) < 6 {
		return nil, Fail(-400, "密码至少 6 位")
	}
	if uname == "" || UTF16Length(uname) > 16 {
		return nil, Fail(-400, "昵称需为 1–16 个字符")
	}
	hash, err := accountHashPassword(c.P["password"])
	if err != nil {
		return nil, err
	}
	var session *store.Session
	err = s.WriteTx(c, func(tx *db.Tx) error {
		var n int
		if e := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM users WHERE username=?", username).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return Fail(-400, "用户名已被占用")
		}
		if e := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM users WHERE uname=?", uname).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return Fail(-400, "昵称已被占用")
		}
		var mid int64
		if e := tx.QueryRowContext(c.Context(), "SELECT COALESCE(MAX(mid),0)+1 FROM users").Scan(&mid); e != nil {
			return e
		}
		now := time.Now().Unix()
		u := &store.User{MID: mid, Username: username, Password: hash, Uname: uname, Face: seed.Avatar(mid), Sex: "保密", Email: c.P["email"], Level: 1, Coins: 10, Role: "user", Ctime: now, TopPhoto: ""}
		if e := store.InsertUser(c.Context(), tx, u); e != nil {
			return e
		}
		folderID, e := store.NextID(c.Context(), tx)
		if e != nil {
			return e
		}
		if e = store.InsertFavFolder(c.Context(), tx, &store.FavFolder{ID: folderID, MID: mid, Title: "默认收藏夹", Ctime: now}); e != nil {
			return e
		}
		session, e = accountNewSession(c.Context(), tx, mid)
		if e != nil {
			return e
		}
		return s.LogTx(c.Context(), tx, mid, "注册", uname)
	})
	if err != nil {
		return nil, err
	}
	c.SetCookies(session)
	return map[string]any{"mid": session.MID}, nil
}

func (s *Server) accountPassword(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	if !accountCheckPassword(u.Password, c.P["old_password"]) {
		return nil, Fail(-400, "原密码错误")
	}
	if UTF16Length(c.P["new_password"]) < 6 {
		return nil, Fail(-400, "新密码至少 6 位")
	}
	hash, err := accountHashPassword(c.P["new_password"])
	if err != nil {
		return nil, err
	}
	result, err := s.Store.DB.ExecContext(c.Context(), "UPDATE users SET password=? WHERE mid=? AND password=?", hash, u.MID, u.Password)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return nil, Fail(-400, "原密码错误")
	}
	return nil, err
}

func (s *Server) accountProfile(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	uname := Trim(c.P["uname"])
	if uname == "" || UTF16Length(uname) > 16 {
		return nil, Fail(-400, "昵称需为 1–16 个字符")
	}
	sex := c.P["sex"]
	if sex == "" {
		sex = "保密"
	}
	err = s.WriteTx(c, func(tx *db.Tx) error {
		var n int
		if e := tx.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM users WHERE uname=? AND mid<>?", uname, u.MID).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return Fail(-400, "昵称已被占用")
		}
		_, e := tx.ExecContext(c.Context(), "UPDATE users SET uname=?,sign=?,sex=?,birthday=?,face=CASE WHEN ?='' THEN face ELSE ? END WHERE mid=?", uname, UTF16Prefix(c.P["sign"], 70), sex, c.P["birthday"], c.P["face"], c.P["face"], u.MID)
		return e
	})
	return nil, err
}
