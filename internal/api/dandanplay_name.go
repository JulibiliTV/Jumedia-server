package api

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

var dandanplayReviewPrefix = regexp.MustCompile(`^(?:[【\[（(]\s*(?:待审核|待审|审核中)\s*[】\]）)]\s*[:：—-]?\s*|(?:待审核|待审|审核中)(?:\s*[:：—-]\s*|\s+))`)
var dandanplaySeasonEpisode = regexp.MustCompile(`(?i)S(\d+)E(\d+)`)
var dandanplayEpisodeNumber = regexp.MustCompile(`^(?:第\s*)?(\d+)\s*(?:[集话期]|$)`)

func dandanplayTitle(title string) string {
	title = strings.TrimSpace(title)
	for {
		clean := strings.TrimSpace(dandanplayReviewPrefix.ReplaceAllString(title, ""))
		if clean == title {
			return title
		}
		title = clean
	}
}

// kind: 0 = archive page, 1 = series episode, 2 = movie.
func dandanplayQueryName(title, part, previous string, kind, position int) string {
	title = dandanplayTitle(title)
	part = dandanplayTitle(part)
	code := dandanplaySeasonEpisode.FindStringSubmatch(part)
	known := dandanplaySeasonEpisode.FindStringSubmatch(previous)
	ordinal := dandanplayEpisodeNumber.FindStringSubmatch(part)
	if kind == 2 || (kind == 0 && (part == title || part == "正片" || len(code)+len(known)+len(ordinal) == 0)) {
		return title
	}
	season, episode := 1, position+1
	if len(code) > 0 {
		season, _ = strconv.Atoi(code[1])
		episode, _ = strconv.Atoi(code[2])
	} else {
		if len(known) > 0 {
			season, _ = strconv.Atoi(known[1])
			episode, _ = strconv.Atoi(known[2])
		}
		if len(ordinal) > 0 {
			episode, _ = strconv.Atoi(ordinal[1])
		}
	}
	return fmt.Sprintf("%s.S%02dE%02d", title, max(0, season), max(1, episode))
}

func dandanplayMediaQuery(ctx context.Context, q db.Querier, cid int64) (string, error) {
	var title, part, previous, origin string
	var kind, position int
	err := q.QueryRowContext(ctx, `SELECT a.title,p.part,COALESCE(d.filename,''),a.origin,0,p.position
		FROM archive_pages p JOIN archives a ON a.aid=p.aid LEFT JOIN dandanplay_sync d ON d.cid=p.cid WHERE p.cid=?
		UNION ALL SELECT s.title,e.title,COALESCE(d.filename,''),'',CASE WHEN s.season_type=2 THEN 2 ELSE 1 END,e.position
		FROM episodes e JOIN seasons s ON s.season_id=e.season_id LEFT JOIN dandanplay_sync d ON d.cid=e.cid WHERE e.cid=? LIMIT 1`, cid, cid).
		Scan(&title, &part, &previous, &origin, &kind, &position)
	if err != nil {
		return "", err
	}
	var source store.ArchiveOrigin
	if json.Unmarshal([]byte(origin), &source) == nil && strings.TrimSpace(source.Meta.Title) != "" {
		title = source.Meta.Title
	}
	return dandanplayQueryName(title, part, previous, kind, position), nil
}
