package api

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"jumedia-server/internal/store"
)

// A fixture with more than two archive pages, distinct orderings, non-ASCII
// titles, unpublished rows, and a disabled uploader catches filtering bugs.
func spaceTestLibrary(t *testing.T, s *Server) (int64, []int64) {
	t.Helper()
	mid := int64(90)
	if err := store.InsertUser(context.Background(), s.Store.DB, &store.User{MID: mid, Username: "space-fixture", Uname: "空间测试", Status: 1, Birthday: "2000-01-23"}); err != nil {
		t.Fatal(err)
	}
	aids := []int64{}
	for i := 0; i < 42; i++ {
		id := int64(900000 + i)
		state := 0
		if i == 41 {
			state = -1
		}
		a := store.Archive{AID: id, BVID: store.AV2BV(id), MID: mid, Title: fmt.Sprintf("视频 AbC %%_ %02d", i), Desc: "简介", TID: int64(1001 + i%2), Pubdate: int64(100 + i), State: state, Stat: store.ArchiveStat{View: int64(1000 - i), Favorite: int64(i), Like: int64(i * 2)}}
		if err := store.InsertArchive(context.Background(), s.Store.DB, &a); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertPage(context.Background(), s.Store.DB, &store.Page{CID: id * 10, AID: id, Position: 0, Part: "第一集", Duration: 65}); err != nil {
			t.Fatal(err)
		}
		aids = append(aids, id)
	}
	return mid, aids
}
func spaceTestAIDs(t *testing.T, raw any) []int64 {
	t.Helper()
	rows, ok := raw.([]any)
	if !ok {
		t.Fatalf("not array: %#v", raw)
	}
	out := []int64{}
	for _, v := range rows {
		out = append(out, int64(v.(map[string]any)["aid"].(float64)))
	}
	return out
}
func spaceTestWantIDs(t *testing.T, raw any, want []int64) {
	t.Helper()
	if got := spaceTestAIDs(t, raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("aids got %v want %v", got, want)
	}
}

func TestSpaceArchiveLists(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		mid, aids := spaceTestLibrary(t, s)
		query := "?mid=" + fmt.Sprint(mid)
		for _, tc := range []struct {
			order string
			want  []int64
		}{{"pubdate", []int64{aids[40], aids[39], aids[38]}}, {"click", aids[:3]}, {"stow", []int64{aids[40], aids[39], aids[38]}}, {"stat_view; DROP TABLE users", []int64{aids[40], aids[39], aids[38]}}} {
			d := spaceTestData(t, get(t, s, "/x/space/wbi/arc/search"+query+"&ps=3&order="+url.QueryEscape(tc.order)))
			list := d["list"].(map[string]any)
			spaceTestWantIDs(t, list["vlist"], tc.want)
			if d["page"].(map[string]any)["count"] != float64(41) {
				t.Fatal(d)
			}
			ts := list["tlist"].(map[string]any)
			if ts["1001"].(map[string]any)["count"] != float64(21) || ts["1002"].(map[string]any)["count"] != float64(20) {
				t.Fatal(ts)
			}
			row := list["vlist"].([]any)[0].(map[string]any)
			if row["length"] != "01:05" || row["duration"] != float64(65) || row["author"] != "空间测试" || row["cid"] == nil || row["bvid"] == nil || row["stat"].(map[string]any)["view"] == nil {
				t.Fatal(row)
			}
		}
		d := spaceTestData(t, get(t, s, "/x/space/wbi/arc/search"+query+"&tid=1002&keyword="+url.QueryEscape("aBc %_ 0")))
		list := d["list"].(map[string]any)
		spaceTestWantIDs(t, list["vlist"], []int64{aids[9], aids[7], aids[5], aids[3], aids[1]})
		if len(list["tlist"].(map[string]any)) != 1 || list["tlist"].(map[string]any)["1002"].(map[string]any)["count"] != float64(5) {
			t.Fatal(list)
		}
		d = spaceTestData(t, get(t, s, "/x/space/wbi/arc/search"+query+"&ps=2&pn=2&order=click"))
		spaceTestWantIDs(t, d["list"].(map[string]any)["vlist"], aids[2:4])
		// JS slice semantics: pn is clamped only for slicing, response echoes it;
		// negative sizes count from the end and decimals truncate slice indices.
		for _, tc := range []struct {
			q    string
			want []int64
		}{{"&ps=2&pn=0", aids[:2]}, {"&ps=0", []int64{}}, {"&ps=-2", aids[:39]}, {"&pn=1.5&ps=3", aids[1:4]}, {"&pn=100", []int64{}}} {
			d = spaceTestData(t, get(t, s, "/x/space/wbi/arc/search"+query+"&order=click"+tc.q))
			spaceTestWantIDs(t, d["list"].(map[string]any)["vlist"], tc.want)
		}
		d = spaceTestData(t, get(t, s, "/x/space/wbi/arc/search"+query+"&keyword="+url.QueryEscape("%' OR 1=1 --")))
		if len(d["list"].(map[string]any)["vlist"].([]any)) != 0 {
			t.Fatal("keyword was not literal")
		}
		d = spaceTestData(t, get(t, s, "/x/space/wbi/arc/search"+query+"&keyword=不存在"))
		if len(d["list"].(map[string]any)["tlist"].(map[string]any)) != 0 {
			t.Fatal("nonempty tlist")
		}
		cursor := "/x/v2/space/archive/cursor?vmid=" + fmt.Sprint(mid) + "&order=click"
		d = spaceTestData(t, get(t, s, cursor))
		spaceTestWantIDs(t, d["item"], aids[:20])
		if d["has_next"] != true || d["count"] != float64(41) {
			t.Fatal(d)
		}
		first := d["item"].([]any)[0].(map[string]any)
		if first["param"] != fmt.Sprint(aids[0]) || first["goto"] != "av" || first["ctime"] != float64(100) {
			t.Fatal(first)
		}
		d = spaceTestData(t, get(t, s, cursor+"&aid="+fmt.Sprint(aids[19])))
		spaceTestWantIDs(t, d["item"], aids[20:40])
		if d["has_next"] != true {
			t.Fatal(d)
		}
		d = spaceTestData(t, get(t, s, cursor+"&aid="+fmt.Sprint(aids[39])))
		spaceTestWantIDs(t, d["item"], aids[40:41])
		if d["has_next"] != false {
			t.Fatal(d)
		}
		d = spaceTestData(t, get(t, s, cursor+"&aid=99999999"))
		spaceTestWantIDs(t, d["item"], aids[:20])
		d = spaceTestData(t, get(t, s, "/x/v2/space/archive/cursor?vmid="+fmt.Sprint(mid)+"&order=stow"))
		if spaceTestAIDs(t, d["item"])[0] != aids[40] {
			t.Fatal("cursor stow should use pubdate")
		}
		top := spaceTestData(t, get(t, s, "/x/space/top/arc?vmid="+fmt.Sprint(mid)))
		if top["aid"] != float64(aids[0]) || top["reason"] != "代表作" || top["duration"] != float64(65) || top["owner"].(map[string]any)["mid"] != float64(mid) {
			t.Fatal(top)
		}
		masterpieces := get(t, s, "/x/space/masterpiece?vmid="+fmt.Sprint(mid))
		assertCode(t, masterpieces, 0)
		spaceTestWantIDs(t, masterpieces["data"], []int64{aids[39], aids[38], aids[37]})
	})
}

func TestSpaceProfilesAndEmptyReads(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		u := spaceTestUser(t, s, 1)
		d := spaceTestData(t, get(t, s, "/x/space/wbi/acc/info?mid=1", session))
		if d["mid"] != float64(1) || d["name"] != u.Uname || d["birthday"] != "06-12" || d["official"].(map[string]any)["title"] != "Jumedia 官方账号" || d["vip"].(map[string]any)["label"].(map[string]any)["text"] != "年度大会员" {
			t.Fatal(d)
		}
		rooms, err := s.Store.ListLiveRoom(context.Background(), "WHERE banned=0 ORDER BY room_id")
		if err != nil || len(rooms) == 0 {
			t.Fatalf("rooms: %v", err)
		}
		r := rooms[0]
		d = spaceTestData(t, get(t, s, "/x/space/wbi/acc/info?mid="+fmt.Sprint(r.UID)))
		room := d["live_room"].(map[string]any)
		if room["roomid"] != float64(r.RoomID) || room["roomStatus"] != float64(1) || room["liveStatus"] != float64(r.LiveStatus) || room["url"] != "/live/"+fmt.Sprint(r.RoomID) || room["watched_show"].(map[string]any)["text_large"] == nil {
			t.Fatal(room)
		}
		spaceTestExec(t, s, "UPDATE rooms SET banned=1 WHERE uid=?", r.UID)
		if spaceTestData(t, get(t, s, "/x/space/wbi/acc/info?mid="+fmt.Sprint(r.UID)))["live_room"] != nil {
			t.Fatal("banned room leaked")
		}
		// The card's likes include unpublished archives; archive_count excludes them.
		mid, aids := spaceTestLibrary(t, s)
		card := spaceTestData(t, get(t, s, "/x/web-interface/card?mid="+fmt.Sprint(mid)))
		if card["archive_count"] != float64(41) || card["like_num"] != float64(1722) || card["card"].(map[string]any)["mid"] != fmt.Sprint(mid) || card["card"].(map[string]any)["level_info"] == nil {
			t.Fatal(card)
		}
		spaceTestExec(t, s, "UPDATE archives SET state=-1 WHERE aid=?", aids[0])
		if spaceTestData(t, get(t, s, "/x/web-interface/card?mid="+fmt.Sprint(mid)))["archive_count"] != float64(40) {
			t.Fatal("card draft count")
		}
		for _, path := range []string{"/x/space/wbi/acc/info", "/x/web-interface/card"} {
			spaceTestError(t, get(t, s, path+"?mid=99999999"), -404, "啥都木有")
		}
		spaceTestError(t, get(t, s, "/x/space/top/arc?vmid=99999999"), 53016, "没有置顶视频")
		for _, path := range []string{"/x/relation/followings?vmid=99999999", "/x/space/wbi/acc/relation?mid=99999999", "/x/relation/stat?vmid=99999999", "/x/space/wbi/arc/search?mid=99999999", "/x/v2/space/archive/cursor?vmid=99999999", "/x/space/masterpiece?vmid=99999999", "/x/polymer/web-space/seasons_series_list?mid=99999999"} {
			assertCode(t, get(t, s, path), 0)
		}
		v := get(t, s, "/x/space/masterpiece?vmid=99999999")
		if rows, ok := v["data"].([]any); !ok || len(rows) != 0 {
			t.Fatal(v)
		}
		d = spaceTestData(t, get(t, s, "/x/v2/space/archive/cursor?vmid=99999999"))
		if len(d["item"].([]any)) != 0 || d["has_next"] != false || d["count"] != float64(0) {
			t.Fatal(d)
		}
		d = spaceTestData(t, get(t, s, "/x/relation/followings?vmid=1&pn=1000"))
		if len(d["list"].([]any)) != 0 || d["total"].(float64) == 0 {
			t.Fatal(d)
		}
	})
}

func TestSpaceCollections(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		mid, aids := spaceTestLibrary(t, s)
		ids := []int64{990001, 990002, 990003}
		for i, id := range ids {
			kind := "season"
			if i == 1 {
				kind = "series"
			}
			x := &store.Collection{ID: id, MID: mid, Kind: kind, Title: "测试合集", Intro: "简介", Cover: "cover", Ctime: 123}
			if err := store.InsertCollection(context.Background(), s.Store.DB, x); err != nil {
				t.Fatal(err)
			}
			if i < 2 {
				for pos := range aids {
					aid := aids[len(aids)-1-pos]
					if err := store.InsertCollectionItem(context.Background(), s.Store.DB, &store.CollectionItem{CollectionID: id, AID: aid, Position: pos}); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		listPath := "/x/polymer/web-space/seasons_series_list?mid=" + fmt.Sprint(mid)
		d := spaceTestData(t, get(t, s, listPath+"&page_num=2&page_size=1"))["items_lists"].(map[string]any)
		if len(d["seasons_list"].([]any)) != 0 || len(d["series_list"].([]any)) != 1 || d["page"].(map[string]any)["total"] != float64(3) {
			t.Fatal(d)
		}
		series := d["series_list"].([]any)[0].(map[string]any)
		if series["meta"].(map[string]any)["series_id"] != float64(ids[1]) || series["meta"].(map[string]any)["mtime"] != float64(123) || series["meta"].(map[string]any)["total"] != float64(41) {
			t.Fatal(series)
		}
		if series["recent_aids"].([]any)[0] != float64(aids[41]) {
			t.Fatal("recent_aids must include draft")
		}
		spaceTestWantIDs(t, series["archives"], []int64{aids[40], aids[39], aids[38], aids[37]})
		d = spaceTestData(t, get(t, s, listPath+"&page_size=1"))["items_lists"].(map[string]any)
		season := d["seasons_list"].([]any)[0].(map[string]any)
		if season["meta"].(map[string]any)["season_id"] != float64(ids[0]) || season["meta"].(map[string]any)["ptime"] != float64(123) {
			t.Fatal(season)
		}
		for _, tc := range []struct {
			path        string
			first, last int64
			pageKey     string
		}{
			{"/x/polymer/web-space/seasons_archives_list?season_id=" + fmt.Sprint(ids[0]), aids[40], aids[0], "page_num"},
			{"/x/polymer/web-space/seasons_archives_list?season_id=" + fmt.Sprint(ids[0]) + "&sort_reverse=true", aids[0], aids[40], "page_num"},
			{"/x/polymer/web-space/seasons_archives_list?season_id=" + fmt.Sprint(ids[0]) + "&sort_reverse=1", aids[0], aids[40], "page_num"},
			{"/x/series/archives?series_id=" + fmt.Sprint(ids[1]), aids[40], aids[0], "pn"},
			{"/x/series/archives?series_id=" + fmt.Sprint(ids[1]) + "&sort=asc", aids[0], aids[40], "pn"},
			// The selected collection's kind controls ordering, not the route name.
			{"/x/series/archives?series_id=" + fmt.Sprint(ids[0]) + "&sort=asc", aids[0], aids[40], "pn"},
		} {
			d = spaceTestData(t, get(t, s, tc.path+"&page_size=1&ps=1"))
			if len(d["archives"].([]any)) != 30 || spaceTestAIDs(t, d["archives"])[0] != tc.first || len(d["aids"].([]any)) != 41 || d["aids"].([]any)[40] != float64(tc.last) {
				t.Fatal("collection ordering/page size", tc.path)
			}
			meta, page := d["meta"].(map[string]any), d["page"].(map[string]any)
			if meta["season_id"] != meta["series_id"] || meta["mid"] != float64(mid) || meta["description"] != "简介" || page["size"] != float64(30) || page["page_size"] != float64(30) || page["total"] != float64(41) {
				t.Fatal(d)
			}
			d = spaceTestData(t, get(t, s, tc.path+"&"+tc.pageKey+"=2"))
			if len(d["archives"].([]any)) != 11 || d["page"].(map[string]any)["num"] != float64(2) || len(d["aids"].([]any)) != 41 {
				t.Fatal(d)
			}
		}
		d = spaceTestData(t, get(t, s, "/x/series/archives?series_id="+strconv.FormatInt(ids[2], 10)))
		if len(d["archives"].([]any)) != 0 || len(d["aids"].([]any)) != 0 || d["meta"].(map[string]any)["total"] != float64(0) {
			t.Fatal(d)
		}
		for _, path := range []string{"/x/polymer/web-space/seasons_archives_list?season_id=99999999", "/x/series/archives?series_id=99999999"} {
			spaceTestError(t, get(t, s, path), -404, "合集不存在")
		}
	})
}

func TestSpaceMockNumericParameters(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  float64
	}{
		{"", 7}, {" ", 0}, {"\ufeff", 0}, {"1_0", 7}, {"0x1_0", 7}, {"0x1p2", 7}, {"+0x1p2", 7}, {"0b10", 2}, {"0o10", 8}, {"0x10", 16}, {"1.5", 1.5}, {"1e2", 100}, {"Infinity", 7}, {"NaN", 7}, {".5", .5}, {"0x10000000000000000", 18446744073709551616},
	} {
		if got := NumberWide(tc.input, 7); got != tc.want {
			t.Errorf("Number(%q): got %v want %v", tc.input, got, tc.want)
		}
	}
	testServers(t, func(t *testing.T, s *Server) {
		d := spaceTestData(t, get(t, s, "/x/relation/stat?vmid=1.5"))
		if d["mid"] != 1.5 || d["follower"] != 14806.5 || d["following"] != float64(0) {
			t.Fatal(d)
		}
		d = spaceTestData(t, get(t, s, "/x/relation/stat?vmid=1e308"))
		if d["follower"] != nil {
			t.Fatal(d)
		} // JSON.stringify(NaN) is null.
	})
}
