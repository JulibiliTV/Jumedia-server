package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"jumedia-server/internal/store"
)

func discoverData(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	assertCode(t, v, 0)
	d, ok := v["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not object: %v", v)
	}
	return d
}
func discoverFields(t *testing.T, v map[string]any, want map[string]any) {
	t.Helper()
	for k, x := range want {
		if !reflect.DeepEqual(v[k], x) {
			t.Fatalf("%s = %#v; want %#v", k, v[k], x)
		}
	}
}
func discoverExec(t *testing.T, s *Server, q string, args ...any) {
	t.Helper()
	if _, err := s.Store.DB.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func discoverArchives(t *testing.T, s *Server) []store.Archive {
	t.Helper()
	as, err := s.Store.PublishedArchives(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return as
}
func discoverFailure(t *testing.T, v map[string]any, code int, message string) {
	t.Helper()
	assertCode(t, v, code)
	discoverFields(t, v, map[string]any{"message": message, "ttl": float64(1), "data": nil})
}

func TestDiscoverFeedAndLists(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		as := discoverArchives(t, s)
		session := login(t, s, "jumedia", "admin123")
		follows, err := s.Store.ListRelation(context.Background(), "WHERE mid=?", session.MID)
		if err != nil {
			t.Fatal(err)
		}
		followed := map[int64]bool{}
		for _, r := range follows {
			followed[r.FID] = true
		}
		for _, idx := range []int{0, 17, 93} {
			mixed := append([]store.Archive{}, as...)
			sort.SliceStable(mixed, func(i, j int) bool {
				return (mixed[i].AID*2654435761+int64(idx))%1000 < (mixed[j].AID*2654435761+int64(idx))%1000
			})
			path := fmt.Sprintf("/x/v2/feed/index?idx=%d&sign=ignored&appkey=ignored", idx)
			for _, auth := range []*store.Session{nil, session} {
				d := discoverData(t, get(t, s, path, auth))
				rows := d["items"].([]any)
				if len(rows) != 20 || d["config"].(map[string]any)["column"] != float64(4) {
					t.Fatal(d)
				}
				for i, row := range rows {
					v := row.(map[string]any)
					a := mixed[(idx+i)%len(mixed)]
					discoverFields(t, v, map[string]any{"param": strconv.FormatInt(a.AID, 10), "bvid": a.BVID, "uri": "/video/" + a.BVID, "card_type": "small_cover_v2", "title": a.Title})
					discoverFields(t, v["player_args"].(map[string]any), map[string]any{"aid": float64(a.AID), "cid": float64(a.Pages[0].CID), "type": "av"})
					reason, ok := v["rcmd_reason"]
					if ok != (auth != nil && followed[a.MID]) {
						t.Fatalf("wrong follow marker: %v", v)
					}
					if ok && reason.(map[string]any)["content"] != "已关注" {
						t.Fatal(reason)
					}
				}
			}
		}
		v := post(t, s, "/x/feed/dislike", url.Values{"id": {"1"}})
		assertCode(t, v, 0)
		if v["data"] != nil {
			t.Fatal(v)
		}
		sort.SliceStable(as, func(i, j int) bool { return as[i].Stat.View > as[j].Stat.View })
		d := discoverData(t, get(t, s, "/x/web-interface/popular?pn=2&ps=3"))
		rows := d["list"].([]any)
		if len(rows) != 3 || d["no_more"] != false {
			t.Fatal(d)
		}
		for i, row := range rows {
			a := as[i+3]
			v := row.(map[string]any)
			reason := ""
			if a.Stat.View > 500000 {
				reason = "百万播放"
			}
			discoverFields(t, v, map[string]any{"aid": float64(a.AID), "rcmd_reason": map[string]any{"content": reason}})
			if v["owner"].(map[string]any)["name"] == "" || len(v["pages"].([]any)) != len(a.Pages) {
				t.Fatal(v)
			}
		}
		d = discoverData(t, get(t, s, "/x/web-interface/popular?pn=9999"))
		if len(d["list"].([]any)) != 0 || d["no_more"] != true {
			t.Fatal(d)
		}
		d = discoverData(t, get(t, s, "/x/web-interface/popular?ps=-1"))
		if len(d["list"].([]any)) != len(as)-1 {
			t.Fatal("negative Array.slice semantics")
		}
		d = discoverData(t, get(t, s, "/x/web-interface/popular?pn=1.5&ps=3"))
		if len(d["list"].([]any)) != 3 || d["list"].([]any)[0].(map[string]any)["aid"] != float64(as[1].AID) {
			t.Fatal("fractional paging")
		}
		d = discoverData(t, get(t, s, "/x/web-interface/popular?ps=0"))
		if len(d["list"].([]any)) != 0 || d["no_more"] != false {
			t.Fatal(d)
		}
		rid := as[0].TID
		filtered := []store.Archive{}
		for _, a := range as {
			if a.TID == rid {
				filtered = append(filtered, a)
			}
		}
		score := func(a store.Archive) int64 { return a.Stat.View + a.Stat.Like*10 + a.Stat.Coin*20 }
		sort.SliceStable(filtered, func(i, j int) bool { return score(filtered[i]) > score(filtered[j]) })
		d = discoverData(t, get(t, s, fmt.Sprintf("/x/web-interface/ranking/v2?rid=%d", rid)))
		rows = d["list"].([]any)
		if len(rows) != len(filtered) || d["note"] != "根据稿件内容质量、近期的数据综合展示，动态更新" {
			t.Fatal(d)
		}
		for i, row := range rows {
			discoverFields(t, row.(map[string]any), map[string]any{"aid": float64(filtered[i].AID), "score": float64(score(filtered[i]))})
		}
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Pubdate > filtered[j].Pubdate })
		d = discoverData(t, get(t, s, fmt.Sprintf("/x/web-interface/region/feed/rcmd?from_region=%d&display_id=2&request_cnt=1", rid)))
		rows = d["archives"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["aid"] != float64(filtered[1].AID) {
			t.Fatal(d)
		}
		for _, path := range []string{"/x/web-interface/region/feed/rcmd", fmt.Sprintf("/x/web-interface/region/feed/rcmd?from_region=%d&display_id=999", rid)} {
			discoverFailure(t, get(t, s, path), 62013, "没有更多了")
		}
		d = discoverData(t, get(t, s, "/x/web-interface/ranking/v2?rid=999999"))
		if len(d["list"].([]any)) != 0 {
			t.Fatal(d)
		}
		// Discovery excludes unpublished archives and all archives by banned owners.
		discoverExec(t, s, "UPDATE users SET status=1 WHERE mid=?", as[0].MID)
		d = discoverData(t, get(t, s, "/x/web-interface/popular?ps=1000"))
		for _, row := range d["list"].([]any) {
			v := row.(map[string]any)
			if v["state"] != float64(0) || v["owner"].(map[string]any)["mid"] == float64(as[0].MID) {
				t.Fatal(v)
			}
		}
		// Empty feed and invalid array indexes throw in the Mock, hence -500.
		assertCode(t, get(t, s, "/x/v2/feed/index?idx=-1"), -500)
		assertCode(t, get(t, s, "/x/v2/feed/index?idx=0.5"), -500)
		discoverExec(t, s, "UPDATE archives SET state=-1")
		assertCode(t, get(t, s, "/x/v2/feed/index"), -500)
	})
}

func TestDiscoverDynamic(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		path := "/x/polymer/web-dynamic/v1/feed/all"
		discoverFailure(t, get(t, s, path), -101, "账号未登录")
		session := login(t, s, "jumedia", "admin123")
		follows, err := s.Store.ListRelation(context.Background(), "WHERE mid=?", session.MID)
		if err != nil {
			t.Fatal(err)
		}
		followed := map[int64]bool{}
		for _, r := range follows {
			followed[r.FID] = true
		}
		expected := []store.Archive{}
		for _, a := range discoverArchives(t, s) {
			if followed[a.MID] {
				expected = append(expected, a)
			}
		}
		sort.SliceStable(expected, func(i, j int) bool { return expected[i].Pubdate > expected[j].Pubdate })
		offset := 0
		for {
			d := discoverData(t, get(t, s, fmt.Sprintf("%s?offset=%d&access_key=%s", path, offset, session.Access)))
			rows := d["items"].([]any)
			if len(rows) != min(12, len(expected)-offset) {
				t.Fatal(d)
			}
			for i, row := range rows {
				a := expected[offset+i]
				v := row.(map[string]any)
				discoverFields(t, v, map[string]any{"id_str": strconv.FormatInt(a.AID*31, 10), "type": "DYNAMIC_TYPE_AV", "visible": true})
				modules := v["modules"].(map[string]any)
				discoverFields(t, modules["module_author"].(map[string]any), map[string]any{"mid": float64(a.MID), "pub_ts": float64(a.Pubdate), "pub_action": "投稿了视频", "jump_url": fmt.Sprintf("/space/%d", a.MID)})
				arc := modules["module_dynamic"].(map[string]any)["major"].(map[string]any)["archive"].(map[string]any)
				discoverFields(t, arc, map[string]any{"aid": strconv.FormatInt(a.AID, 10), "bvid": a.BVID, "jump_url": "/video/" + a.BVID})
				if _, ok := arc["stat"].(map[string]any)["play"].(string); !ok {
					t.Fatal(arc)
				}
				if modules["module_stat"].(map[string]any)["like"].(map[string]any)["count"] != float64(a.Stat.Like) {
					t.Fatal(modules)
				}
			}
			offset += len(rows)
			discoverFields(t, d, map[string]any{"offset": strconv.Itoa(offset), "has_more": offset < len(expected), "update_num": float64(0)})
			if offset == len(expected) {
				break
			}
		}
		d := discoverData(t, get(t, s, path+"?offset=9999", session))
		if len(d["items"].([]any)) != 0 || d["offset"] != "9999" || d["has_more"] != false {
			t.Fatal(d)
		}
		discoverExec(t, s, "DELETE FROM relations WHERE mid=?", session.MID)
		d = discoverData(t, get(t, s, path, session))
		if len(d["items"].([]any)) != 0 || d["has_more"] != false {
			t.Fatal(d)
		}
		discoverExec(t, s, "UPDATE users SET status=1 WHERE mid=?", session.MID)
		discoverFailure(t, get(t, s, path, session), -403, "账号已被封禁")
	})
}

func TestDiscoverSearch(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		d := discoverData(t, get(t, s, "/x/web-interface/search/all/v2?page=999&keyword=%20%20"))
		discoverFields(t, d, map[string]any{"page": float64(1), "pagesize": float64(20), "numResults": float64(len(discoverArchives(t, s))), "numPages": math.Ceil(float64(len(discoverArchives(t, s))) / 20)})
		if d["seid"] == "" {
			t.Fatal(d)
		}
		groups := d["result"].([]any)
		if len(groups) != 5 {
			t.Fatal(d)
		}
		kinds := []string{"media_bangumi", "media_ft", "bili_user", "live_room", "video"}
		for i, group := range groups {
			g := group.(map[string]any)
			if g["result_type"] != kinds[i] {
				t.Fatal(g)
			}
			rows := g["data"].([]any)
			if len(rows) == 0 {
				t.Fatal(g)
			}
			if kinds[i] == "bili_user" && len(rows) != 1 {
				t.Fatal(g)
			}
			if kinds[i] == "live_room" && len(rows) > 4 {
				t.Fatal(g)
			}
			if kinds[i] == "video" && len(rows) != 20 {
				t.Fatal(g)
			}
			typed := discoverData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type="+kinds[i]+"&page_size=2&w_rid=ignored&wts=0"))
			if typed["numResults"] != d["top_tlist"].(map[string]any)[kinds[i]] {
				t.Fatal(typed)
			}
			result := typed["result"]
			if kinds[i] == "live_room" {
				result = result.(map[string]any)["live_room"]
				info := typed["pageinfo"].(map[string]any)["live_room"].(map[string]any)
				discoverFields(t, info, map[string]any{"numResults": typed["numResults"], "numPages": typed["numPages"]})
			}
			if len(result.([]any)) != 2 {
				t.Fatal(typed)
			}
			v := result.([]any)[0].(map[string]any)
			switch kinds[i] {
			case "video":
				if v["type"] != "video" || v["aid"] != v["id"] || v["bvid"] == "" || v["duration"] == "" || v["typename"] == "" {
					t.Fatal(v)
				}
			case "bili_user":
				if v["type"] != "bili_user" || v["uname"] == "" || v["videos"] == nil || v["fans"] == nil {
					t.Fatal(v)
				}
			case "live_room":
				if v["roomid"] != v["room_id"] || v["uname"] == "" || v["cover"] == "" {
					t.Fatal(v)
				}
			default:
				if v["season_id"] == nil || v["first_ep"].(map[string]any)["ep_id"] == nil || v["new_ep"].(map[string]any)["id"] == nil {
					t.Fatal(v)
				}
			}
		}
		for _, kind := range append(kinds, "unknown", "") {
			path := "/x/web-interface/wbi/search/type?search_type=" + kind + "&keyword=" + url.QueryEscape("a' OR 1=1 -- 無匹配")
			d = discoverData(t, get(t, s, path))
			discoverFields(t, d, map[string]any{"numResults": float64(0), "numPages": float64(1)})
			result := d["result"]
			if kind == "live_room" {
				result = result.(map[string]any)["live_room"]
			}
			if len(result.([]any)) != 0 {
				t.Fatal(d)
			}
		}
		d = discoverData(t, get(t, s, "/x/web-interface/search/all/v2?keyword=NoMatch123"))
		if d["numPages"] != float64(0) || d["numResults"] != float64(0) {
			t.Fatal(d)
		}
		d = discoverData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=video&page_size=0"))
		if d["numPages"] != nil || len(d["result"].([]any)) != 0 {
			t.Fatal(d)
		}
		d = discoverData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=video&page=9999"))
		if len(d["result"].([]any)) != 0 {
			t.Fatal(d)
		}
		// Matching ignores case; highlighting is exact-case and replaces every hit.
		as := discoverArchives(t, s)
		discoverExec(t, s, "UPDATE archives SET title=?,description=?,tag=? WHERE aid=?", "MiXeD MiXeD", "uniqueDesc", "uniqueTag", as[0].AID)
		for _, kw := range []string{"mixed", "MiXeD", "uniqueDesc", "uniqueTag"} {
			d = discoverData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=video&keyword="+kw))
			if d["numResults"] != float64(1) {
				t.Fatal(d)
			}
			title := "MiXeD MiXeD"
			if kw == "MiXeD" {
				title = `<em class="keyword">MiXeD</em> <em class="keyword">MiXeD</em>`
			}
			if d["result"].([]any)[0].(map[string]any)["title"] != title {
				t.Fatal(d)
			}
		}
		discoverExec(t, s, "UPDATE users SET sign=? WHERE mid=?", "OnlyInSignature", 2)
		d = discoverData(t, get(t, s, "/x/web-interface/search/all/v2?keyword=OnlyInSignature"))
		if d["top_tlist"].(map[string]any)["bili_user"] != float64(0) {
			t.Fatal(d)
		}
		d = discoverData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=bili_user&keyword=OnlyInSignature"))
		if d["numResults"] != float64(1) {
			t.Fatal(d)
		}
		seasons, err := s.Store.ListSeason(context.Background(), "WHERE season_type=1 ORDER BY season_id")
		if err != nil {
			t.Fatal(err)
		}
		discoverExec(t, s, "UPDATE seasons SET styles=? WHERE season_id=?", `["OnlyInStyles"]`, seasons[0].SeasonID)
		d = discoverData(t, get(t, s, "/x/web-interface/search/all/v2?keyword=OnlyInStyles"))
		if d["top_tlist"].(map[string]any)["media_bangumi"] != float64(1) {
			t.Fatal(d)
		}
		d = discoverData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=media_bangumi&keyword=OnlyInStyles"))
		if d["numResults"] != float64(0) {
			t.Fatal(d)
		}
		for _, kw := range []string{"", "%20", "MiXeD", "mixed", "NoMatch123"} {
			v := get(t, s, "/x/web-interface/suggest?term="+kw)
			assertCode(t, v, 0)
			if len(v) != 2 || v["data"] != nil {
				t.Fatal("suggest must have only code/result")
			}
			tags := v["result"].(map[string]any)["tag"].([]any)
			if kw == "MiXeD" || kw == "mixed" {
				if len(tags) != 1 {
					t.Fatal(v)
				}
				discoverFields(t, tags[0].(map[string]any), map[string]any{"value": "MiXeD MiXeD", "term": "MiXeD MiXeD", "ref": float64(0), "spid": float64(0)})
			} else if len(tags) != 0 {
				t.Fatal(v)
			}
		}
		// Suggestions intentionally include hidden seasons and banned user names.
		discoverExec(t, s, "UPDATE seasons SET title=?,state=-1 WHERE season_id=?", "HiddenSuggest", seasons[0].SeasonID)
		discoverExec(t, s, "UPDATE users SET uname=?,status=1 WHERE mid=?", "HiddenSuggestUser", 2)
		v := get(t, s, "/x/web-interface/suggest?term=HiddenSuggest")
		assertCode(t, v, 0)
		if len(v["result"].(map[string]any)["tag"].([]any)) != 2 {
			t.Fatal(v)
		}
		d = discoverData(t, get(t, s, "/x/web-interface/search/all/v2?keyword=HiddenSuggest"))
		for kind, n := range d["top_tlist"].(map[string]any) {
			// Room search checks room.banned, not the owner's account status.
			if kind != "live_room" && n != float64(0) {
				t.Fatal(d["top_tlist"])
			}
		}
		discoverExec(t, s, "UPDATE rooms SET banned=1")
		d = discoverData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=live_room"))
		if d["numResults"] != float64(0) {
			t.Fatal(d)
		}
	})
}

func TestDiscoverViewAndPlayer(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		as := discoverArchives(t, s)
		a := as[0]
		// Subtitle and multi-part behavior is independent of catalog demo content.
		for i := 1; i <= 2; i++ {
			p := a.Pages[0]
			p.CID = 900000 + int64(i)
			p.Position = i
			if err := store.InsertPage(t.Context(), s.Store.DB, &p); err != nil {
				t.Fatal(err)
			}
		}
		discoverExec(t, s, "UPDATE archives SET subtitles=?,chapters=? WHERE aid=?", `[{"lan":"zh-CN","lan_doc":"中文（中国）","body":[{"from":0,"to":4,"content":"测试字幕"}]}]`, `[{"from":0,"to":8,"content":"开场"},{"from":8,"to":16,"content":"正片"},{"from":16,"to":24,"content":"片尾"}]`, a.AID)
		updated, err := s.Store.ArchiveByID(t.Context(), a.AID)
		if err != nil {
			t.Fatal(err)
		}
		a = *updated

		admin := login(t, s, "jumedia", "admin123")
		owner := login(t, s, "oolong", "demo123")
		other := login(t, s, "lumen", "demo123")
		for _, q := range []string{"aid=" + strconv.FormatInt(a.AID, 10), "oid=" + strconv.FormatInt(a.AID, 10), "rid=" + strconv.FormatInt(a.AID, 10), "avid=" + strconv.FormatInt(a.AID, 10), "bvid=" + a.BVID + "&aid=0"} {
			d := discoverData(t, get(t, s, "/x/web-interface/view?"+q))
			discoverFields(t, d, map[string]any{"aid": float64(a.AID), "bvid": a.BVID, "cid": float64(a.Pages[0].CID), "videos": float64(3), "state": float64(0)})
			if len(d["pages"].([]any)) != 3 || d["owner"].(map[string]any)["mid"] != float64(a.MID) {
				t.Fatal(d)
			}
			first := d["pages"].([]any)[0].(map[string]any)
			discoverFields(t, first, map[string]any{"cid": float64(a.Pages[0].CID), "page": float64(1), "from": "vupload", "part": a.Pages[0].Part})
			if first["dimension"].(map[string]any)["width"] != float64(1280) {
				t.Fatal(first)
			}
		}
		for _, path := range []string{"/x/web-interface/view?aid=0", "/x/web-interface/view?aid=100100.1", "/x/web-interface/view?bvid=invalid", "/x/web-interface/view/detail?aid=0", "/x/web-interface/view/detail?bvid=" + a.BVID, "/x/web-interface/view?aid=&oid=100100"} {
			discoverFailure(t, get(t, s, path), -404, "啥都木有")
		}
		for _, route := range []string{"/x/web-interface/view", "/x/web-interface/view/detail"} {
			path := route + "?aid=100999"
			discoverFailure(t, get(t, s, path), -404, "啥都木有")
			discoverFailure(t, get(t, s, path, other), -404, "啥都木有")
			for _, session := range []*store.Session{owner, admin} {
				assertCode(t, get(t, s, path, session), 0)
			}
		}
		d := discoverData(t, get(t, s, fmt.Sprintf("/x/web-interface/view/detail?aid=%d", a.AID), admin))
		card := d["Card"].(map[string]any)
		discoverFields(t, card["card"].(map[string]any), map[string]any{"mid": strconv.FormatInt(a.MID, 10), "name": "流明实验室"})
		if card["following"] != true || card["follower"] != card["card"].(map[string]any)["fans"] || card["archive_count"] == nil || card["card"].(map[string]any)["level_info"] == nil {
			t.Fatal(card)
		}
		tags := d["Tags"].([]any)
		if len(tags) != len(strings.Split(a.Tag, ",")) || tags[0].(map[string]any)["tag_name"] != strings.Split(a.Tag, ",")[0] {
			t.Fatal(tags)
		}
		related := d["Related"].([]any)
		if len(related) != 12 || related[0].(map[string]any)["tid"] != float64(a.TID) {
			t.Fatal("related ordering")
		}
		seenOther := false
		last := math.Inf(1)
		for _, row := range related {
			v := row.(map[string]any)
			if v["aid"] == float64(a.AID) {
				t.Fatal("self related")
			}
			same := v["tid"] == float64(a.TID)
			if !same && !seenOther {
				seenOther = true
				last = math.Inf(1)
			}
			if same && seenOther {
				t.Fatal("same region after other")
			}
			views := v["stat"].(map[string]any)["view"].(float64)
			if views > last {
				t.Fatal("related views")
			}
			last = views
		}
		playerPath := fmt.Sprintf("/x/player/wbi/v2?aid=%d&cid=%d&w_rid=invalid&wts=0", a.AID, a.Pages[1].CID)
		d = discoverData(t, get(t, s, playerPath))
		discoverFields(t, d, map[string]any{"aid": float64(a.AID), "cid": float64(a.Pages[1].CID), "login_mid": float64(0), "last_play_time": float64(0), "last_play_cid": float64(0), "online_count": float64(1 + a.Pages[1].CID%97)})
		subs := d["subtitle"].(map[string]any)
		if subs["allow_submit"] != false || len(subs["subtitles"].([]any)) != 1 {
			t.Fatal(subs)
		}
		sub := subs["subtitles"].([]any)[0].(map[string]any)
		discoverFields(t, sub, map[string]any{"id": float64(a.AID * 10), "id_str": strconv.FormatInt(a.AID*10, 10), "lan": "zh-CN", "lan_doc": "中文（中国）", "subtitle_url": fmt.Sprintf("http://example.com/bfs/subtitle/%d-0.json", a.AID), "type": float64(0)})
		chapters := d["view_points"].([]any)
		if len(chapters) != 3 {
			t.Fatal(d)
		}
		discoverFields(t, chapters[0].(map[string]any), map[string]any{"type": float64(2), "from": float64(0), "to": float64(8), "content": "开场", "imgUrl": a.Pic})
		for _, progress := range []int{-1, 0, 14} {
			discoverExec(t, s, "UPDATE history SET progress=?,cid=? WHERE mid=? AND aid=?", progress, a.Pages[2].CID, admin.MID, a.AID)
			d = discoverData(t, get(t, s, playerPath, admin))
			discoverFields(t, d, map[string]any{"login_mid": float64(1), "last_play_time": float64(max(0, progress) * 1000), "last_play_cid": float64(a.Pages[2].CID)})
		}
		d = discoverData(t, get(t, s, "/x/player/wbi/v2?aid=999999&cid=4"))
		discoverFields(t, d, map[string]any{"bvid": "", "last_play_time": float64(0), "online_count": float64(5)})
		if len(d["subtitle"].(map[string]any)["subtitles"].([]any)) != 0 || len(d["view_points"].([]any)) != 0 {
			t.Fatal(d)
		}
		for _, suffix := range []string{fmt.Sprintf("%d-0.json", a.AID), fmt.Sprintf("%d-99.json", a.AID), "999999-0.json", "999999999999999999999999999999-0.json"} {
			raw := get(t, s, "/bfs/subtitle/"+suffix)
			if _, ok := raw["code"]; ok {
				t.Fatal(raw)
			}
			discoverFields(t, raw, map[string]any{"font_size": 0.4, "font_color": "#FFFFFF", "background_alpha": 0.5, "background_color": "#9C27B0", "Stroke": "none"})
			n := 0
			if suffix == fmt.Sprintf("%d-0.json", a.AID) {
				n = 1
			}
			if len(raw["body"].([]any)) != n {
				t.Fatal(raw)
			}
			if n > 0 {
				discoverFields(t, raw["body"].([]any)[0].(map[string]any), map[string]any{"from": float64(0), "to": float64(4), "content": a.Subtitles[0].Body[0].Content})
			}
		}
		for _, suffix := range []string{"x-0.json", "100100--1.json", "100100-0.txt"} {
			discoverFailure(t, get(t, s, "/bfs/subtitle/"+suffix), -404, "接口不存在")
		}
		d = discoverData(t, get(t, s, "/x/player/videoshot?cid=unknown"))
		discoverFields(t, d, map[string]any{"pvdata": "", "img_x_len": float64(0), "img_y_len": float64(0), "img_x_size": float64(0), "img_y_size": float64(0), "image": []any{}, "index": []any{}})
	})
}

func TestDiscoverPlayURL(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		a := discoverArchives(t, s)[0]
		p := a.Pages[0]
		session := login(t, s, "jumedia", "admin123")
		path := fmt.Sprintf("/x/player/wbi/playurl?cid=%d", p.CID)
		plays := int64(0)
		count := func() {
			t.Helper()
			got, err := s.Store.ArchiveByID(context.Background(), a.AID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Stat.View != a.Stat.View+plays {
				t.Fatalf("views %d; want %d", got.Stat.View, a.Stat.View+plays)
			}
		}
		for _, tc := range []struct {
			qn      string
			quality float64
		}{{"", 80}, {"&qn=120", 80}, {"&qn=79", 64}, {"&qn=64", 64}, {"&qn=63", 32}, {"&qn=0", 32}, {"&qn=-1", 32}, {"&qn=invalid", 80}} {
			d := discoverData(t, get(t, s, path+tc.qn+"&w_rid=unchecked&wts=0"))
			plays++
			discoverFields(t, d, map[string]any{"from": "local", "result": "suee", "format": "mp4", "quality": tc.quality, "timelength": float64(p.Duration * 1000), "video_codecid": float64(7), "accept_format": "mp4,mp4720,mp4480", "accept_quality": []any{float64(80), float64(64), float64(32)}, "accept_description": []any{"1080P 高清", "720P 准高清", "480P 标清"}})
			durl := d["durl"].([]any)
			if len(durl) != 1 {
				t.Fatal(d)
			}
			discoverFields(t, durl[0].(map[string]any), map[string]any{"order": float64(1), "length": float64(p.Duration * 1000), "size": float64(0), "ahead": "", "vhead": "", "url": "http://example.com/media/emby/item0_0.mp4?source=source_item0_0", "backup_url": []any{}})
			formats := d["support_formats"].([]any)
			if len(formats) != 3 {
				t.Fatal(d)
			}
			discoverFields(t, formats[1].(map[string]any), map[string]any{"quality": float64(64), "format": "flv720", "new_description": "720P 准高清", "display_desc": "720P"})
			count()
		}
		if err := s.Store.SetSetting(context.Background(), "allowGuestPlay", false); err != nil {
			t.Fatal(err)
		}
		discoverFailure(t, get(t, s, path), -101, "请登录后观看")
		count()
		for _, q := range []string{"", "?cid=99999999", "?cid=50001.1", "?aid=100100"} {
			discoverFailure(t, get(t, s, "/x/player/wbi/playurl"+q), -404, "视频不存在")
		}
		episodes, err := s.Store.ListEpisode(context.Background(), "ORDER BY ep_id LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		discoverFailure(t, get(t, s, fmt.Sprintf("/x/player/wbi/playurl?cid=%d", episodes[0].CID)), -404, "视频不存在")
		assertCode(t, get(t, s, path+"&access_key="+session.Access), 0)
		plays++
		count()
		// Playurl intentionally checks session presence, not Login(), like the Mock.
		discoverExec(t, s, "UPDATE users SET status=1 WHERE mid=?", session.MID)
		assertCode(t, get(t, s, path, session), 0)
		plays++
		count()
		if err := s.Store.SetSetting(context.Background(), "allowGuestPlay", true); err != nil {
			t.Fatal(err)
		}
		draft, err := s.Store.ArchiveByID(context.Background(), 100999)
		if err != nil {
			t.Fatal(err)
		}
		assertCode(t, get(t, s, fmt.Sprintf("/x/player/wbi/playurl?cid=%d", draft.Pages[0].CID)), 0)
		draftAfter, err := s.Store.ArchiveByID(context.Background(), 100999)
		if err != nil {
			t.Fatal(err)
		}
		if draftAfter.Stat.View != draft.Stat.View {
			t.Fatal("draft count incremented")
		}
		for _, tc := range []struct{ media, origin, host, proto, want string }{
			{"/upload/a.mp4", "https://public.test/", "", "", "https://public.test/upload/a.mp4"},
			{"upload/a.mp4", "", "edge.test, ignored.test", "https, http", "https://edge.test/upload/a.mp4"},
			{"//cdn.test/a.mp4", "", "edge.test", "https", "https://cdn.test/a.mp4"},
			{"https://cdn.test/a.mp4?token=opaque", "", "edge.test", "https", "https://cdn.test/a.mp4?token=opaque"},
			{"/upload/test.mp4", "", "", "", "http://example.com/upload/test.mp4"},
		} {
			discoverExec(t, s, "UPDATE archive_pages SET media=? WHERE cid=?", tc.media, p.CID)
			s.Config.PublicOrigin = tc.origin
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("X-Forwarded-Host", tc.host)
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			var v map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			d := discoverData(t, v)
			if d["durl"].([]any)[0].(map[string]any)["url"] != tc.want {
				t.Fatal(d)
			}
			plays++
			count()
		}
		discoverExec(t, s, "UPDATE archives SET subtitles=? WHERE aid=?", `[{"lan":"zh-CN","lan_doc":"中文","body":[]}]`, a.AID)
		s.Config.PublicOrigin = "https://media.test/"
		d := discoverData(t, get(t, s, fmt.Sprintf("/x/player/wbi/v2?aid=%d", a.AID)))
		subtitleURL := d["subtitle"].(map[string]any)["subtitles"].([]any)[0].(map[string]any)["subtitle_url"].(string)
		if !strings.HasPrefix(subtitleURL, "https://media.test/bfs/subtitle/") {
			t.Fatal(subtitleURL)
		}
	})
}

func TestDiscoverMockCoercion(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		// Number('1_0') is NaN in JavaScript and uses the default (20).
		d := discoverData(t, get(t, s, "/x/web-interface/popular?ps=1_0"))
		if len(d["list"].([]any)) != 20 {
			t.Fatalf("Go numeric literal accepted as JS Number: %d", len(d["list"].([]any)))
		}
		a := discoverArchives(t, s)[0]
		// The Mock decoder ignores the prefix's first three characters.
		d = discoverData(t, get(t, s, "/x/web-interface/view?bvid=abc"+a.BVID[3:]))
		if d["aid"] != float64(a.AID) {
			t.Fatal(d)
		}
	})
}
