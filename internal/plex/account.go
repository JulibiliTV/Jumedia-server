package plex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func decodeAccount(res *http.Response, out any) error {
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return ErrAuth
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(b) > 4<<20 || json.Unmarshal(b, out) != nil {
		return ErrResponse
	}
	return nil
}

// Account passwords are sent only to the fixed HTTPS account endpoint.
func (c *Client) account(ctx context.Context, path, token string, form url.Values, out any) error {
	method := "GET"
	body := ""
	if form != nil {
		method = "POST"
		body = form.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://plex.tv/api/v2/"+path, strings.NewReader(body))
	if err != nil {
		return ErrResponse
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Client-Identifier", "jumedia-server")
	req.Header.Set("X-Plex-Product", "Jumedia")
	req.Header.Set("X-Plex-Version", "0.1.0")
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := c.accountHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrAuth
	}
	return decodeAccount(res, out)
}

func (c *Client) authorize(ctx context.Context, rejected string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && (rejected == "" || rejected != c.token) {
		return c.token, nil
	}
	c.token = ""
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Check the entered server through its restricted transport before account sign-in.
	res, err := c.requestToken(ctx, "GET", "/identity", nil, nil, "")
	if err != nil {
		return "", err
	}
	var identity struct {
		Container struct {
			ID string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if err = decodeAccount(res, &identity); err != nil {
		return "", err
	}
	if identity.Container.ID == "" {
		return "", ErrResponse
	}
	var user struct {
		Token string `json:"authToken"`
	}
	if err = c.account(ctx, "users/signin", "", url.Values{"login": {c.username}, "password": {c.password}, "rememberMe": {"true"}}, &user); err != nil {
		return "", err
	}
	if user.Token == "" || strings.ContainsAny(user.Token, " \r\n\t") {
		return "", ErrAuth
	}
	var resources []struct {
		ID       string `json:"clientIdentifier"`
		Provides string `json:"provides"`
		Token    string `json:"accessToken"`
	}
	if err = c.account(ctx, "resources?includeHttps=1&includeRelay=1", user.Token, nil, &resources); err != nil {
		return "", err
	}
	for _, r := range resources {
		if r.ID != identity.Container.ID {
			continue
		}
		for _, kind := range strings.Split(r.Provides, ",") {
			if kind == "server" && r.Token != "" && !strings.ContainsAny(r.Token, " \r\n\t") {
				c.token = r.Token
				return c.token, nil
			}
		}
	}
	return "", ErrAuth
}
