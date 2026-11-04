package api

import (
	"net/url"
	"strings"

	"jumedia-server/internal/dandanplay"
)

const dandanplaySecretKey = "_dandanplay_secret"

// Only the encrypted secret is stored. Empty form values preserve it.
func (s *Server) dandanplayInput(c *Ctx, settings map[string]any) (string, error) {
	if raw, ok := c.P["dandanplayEnabled"]; ok && raw != "true" && raw != "false" {
		return "", Fail(-400, "弹幕同步开关无效")
	}
	if raw, ok := c.P["dandanplayApi"]; ok {
		raw = strings.TrimRight(strings.TrimSpace(raw), "/")
		if raw == "" {
			raw = dandanplay.DefaultAPI
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", Fail(-400, "弹弹play 接口地址无效")
		}
		c.P["dandanplayApi"] = raw
	}
	if v, ok := c.P["dandanplayAppId"]; ok {
		c.P["dandanplayAppId"] = strings.TrimSpace(v)
	}
	secret := serviceString(settings[dandanplaySecretKey])
	if c.P["dandanplaySecretClear"] == "true" {
		secret = ""
	}
	if raw := strings.TrimSpace(c.P["dandanplayAppSecret"]); raw != "" {
		if err := s.InitDataSources(c.Context()); err != nil {
			return "", err
		}
		var err error
		secret, err = s.encryptPassword(raw)
		if err != nil {
			return "", err
		}
	}
	enabled := Truthy(settings["dandanplayEnabled"])
	if v, ok := c.P["dandanplayEnabled"]; ok {
		enabled = v == "true"
	}
	appID := serviceString(settings["dandanplayAppId"])
	if v, ok := c.P["dandanplayAppId"]; ok {
		appID = v
	}
	if enabled && (appID == "") != (secret == "") {
		return "", Fail(-400, "App ID 和 App Secret 请同时填写，或同时留空以使用无需签名的服务")
	}
	return secret, nil
}
