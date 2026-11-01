package api

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func pgcObject(t *testing.T, value any) map[string]any {
	t.Helper()
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", value)
	}
	return obj
}
func pgcArray(t *testing.T, value any) []any {
	t.Helper()
	rows, ok := value.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", value)
	}
	return rows
}
func pgcPayload(t *testing.T, srv *Server, path, key string, session ...*store.Session) map[string]any {
	t.Helper()
	response := get(t, srv, path, session...)
	assertCode(t, response, 0)
	return pgcObject(t, response[key])
}
func pgcFields(t *testing.T, obj map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if !reflect.DeepEqual(obj[key], value) {
			t.Errorf("%s = %#v, want %#v", key, obj[key], value)
		}
	}
}
func pgcExec(t *testing.T, srv *Server, query string, args ...any) {
	t.Helper()
	if _, err := srv.Store.DB.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func pgcSeedSeason(t *testing.T, srv *Server, sid int64) *store.Season {
	t.Helper()
	season, err := srv.Store.SeasonByID(t.Context(), sid)
	if err != nil {
		t.Fatal(err)
	}
	return season
}

func TestPgcBrowseContract(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := login(t, srv, "jumedia", "admin123")
		for _, path := range []string{"/pgc/web/rank/list", "/pgc/season/rank/list"} {
			key := "data"
			if path == "/pgc/web/rank/list" {
				key = "result"
			}
			result := pgcPayload(t, srv, path, key)
			rows := pgcArray(t, result["list"])
			if len(rows) < 2 {
				t.Fatal("missing ranked seasons")
			}
			previous := float64(1e20)
			for i, row := range rows {
				card := pgcObject(t, row)
				pgcFields(t, card, map[string]any{"rank": float64(i + 1), "season_type": float64(1)})
				views := pgcObject(t, card["stat"])["view"].(float64)
				if views > previous {
					t.Fatal("rank not ordered by views")
				}
				previous = views
				if pgcObject(t, card["first_ep"])["ep_id"] == nil || pgcObject(t, card["new_ep"])["id"] == nil || card["link"] == "" {
					t.Fatal("missing season card fields")
				}
			}
			if key == "result" && result["note"] != "根据播放、追番、互动数据综合计算" {
				t.Fatal("rank note")
			}
			empty := pgcPayload(t, srv, path+"?season_type=999", key)
			if len(pgcArray(t, empty["list"])) != 0 {
				t.Fatal("invalid type should be empty")
			}
		}
		tabs := pgcArray(t, pgcPayload(t, srv, "/pgc/season/rank/index", "data")["index"])
		if len(tabs) < 2 {
			t.Fatal("missing style tabs")
		}
		pgcFields(t, pgcObject(t, tabs[0]), map[string]any{"tab_id": float64(1), "title": "综合", "url": "/pgc/season/rank/list?season_type=1&style_id=0&day=3"})
		for _, tab := range tabs[1:] {
			item := pgcObject(t, tab)
			u, err := url.Parse(item["url"].(string))
			if err != nil || u.Scheme != "" || u.Host != "" || u.Path != "/pgc/season/rank/list" {
				t.Fatalf("tab URL: %v", u)
			}
			rows := pgcArray(t, pgcPayload(t, srv, u.RequestURI(), "data")["list"])
			if len(rows) == 0 {
				t.Fatal("style should match seasons")
			}
			for _, row := range rows {
				if !strings.Contains(pgcObject(t, row)["styles"].(string), item["title"].(string)) {
					t.Fatal("style filter")
				}
			}
		}
		defaults := pgcPayload(t, srv, "/pgc/season/rank/list", "data")
		invalid := pgcPayload(t, srv, "/pgc/season/rank/list?style_id=999&season_type=nonsense", "data")
		if !reflect.DeepEqual(defaults, invalid) {
			t.Fatal("invalid style/type defaults differ")
		}

		for name, kind := range map[string]int{"anime": 1, "guochuang": 4, "documentary": 3, "movie": 2, "tv": 5, "variety": 7, "unknown": 1} {
			modern := pgcPayload(t, srv, "/pgc/page/web/v3/feed?name="+name, "data")
			legacy := pgcPayload(t, srv, "/pgc/page/web/feed?name="+name, "data")
			if !reflect.DeepEqual(modern, legacy) {
				t.Fatal("feed aliases differ")
			}
			items := pgcArray(t, modern["items"])
			if kind == 1 || kind == 4 {
				wrapper := pgcObject(t, items[0])
				pgcFields(t, wrapper, map[string]any{"card_style": "v_card"})
				items = pgcArray(t, wrapper["sub_items"])
			}
			if kind == 3 {
				if len(items) != 0 {
					t.Fatal("catalog has no documentary")
				}
				continue
			}
			if len(items) == 0 {
				t.Fatalf("empty %s", name)
			}
			card := pgcObject(t, items[0])
			sid := int64(card["season_id"].(float64))
			season := pgcSeedSeason(t, srv, sid)
			if season.SeasonType != kind {
				t.Fatal("wrong feed type")
			}
			pgcFields(t, card, map[string]any{"episode_id": float64(season.Episodes[0].EpID), "title": season.Title, "rating": fmt.Sprint(season.Score), "link": fmt.Sprintf("/bangumi/play/ss%d", sid)})
			if _, ok := pgcObject(t, card["stat"])["view"].(string); !ok {
				t.Fatal("feed view should be compact string")
			}
			if len(pgcArray(t, pgcObject(t, card["hover"])["text"])) != 2 {
				t.Fatal("hover text")
			}
			end := pgcPayload(t, srv, "/pgc/page/web/v3/feed?name="+name+"&coursor=999", "data")
			pgcFields(t, end, map[string]any{"coursor": float64(999), "has_next": false})
		}
		for _, name := range []string{"anime", "guochuang", "movie"} {
			modules := pgcArray(t, pgcPayload(t, srv, "/pgc/page/web/v3?name="+name, "data", session)["modules"])
			if len(modules) != 3 {
				t.Fatal("three modules required")
			}
			for i, style := range []string{"web_banner_v3", "web_follow_v3", "web_feed_v3"} {
				if pgcObject(t, modules[i])["style"] != style {
					t.Fatal("module style")
				}
			}
			banner := pgcObject(t, pgcArray(t, pgcObject(t, modules[0])["items"])[0])
			season := pgcSeedSeason(t, srv, int64(banner["season_id"].(float64)))
			pgcFields(t, banner, map[string]any{"cover": season.Horizontal, "big_cover": season.Horizontal, "evaluate": season.Evaluate})
			follows := pgcArray(t, pgcObject(t, modules[1])["items"])
			if len(follows) != 2 {
				t.Fatalf("follows = %d", len(follows))
			}
			pgcFields(t, pgcObject(t, follows[0]), map[string]any{"follow_item_type": "user", "progress": "尚未观看"})
			kindName := "anime"
			if name == "guochuang" {
				kindName = name
			}
			feed := pgcPayload(t, srv, "/pgc/page/web/v3/feed?name="+kindName, "data")
			if !reflect.DeepEqual(pgcObject(t, modules[2])["items"], feed["items"]) {
				t.Fatal("module feed type")
			}
		}
		guest := pgcArray(t, pgcPayload(t, srv, "/pgc/page/web/v3", "data")["modules"])
		if len(pgcArray(t, pgcObject(t, guest[1])["items"])) != 0 {
			t.Fatal("guest follows")
		}
	})
}

func TestPgcIndexSortAndPagination(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		// Pin independent values so every documented ordering is distinguishable.
		pgcExec(t, srv, "UPDATE seasons SET state=-1")
		for i, values := range [][4]any{{10, 30, 9.0, 100}, {30, 10, 8.0, 300}, {20, 20, 9.5, 200}} {
			pgcExec(t, srv, "UPDATE seasons SET state=0,season_type=1,views=?,follow=?,score=?,ctime=? WHERE season_id=?", values[0], values[1], values[2], values[3], 40000+i*11)
		}
		for _, tc := range []struct {
			order int
			ids   []float64
		}{{0, []float64{40011, 40022, 40000}}, {2, []float64{40011, 40022, 40000}}, {3, []float64{40000, 40022, 40011}}, {4, []float64{40022, 40000, 40011}}, {5, []float64{40011, 40022, 40000}}} {
			for _, asc := range []bool{false, true} {
				path := fmt.Sprintf("/pgc/season/index/result?order=%d", tc.order)
				if asc {
					path += "&sort=1"
				}
				data := pgcPayload(t, srv, path, "data")
				rows := pgcArray(t, data["list"])
				pgcFields(t, data, map[string]any{"num": float64(1), "size": float64(24), "total": float64(3), "has_next": float64(0)})
				for i, row := range rows {
					want := tc.ids[i]
					if asc {
						want = tc.ids[2-i]
					}
					card := pgcObject(t, row)
					if card["season_id"] != want {
						t.Errorf("order %d asc %v row %d: %v", tc.order, asc, i, card["season_id"])
					}
					label := card["order"].(string)
					suffix := "次播放"
					if tc.order == 4 {
						suffix = "分"
					} else if tc.order == 3 {
						suffix = "追番"
					}
					if !strings.HasSuffix(label, suffix) {
						t.Fatal("order label")
					}
				}
			}
		}
		for _, tc := range []struct {
			query           string
			count           int
			num, size, next float64
		}{{"page=1&pagesize=2", 2, 1, 2, 1}, {"page=2&pagesize=2", 1, 2, 2, 0}, {"page=99&pagesize=2", 0, 99, 2, 0}, {"page=0&pagesize=2", 2, 0, 2, 1}, {"page=1&pagesize=0", 0, 1, 0, 1}, {"page=invalid&pagesize=invalid", 3, 1, 24, 0}, {"page=1&pagesize=-1", 2, 1, -1, 1}, {"page=1.5&pagesize=2", 2, 1.5, 2, 0}} {
			data := pgcPayload(t, srv, "/pgc/season/index/result?"+tc.query, "data")
			pgcFields(t, data, map[string]any{"num": tc.num, "size": tc.size, "has_next": tc.next})
			if len(pgcArray(t, data["list"])) != tc.count {
				t.Errorf("pagination %s", tc.query)
			}
		}
		// Public lists exclude hidden seasons; detail intentionally still recommends
		// same-type hidden seasons, as the mock does.
		result := pgcPayload(t, srv, "/pgc/view/web/season?season_id=40000", "result")
		if len(pgcArray(t, result["seasons"])) < 3 {
			t.Fatal("related seasons must include hidden rows")
		}
	})
}

func TestPgcFeedCursor(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		pgcExec(t, srv, "UPDATE seasons SET state=-1")
		err := srv.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error {
			for i := 0; i < 15; i++ {
				season := store.Season{SeasonID: int64(90000 + i), MediaID: int64(91000 + i), SeasonType: 1, Title: fmt.Sprintf("分页番剧%d", i), Follow: int64(100 - i), Areas: []string{}, Styles: []string{}}
				if err := store.InsertSeason(t.Context(), tx, &season); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/pgc/page/web/v3/feed", "/pgc/page/web/feed"} {
			for _, tc := range []struct {
				cursor string
				count  int
				next   float64
				more   bool
				first  float64
			}{{"0", 12, 12, true, 90000}, {"12", 3, 15, false, 90012}, {"15", 0, 15, false, 0}, {"-3", 0, -3, true, 0}, {"-20", 7, -13, true, 90000}, {"1.5", 12, 13.5, true, 90001}, {"oops", 12, 12, true, 90000}} {
				data := pgcPayload(t, srv, path+"?coursor="+tc.cursor, "data")
				rows := pgcArray(t, pgcObject(t, pgcArray(t, data["items"])[0])["sub_items"])
				pgcFields(t, data, map[string]any{"coursor": tc.next, "has_next": tc.more})
				if len(rows) != tc.count {
					t.Fatalf("%s count=%d", tc.cursor, len(rows))
				}
				if len(rows) > 0 {
					row := pgcObject(t, rows[0])
					pgcFields(t, row, map[string]any{"season_id": tc.first})
					if _, ok := row["episode_id"]; ok {
						t.Fatal("undefined episode_id should be omitted")
					}
				}
			}
			ignored := pgcPayload(t, srv, path+"?cursor=12", "data")
			pgcFields(t, ignored, map[string]any{"coursor": float64(12)})
		}
	})
}

func TestPgcTimelineContract(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := login(t, srv, "jumedia", "admin123")
		for _, tc := range []struct {
			query string
			count int
			today int
			kind  int
		}{{"", 13, 6, 1}, {"types=4&before=1&after=2", 4, 1, 4}, {"types=1&before=0&after=0", 1, 0, 1}, {"types=999&before=1&after=1", 3, 1, 999}, {"before=-2&after=0", 0, -1, 1}} {
			response := get(t, srv, "/pgc/web/timeline?"+tc.query, session)
			assertCode(t, response, 0)
			days := pgcArray(t, response["result"])
			if len(days) != tc.count {
				t.Fatalf("timeline days: %d", len(days))
			}
			for i, item := range days {
				day := pgcObject(t, item)
				stamp := int64(day["date_ts"].(float64))
				date := time.Unix(stamp, 0).In(time.Local)
				today := float64(0)
				if i == tc.today {
					today = 1
				}
				weekday := (int(date.Weekday())+6)%7 + 1
				pgcFields(t, day, map[string]any{"date": fmt.Sprintf("%d-%d", date.Month(), date.Day()), "day_of_week": float64(weekday), "is_today": today})
				if i > 0 && stamp-int64(pgcObject(t, days[i-1])["date_ts"].(float64)) != 86400 {
					t.Fatal("timeline stride")
				}
				for _, item := range pgcArray(t, day["episodes"]) {
					ep := pgcObject(t, item)
					season := pgcSeedSeason(t, srv, int64(ep["season_id"].(float64)))
					if season.SeasonType != tc.kind || season.Weekday != weekday {
						t.Fatal("timeline type/weekday")
					}
					published, followed := float64(0), float64(0)
					if i <= tc.today {
						published = 1
					}
					if season.SeasonID == 40000 || season.SeasonID == 40066 {
						followed = 1
					}
					pgcFields(t, ep, map[string]any{"episode_id": float64(season.Episodes[len(season.Episodes)-1].EpID), "pub_time": season.PubTime, "published": published, "follow": followed, "square_cover": season.Cover})
					wantCover := season.Episodes[len(season.Episodes)-1].Cover
					if wantCover == "" {
						wantCover = season.Horizontal
					}
					if wantCover == "" {
						wantCover = season.Cover
					}
					if ep["ep_cover"] != wantCover {
						t.Fatalf("timeline ep_cover: %v", ep["ep_cover"])
					}
					index := "已完结"
					if !season.IsFinish {
						n := len(season.Episodes)
						if i > tc.today {
							n++
						}
						index = fmt.Sprintf("第%d话", n)
					}
					if ep["pub_index"] != index {
						t.Fatal("timeline pub_index")
					}
				}
			}
		}
	})
}
