package api

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

// The Mock's decoder deliberately accepts any prefix and alphabet characters.
// Retaining the low 51 bits each step is equivalent to its final BigInt mask.
func discoverBV2AV(bvid string) int64 {
	const alphabet = "FcwAPNKTMug3GV5Lj7EJnHpWsx4tb8haYeviqBz6rkCy12mUSDQX9RdoZf"
	const mask uint64 = 2251799813685247
	b := utf16.Encode([]rune(bvid))
	for len(b) < 10 {
		b = append(b, 0) // JavaScript's missing characters map to index -1.
	}
	b[3], b[9] = b[9], b[3]
	b[4], b[7] = b[7], b[4]
	var n uint64
	for _, ch := range b[3:] {
		n = (n*58 + uint64(strings.IndexRune(alphabet, rune(ch)))) & mask
	}
	return int64(n ^ 23442827791579)
}
func (s *Server) discoverArchive(c *Ctx, withBVID bool) (*store.Archive, error) {
	value := ""
	for _, key := range []string{"aid", "oid", "rid", "avid"} {
		if v, ok := c.P[key]; ok {
			value = v
			break
		}
	}
	aid := NonnegativeID(NumberUnicode(value, 0))
	if withBVID && c.P["bvid"] != "" {
		aid = discoverBV2AV(c.P["bvid"])
	}
	a, err := s.Store.ArchiveByID(c.Context(), aid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "啥都木有")
	}
	if err != nil {
		return nil, err
	}
	if a.State != 0 && (c.Me == nil || (c.Me.MID != a.MID && c.Me.Role != "admin")) {
		return nil, Fail(-404, "啥都木有")
	}
	return a, nil
}
func (s *Server) discoverView(c *Ctx) (any, error) {
	a, err := s.discoverArchive(c, true)
	if err != nil {
		return nil, err
	}
	return s.Render.ArcView(c.Context(), *a)
}
func (s *Server) discoverDetail(c *Ctx) (any, error) {
	a, err := s.discoverArchive(c, false)
	if err != nil {
		return nil, err
	}
	u, err := s.Store.UserByID(c.Context(), a.MID)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	related := []store.Archive{}
	for _, x := range all {
		if x.AID != a.AID {
			related = append(related, x)
		}
	}
	sort.SliceStable(related, func(i, j int) bool {
		x, y := related[i], related[j]
		if (x.TID == a.TID) != (y.TID == a.TID) {
			return x.TID == a.TID
		}
		return x.Stat.View > y.Stat.View
	})
	rows, err := discoverMap(related[:min(12, len(related))], func(x store.Archive) (render.M, error) { return s.Render.ArcView(c.Context(), x) })
	if err != nil {
		return nil, err
	}
	following := false
	if c.Me != nil {
		f, e := s.discoverFollows(c, c.Me.MID)
		if e != nil {
			return nil, e
		}
		following = f[a.MID]
	}
	fans, err := s.Store.FollowerCount(c.Context(), u.MID)
	if err != nil {
		return nil, err
	}
	attention, err := s.Store.FollowingCount(c.Context(), u.MID)
	if err != nil {
		return nil, err
	}
	var count int
	if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM archives WHERE mid=? AND state=0", u.MID).Scan(&count); err != nil {
		return nil, err
	}
	view, err := s.Render.ArcView(c.Context(), *a)
	if err != nil {
		return nil, err
	}
	tags := []render.M{}
	for _, tag := range strings.Split(a.Tag, ",") {
		if tag != "" {
			tags = append(tags, render.M{"tag_id": len(tags) + 1, "tag_name": tag})
		}
	}
	return render.M{"View": view, "Card": render.M{"card": render.M{"mid": strconv.FormatInt(u.MID, 10), "name": u.Uname, "face": render.Face(u.MID, u.Face), "sign": u.Sign, "fans": fans, "attention": attention, "level_info": render.LevelInfo(*u)}, "following": following, "archive_count": count, "follower": fans, "like_num": 0}, "Tags": tags, "Related": rows}, nil
}
func (s *Server) discoverPlayer(c *Ctx) (any, error) {
	aid, cid := NumberUnicode(c.P["aid"], 0), NumberUnicode(c.P["cid"], 0)
	a, err := s.Store.ArchiveByID(c.Context(), NonnegativeID(aid))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var mid, lastCID int64
	var lastTime int
	if c.Me != nil {
		mid = c.Me.MID
		h, e := s.Store.ListHistory(c.Context(), "WHERE mid=? AND aid=? ORDER BY view_at DESC LIMIT 1", mid, NonnegativeID(aid))
		if e != nil {
			return nil, e
		}
		if len(h) > 0 {
			lastCID = h[0].CID
			if h[0].Progress > 0 {
				lastTime = h[0].Progress * 1000
			}
		}
	}
	subs, chapters := []render.M{}, []render.M{}
	bvid := ""
	if a != nil {
		bvid = a.BVID
		for i, sub := range a.Subtitles {
			id := a.AID*10 + int64(i)
			subs = append(subs, render.M{"id": id, "id_str": strconv.FormatInt(id, 10), "lan": sub.Lan, "lan_doc": sub.LanDoc, "subtitle_url": fmt.Sprintf("%s/bfs/subtitle/%d-%d.json", s.Origin(c.R), a.AID, i), "type": 0})
		}
		for _, ch := range a.Chapters {
			chapters = append(chapters, render.M{"type": 2, "from": ch.From, "to": ch.To, "content": ch.Content, "imgUrl": a.Pic})
		}
	}
	if a == nil {
		chapters, err = s.episodePoints(c, cid)
		if err != nil {
			return nil, err
		}
	}

	return render.M{"aid": aid, "bvid": bvid, "cid": cid, "login_mid": mid, "online_count": 1 + math.Mod(cid, 97), "last_play_time": lastTime, "last_play_cid": lastCID, "subtitle": render.M{"allow_submit": false, "subtitles": subs}, "view_points": chapters}, nil
}

var discoverSubtitlePath = regexp.MustCompile(`^(\d+)-(\d+)\.json$`)

func (s *Server) discoverSubtitle(c *Ctx) (any, error) {
	m := discoverSubtitlePath.FindStringSubmatch(c.R.PathValue("file"))
	if m == nil {
		return nil, Fail(-404, "接口不存在")
	}
	body := []store.Chapter{}
	aid, aidErr := strconv.ParseInt(m[1], 10, 64)
	index, indexErr := strconv.ParseUint(m[2], 10, 64)
	if aidErr == nil && indexErr == nil {
		a, err := s.Store.ArchiveByID(c.Context(), aid)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if a != nil && index < uint64(len(a.Subtitles)) && a.Subtitles[index].Body != nil {
			body = a.Subtitles[index].Body
		}
	}
	return Raw(render.M{"font_size": 0.4, "font_color": "#FFFFFF", "background_alpha": 0.5, "background_color": "#9C27B0", "Stroke": "none", "body": body}), nil
}
func discoverPlayPayload(media string, duration int, qn float64) render.M {
	formats := []render.M{{"quality": 80, "format": "flv", "new_description": "1080P 高清", "display_desc": "1080P"}, {"quality": 64, "format": "flv720", "new_description": "720P 准高清", "display_desc": "720P"}, {"quality": 32, "format": "flv480", "new_description": "480P 标清", "display_desc": "480P"}}
	quality := 32
	for _, q := range []int{80, 64, 32} {
		if float64(q) <= qn {
			quality = q
			break
		}
	}
	return render.M{"from": "local", "result": "suee", "quality": quality, "format": "mp4", "timelength": duration * 1000, "accept_format": "mp4,mp4720,mp4480", "accept_description": []string{"1080P 高清", "720P 准高清", "480P 标清"}, "accept_quality": []int{80, 64, 32}, "video_codecid": 7, "durl": []render.M{{"order": 1, "length": duration * 1000, "size": 0, "ahead": "", "vhead": "", "url": media, "backup_url": []any{}}}, "support_formats": formats}
}
func (s *Server) discoverPlayURL(c *Ctx) (any, error) {
	pages, err := s.Store.ListPage(c.Context(), "WHERE cid=?", NonnegativeID(NumberUnicode(c.P["cid"], 0)))
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, Fail(-404, "视频不存在")
	}
	p := pages[0]
	allow, err := s.Store.Setting(c.Context(), "allowGuestPlay")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if !NonzeroTruthy(allow) && c.Me == nil {
		return nil, Fail(-101, "请登录后观看")
	}
	media, err := discoverAbsoluteMedia(s.Origin(c.R), p.Media)
	if err != nil {
		return nil, err
	}
	// A single conditional increment is atomic on both database engines and does
	// not overwrite concurrent likes/coins or increment unpublished archives.
	if _, err = s.Store.DB.ExecContext(c.Context(), "UPDATE archives SET stat_view=stat_view+1 WHERE aid=? AND state=0", p.AID); err != nil {
		return nil, err
	}
	payload := discoverPlayPayload(media, p.Duration, NumberUnicode(c.P["qn"], 80))
	if _, _, _, mode, ok := emby.ParseDSRef(p.Media); ok && strings.HasPrefix(mode, "raw-") {
		payload["format"] = strings.TrimPrefix(mode, "raw-")
	}
	return payload, nil
}

func discoverAbsoluteMedia(origin, media string) (string, error) {
	if ds, item, source, mode, ok := emby.ParseDSRef(media); ok {
		media = emby.DSPath(ds, item, source, mode)
	}
	ref, err := url.Parse(media)
	if err != nil {
		return "", err
	}
	if ref.IsAbs() {
		return media, nil
	}
	base, err := url.Parse(strings.TrimRight(origin, "/") + "/")
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}
