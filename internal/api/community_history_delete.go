package api

import (
	"math"
	"regexp"
)

var historyDeleteKey = regexp.MustCompile(`^(archive|pgc)_([0-9]+)$`)

func (s *Server) communityHistoryDelete(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	key := historyDeleteKey.FindStringSubmatch(c.P["kid"])
	if key == nil {
		return nil, Fail(-400, "请求错误")
	}
	// Out-of-range JS numbers cannot match a stored BIGINT, but remain valid keys.
	id := DBID(Number(key[2], math.NaN()))
	if key[1] == "pgc" {
		_, err = s.Store.DB.ExecContext(c.Context(), "DELETE FROM history WHERE mid=? AND sid=?", u.MID, id)
	} else {
		_, err = s.Store.DB.ExecContext(c.Context(), "DELETE FROM history WHERE mid=? AND aid=? AND COALESCE(sid,0)=0", u.MID, id)
	}
	return nil, err
}
