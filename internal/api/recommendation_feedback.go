package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

func (s *Server) recommendationFeedback(c *Ctx) (any, error) {
	return s.changeRecommendationFeedback(c, false, false)
}

func (s *Server) cancelRecommendationFeedback(c *Ctx) (any, error) {
	return s.changeRecommendationFeedback(c, true, false)
}

func (s *Server) legacyRecommendationFeedback(c *Ctx) (any, error) {
	return s.changeRecommendationFeedback(c, false, true)
}

func (s *Server) changeRecommendationFeedback(c *Ctx, cancel, legacy bool) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	// Only the legacy access-token entry point permits a GET mutation.
	accessAuth := legacy && c.Session != nil && c.P["access_key"] != "" && c.P["access_key"] == c.Session.Access
	if !accessAuth {
		csrf := c.P["csrf"]
		if csrf == "" {
			csrf = c.P["biliCSRF"]
		}
		if c.Session == nil || csrf == "" || csrf != c.Session.CSRF {
			return nil, Fail(-111, "csrf 校验失败")
		}
	}
	if c.R.Method != http.MethodPost && !(accessAuth && c.R.Method == http.MethodGet) {
		return nil, Fail(-405, "请求方法不支持")
	}
	// Promotion acknowledgements retain their existing contract; they do not
	// create video or author preferences because Jumedia has no promoted cards.
	if !legacy && c.P["goto"] == "ad" {
		return nil, nil
	}
	if c.P["goto"] != "av" {
		return nil, Fail(-400, "推荐目标类型不支持")
	}
	reason := 0
	switch c.P["reason_id"] {
	case "1":
		reason = 1
	case "4":
		reason = 4
	default:
		return nil, Fail(-400, "推荐反馈原因不支持")
	}
	aid, err := strconv.ParseInt(c.P["id"], 10, 64)
	if err != nil || aid <= 0 {
		return nil, Fail(-400, "视频 ID 无效")
	}
	a, err := s.Store.ArchiveByID(c.Context(), aid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "视频不存在")
	}
	if err != nil {
		return nil, err
	}
	if raw := c.P["mid"]; raw != "" {
		owner, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || owner < 0 || (owner != 0 && owner != a.MID) {
			return nil, Fail(-400, "作者与视频不匹配")
		}
	}
	target := aid
	if reason == 4 {
		target = a.MID
	}
	return nil, s.Store.SetRecommendationFeedback(c.Context(), u.MID, reason, target, cancel)
}
