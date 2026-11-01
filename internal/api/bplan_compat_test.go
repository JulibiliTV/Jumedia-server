package api

import (
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func TestBPlanSearchAndJuliRegression(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		arcs, err := s.Store.PublishedArchives(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		base := bpData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=video&page_size=1000"))
		rows := base["result"].([]any)
		if len(rows) != len(arcs) {
			t.Fatal("Juli result count")
		}
		for i, a := range arcs {
			if rows[i].(map[string]any)["aid"] != float64(a.AID) {
				t.Fatal("Juli match order changed")
			}
		}
		for _, p := range []string{"&order=totalrank&duration=0&tids=0", "&order=not-a-sql-column", "&order=click%20DESC%3BDELETE%20FROM%20archives"} {
			if got := bpData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=video&page_size=1000"+p)); !reflect.DeepEqual(base, got) {
				t.Fatal("unknown/default order changed results")
			}
		}
		// A deliberately constructed set covers all closed/open duration boundaries.
		bpExec(t, s, "UPDATE archives SET state=-100")
		for i, seconds := range []int{600, 601, 1800, 1801, 3600, 3601} {
			aid := int64(800000 + i)
			a := store.Archive{AID: aid, BVID: store.AV2BV(aid), MID: 1, TID: 1001, Title: fmt.Sprint("duration ", seconds), Stat: store.ArchiveStat{View: int64(i), Danmaku: int64(9 - i), Favorite: int64(i * 2), Reply: int64(8 - i)}}
			if err := store.InsertArchive(t.Context(), s.Store.DB, &a); err != nil {
				t.Fatal(err)
			}
			if err := store.InsertPage(t.Context(), s.Store.DB, &store.Page{CID: aid, AID: aid, Duration: seconds}); err != nil {
				t.Fatal(err)
			}
		}
		for _, tt := range []struct {
			duration, order string
			want            []int
		}{{"1", "click", []int{0}}, {"2", "click", []int{2, 1}}, {"3", "dm", []int{3, 4}}, {"4", "stow", []int{5}}, {"2", "scores", []int{1, 2}}} {
			d := bpData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=video&tids=1001&duration="+tt.duration+"&order="+tt.order))
			list := d["result"].([]any)
			if len(list) != len(tt.want) {
				t.Fatal(d)
			}
			for i, w := range tt.want {
				if list[i].(map[string]any)["aid"] != float64(800000+w) {
					t.Fatal(d)
				}
			}
		}
		d := bpData(t, get(t, s, "/x/web-interface/wbi/search/type?search_type=video&tids=1003&duration=2&order=click"))
		if d["numResults"] != float64(0) {
			t.Fatal(d)
		}
	})
}

func TestBPlanIndexAndFollowRegression(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		seasons, err := s.Store.ListSeason(t.Context(), "WHERE state=0 AND season_type=1 ORDER BY season_id")
		if err != nil {
			t.Fatal(err)
		}
		sort.SliceStable(seasons, func(i, j int) bool { return seasons[i].Views > seasons[j].Views })
		base := bpData(t, get(t, s, "/pgc/season/index/result?season_type=1&order=2&sort=0&page=1&pagesize=100"))
		rows := base["list"].([]any)
		if len(rows) != len(seasons) {
			t.Fatal(base)
		}
		for i, v := range seasons {
			row := rows[i].(map[string]any)
			if row["season_id"] != float64(v.SeasonID) || row["season_status"] != float64(1) || row["duration"] != float64(0) || row["year"] != float64(time.Unix(v.Ctime, 0).In(time.Local).Year()) {
				t.Fatal(row)
			}
		}
		if got := bpData(t, get(t, s, "/pgc/season/index/result?season_type=1&order=2&sort=0&page=1&pagesize=100&area=-1&style_id=-1&year=-1&release_date=-1&is_finish=-1&copyright=-1&season_status=-1")); !reflect.DeepEqual(base, got) {
			t.Fatal("Juli index changed")
		}
		// Explicit catalogue makes the Chinese style IDs and exclusive/free semantics reviewable.
		bpExec(t, s, "UPDATE seasons SET state=-1")
		for i := 0; i < 3; i++ {
			sid := int64(700000 + i)
			v := store.Season{SeasonID: sid, MediaID: sid, SeasonType: 2, Title: fmt.Sprint(i), Areas: []string{"中国大陆"}, Styles: []string{"科幻", "动作"}, Copyright: 3, IsFinish: true, Ctime: time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local).Unix()}
			if i == 1 {
				v.Areas = []string{"日本"}
				v.Copyright = 1
				v.Styles = []string{"冒险"}
			}
			if i == 2 {
				v.Ctime = time.Date(2021, 1, 1, 0, 0, 0, 0, time.Local).Unix()
			}
			if err := store.InsertSeason(t.Context(), s.Store.DB, &v); err != nil {
				t.Fatal(err)
			}
			if err := store.InsertEpisode(t.Context(), s.Store.DB, &store.Episode{EpID: sid, SeasonID: sid, AID: sid, CID: sid, Duration: 7200}); err != nil {
				t.Fatal(err)
			}
		}
		condition := bpData(t, get(t, s, "/pgc/season/index/condition?season_type=2"))
		styles := condition["filter"].([]any)[1].(map[string]any)["values"].([]any)
		wantStyles := []string{"全部", "动作", "科幻", "冒险"}
		for i, w := range wantStyles {
			if styles[i].(map[string]any)["name"] != w {
				t.Fatal(styles)
			}
		}
		for _, year := range []string{"year=2020", "year=%5B2020%2C2021%29", "year=-1&release_date=2020"} {
			d := bpData(t, get(t, s, "/pgc/season/index/result?season_type=2&area=1,3&style_id=1&is_finish=1&copyright=3&season_status=1&"+year))
			list := d["list"].([]any)
			if len(list) != 1 {
				t.Fatal(d)
			}
			v := list[0].(map[string]any)
			if v["season_id"] != float64(700000) || v["duration"] != float64(7200) || v["copyright"] != float64(3) || v["year"] != float64(2020) {
				t.Fatal(v)
			}
		}
		for _, filter := range []string{"season_status=2,6", "season_status=4,6", "style_id=999", "area=999", "year=2022", "is_finish=0"} {
			d := bpData(t, get(t, s, "/pgc/season/index/result?season_type=2&"+filter))
			if d["total"] != float64(0) {
				t.Fatal(d)
			}
		}
		bpExec(t, s, "INSERT INTO bangumi_follow(mid,season_id,status,mtime) VALUES (?,?,?,?)", 1, 700000, 1, time.Now().Unix()+1)
		// type 1 = 番剧 / 国创 only (what Juli requests), 2 = the other types; no other type, ps 1–30.
		for kind, anime := range map[string]bool{"1": true, "2": false} {
			d := bpData(t, get(t, s, "/x/space/bangumi/follow/list?vmid=1&pn=1&ps=30&type="+kind))
			list := d["list"].([]any)
			if d["total"] != float64(len(list)) {
				t.Fatal(d)
			}
			for _, item := range list {
				st := item.(map[string]any)["season_type"]
				if (st == float64(1) || st == float64(4)) != anime {
					t.Fatalf("type=%s returned season_type %v", kind, st)
				}
			}
		}
		d := bpData(t, get(t, s, "/x/space/bangumi/follow/list?vmid=1&type=2&pn=1&ps=1"))
		if d["list"].([]any)[0].(map[string]any)["season_id"] != float64(700000) {
			t.Fatal(d)
		}
		for _, query := range []string{"", "&type=0", "&type=3", "&type=1&ps=31", "&type=1&ps=0", "&type=1&pn=0"} {
			assertCode(t, get(t, s, "/x/space/bangumi/follow/list?vmid=1"+query), -400)
		}
	})
}

func TestBPlanRegionsCreatorAndBanner(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		tops, err := s.Store.ListRegion(t.Context(), "WHERE COALESCE(parent,0)=0 AND visible=1 ORDER BY sort,tid")
		if err != nil {
			t.Fatal(err)
		}
		v := get(t, s, "/x/web-interface/region/list")
		assertCode(t, v, 0)
		rows := v["data"].([]any)
		if len(rows) != len(tops) {
			t.Fatal(v)
		}
		for i, r := range tops {
			row := rows[i].(map[string]any)
			if row["tid"] != float64(r.TID) || row["id"] != float64(r.TID) || row["name"] != r.Name {
				t.Fatal("Juli region fields changed")
			}
			if len(row["children"].([]any)) == 0 {
				t.Fatal("missing children")
			}
		}
		bpExec(t, s, "UPDATE regions SET visible=0 WHERE tid=100101")
		v = get(t, s, "/x/web-interface/region/list")
		for _, row := range v["data"].([]any) {
			for _, child := range row.(map[string]any)["children"].([]any) {
				if child.(map[string]any)["tid"] == float64(100101) {
					t.Fatal("hidden child")
				}
			}
		}
		form := url.Values{"title": {"T18 投稿"}, "tid": {"1001"}, "sub_tid": {"100102"}, "videos": {`[{"filename":"/video.mp4","title":"分P","duration":61}]`}}
		d := bpData(t, bpPost(t, s, "/x/vu/web/add/v3", form, session))
		aid := int64(d["aid"].(float64))
		d = bpData(t, get(t, s, fmt.Sprintf("/x/web-interface/view?aid=%d", aid)))
		if d["sub_tid"] != float64(100102) || d["sub_tname"] != "影视剪辑" {
			t.Fatal(d)
		}
		form.Set("aid", fmt.Sprint(aid))
		form.Set("sub_tid", "100103")
		assertCode(t, bpPost(t, s, "/x/vu/web/edit", form, session), 0)
		for _, path := range []string{"/x/vu/web/add/v3", "/x/vu/web/edit"} {
			form.Set("tid", "100103")
			bpError(t, bpPost(t, s, path, form, session), 21002, "请选择分区")
			form.Set("tid", "1001")
			form.Set("sub_tid", "100501")
			bpError(t, bpPost(t, s, path, form, session), 21002, "子分区不属于所选分区")
		}
		regions := bpData(t, get(t, s, "/x/admin/regions", session))["list"].([]any)
		found := false
		for _, r := range regions {
			row := r.(map[string]any)
			if row["tid"] == float64(100103) {
				found = true
				if row["parent"] != float64(1001) || row["count"].(float64) < 1 {
					t.Fatal(row)
				}
			}
		}
		if !found {
			t.Fatal("admin child absent")
		}
		bpError(t, bpPost(t, s, "/x/admin/region/save", url.Values{"name": {"无效"}, "parent": {"100101"}}, session), -400, "上级分区不存在")
		assertCode(t, bpPost(t, s, "/x/admin/region/save", url.Values{"name": {"新子分区"}, "parent": {"1001"}, "visible": {"true"}}, session), 0)
		var child int64
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT tid FROM regions WHERE name=?", "新子分区").Scan(&child); err != nil || child != 100107 {
			t.Fatal(child, err)
		}
		assertCode(t, bpPost(t, s, "/x/admin/region/save", url.Values{"name": {"新顶级"}, "visible": {"true"}}, session), 0)
		var top int64
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT tid FROM regions WHERE name=?", "新顶级").Scan(&top); err != nil || top != 1032 {
			t.Fatal(top, err)
		}
		assertCode(t, bpPost(t, s, "/x/admin/region/save", url.Values{"name": {"新顶级的子级"}, "parent": {fmt.Sprint(top)}, "visible": {"true"}}, session), 0)
		bpError(t, bpPost(t, s, "/x/admin/region/delete", url.Values{"tid": {"1001"}}, session), -400, "该分区下仍有稿件，请先迁移")
		bpExec(t, s, "UPDATE archives SET sub_tid=? WHERE aid=?", top*100+1, aid)
		assertCode(t, bpPost(t, s, "/x/admin/region/delete", url.Values{"tid": {fmt.Sprint(top)}}, session), 0)
		a, err := s.Store.ArchiveByID(t.Context(), aid)
		if err != nil || a.SubTID != 0 {
			t.Fatal(a, err)
		}
		bpExec(t, s, "UPDATE archives SET sub_tid=100103 WHERE aid=?", aid)
		assertCode(t, bpPost(t, s, "/x/admin/region/delete", url.Values{"tid": {"100103"}}, session), 0)
		a, err = s.Store.ArchiveByID(t.Context(), aid)
		if err != nil || a.SubTID != 0 {
			t.Fatal(a, err)
		}
		bpError(t, bpPost(t, s, "/x/admin/banner/save", url.Values{"position": {"channel"}}, session), -400, "请选择频道")
		assertCode(t, bpPost(t, s, "/x/admin/banner/save", url.Values{"position": {"channel"}, "loc": {"1001"}, "title": {"T18 频道"}, "img": {"/pic.png"}, "url": {"/video/test"}, "sort": {"-100"}}, session), 0)
		d = bpData(t, get(t, s, "/x/web-show/res/locs?ids=1001"))
		banner := d["1001"].([]any)[0].(map[string]any)
		if banner["name"] != "T18 频道" {
			t.Fatal(d)
		}
		id := int64(banner["id"].(float64))
		assertCode(t, bpPost(t, s, "/x/admin/banner/save", url.Values{"id": {strconv.FormatInt(id, 10)}, "position": {"home"}, "loc": {"1001"}, "img": {"/pic.png"}}, session), 0)
		var count int
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM banners WHERE id=? AND loc IS NULL", id).Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
		d = bpData(t, get(t, s, "/x/admin/dashboard", session))
		for _, row := range d["regions"].([]any) {
			if row.(map[string]any)["tid"].(float64) > 100000 {
				t.Fatal("dashboard counts child")
			}
		}
	})
}
