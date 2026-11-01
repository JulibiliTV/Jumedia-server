package api

import (
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func TestT19WeeklySelection(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		arcs, err := s.Store.PublishedArchives(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		sort.SliceStable(arcs, func(i, j int) bool {
			return arcs[i].Stat.View+arcs[i].Stat.Like*10 > arcs[j].Stat.View+arcs[j].Stat.Like*10
		})
		d := bpData(t, get(t, s, "/x/web-interface/popular/series/one?number=1"))
		cfg := d["config"].(map[string]any)
		epoch := time.Date(2026, 1, 2, 18, 0, 0, 0, time.Local).Unix()
		want := map[string]any{"id": float64(1), "type": "weekly_selected", "number": float64(1), "subject": "名场面放映室", "label": "第1期", "name": "2026第1期", "stime": float64(epoch - 604800), "etime": float64(epoch), "status": float64(2), "share_title": "每周必看 · 第1期", "share_subtitle": ""}
		if !reflect.DeepEqual(cfg, want) {
			t.Fatal(cfg, want)
		}
		reasons := []string{"本周最多人看完", "编辑推荐", "百万播放", "老番新看", "综艺幕后", "下饭必备", "名场面", "幕后揭秘", "二刷必看", "高分好评"}
		for i, row := range d["list"].([]any) {
			a := arcs[(3+i)%len(arcs)]
			v := row.(map[string]any)
			if v["aid"] != float64(a.AID) || v["rcmd_reason"] != reasons[(int(a.AID)+i)%10] || v["bvid"] != a.BVID || v["owner"].(map[string]any)["mid"] != float64(a.MID) || v["stat"].(map[string]any)["view"] != float64(a.Stat.View) {
				t.Fatal(v, a)
			}
		}
		// Public list deliberately has no error path in Mock.
		list := bpData(t, get(t, s, "/x/web-interface/popular/series/list?number=-1"))["list"].([]any)
		for i := 1; i < len(list); i++ {
			if list[i-1].(map[string]any)["number"].(float64)-list[i].(map[string]any)["number"].(float64) != 1 {
				t.Fatal(list)
			}
		}
		if c := bpData(t, get(t, s, "/x/web-interface/popular/series/one?number=bad"))["config"].(map[string]any); c["number"] != list[0].(map[string]any)["number"] {
			t.Fatal(c)
		}
		// JavaScript's int() accepts finite fractions, omitting an undefined subject.
		fraction := bpData(t, get(t, s, "/x/web-interface/popular/series/one?number=1.5"))["config"].(map[string]any)
		if _, exists := fraction["subject"]; exists || fraction["number"] != 1.5 {
			t.Fatal(fraction)
		}
		bpExec(t, s, "UPDATE archives SET state=-1")
		empty := bpData(t, get(t, s, "/x/web-interface/popular/series/one?number=1"))["list"].([]any)
		if len(empty) != 0 {
			t.Fatal(empty)
		}
	})
}

func TestT19HistoryDeleteIsolation(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		bpExec(t, s, "DELETE FROM history")
		for _, mid := range []int64{1, 2} {
			for _, h := range []store.History{{MID: mid, AID: 100100}, {MID: mid, AID: 100101, SID: 40000}, {MID: mid, AID: 100102, SID: 40000}, {MID: mid, AID: 100103, SID: 40088}} {
				if _, err := s.Store.DB.ExecContext(t.Context(), "INSERT INTO history(mid,aid,sid) VALUES (?,?,?)", h.MID, h.AID, store.NullableID(h.SID)); err != nil {
					t.Fatal(err)
				}
			}
		}
		count := func(mid, want int) {
			t.Helper()
			var n int
			if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM history WHERE mid=?", mid).Scan(&n); err != nil || n != want {
				t.Fatal(n, err)
			}
		}
		path := "/x/v2/history/delete"
		assertCode(t, bpPost(t, s, path, url.Values{"kid": {"archive_100101"}}, admin), 0)
		count(1, 4)
		assertCode(t, bpPost(t, s, path, url.Values{"kid": {"archive_00100100"}}, admin), 0)
		count(1, 3)
		count(2, 4)
		assertCode(t, bpPost(t, s, path, url.Values{"kid": {"pgc_40000"}}, admin), 0)
		count(1, 1)
		count(2, 4)
		for _, kid := range []string{"archive_100100", "pgc_999999", "archive_999999999999999999999999999999999"} {
			assertCode(t, bpPost(t, s, path, url.Values{"kid": {kid}}, admin), 0)
		}
		for _, kid := range []string{"", "archive_-1", "pgc_1.5", "archive_1\n", "pgc_1 OR 1=1"} {
			bpError(t, bpPost(t, s, path, url.Values{"kid": {kid}}, admin), -400, "请求错误")
		}
		assertCode(t, post(t, s, path, url.Values{"kid": {"pgc_40088"}}, admin), -111)
		count(1, 1)
		bpExec(t, s, "INSERT INTO history(mid,aid) VALUES (1,-1)")
		assertCode(t, bpPost(t, s, path, url.Values{"kid": {"archive_" + strings.Repeat("9", 400)}}, admin), 0)
		count(1, 2)
	})
}

func TestT19MedalPaginationAndOwnership(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		other := login(t, s, "lumen", "demo123")
		path := "/xlive/app-ucenter/v1/fansMedal/panel"
		panel := bpData(t, get(t, s, path+"?pageSize=2", admin))
		if len(panel["list"].([]any)) != 2 || panel["page_info"].(map[string]any)["total_page"] != float64(3) || panel["page_info"].(map[string]any)["has_more"] != true {
			t.Fatal(panel)
		}
		panel = bpData(t, get(t, s, path+"?pageSize=2&page=2", admin))
		if len(panel["special_list"].([]any)) != 0 || panel["list"].([]any)[0].(map[string]any)["medal"].(map[string]any)["medal_id"] != float64(7004) {
			t.Fatal(panel)
		}
		for _, page := range []string{"0", "1.5", "-1", "9"} {
			bpData(t, get(t, s, path+"?pageSize=2&page="+page, admin))
		}
		panel = bpData(t, get(t, s, path+"?pageSize=0", admin))
		if panel["page_info"].(map[string]any)["total_page"] != nil || len(panel["list"].([]any)) != 0 {
			t.Fatal(panel)
		}
		panel = bpData(t, get(t, s, path+"?target_id=1", admin))
		if panel["total_number"] != float64(1) || len(panel["special_list"].([]any)) != 1 {
			t.Fatal(panel)
		}
		panel = bpData(t, get(t, s, path+"?target_id=1.5", admin))
		if panel["total_number"] != float64(0) || panel["page_info"].(map[string]any)["total_page"] != float64(1) {
			t.Fatal(panel)
		}
		wear := "/xlive/web-room/v1/fansMedal/wear"
		bpError(t, bpPost(t, s, wear, url.Values{"medal_id": {"7001"}}, other), -404, "勋章不存在")
		bpError(t, bpPost(t, s, wear, url.Values{"medal_id": {"7001.1"}}, admin), -404, "勋章不存在")
		assertCode(t, post(t, s, wear, url.Values{"medal_id": {"7002"}}, admin), -111)
		assertCode(t, post(t, s, "/xlive/web-room/v1/fansMedal/take_off", url.Values{}, admin), -111)
		room, err := s.Store.ListLiveRoom(t.Context(), "WHERE uid=2 ORDER BY room_id LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		join := "/xlive/web-room/v1/fansMedal/join"
		assertCode(t, post(t, s, join, url.Values{"roomid": {fmt.Sprint(room[0].RoomID)}}, admin), -111)
		bpError(t, bpPost(t, s, join, url.Values{"roomid": {"-1"}}, admin), 19002003, "房间不存在或已被封禁")
		before := bpData(t, bpPost(t, s, join, url.Values{"roomid": {fmt.Sprint(room[0].RoomID)}}, admin))
		after := bpData(t, bpPost(t, s, join, url.Values{"roomid": {fmt.Sprint(room[0].RoomID)}}, admin))
		if !reflect.DeepEqual(before, after) || before["medal"].(map[string]any)["wearing_status"] != float64(0) {
			t.Fatal(before, after)
		}
		bpExec(t, s, "UPDATE rooms SET banned=1 WHERE room_id=?", room[0].RoomID)
		bpError(t, get(t, s, fmt.Sprintf("/rankdb/v1/RoomRank/webMedalRank?roomid=%d", room[0].RoomID)), 19002003, "房间不存在或已被封禁")
		if d := bpData(t, get(t, s, fmt.Sprintf("/rankdb/v1/RoomRank/webMedalRank?roomid=%d", room[0].RoomID), admin)); d["own"] == nil {
			t.Fatal(d)
		}
	})
}

func TestT19ConcurrentMedalWrites(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		rooms, err := s.Store.ListLiveRoom(t.Context(), "WHERE uid=2 ORDER BY room_id LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		rid := fmt.Sprint(rooms[0].RoomID)
		bpExec(t, s, "DELETE FROM fan_medals WHERE mid=1")
		var wg sync.WaitGroup
		results := make(chan map[string]any, 30)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- bpPost(t, s, "/xlive/web-room/v1/fansMedal/join", url.Values{"roomid": {rid}}, admin)
			}()
		}
		wg.Wait()
		for i := 0; i < 10; i++ {
			d := bpData(t, <-results)
			if d["medal"].(map[string]any)["wearing_status"] != float64(1) {
				t.Fatal(d)
			}
		}
		for i := 0; i < 25; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- bpPost(t, s, "/msg/send", url.Values{"roomid": {rid}, "msg": {"并发"}}, admin)
			}()
		}
		wg.Wait()
		for i := 0; i < 25; i++ {
			assertCode(t, <-results, 0)
		}
		var intimacy, today, count int
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT intimacy,today FROM fan_medals WHERE mid=1 AND ruid=2").Scan(&intimacy, &today); err != nil || intimacy != 100 || today != 100 {
			t.Fatal(intimacy, today, err)
		}
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM fan_medals WHERE mid=1 AND wearing=1").Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
		// Independent handlers share the DB lock, not an in-process medal mutex.
		bpExec(t, s, "INSERT INTO fan_medals(medal_id,mid,ruid) VALUES (9900,1,3)")
		medals, err := readFanMedals(t.Context(), s.Store.DB, "SELECT "+medalColumns+" FROM fan_medals WHERE mid=1 ORDER BY medal_id")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results <- bpPost(t, s, "/xlive/web-room/v1/fansMedal/wear", url.Values{"medal_id": {fmt.Sprint(medals[i%2].ID)}}, admin)
			}(i)
		}
		wg.Wait()
		for i := 0; i < 10; i++ {
			assertCode(t, <-results, 0)
		}
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM fan_medals WHERE mid=1 AND wearing=1").Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
	})
}

func TestT19FeedRolloverAndRollback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		now := time.Date(2026, 10, 30, 23, 59, 59, 0, time.Local)
		bpExec(t, s, "UPDATE fan_medals SET intimacy=500,today=98,day=? WHERE mid=1 AND ruid=2", medalDay(now))
		feed := func(at time.Time) error {
			return s.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error {
				if err := LockTx(t.Context(), tx); err != nil {
					return err
				}
				return feedMedal(t.Context(), tx, 1, 2, at)
			})
		}
		read := func(want int64) {
			t.Helper()
			var n int64
			if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT intimacy FROM fan_medals WHERE mid=1 AND ruid=2").Scan(&n); err != nil || n != want {
				t.Fatal(n, err)
			}
		}
		if err := feed(now); err != nil {
			t.Fatal(err)
		}
		read(502)
		if err := feed(now); err != nil {
			t.Fatal(err)
		}
		read(502)
		if err := feed(now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		read(507)
		// A failed feed must also roll back the persisted chat and global sequence.
		bpExec(t, s, "ALTER TABLE fan_medals RENAME TO unavailable_medals")
		admin := login(t, s, "jumedia", "admin123")
		var before, seq int64
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM live_messages").Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT value FROM ids WHERE name='global'").Scan(&seq); err != nil {
			t.Fatal(err)
		}
		assertCode(t, bpPost(t, s, "/msg/send", url.Values{"roomid": {"20000"}, "msg": {"回滚"}}, admin), -500)
		var after, next int64
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM live_messages").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT value FROM ids WHERE name='global'").Scan(&next); err != nil {
			t.Fatal(err)
		}
		if before != after || seq != next {
			t.Fatal("partial commit", before, after, seq, next)
		}
		bpExec(t, s, "ALTER TABLE unavailable_medals RENAME TO fan_medals")
	})
}

func TestT19RankTop50AndOwn(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		bpExec(t, s, "DELETE FROM fan_medals WHERE ruid=2")
		for i := 0; i < 55; i++ {
			bpExec(t, s, "INSERT INTO users(mid,username,uname) VALUES (?,?,?)", 100+i, fmt.Sprintf("fan%d", i), fmt.Sprintf("粉丝%d", i))
			bpExec(t, s, "INSERT INTO fan_medals(medal_id,mid,ruid,intimacy) VALUES (?,?,?,?)", 9000+i, 100+i, 2, 10000-i*10)
		}
		bpExec(t, s, "INSERT INTO fan_medals(medal_id,mid,ruid,intimacy) VALUES (9999,1,2,0)")
		rooms, err := s.Store.ListLiveRoom(t.Context(), "WHERE uid=2 ORDER BY room_id LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/rankdb/v1/RoomRank/webMedalRank?roomid=%d", rooms[0].RoomID)
		d := bpData(t, get(t, s, path, admin))
		list := d["list"].([]any)
		own := d["own"].(map[string]any)
		if len(list) != 50 || d["total"] != float64(56) || own["rank"] != float64(56) || own["isSelf"] != float64(1) || own["level"] != float64(1) {
			t.Fatal(d)
		}
		for i, row := range list {
			v := row.(map[string]any)
			if v["uid"] != float64(100+i) || v["rank"] != float64(i+1) || v["target_id"] != float64(2) || v["isSelf"] != float64(0) {
				t.Fatal(v)
			}
		}
		if d := bpData(t, get(t, s, path)); d["own"] != nil {
			t.Fatal(d)
		}
		bpExec(t, s, "DELETE FROM fan_medals WHERE ruid=2")
		d = bpData(t, get(t, s, path, admin))
		if len(d["list"].([]any)) != 0 || d["own"] != nil || d["total"] != float64(0) {
			t.Fatal(d)
		}
	})
}
