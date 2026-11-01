package seed

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

var bareNumber = regexp.MustCompile(`^(第\s*)?\d+\s*[集话期]?$`)

func prefix(s string, n int) string {
	p := utf16.Encode([]rune(s))
	return string(utf16.Decode(p[:min(n, len(p))]))
}
func regionFor(library string, genres []string) int64 {
	g := strings.ToLower(strings.Join(genres, " "))
	for _, v := range []struct {
		words []string
		tid   int64
	}{{[]string{"美食", "food", "cooking"}, 1020}, {[]string{"音乐", "music"}, 1003}, {[]string{"纪录", "documentary"}, 1010}, {[]string{"体育", "sport"}, 1018}, {[]string{"旅行", "travel"}, 1030}} {
		for _, w := range v.words {
			if strings.Contains(g, w) {
				return v.tid
			}
		}
	}
	if library == "动漫" {
		return 1005
	}
	if library == "综艺" {
		return 1002
	}
	return 1001
}
func seasonType(s emby.Series) int {
	if s.Library == "动漫" {
		for _, c := range s.Countries {
			if strings.Contains(strings.ToLower(c), "china") || strings.Contains(c, "中国") || strings.Contains(c, "大陆") {
				return 4
			}
		}
		return 1
	}
	if s.Library == "综艺" {
		return 7
	}
	return 5
}

type clip struct {
	Title, Desc, Pic, Tag string
	TID                   int64
	Pages                 []store.Page
}

func clipsFrom(c emby.Catalog) []clip {
	clips := []clip{}
	tag := func(library string, genres []string) string {
		parts := []string{}
		for _, p := range append([]string{library}, genres[:min(2, len(genres))]...) {
			if p != "" {
				parts = append(parts, p)
			}
		}
		return strings.Join(parts, ",")
	}
	for round := 0; len(clips) < 40 && round < 16; round++ {
		for _, s := range c.Series {
			if len(clips) >= 40 {
				break
			}
			if round >= len(s.Episodes) {
				continue
			}
			e := s.Episodes[round]
			wide := s.Images.Wide()
			v := clip{Desc: e.Overview, TID: regionFor(s.Library, s.Genres), Pic: emby.Image(e.Image, &wide), Tag: tag(s.Library, s.Genres), Pages: []store.Page{}}
			if v.Desc == "" {
				v.Desc = s.Overview
			}
			if round == 1 && len(s.Episodes) >= 4 {
				v.Title = "《" + s.Name + "》连看三集"
				v.Desc = s.Overview
				for _, e := range s.Episodes[1:4] {
					part := e.Name
					if bareNumber.MatchString(strings.TrimSpace(part)) {
						part = fmt.Sprintf("第%d集", e.Index)
					}
					v.Pages = append(v.Pages, store.Page{Part: part, Duration: e.Runtime, Media: emby.Ref(e.ID, e.SourceID)})
				}
			} else {
				unit := "集"
				if s.Library == "动漫" {
					unit = "话"
				}
				title := fmt.Sprintf("%s 第%d%s", s.Name, e.Index, unit)
				if s.Library == "综艺" {
					title = s.Name + "：" + e.Name
				} else if !bareNumber.MatchString(strings.TrimSpace(e.Name)) {
					title += " " + e.Name
				}
				v.Title = prefix(title, 80)
				v.Pages = []store.Page{{Part: prefix(title, 40), Duration: e.Runtime, Media: emby.Ref(e.ID, e.SourceID)}}
			}
			clips = append(clips, v)
		}
	}
	for _, m := range c.Movies[:min(4, len(c.Movies))] {
		clips = append(clips, clip{Title: m.Name, Desc: m.Overview, TID: regionFor("电影", m.Genres), Pic: m.Images.Wide(), Tag: strings.Join(append([]string{"电影"}, m.Genres[:min(2, len(m.Genres))]...), ","), Pages: []store.Page{{Part: m.Name, Duration: m.Runtime, Media: emby.Ref(m.ID, m.SourceID)}}})
	}
	return clips
}
func catalogBackdrops(c emby.Catalog) []string {
	out := []string{}
	for _, s := range c.Series {
		if s.Images.Backdrop != nil && *s.Images.Backdrop != "" {
			out = append(out, *s.Images.Backdrop)
		}
	}
	for _, m := range c.Movies {
		if m.Images.Backdrop != nil && *m.Images.Backdrop != "" {
			out = append(out, *m.Images.Backdrop)
		}
	}
	return out
}
func pickString(values []string, id int64) string {
	if len(values) == 0 {
		return ""
	}
	n := id % int64(len(values))
	if n < 0 {
		n = -n
	}
	return values[n]
}
func bannerImage(backdrops []string, i int, fallback string) string {
	if i < len(backdrops) {
		return backdrops[i]
	}
	return fallback
}

var countries = map[string]string{"China": "中国大陆", "People's Republic of China": "中国大陆", "Japan": "日本", "South Korea": "韩国", "Korea": "韩国", "United States of America": "美国", "USA": "美国", "Hong Kong": "中国香港", "Taiwan": "中国台湾", "United Kingdom": "英国"}
var weekdays = map[string]int{"Monday": 1, "Tuesday": 2, "Wednesday": 3, "Thursday": 4, "Friday": 5, "Saturday": 6, "Sunday": 7}

func catalogSeasons(c emby.Catalog, t0 int64, r func() float64, cid *int64) []store.Season {
	series := append([]emby.Series{}, c.Series...)
	for _, m := range c.Movies {
		series = append(series, emby.Series{ID: m.ID, Library: "电影", Name: m.Name, Overview: m.Overview, Countries: m.Countries, Genres: m.Genres, Rating: m.Rating, Images: m.Images, Ended: true, Premiere: m.Premiere, Episodes: []emby.Episode{{ID: m.ID, SourceID: m.SourceID, Name: m.Name, Index: 1, Runtime: m.Runtime, Premiere: m.Premiere, Image: func() *string {
			if m.Images.Thumb != nil {
				return m.Images.Thumb
			}
			return m.Images.Backdrop
		}()}}})
	}
	out := []store.Season{}
	ep := int64(300000)
	for i, b := range series {
		typ := seasonType(b)
		if i >= len(c.Series) {
			typ = 2
		}
		sid := int64(40000 + i*11)
		horizontal := b.Images.Wide()
		areas := []string{}
		seen := map[string]bool{}
		for _, country := range b.Countries {
			if v, ok := countries[country]; ok {
				country = v
			}
			if !seen[country] && len(areas) < 2 {
				areas = append(areas, country)
				seen[country] = true
			}
		}
		score := b.Rating
		if score > 0 {
			score = math.Round(min(10, score)*10) / 10
		} else {
			score = math.Round((8+r()*1.9)*10) / 10
		}
		weekday := i%7 + 1
		if len(b.AirDays) > 0 {
			if d := weekdays[b.AirDays[0]]; d > 0 {
				weekday = d
			}
		}
		pubTime := b.AirTime
		if pubTime == "" {
			pubTime = []string{"18:00", "19:30", "20:00", "22:00", "23:30"}[i%5]
		}
		ctime := b.Premiere
		if ctime == 0 {
			ctime = t0 - int64(86400*(30+i*5))
		}
		s := store.Season{SeasonID: sid, MediaID: 28000000 + sid, SeasonType: typ, Title: b.Name, Cover: emby.Image(b.Images.Primary, &horizontal), Horizontal: horizontal, Evaluate: b.Overview, Areas: areas, Styles: append([]string{}, b.Genres[:min(3, len(b.Genres))]...), Score: score, IsFinish: b.Ended, Weekday: weekday, PubTime: pubTime, Views: int64(1e5 + r()*5e7), Follow: int64(1e4 + r()*3e6), Danmakus: int64(1e3 + r()*2e5), Ctime: ctime, Episodes: []store.Episode{}}
		for k, e := range b.Episodes {
			ep++
			*cid += 1
			title := strconv.Itoa(e.Index)
			long := e.Name
			if bareNumber.MatchString(strings.TrimSpace(long)) {
				long = ""
			}
			if typ == 2 {
				title = "正片"
				long = b.Name
			}
			pub := e.Premiere
			if pub == 0 {
				pub = t0 - int64(86400*7*(len(b.Episodes)-k))
			}
			badge := ""
			if k == len(b.Episodes)-1 && !b.Ended {
				badge = "新"
			}
			s.Episodes = append(s.Episodes, store.Episode{EpID: ep, SeasonID: sid, Position: k, AID: 700000 + ep, CID: *cid, Title: title, LongTitle: long, Cover: emby.Image(e.Image, &horizontal), Duration: e.Runtime, PubTime: pub, Media: emby.Ref(e.ID, e.SourceID), Badge: badge})
		}
		out = append(out, s)
	}
	return out
}
func catalogRooms(c emby.Catalog, t0 int64, r func() float64) []store.LiveRoom {
	type show struct{ name, kind, item, source, image string }
	shows := []show{}
	for _, m := range c.Movies {
		shows = append(shows, show{m.Name, "电影", m.ID, m.SourceID, m.Images.Wide()})
	}
	for _, s := range c.Series {
		if len(s.Episodes) == 0 {
			continue
		}
		e := s.Episodes[0]
		wide := s.Images.Wide()
		shows = append(shows, show{s.Name, s.Library, e.ID, e.SourceID, emby.Image(e.Image, &wide)})
	}
	rooms := []store.LiveRoom{}
	for i, show := range shows[min(1, len(shows)):min(17, len(shows))] {
		parent, area := int64(10), int64(215)
		if show.kind == "综艺" {
			parent = 1
			area = 21
		} else if show.kind == "动漫" {
			parent = 9
			area = 371
		}
		rooms = append(rooms, store.LiveRoom{RoomID: int64(21000 + i*3), UID: int64(i%11 + 2), Title: prefix("【放映厅】"+show.name, 40), Cover: show.image, ParentAreaID: parent, AreaID: area, LiveStatus: store.BoolInt(i%6 != 5), Online: int64(200 + r()*r()*90000), LiveTime: t0 - int64(r()*3600), Tags: show.kind, Description: "正在放映《" + show.name + "》，欢迎一起观看～", Source: &store.StreamSource{Type: "m3u8", URL: emby.HLSPath(show.item, show.source), Loop: true, Format: "ts", Kind: "vod"}})
	}
	if len(shows) > 0 {
		show := shows[0]
		rooms = append(rooms, store.LiveRoom{RoomID: 20000, UID: 1, Title: prefix("聚媒官方 · "+show.name, 40), Cover: show.image, ParentAreaID: 10, AreaID: 215, LiveStatus: 1, Online: 3260, LiveTime: t0, Tags: "官方", Description: "直播源为 m3u8 点播列表，由平台按时间轴循环转播为直播。", Source: &store.StreamSource{Type: "m3u8", URL: emby.HLSPath(show.item, show.source), Loop: true, Format: "ts", Kind: "vod"}})
	}
	return rooms
}
