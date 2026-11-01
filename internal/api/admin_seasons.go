package api

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func adminJSONNumber(v any, fallback float64) float64 {
	var n float64
	switch v := v.(type) {
	case nil:
		n = 0
	case bool:
		if v {
			n = 1
		}
	case float64:
		n = v
	case string:
		n = RawNumber(v)
	default:
		n = math.NaN()
	}
	if n == 0 || math.IsNaN(n) {
		return fallback
	}
	return n
}
func adminJSONString(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
func adminJSONText(v any, fallback string, nullish bool) string {
	if (nullish && v == nil) || (!nullish && !Truthy(v)) {
		return fallback
	}
	return adminJSONString(v)
}
func (s *Server) adminSeasonSave(c *Ctx) (any, error) {
	var input map[string]any
	raw, present := c.P["season"]
	if !present {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("season must be an object")
	}
	if !Truthy(input["title"]) {
		return nil, Fail(-400, "请填写标题")
	}
	title := adminJSONString(input["title"])
	rawEpisodes := []any{}
	if input["episodes"] != nil {
		var ok bool
		rawEpisodes, ok = input["episodes"].([]any)
		if !ok {
			return nil, fmt.Errorf("episodes must be an array")
		}
	}
	var seasonID int64
	action := "新建番剧"
	if Truthy(input["season_id"]) {
		action = "编辑番剧"
	}
	_, err := s.adminWrite(c, action, title, func(tx *db.Tx) error {
		// Match strict equality: a JSON string season_id does not identify an existing season.
		var existingID any
		if n, ok := input["season_id"].(float64); ok {
			existingID = DBID(n)
		}
		var mediaID int64
		err := tx.QueryRowContext(c.Context(), "SELECT season_id,media_id FROM seasons WHERE season_id=?", existingID).Scan(&seasonID, &mediaID)
		existing := err == nil
		if err != nil && !adminMissing(err) {
			return err
		}
		episodes := []store.Episode{}
		extras := []episodeExtra{}
		next := func(v any) (int64, error) {
			n := adminJSONNumber(v, 0)
			if n != 0 {
				return int64(n), nil
			}
			return store.NextID(c.Context(), tx)
		}
		for i, raw := range rawEpisodes {
			e, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("episode must be an object")
			}
			ep := store.Episode{Position: i, Title: adminJSONText(e["title"], strconv.Itoa(i+1), false), LongTitle: adminJSONText(e["long_title"], "", true), Cover: adminJSONText(e["cover"], adminJSONText(input["horizontal"], "", false), false), Duration: int(adminJSONNumber(e["duration"], 1440)), PubTime: int64(adminJSONNumber(e["pub_time"], float64(time.Now().Unix()))), Media: adminJSONText(e["media"], "", false), Badge: adminJSONText(e["badge"], "", true)}
			if ep.EpID, err = next(e["ep_id"]); err != nil {
				return err
			}
			if ep.AID, err = next(e["aid"]); err != nil {
				return err
			}
			if ep.CID, err = next(e["cid"]); err != nil {
				return err
			}
			extra, err := episodeExtras(e, ep.Duration)
			if err != nil {
				return err
			}
			extras = append(extras, extra)
			episodes = append(episodes, ep)
		}
		// Validate all three unique keys before replacing any existing episodes.
		// The admin transaction holds the shared lock, so concurrent saves cannot
		// pass this check together and then collide during insertion.
		seenEP, seenAID, seenCID := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
		for _, ep := range episodes {
			for _, key := range []struct {
				name, query string
				id          int64
				seen        map[int64]bool
			}{
				{"ep_id", "SELECT COUNT(*) FROM episodes WHERE ep_id=? AND season_id<>?", ep.EpID, seenEP},
				{"aid", "SELECT COUNT(*) FROM episodes WHERE aid=? AND season_id<>?", ep.AID, seenAID},
				{"cid", "SELECT COUNT(*) FROM episodes WHERE cid=? AND season_id<>?", ep.CID, seenCID},
			} {
				if key.seen[key.id] {
					return Fail(-400, "剧集 ID 重复（"+key.name+"）")
				}
				key.seen[key.id] = true
				n, err := Count(c, tx, key.query, key.id, seasonID)
				if err != nil {
					return err
				}
				if n > 0 {
					return Fail(-400, "剧集 ID 重复（"+key.name+"）")
				}
			}
		}

		if !existing {
			if seasonID, err = store.NextID(c.Context(), tx); err != nil {
				return err
			}
			if mediaID, err = store.NextID(c.Context(), tx); err != nil {
				return err
			}
		}
		arrays := func(key string) (string, error) {
			v := input[key]
			if v == nil {
				v = []string{}
			}
			raw, err := json.Marshal(v)
			return string(raw), err
		}
		areas, err := arrays("areas")
		if err != nil {
			return err
		}
		styles, err := arrays("styles")
		if err != nil {
			return err
		}
		cover := adminJSONText(input["cover"], "", false)
		horizontal := adminJSONText(input["horizontal"], cover, false)
		args := []any{title, adminJSONText(input["evaluate"], "", true), adminJSONNumber(input["season_type"], 1), areas, styles, adminJSONNumber(input["score"], 0), store.BoolInt(Truthy(input["is_finish"])), adminJSONNumber(input["weekday"], 1), adminJSONText(input["pub_time"], "20:00", false), cover, horizontal, adminJSONNumber(input["state"], 0)}
		if existing {
			_, err = tx.ExecContext(c.Context(), "UPDATE seasons SET title=?,evaluate=?,season_type=?,areas=?,styles=?,score=?,is_finish=?,weekday=?,pub_time=?,cover=?,horizontal=?,state=? WHERE season_id=?", append(args, seasonID)...)
		} else {
			_, err = tx.ExecContext(c.Context(), "INSERT INTO seasons (title,evaluate,season_type,areas,styles,score,is_finish,weekday,pub_time,cover,horizontal,state,season_id,media_id,views,follow,danmakus,ctime) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,0,?)", append(args, seasonID, mediaID, time.Now().Unix())...)
		}
		if err != nil {
			return err
		}
		// History has SET NULL FKs to episodes. Detach before replacement, then restore
		// references only for retained episodes, preserving watch progress on edits.
		type historyRef struct{ mid, aid, epid int64 }
		refs := []historyRef{}
		rows, err := tx.QueryContext(c.Context(), "SELECT mid,aid,epid FROM history WHERE epid IN (SELECT ep_id FROM episodes WHERE season_id=?)", seasonID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r historyRef
			if err = rows.Scan(&r.mid, &r.aid, &r.epid); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(c.Context(), "DELETE FROM episodes WHERE season_id=?", seasonID); err != nil {
			return err
		}
		retained := map[int64]bool{}
		for i, ep := range episodes {
			ep.SeasonID = seasonID
			if err = store.InsertEpisode(c.Context(), tx, &ep); err != nil {
				return err
			}
			if err = saveEpisodeExtras(c.Context(), tx, ep.EpID, extras[i]); err != nil {
				return err
			}
			retained[ep.EpID] = true
		}
		for _, r := range refs {
			if retained[r.epid] {
				if _, err = tx.ExecContext(c.Context(), "UPDATE history SET epid=? WHERE mid=? AND aid=?", r.epid, r.mid, r.aid); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"season_id": seasonID}, nil
}
