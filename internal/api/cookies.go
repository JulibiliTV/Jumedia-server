package api

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"jumedia-server/internal/store"
)

const SessionMaxAge = 86400 * 180

func CookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	v, err := url.PathUnescape(c.Value)
	if err != nil {
		return c.Value
	}
	return v
}
func SetCookies(w http.ResponseWriter, s *store.Session) { setCookies(w, s, false) }
func ClearCookies(w http.ResponseWriter)                 { setCookies(w, &store.Session{}, true) }
func setCookies(w http.ResponseWriter, s *store.Session, clear bool) {
	values := []struct{ name, value string }{{"SESSDATA", s.Token}, {"bili_jct", s.CSRF}, {"DedeUserID", strconv.FormatInt(s.MID, 10)}}
	for _, v := range values {
		c := &http.Cookie{Name: v.name, Value: v.value, Path: "/", MaxAge: SessionMaxAge, Expires: time.Now().Add(time.Duration(SessionMaxAge) * time.Second), HttpOnly: v.name == "SESSDATA", SameSite: http.SameSiteLaxMode}
		if clear {
			c.Value = ""
			c.MaxAge = -1
			c.Expires = time.Unix(1, 0)
		}
		http.SetCookie(w, c)
	}
}
func (c *Ctx) SetCookies(s *store.Session) { SetCookies(c.W, s) }
func (c *Ctx) ClearCookies()               { ClearCookies(c.W) }
