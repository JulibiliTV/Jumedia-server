package api

import (
	"database/sql"
	"errors"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func (s *Server) accountAuthCode(c *Ctx) (any, error) {
	code, err := accountRandom(32)
	if err != nil {
		return nil, err
	}
	if err = store.InsertQRCode(c.Context(), s.Store.DB, &store.QRCode{Code: code, Status: "pending", Ctime: time.Now().Unix()}); err != nil {
		return nil, err
	}
	return map[string]any{"url": s.Origin(c.R) + "/tv-login?auth_code=" + code, "auth_code": code}, nil
}

func (s *Server) accountPoll(c *Ctx) (any, error) {
	var session *store.Session
	err := s.WriteTx(c, func(tx *db.Tx) error {
		qr, e := accountQRCode(c.Context(), tx, c.P["auth_code"], time.Now().Unix())
		if e != nil {
			return e
		}
		switch qr.Status {
		case "pending":
			return Fail(86039, "二维码尚未确认")
		case "scanned":
			return Fail(86090, "二维码已扫码未确认")
		}
		session, e = accountNewSession(c.Context(), tx, qr.MID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(c.Context(), "DELETE FROM qrcodes WHERE code=?", qr.Code)
		return e
	})
	if err != nil {
		return nil, err
	}
	return s.accountTokenPayload(c, session, true), nil
}

func (s *Server) accountScan(c *Ctx) (any, error) {
	if _, err := c.Login(); err != nil {
		return nil, err
	}
	err := s.WriteTx(c, func(tx *db.Tx) error {
		qr, e := accountQRCode(c.Context(), tx, c.P["auth_code"], time.Now().Unix())
		if e != nil {
			return e
		}
		if qr.Status != "pending" {
			return nil
		}
		_, e = tx.ExecContext(c.Context(), "UPDATE qrcodes SET status=? WHERE code=?", "scanned", qr.Code)
		return e
	})
	return nil, err
}

func (s *Server) accountConfirm(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	err = s.WriteTx(c, func(tx *db.Tx) error {
		qr, e := accountQRCode(c.Context(), tx, c.P["auth_code"], time.Now().Unix())
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(c.Context(), "UPDATE qrcodes SET status=?,mid=? WHERE code=?", "confirmed", u.MID, qr.Code); e != nil {
			return e
		}
		return s.LogTx(c.Context(), tx, u.MID, "TV 扫码登录", u.Uname)
	})
	return nil, err
}

func (s *Server) accountRefresh(c *Ctx) (any, error) {
	var session *store.Session
	err := s.WriteTx(c, func(tx *db.Tx) error {
		var mid int64
		e := tx.QueryRowContext(c.Context(), "SELECT mid FROM sessions WHERE refresh=?", c.P["refresh_token"]).Scan(&mid)
		if errors.Is(e, sql.ErrNoRows) {
			return Fail(-101, "刷新令牌无效")
		}
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(c.Context(), "DELETE FROM sessions WHERE refresh=?", c.P["refresh_token"]); e != nil {
			return e
		}
		session, e = accountNewSession(c.Context(), tx, mid)
		return e
	})
	if err != nil {
		return nil, err
	}
	return s.accountTokenPayload(c, session, false), nil
}
