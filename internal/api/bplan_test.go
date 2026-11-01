package api

import (
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func bpData(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	assertCode(t, v, 0)
	d, ok := v["data"].(map[string]any)
	if !ok {
		t.Fatalf("data object: %v", v)
	}
	return d
}
func bpError(t *testing.T, v map[string]any, code int, msg string) {
	t.Helper()
	assertCode(t, v, code)
	if v["message"] != msg {
		t.Fatalf("message: %v", v)
	}
}
func bpExec(t *testing.T, s *Server, q string, args ...any) {
	t.Helper()
	if _, err := s.Store.DB.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func bpPost(t *testing.T, s *Server, path string, p url.Values, session *store.Session) map[string]any {
	t.Helper()
	p.Set("csrf", session.CSRF)
	return post(t, s, path, p, session)
}

func TestBPlanPublicContracts(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		arcs, err := s.Store.PublishedArchives(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		a := arcs[0]
		d := bpData(t, get(t, s, fmt.Sprintf("/x/player/online/total?aid=%d", a.AID)))
		if d["show_switch"].(map[string]any)["total"] != true || d["abtest"].(map[string]any)["group"] != "b" {
			t.Fatal(d)
		}
		n := math.Max(1, math.Floor(float64(a.Stat.View)/4000+float64((a.AID*7+time.Now().Unix()/60)%23)+.5))
		if d["count"] != strconv.FormatFloat(math.Max(1, math.Floor(n*.6+.5)), 'f', 0, 64) {
			t.Fatal(d)
		}
		if b := bpData(t, get(t, s, "/x/player/online/total?aid=0&bvid="+a.BVID)); !reflect.DeepEqual(b, d) {
			t.Fatal(b, d)
		}
		bpError(t, get(t, s, "/x/player/online/total?aid=-1"), -404, "啥都木有")
		bpError(t, get(t, s, "/x/player/online/total?aid=100100.1"), -404, "啥都木有")
		d = bpData(t, get(t, s, fmt.Sprintf("/x/space/upstat?mid=%d", a.MID)))
		var views, likes int64
		for _, v := range arcs {
			if v.MID == a.MID {
				views += v.Stat.View
				likes += v.Stat.Like
			}
		}
		if d["archive"].(map[string]any)["view"] != float64(views) || d["likes"] != float64(likes) {
			t.Fatal(d)
		}
		if bpData(t, get(t, s, "/x/space/upstat?mid=-1"))["likes"] != float64(0) {
			t.Fatal("absent upstat")
		}
		v := get(t, s, "/x/space/notice?mid=1")
		assertCode(t, v, 0)
		if v["data"] == "" {
			t.Fatal(v)
		}
		v = get(t, s, "/x/space/notice?mid=-1")
		assertCode(t, v, 0)
		if v["data"] != "" {
			t.Fatal(v)
		}
		v = get(t, s, "/main/hotword")
		assertCode(t, v, 0)
		if _, ok := v["data"]; ok {
			t.Fatal("hotword must be root")
		}
		words := v["list"].([]any)
		if len(words) == 0 || v["total_count"] != float64(10) || words[0].(map[string]any)["word_type"] != float64(4) {
			t.Fatal(v)
		}
		d = bpData(t, get(t, s, "/x/web-interface/wbi/search/default"))
		if d["goto_type"] != float64(1) || !strings.HasPrefix(d["url"].(string), "/video/") {
			t.Fatal(d)
		}
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
		bpExec(t, s, "UPDATE archives SET pubdate=? WHERE aid=?", today, a.AID)
		d = bpData(t, get(t, s, "/x/web-interface/online"))
		counts := d["region_count"].(map[string]any)
		if counts[strconv.FormatInt(a.TID, 10)].(float64) < 1 || counts[strconv.FormatInt(a.SubTID, 10)].(float64) < 1 || d["all_count"] != float64(len(arcs)) {
			t.Fatal(d)
		}
		bpExec(t, s, "UPDATE archives SET tag=?,stat_view=? WHERE aid=?", "测试标签,测试标签,😀", 123, a.AID)
		v = get(t, s, fmt.Sprintf("/x/tag/hots?rid=%d", a.SubTID))
		assertCode(t, v, 0)
		tags := v["data"].([]any)[0].(map[string]any)["tags"].([]any)
		found := false
		for _, x := range tags {
			tag := x.(map[string]any)
			if tag["tag_name"] == "测试标签" {
				found = true
				if tag["count"].(map[string]any)["view"] != float64(246) || tag["tag_id"] != float64(245128) {
					t.Fatal(tag)
				}
			}
		}
		if !found {
			t.Fatal(tags)
		}
		v = get(t, s, "/x/tag/hots?rid=-1")
		assertCode(t, v, 0)
		if len(v["data"].([]any)[0].(map[string]any)["tags"].([]any)) != 0 {
			t.Fatal(v)
		}
		d = bpData(t, get(t, s, fmt.Sprintf("/x/web-interface/dynamic/region?rid=%d&order=click&duration=1&ps=100", a.SubTID)))
		if d["page"].(map[string]any)["size"] != float64(50) {
			t.Fatal(d)
		}
		for _, row := range d["archives"].([]any) {
			arc := row.(map[string]any)
			if arc["sub_tid"] != float64(a.SubTID) || arc["duration"].(float64) > 600 || arc["sub_tname"] == "" {
				t.Fatal(arc)
			}
		}
		bpError(t, get(t, s, "/x/web-interface/dynamic/region?rid=-1"), -400, "分区不存在")
		d = bpData(t, get(t, s, "/x/web-show/res/locs?ids=12,1001,-999"))
		if len(d["12"].([]any)) == 0 || len(d["-999"].([]any)) != 0 {
			t.Fatal(d)
		}
		banner := d["12"].([]any)[0].(map[string]any)
		if banner["src_id"] != float64(12) || banner["pos_num"] != float64(1) || banner["is_ad"] != false {
			t.Fatal(banner)
		}
		if len(bpData(t, get(t, s, "/x/web-show/res/locs?ids=abc,0"))) != 0 {
			t.Fatal("invalid locs")
		}
		d = bpData(t, get(t, s, "/pgc/season/index/condition?season_type=2"))
		filters := d["filter"].([]any)
		if len(filters) != 5 || filters[1].(map[string]any)["field"] != "style_id" || len(d["order"].([]any)) != 5 {
			t.Fatal(d)
		}
		d = bpData(t, get(t, s, "/pgc/season/index/condition?season_type=-999"))
		if len(d["filter"].([]any)[0].(map[string]any)["values"].([]any)) != 1 {
			t.Fatal(d)
		}
		// Empty catalogue is a successful response, never a fabricated business error.
		bpExec(t, s, "UPDATE archives SET state=-100")
		v = get(t, s, "/main/hotword")
		assertCode(t, v, 0)
		if len(v["list"].([]any)) != 0 {
			t.Fatal(v)
		}
		d = bpData(t, get(t, s, "/x/web-interface/wbi/search/default"))
		if d["id"] != float64(0) || d["goto_type"] != float64(0) || d["show_name"] != "" {
			t.Fatal(d)
		}
	})
}

func TestBPlanNoticePortalReservations(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		other := login(t, s, "lumen", "demo123")
		bpError(t, get(t, s, "/x/polymer/web-dynamic/v1/portal"), -101, "账号未登录")
		d := bpData(t, get(t, s, "/x/polymer/web-dynamic/v1/portal", session))
		if d["my_info"].(map[string]any)["mid"] != float64(1) || d["up_list"].(map[string]any)["has_more"] != false {
			t.Fatal(d)
		}
		bpExec(t, s, "INSERT INTO relations(mid,fid,mtime) VALUES (?,?,?) ON CONFLICT(mid,fid) DO NOTHING", 1, 2, time.Now().Unix())
		bpExec(t, s, "UPDATE rooms SET live_status=1,banned=0 WHERE uid=2")
		bpExec(t, s, "UPDATE archives SET pubdate=? WHERE mid=2", time.Now().Unix())
		d = bpData(t, get(t, s, "/x/polymer/web-dynamic/v1/portal", session))
		if d["live_users"].(map[string]any)["count"].(float64) < 1 {
			t.Fatal(d)
		}
		found := false
		for _, row := range d["up_list"].(map[string]any)["items"].([]any) {
			up := row.(map[string]any)
			if up["mid"] == float64(2) {
				found = up["has_update"] == true
			}
		}
		if !found {
			t.Fatal(d)
		}
		bpError(t, post(t, s, "/x/space/notice/set", url.Values{"notice": {"a"}}), -101, "账号未登录")
		assertCode(t, post(t, s, "/x/space/notice/set", url.Values{"notice": {"a"}}, session), -111)
		bpError(t, bpPost(t, s, "/x/space/notice/set", url.Values{"notice": {strings.Repeat("😀", 76)}}, session), -400, "公告最多 150 个字符")
		assertCode(t, bpPost(t, s, "/x/space/notice/set", url.Values{"notice": {"  " + strings.Repeat("😀", 75) + "  "}}, session), 0)
		if get(t, s, "/x/space/notice?mid=1")["data"] != strings.Repeat("😀", 75) {
			t.Fatal("notice trim or UTF16")
		}
		v := get(t, s, "/x/space/reservation?vmid=1")
		assertCode(t, v, 0)
		if len(v["data"].([]any)) == 0 {
			t.Fatal("seed reservation")
		}
		v = get(t, s, "/x/space/reservation?vmid=-1")
		assertCode(t, v, 0)
		if len(v["data"].([]any)) != 0 {
			t.Fatal(v)
		}
		bpError(t, post(t, s, "/x/web/reservation/add", url.Values{}), -101, "账号未登录")
		assertCode(t, post(t, s, "/x/web/reservation/add", url.Values{}, session), -111)
		future := time.Now().Unix() + 3600
		for _, title := range []string{"", strings.Repeat("😀", 21)} {
			bpError(t, bpPost(t, s, "/x/web/reservation/add", url.Values{"title": {title}, "stime": {fmt.Sprint(future)}}, session), -400, "标题需为 1–40 个字符")
		}
		bpError(t, bpPost(t, s, "/x/web/reservation/add", url.Values{"title": {"预约"}, "stime": {"1"}}, session), -400, "开播时间需晚于现在")
		d = bpData(t, bpPost(t, s, "/x/web/reservation/add", url.Values{"title": {"  我的预约  "}, "stime": {fmt.Sprint(future)}}, session))
		sid := int64(d["sid"].(float64))
		if sid <= 5011 {
			t.Fatal("sequence may collide with seed", sid)
		}
		v = get(t, s, "/x/space/reservation?vmid=1")
		assertCode(t, v, 0)
		r := v["data"].([]any)[0].(map[string]any)
		if r["sid"] != float64(sid) || r["name"] != "我的预约" || r["etime"] != float64(future+7200) || r["state"] != float64(100) || r["lottery_prize_info"] != nil || !strings.HasSuffix(r["subtitle"].(string), " 直播") {
			t.Fatal(r)
		}
		bpExec(t, s, "INSERT INTO reservations(sid,mid,title,stime,ctime) VALUES (?,?,?,?,?)", 999999, 1, "过去", time.Now().Unix()-1, time.Now().Unix())
		v = get(t, s, "/x/space/reservation?vmid=1")
		for _, row := range v["data"].([]any) {
			if row.(map[string]any)["sid"] == float64(999999) {
				t.Fatal("past reservation exposed")
			}
		}
		bpError(t, bpPost(t, s, "/x/web/reservation/delete", url.Values{"sid": {fmt.Sprint(sid)}}, other), -404, "预约不存在")
		assertCode(t, bpPost(t, s, "/x/web/reservation/delete", url.Values{"sid": {fmt.Sprint(sid)}}, session), 0)
		bpError(t, bpPost(t, s, "/x/web/reservation/delete", url.Values{"sid": {fmt.Sprint(sid)}}, session), -404, "预约不存在")
		assertCode(t, post(t, s, "/x/web/reservation/delete", url.Values{"sid": {"4999"}}, session), -111)
	})
}

func TestBPlanReplyAndLiveRanks(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		bpExec(t, s, "DELETE FROM reply_reactions")
		bpExec(t, s, "DELETE FROM replies")
		for _, r := range []store.Reply{{RPID: 90001, OID: 100100, MID: 2, Message: "old hot", Ctime: 1}, {RPID: 90002, OID: 100100, MID: 3, Message: "new", Ctime: 2}, {RPID: 90003, OID: 100100, MID: 4, Root: 90001, Parent: 90001, Message: "child", Ctime: 3}} {
			if err := store.InsertReply(t.Context(), s.Store.DB, &r); err != nil {
				t.Fatal(err)
			}
		}
		bpExec(t, s, "INSERT INTO reply_reactions(rpid,mid,reaction) VALUES (?,?,?)", 90001, 1, 1)
		d := bpData(t, get(t, s, "/x/v2/reply/wbi/main?oid=100100&mode=3&ps=1&next=0"))
		if d["replies"].([]any)[0].(map[string]any)["rpid"] != float64(90001) || d["cursor"].(map[string]any)["all_count"] != float64(3) || d["cursor"].(map[string]any)["is_begin"] != true {
			t.Fatal(d)
		}
		d = bpData(t, get(t, s, "/x/v2/reply/wbi/main?oid=100100&mode=2&ps=99"))
		if d["replies"].([]any)[0].(map[string]any)["rpid"] != float64(90002) || d["cursor"].(map[string]any)["mode_text"] != "最新评论" {
			t.Fatal(d)
		}
		d = bpData(t, get(t, s, "/x/v2/reply/wbi/main?oid=100100&mode=3&ps=1&next=2"))
		if d["replies"].([]any)[0].(map[string]any)["rpid"] != float64(90002) || d["cursor"].(map[string]any)["is_end"] != true {
			t.Fatal(d)
		}
		d = bpData(t, get(t, s, "/x/v2/reply/wbi/main?oid=-1"))
		if len(d["replies"].([]any)) != 0 || d["upper"].(map[string]any)["mid"] != float64(0) {
			t.Fatal(d)
		}
		room, err := s.Store.RoomByID(t.Context(), 20000)
		if err != nil {
			t.Fatal(err)
		}
		bpExec(t, s, "DELETE FROM live_messages WHERE room_id=?", room.RoomID)
		for i, v := range []struct {
			uid   int64
			price float64
		}{{2, 0}, {3, 2}, {3, 0}, {room.UID, 1000}} {
			bpExec(t, s, "INSERT INTO live_messages(id,room_id,uid,text,ts,price) VALUES (?,?,?,?,?,?)", 90000+i, room.RoomID, v.uid, "test", 1, v.price)
		}
		d = bpData(t, get(t, s, "/xlive/general-interface/v1/rank/getOnlineGoldRank?roomId=20000&pageSize=1"))
		items := d["OnlineRankItem"].([]any)
		if d["onlineNum"] != float64(2) || len(items) != 1 || items[0].(map[string]any)["uid"] != float64(3) || items[0].(map[string]any)["score"] != float64(22) {
			t.Fatal(d)
		}
		d = bpData(t, get(t, s, "/xlive/general-interface/v1/rank/getOnlineGoldRank?roomId=20000&pageSize=1&page=2"))
		if d["OnlineRankItem"].([]any)[0].(map[string]any)["userRank"] != float64(2) {
			t.Fatal(d)
		}
		bpError(t, get(t, s, "/xlive/general-interface/v1/rank/getOnlineGoldRank?roomId=-1"), 19002003, "房间不存在或已被封禁")
		bpExec(t, s, "DELETE FROM relations WHERE fid=?", room.UID)
		for i := 2; i <= 7; i++ {
			bpExec(t, s, "INSERT INTO relations(mid,fid,mtime) VALUES (?,?,?)", i, room.UID, 100-i)
		}
		d = bpData(t, get(t, s, "/xlive/app-room/v1/guardTab/topList?roomid=20000&page_size=2"))
		top := d["top3"].([]any)
		if len(top) != 3 || top[0].(map[string]any)["uid"] != float64(7) || top[0].(map[string]any)["guard_level"] != float64(2) || d["list"].([]any)[0].(map[string]any)["rank"] != float64(4) {
			t.Fatal(d)
		}
		d = bpData(t, get(t, s, "/xlive/app-room/v1/guardTab/topList?roomid=20000&page_size=2&page=2"))
		if len(d["top3"].([]any)) != 0 || d["list"].([]any)[0].(map[string]any)["rank"] != float64(6) {
			t.Fatal(d)
		}
		bpError(t, get(t, s, "/xlive/app-room/v1/guardTab/topList?roomid=-1"), 19002003, "房间不存在或已被封禁")
		bpExec(t, s, "UPDATE rooms SET banned=1 WHERE room_id=20000")
		bpError(t, get(t, s, "/xlive/app-room/v1/guardTab/topList?roomid=20000"), 19002003, "房间不存在或已被封禁")
	})
}

// These read routes deliberately have no mock business error. A failed database
// must propagate through the standard error envelope, not appear as an empty success.
func TestBPlanReadFailures(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		if err := s.Store.Close(); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/x/player/online/total?aid=100100", "/x/space/upstat?mid=1", "/x/space/notice?mid=1", "/main/hotword", "/x/web-interface/wbi/search/default", "/x/web-interface/online", "/x/tag/hots?rid=1001", "/x/web-interface/dynamic/region?rid=1001", "/x/web-show/res/locs?ids=12", "/x/v2/reply/wbi/main?oid=100100", "/pgc/season/index/condition", "/xlive/general-interface/v1/rank/getOnlineGoldRank?roomId=20000", "/xlive/app-room/v1/guardTab/topList?roomid=20000", "/x/space/reservation?vmid=1"} {
			t.Run(path, func(t *testing.T) { assertCode(t, get(t, s, path), -500) })
		}
	})
}
