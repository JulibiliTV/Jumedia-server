package api

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) pgcSeason(c *Ctx) (any, error) {
	seasons, err := s.pgcSeasons(c)
	if err != nil {
		return nil, err
	}
	epid, sid := NumberUnicode(c.P["ep_id"], 0), NumberUnicode(c.P["season_id"], 0)
	for _, season := range seasons {
		if season.State != 0 {
			continue
		}
		match := float64(season.SeasonID) == sid
		if epid != 0 {
			match = false
			for _, ep := range season.Episodes {
				if float64(ep.EpID) == epid {
					match = true
					break
				}
			}
		}
		if match {
			result, err := s.pgcSeasonResult(c, season, seasons)
			if err != nil {
				return nil, err
			}
			return Result(result), nil
		}
	}
	return nil, Fail(-404, "番剧不存在")
}
func (s *Server) pgcSeasonResult(c *Ctx, season store.Season, seasons []store.Season) (render.M, error) {
	areas, episodes, related := []render.M{}, []render.M{}, []render.M{}
	for i, name := range season.Areas {
		areas = append(areas, render.M{"id": i + 1, "name": name})
	}
	for i, ep := range season.Episodes {
		title := fmt.Sprintf("第%s话 %s", ep.Title, ep.LongTitle)
		if season.SeasonType == 2 {
			title = "正片"
		}
		episodes = append(episodes, render.M{"id": ep.EpID, "ep_id": ep.EpID, "aid": ep.AID, "cid": ep.CID, "bvid": store.AV2BV(ep.AID), "title": ep.Title, "long_title": ep.LongTitle, "show_title": title, "cover": ep.Cover, "duration": ep.Duration * 1000, "pub_time": ep.PubTime, "badge": ep.Badge, "index": i + 1, "link": fmt.Sprintf("/bangumi/play/ep%d", ep.EpID)})
	}
	for _, other := range seasons {
		if other.SeasonType == season.SeasonType && other.SeasonID != season.SeasonID && len(related) < 6 {
			related = append(related, render.SeasonCard(other))
		}
	}
	desc := fmt.Sprintf("连载中, 每周%s %s更新", pgcWeekday(season.Weekday), season.PubTime)
	if season.IsFinish {
		desc = fmt.Sprintf("已完结, 全%d话", len(season.Episodes))
	}
	newEP := render.M{"desc": desc, "is_new": store.BoolInt(!season.IsFinish)}
	if len(season.Episodes) > 0 {
		last := season.Episodes[len(season.Episodes)-1]
		newEP["id"], newEP["title"] = last.EpID, last.Title
	}
	userStatus := render.M{"follow": 0, "follow_status": 0, "progress": render.M{}}
	if c.Me != nil {
		follows, err := s.Store.ListBangumiFollow(c.Context(), "WHERE mid=? AND season_id=?", c.Me.MID, season.SeasonID)
		if err != nil {
			return nil, err
		}
		if len(follows) > 0 {
			userStatus["follow"], userStatus["follow_status"] = 1, follows[0].Status
		}
		last, err := s.pgcLastHistory(c, c.Me.MID, season.SeasonID)
		if err != nil {
			return nil, err
		}
		if last != nil {
			progress := render.M{"last_ep_id": last.EpID, "last_time": last.Progress}
			for _, ep := range season.Episodes {
				if ep.EpID == last.EpID {
					progress["last_ep_index"] = ep.Title
					break
				}
			}
			userStatus["progress"] = progress
		}
	}
	owner, err := s.Render.Owner(c.Context(), 1)
	if err != nil {
		return nil, err
	}
	followers, err := s.Store.FollowerCount(c.Context(), 1)
	if err != nil {
		return nil, err
	}
	floor := func(divisor float64) float64 { return math.Floor(float64(season.Views) / divisor) }
	return render.M{
		"season_id": season.SeasonID, "media_id": season.MediaID, "season_title": season.Title, "title": season.Title, "cover": season.Cover, "evaluate": season.Evaluate, "type": season.SeasonType, "total": len(episodes),
		"areas": areas, "styles": season.Styles, "rating": render.M{"score": season.Score, "count": math.Floor(float64(season.Follow) / 30)}, "new_ep": newEP,
		"publish":  render.M{"is_finish": store.BoolInt(season.IsFinish), "pub_time": time.Unix(season.Ctime, 0).UTC().Format("2006-01-02"), "weekday": season.Weekday},
		"stat":     render.M{"views": season.Views, "favorites": season.Follow, "danmakus": season.Danmakus, "coins": floor(200), "likes": floor(40), "reply": floor(900), "share": floor(1200), "follow": season.Follow},
		"episodes": episodes, "section": []any{}, "seasons": related, "up_info": render.M{"mid": 1, "uname": "聚媒番剧", "avatar": owner["face"], "follower": render.CompactCount(followers)}, "user_status": userStatus,
	}, nil
}
func (s *Server) pgcLastHistory(c *Ctx, mid, sid int64) (*store.History, error) {
	rows, err := s.Store.ListHistory(c.Context(), "WHERE mid=? AND sid=? ORDER BY view_at DESC, aid DESC LIMIT 1", mid, sid)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}
func (s *Server) pgcPlayURL(c *Ctx) (any, error) {
	episodes, err := s.Store.ListEpisode(c.Context(), "WHERE ep_id=?", DBID(NumberUnicode(c.P["ep_id"], 0)))
	if err != nil {
		return nil, err
	}
	if len(episodes) == 0 {
		return nil, Fail(-404, "剧集不存在")
	}
	ep := episodes[0]
	// Like the mock, PGC playback does not gate on state or allowGuestPlay.
	if _, err = s.Store.SeasonByID(c.Context(), ep.SeasonID); errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "剧集不存在")
	} else if err != nil {
		return nil, err
	}
	media, err := discoverAbsoluteMedia(s.Origin(c.R), ep.Media)
	if err != nil {
		return nil, err
	}
	if _, err = s.Store.DB.ExecContext(c.Context(), "UPDATE seasons SET views=views+1 WHERE season_id=?", ep.SeasonID); err != nil {
		return nil, err
	}
	extra, err := readEpisodeExtras(c.Context(), s.Store.DB, ep.EpID)
	if err != nil {
		return nil, err
	}
	clips := []render.M{}
	if len(extra.OP) == 2 {
		clips = append(clips, render.M{"start": extra.OP[0], "end": extra.OP[1], "clipType": "CLIP_TYPE_OP", "toastText": "即将跳过片头"})
	}
	result := pgcPlayurlPayload(media, ep.Duration, NumberUnicode(c.P["qn"], 80))
	result["clip_info_list"] = clips
	return Result(result), nil
}
func pgcPlayurlPayload(media string, duration int, qn float64) render.M {
	qualities := []render.M{
		{"quality": 80, "format": "flv", "new_description": "1080P 高清", "display_desc": "1080P"},
		{"quality": 64, "format": "flv720", "new_description": "720P 准高清", "display_desc": "720P"},
		{"quality": 32, "format": "flv480", "new_description": "480P 标清", "display_desc": "480P"},
	}
	quality := 32
	for _, q := range []int{80, 64, 32} {
		if float64(q) <= qn {
			quality = q
			break
		}
	}
	return render.M{"from": "local", "result": "suee", "quality": quality, "format": "mp4", "timelength": duration * 1000, "accept_format": "mp4,mp4720,mp4480", "accept_description": []string{"1080P 高清", "720P 准高清", "480P 标清"}, "accept_quality": []int{80, 64, 32}, "video_codecid": 7, "durl": []render.M{{"order": 1, "length": duration * 1000, "size": 0, "ahead": "", "vhead": "", "url": media, "backup_url": []string{}}}, "support_formats": qualities}
}
