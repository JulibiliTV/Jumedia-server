package api

import (
	"math"
	"regexp"
	"strings"
	"time"

	"jumedia-server/internal/db"
)

var moderationAd = regexp.MustCompile(`(?i)https?://|www\.|[0-9]{7,}`)

// Number() coercion for new v13 fields, including non-finite values before
// the route applies its own filtering/defaults.
func publicationNumber(raw string) float64 {
	switch Trim(raw) {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	return numberValue(raw, numberSyntax{wideRadix: true})
}

func flagOf(words, text string) string {
	lower := strings.ToLower(text)
	for _, word := range strings.Split(words, "\n") {
		word = Trim(word)
		if word != "" && strings.Contains(lower, strings.ToLower(word)) {
			return "敏感词「" + word + "」"
		}
	}
	if moderationAd.MatchString(text) {
		return "疑似广告"
	}
	return ""
}
func (s *Server) sensitiveWords(c *Ctx) (string, error) {
	words, err := s.Store.Setting(c.Context(), "sensitiveWords")
	if err != nil {
		return "", err
	}
	return adminJSONText(words, "", true), nil
}
func (s *Server) moderationStat(c *Ctx) (map[string]int64, error) {
	now := publicationNow(c.Context())
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	out := map[string]int64{}
	for key, query := range map[string]string{"reply_today": "SELECT COUNT(*) FROM replies WHERE ctime>=?", "danmaku_today": "SELECT COUNT(*) FROM danmaku WHERE weight=10 AND ctime>=?"} {
		var n int64
		if err := s.Store.DB.QueryRowContext(c.Context(), query, start).Scan(&n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, nil
}
func (s *Server) adminRegionSort(c *Ctx) (any, error) {
	ids := []any{}
	for _, raw := range strings.Split(c.P["tids"], ",") {
		n := publicationNumber(raw)
		if n != 0 && !math.IsNaN(n) {
			ids = append(ids, DBID(n))
		}
	}
	return nil, s.WriteTx(c, func(tx *db.Tx) error {
		invalid := func() error { return Fail(-400, "分区排序参数无效") }
		if len(ids) == 0 {
			return invalid()
		}
		var parent int64
		names := []string{}
		seen := map[int64]bool{}
		tids := []int64{}
		for i, id := range ids {
			var tid, p int64
			var name string
			err := tx.QueryRowContext(c.Context(), "SELECT tid,COALESCE(parent,0),name FROM regions WHERE tid=?", id).Scan(&tid, &p, &name)
			if adminMissing(err) {
				return invalid()
			}
			if err != nil {
				return err
			}
			if seen[tid] || (i > 0 && p != parent) {
				return invalid()
			}
			seen[tid] = true
			parent = p
			names = append(names, name)
			tids = append(tids, tid)
		}
		for i, id := range tids {
			if _, err := tx.ExecContext(c.Context(), "UPDATE regions SET sort=? WHERE tid=?", i, id); err != nil {
				return err
			}
		}
		return s.LogTx(c.Context(), tx, ActorID(c), "调整分区排序", strings.Join(names, "、"))
	})
}
