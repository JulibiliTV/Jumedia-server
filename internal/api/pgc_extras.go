package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

type episodePoint struct {
	From    int    `json:"from"`
	Content string `json:"content"`
}
type episodeExtra struct {
	OP     []int          `json:"op,omitempty"`
	Points []episodePoint `json:"points,omitempty"`
}

func episodeNumber(v any, present bool) float64 {
	if !present {
		return math.NaN()
	}
	switch v := v.(type) {
	case nil:
		return 0
	case float64:
		return v
	case string:
		return publicationNumber(v)
	case []any:
		return publicationNumber(episodeString(v))
	case bool:
		if v {
			return 1
		}
		return 0
	}
	return math.NaN()
}

// String() for JSON values, including array-to-primitive coercion used by
// Number([value]) and content strings in the Mock editor.
func episodeString(v any) string {
	switch value := v.(type) {
	case []any:
		parts := make([]string, len(value))
		for i, item := range value {
			if item != nil {
				parts[i] = episodeString(item)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	default:
		return adminJSONString(v)
	}
}

func episodeExtras(e map[string]any, duration int) (episodeExtra, error) {
	out := episodeExtra{}
	if op, ok := e["op"].([]any); ok && len(op) == 2 {
		start, end := episodeNumber(op[0], true), episodeNumber(op[1], true)
		if start >= 0 && end > start && end <= float64(duration) {
			out.OP = []int{int(math.Floor(start + 0.5)), int(math.Floor(end + 0.5))}
		}
	}
	if points, ok := e["points"].([]any); ok {
		for _, point := range points {
			if point == nil {
				return out, fmt.Errorf("null episode point")
			}
			p, ok := point.(map[string]any)
			if !ok {
				continue
			}
			v, present := p["from"]
			from := math.Floor(episodeNumber(v, present) + 0.5)
			content := ""
			if p["content"] != nil {
				content = UTF16PrefixNormalized(Trim(episodeString(p["content"])), 40)
			}
			if content != "" && from >= 0 && from < float64(duration) {
				out.Points = append(out.Points, episodePoint{int(from), content})
			}
		}
	}
	sort.SliceStable(out.Points, func(i, j int) bool { return out.Points[i].From < out.Points[j].From })
	return out, nil
}
func readEpisodeExtras(ctx context.Context, q db.Querier, id int64) (episodeExtra, error) {
	var op, points string
	out := episodeExtra{}
	err := q.QueryRowContext(ctx, "SELECT op,points FROM episodes WHERE ep_id=?", id).Scan(&op, &points)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(op), &out.OP); err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(points), &out.Points)
	return out, err
}
func saveEpisodeExtras(ctx context.Context, q db.Querier, id int64, e episodeExtra) error {
	op, err := json.Marshal(e.OP)
	if err != nil {
		return err
	}
	points, err := json.Marshal(e.Points)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE episodes SET op=?,points=? WHERE ep_id=?", string(op), string(points), id)
	return err
}
func (s *Server) episodePoints(c *Ctx, cid float64) ([]render.M, error) {
	out := []render.M{}
	episodes, err := s.Store.ListEpisode(c.Context(), "WHERE cid=?", DBID(cid))
	if err != nil {
		return nil, err
	}
	if len(episodes) == 0 {
		return out, nil
	}
	e := episodes[0]
	extra, err := readEpisodeExtras(c.Context(), s.Store.DB, e.EpID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(extra.Points, func(i, j int) bool { return extra.Points[i].From < extra.Points[j].From })
	for i, p := range extra.Points {
		to := e.Duration
		if i+1 < len(extra.Points) {
			to = extra.Points[i+1].From
		}
		out = append(out, render.M{"type": 2, "from": p.From, "to": to, "content": p.Content, "imgUrl": e.Cover})
	}
	return out, nil
}
func (s *Server) adminEpisode(c *Ctx, e store.Episode) (map[string]any, error) {
	out := adminObject(e)
	extra, err := readEpisodeExtras(c.Context(), s.Store.DB, e.EpID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if len(extra.OP) > 0 {
		out["op"] = extra.OP
	}
	if len(extra.Points) > 0 {
		out["points"] = extra.Points
	}
	return out, nil
}
