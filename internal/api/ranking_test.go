package api

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func TestRegionRankingAndFeedback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		// Recent first outranks a higher score, then score breaks same-window ties.
		discoverExec(t, s, "UPDATE archives SET tid=?,stat_view=1,stat_like=0,stat_coin=0,pubdate=0", 1001)
		now := time.Now().Unix()
		for _, v := range []struct{ aid, view, like, coin, pub int64 }{{100100, 100000, 100, 100, now - 10*86400}, {100107, 10, 2, 3, now}, {100114, 50, 1, 0, now}, {100121, 999999, 0, 0, now}} {
			discoverExec(t, s, "UPDATE archives SET tid=?,stat_view=?,stat_like=?,stat_coin=?,pubdate=? WHERE aid=?", 1005, v.view, v.like, v.coin, v.pub, v.aid)
		}
		discoverExec(t, s, "UPDATE archives SET state=-1 WHERE aid=?", 100121)
		for _, rid := range []string{"51", "1005", "1", "13", "32", "33", "152", "167"} {
			v := get(t, s, "/x/web-interface/ranking/region?rid="+rid)
			assertCode(t, v, 0)
			rows, ok := v["data"].([]any)
			if !ok || len(rows) != 3 {
				t.Fatal(v)
			}
			for i, id := range []float64{100107, 100114, 100100} {
				if rows[i].(map[string]any)["aid"] != id {
					t.Fatal(rows)
				}
			}
			first := rows[0].(map[string]any)
			want := map[string]any{"aid": float64(100107), "bvid": store.AV2BV(100107), "typename": "动画", "subtitle": "", "play": float64(10), "mid": float64(3), "author": "乌龙茶不加糖", "coins": float64(3), "badgepay": false, "pts": float64(90), "create": time.Unix(now, 0).UTC().Format("2006-01-02 15:04")}
			discoverFields(t, first, want)
			for _, field := range []string{"title", "review", "video_review", "favorites", "description", "pic", "duration"} {
				if _, ok := first[field]; !ok {
					t.Fatal("missing field", field)
				}
			}
			if len(first) != 18 {
				t.Fatalf("unexpected shape %v", first)
			}
		}
		v := get(t, s, "/x/web-interface/ranking/region?rid=51&day=30")
		assertCode(t, v, 0)
		if v["data"].([]any)[0].(map[string]any)["aid"] != float64(100100) {
			t.Fatal(v)
		}
		for _, rid := range []string{"999999", "bad", "0"} {
			v = get(t, s, "/x/web-interface/ranking/region?rid="+rid)
			assertCode(t, v, 0)
			if !reflect.DeepEqual(v["data"], []any{}) {
				t.Fatal(v)
			}
		}
		session := login(t, s, "jumedia", "admin123")
		discoverFailure(t, post(t, s, "/x/web-interface/feedback/dislike", nil), -101, "账号未登录")
		discoverFailure(t, post(t, s, "/x/web-interface/feedback/dislike", nil, session), -111, "csrf 校验失败")
		v = post(t, s, "/x/web-interface/feedback/dislike", url.Values{"csrf": {session.CSRF}, "goto": {"ad"}}, session)
		assertCode(t, v, 0)
		if v["data"] != nil {
			t.Fatal(v)
		}
		if err := s.Store.Close(); err != nil {
			t.Fatal(err)
		}
		assertCode(t, get(t, s, "/x/web-interface/ranking/region?rid=51"), -500)
	})
}
func TestPGCRankingModesAndVariety(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		discoverExec(t, s, "UPDATE seasons SET views=1,follow=1")
		discoverExec(t, s, "UPDATE seasons SET views=900,follow=2 WHERE season_id=?", 40000)
		discoverExec(t, s, "UPDATE seasons SET views=2,follow=900 WHERE season_id=?", 40077)
		for _, tc := range []struct {
			query string
			first float64
		}{{"season_type=0&style_id=210", 40000}, {"season_type=0&style_id=430", 40077}, {"season_type=7&style_id=430", 40077}, {"season_type=1&style_id=430", 40000}} {
			d := pgcPayload(t, s, "/pgc/season/rank/list?"+tc.query, "data")
			rows := pgcArray(t, d["list"])
			if len(rows) == 0 || pgcObject(t, rows[0])["season_id"] != tc.first {
				t.Fatal(d)
			}
			for i, row := range rows {
				if pgcObject(t, row)["rank"] != float64(i+1) {
					t.Fatal(row)
				}
			}
		}
		tabs := pgcPayload(t, s, "/pgc/season/rank/index", "data")["index"].([]any)
		tab := tabs[1].(map[string]any)
		u, err := url.Parse(tab["url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		d := pgcPayload(t, s, u.RequestURI(), "data")
		for _, row := range pgcArray(t, d["list"]) {
			if !strings.Contains(pgcObject(t, row)["styles"].(string), tab["title"].(string)) {
				t.Fatal(row)
			}
		}
		d = pgcPayload(t, s, "/pgc/season/rank/list?season_type=999", "data")
		if len(pgcArray(t, d["list"])) != 0 {
			t.Fatal(d)
		}
		for _, kind := range []struct{ name, unit, badge string }{{"variety", "期", "综艺"}, {"tv", "集", "电视剧"}, {"anime", "话", "番剧"}} {
			d = pgcPayload(t, s, "/pgc/page/web/feed?name="+kind.name, "data")
			rows := pgcArray(t, d["items"])
			if kind.name == "anime" {
				rows = pgcArray(t, pgcObject(t, rows[0])["sub_items"])
			}
			if len(rows) == 0 {
				t.Fatal(d)
			}
			card := pgcObject(t, rows[0])
			if !strings.HasSuffix(card["sub_title"].(string), kind.unit) || pgcObject(t, card["badge_info"])["text"] != kind.badge {
				t.Fatal(card)
			}
		}
		for i := 0; i < 110; i++ {
			season := store.Season{SeasonID: int64(80000 + i), MediaID: int64(90000 + i), SeasonType: 1, Title: fmt.Sprint(i), Styles: []string{}, Areas: []string{}}
			if err := store.InsertSeason(t.Context(), s.Store.DB, &season); err != nil {
				t.Fatal(err)
			}
		}
		d = pgcPayload(t, s, "/pgc/season/rank/list?season_type=0", "data")
		if len(pgcArray(t, d["list"])) != 100 {
			t.Fatal("rank limit")
		}
		if err = s.Store.Close(); err != nil {
			t.Fatal(err)
		}
		assertCode(t, get(t, s, "/pgc/season/rank/list"), -500)
	})
}
