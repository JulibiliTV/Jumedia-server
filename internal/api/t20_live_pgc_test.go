package api

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"jumedia-server/internal/db"
)

func TestT20StreamKey(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		path := "/xlive/app-blink/v1/live/getWebUpStreamAddr"
		u := login(t, s, "jumedia", "admin123")
		bpError(t, get(t, s, path), -101, "账号未登录")
		getInfo := func() map[string]any {
			return creatorData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", u))["rtmp"].(map[string]any)
		}
		before := getInfo()
		same := getInfo()
		if !reflect.DeepEqual(before, same) {
			t.Fatal("key not persistent", before, same)
		}
		addr := creatorData(t, get(t, s, path, u))
		if !reflect.DeepEqual(addr["addr"], before) || len(addr["line_list"].([]any)) != 0 || addr["srt_addr"] != nil {
			t.Fatal(addr)
		}
		bpError(t, get(t, s, path+"?reset_key=true", u), -111, "csrf 校验失败")
		bpError(t, post(t, s, path, url.Values{"reset_key": {"1"}}, u), -111, "csrf 校验失败")
		if !reflect.DeepEqual(getInfo(), before) {
			t.Fatal("unauthorized rotation")
		}
		reset := creatorData(t, post(t, s, path, url.Values{"csrf": {u.CSRF}, "reset_key": {"true"}}, u))["addr"].(map[string]any)
		if reset["code"] == before["code"] || !reflect.DeepEqual(reset, getInfo()) {
			t.Fatal(reset, before)
		}
		adminTestLog(t, s, "重置推流码", creatorData(t, get(t, s, "/xlive/app-blink/v1/room/GetInfo", u))["title"].(string))
		reset2 := creatorData(t, get(t, s, path+"?reset_key=true&csrf="+u.CSRF, u))["addr"].(map[string]any)
		if reset2["code"] == reset["code"] {
			t.Fatal("GET reset did not rotate")
		}
		bpExec(t, s, "ALTER TABLE logs RENAME TO t20_stream_logs")
		assertCode(t, post(t, s, path, url.Values{"csrf": {u.CSRF}, "reset_key": {"true"}}, u), -500)
		if !reflect.DeepEqual(getInfo(), reset2) {
			t.Fatal("failed reset changed key")
		}
		bpExec(t, s, "ALTER TABLE t20_stream_logs RENAME TO logs")
		// A new user without a room gets one through the new route, including races.
		viewer := creatorOther(t, s)
		bpExec(t, s, "DELETE FROM rooms WHERE uid=?", viewer.MID)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); creatorData(t, get(t, s, path, viewer)) }()
		}
		wg.Wait()
		if adminTestCount(t, s, "SELECT COUNT(*) FROM rooms WHERE uid=?", viewer.MID) != 1 {
			t.Fatal("duplicate room")
		}
		var key string
		if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT stream_key FROM rooms WHERE uid=?", viewer.MID).Scan(&key); err != nil || len(key) != 24 {
			t.Fatal(key, err)
		}
	})
}

func TestT20EpisodeExtras(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "jumedia", "admin123")
		input := map[string]any{"title": "T20 看点", "episodes": []any{
			map[string]any{"duration": 600, "media": "/mock/media/sample1.mp4", "cover": "/poster.jpg", "op": []any{30.3, 119.6}, "points": []any{map[string]any{"from": 300.5, "content": " 转折 "}, map[string]any{"from": 0, "content": strings.Repeat("前", 45)}, map[string]any{"from": 600, "content": "越界"}, map[string]any{"from": -1, "content": "负数"}, map[string]any{"from": 25, "content": " "}, map[string]any{"from": "nan", "content": "无效"}, map[string]any{"content": "缺失时间"}}},
			map[string]any{"duration": 100, "op": []any{20, 101}, "points": []any{}},
		}}
		data := creatorData(t, adminTestSeasonSave(t, s, u, input))
		id := data["season_id"]
		rows := creatorData(t, get(t, s, "/x/admin/seasons?keyword="+url.QueryEscape("T20 看点"), u))["list"].([]any)
		eps := rows[0].(map[string]any)["episodes"].([]any)
		first := eps[0].(map[string]any)
		second := eps[1].(map[string]any)
		if !reflect.DeepEqual(first["op"], []any{float64(30), float64(120)}) || second["op"] != nil || second["points"] != nil {
			t.Fatal(eps)
		}
		points := first["points"].([]any)
		if len(points) != 2 || points[0].(map[string]any)["from"] != float64(0) || UTF16Length(points[0].(map[string]any)["content"].(string)) != 40 || points[1].(map[string]any)["from"] != float64(301) {
			t.Fatal(points)
		}
		play := get(t, s, fmt.Sprintf("/pgc/player/web/playurl?ep_id=%v", first["ep_id"]))
		assertCode(t, play, 0)
		clips := play["result"].(map[string]any)["clip_info_list"].([]any)
		if len(clips) != 1 || !reflect.DeepEqual(clips[0], map[string]any{"start": float64(30), "end": float64(120), "clipType": "CLIP_TYPE_OP", "toastText": "即将跳过片头"}) {
			t.Fatal(clips)
		}
		player := creatorData(t, get(t, s, fmt.Sprintf("/x/player/wbi/v2?aid=%v&cid=%v", first["aid"], first["cid"])))
		vp := player["view_points"].([]any)
		if len(vp) != 2 || vp[0].(map[string]any)["type"] != float64(2) || vp[0].(map[string]any)["to"] != float64(301) || vp[1].(map[string]any)["to"] != float64(600) || !strings.HasSuffix(vp[0].(map[string]any)["imgUrl"].(string), "/poster.jpg") {
			t.Fatal(player)
		}
		play = get(t, s, fmt.Sprintf("/pgc/player/web/playurl?ep_id=%v", second["ep_id"]))
		assertCode(t, play, 0)
		if len(play["result"].(map[string]any)["clip_info_list"].([]any)) != 0 {
			t.Fatal(play)
		}
		noPoints := creatorData(t, get(t, s, "/x/player/wbi/v2?aid=-1&cid=-1"))
		if len(noPoints["view_points"].([]any)) != 0 {
			t.Fatal(noPoints)
		}
		bpError(t, get(t, s, "/pgc/player/web/playurl?ep_id=-1"), -404, "剧集不存在")
		bpError(t, adminTestSeasonSave(t, s, u, map[string]any{}), -400, "请填写标题")
		assertCode(t, get(t, s, "/x/admin/seasons"), -101)
		// Invalid extras are discarded on replacement, not inherited from old data.
		input["season_id"] = id
		input["episodes"] = []any{map[string]any{"ep_id": first["ep_id"], "aid": first["aid"], "cid": first["cid"], "duration": 600, "op": []any{5, 4}, "points": []any{map[string]any{"from": 0, "content": ""}}}}
		assertCode(t, adminTestSeasonSave(t, s, u, input), 0)
		extra, err := readEpisodeExtras(t.Context(), s.Store.DB, int64(first["ep_id"].(float64)))
		if err != nil || len(extra.OP) != 0 || len(extra.Points) != 0 {
			t.Fatal(extra, err)
		}
	})
}

func TestT20MedalDecay(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "jumedia", "admin123")
		now := time.Now()
		old := medalDay(now.Add(-35 * 24 * time.Hour))
		bpExec(t, s, "UPDATE fan_medals SET wearing=0 WHERE mid=1")
		bpExec(t, s, "UPDATE fan_medals SET intimacy=120,today=90,day=?,wearing=1 WHERE medal_id=7001", old)
		bpExec(t, s, "UPDATE fan_medals SET intimacy=90,today=0,day=? WHERE medal_id=7002", medalDay(now))
		medals, err := readFanMedals(t.Context(), s.Store.DB, "SELECT "+medalColumns+" FROM fan_medals WHERE medal_id=7001")
		if err != nil {
			t.Fatal(err)
		}
		m := medals[0]
		if medalIntimacy(m, now) != 20 {
			t.Fatal(m, medalIntimacy(m, now))
		}
		panel := creatorData(t, get(t, s, "/xlive/app-ucenter/v1/fansMedal/panel", u))
		special := panel["special_list"].([]any)[0].(map[string]any)["medal"].(map[string]any)
		if special["total_intimacy"] != float64(20) || special["level"] != float64(1) || special["today_feed"] != float64(0) {
			t.Fatal(special)
		}
		worn, err := s.wornMedal(t.Context(), 1)
		if err != nil || worn["level"] != 1 {
			t.Fatal(worn, err)
		}
		// Feed uses the decayed total once, then the second same-day chat adds only 5.
		for i := 0; i < 2; i++ {
			if err = s.Store.DB.WithTx(t.Context(), func(tx *db.Tx) error { return feedMedal(t.Context(), tx, 1, m.RUID, now) }); err != nil {
				t.Fatal(err)
			}
		}
		var total, today, day int64
		if err = s.Store.DB.QueryRowContext(t.Context(), "SELECT intimacy,today,day FROM fan_medals WHERE medal_id=7001").Scan(&total, &today, &day); err != nil || total != 30 || today != 10 || day != medalDay(now) {
			t.Fatal(total, today, day, err)
		}
		roomsForChat, err := s.Store.ListLiveRoom(t.Context(), "WHERE uid=? ORDER BY room_id", m.RUID)
		if err != nil || len(roomsForChat) == 0 {
			t.Fatal(roomsForChat, err)
		}
		assertCode(t, bpPost(t, s, "/msg/send", url.Values{"roomid": {fmt.Sprint(roomsForChat[0].RoomID)}, "msg": {"今天继续互动"}}, u), 0)
		if got := adminTestCount(t, s, "SELECT intimacy FROM fan_medals WHERE medal_id=7001"); got != 35 {
			t.Fatal(got)
		}
		// Boundary and ctime fallback do not truncate negative fractional days toward zero.
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		for _, tc := range []struct {
			days int
			want int64
		}{{30, 120}, {31, 100}, {50, 0}} {
			v := fanMedal{Intimacy: 120, Ctime: start.Add(-time.Duration(tc.days) * 24 * time.Hour).Unix()}
			if got := medalIntimacy(v, start); got != tc.want {
				t.Fatal(tc, got)
			}
		}
		// Decay must change the ordering, even when raw intimacy suggests the reverse.
		bpExec(t, s, "UPDATE fan_medals SET wearing=0,intimacy=0,day=? WHERE mid=1", medalDay(now))
		bpExec(t, s, "UPDATE fan_medals SET intimacy=150,day=? WHERE medal_id=7001", old)
		bpExec(t, s, "UPDATE fan_medals SET intimacy=90 WHERE medal_id=7002")
		panel = creatorData(t, get(t, s, "/xlive/app-ucenter/v1/fansMedal/panel", u))
		if panel["list"].([]any)[0].(map[string]any)["medal"].(map[string]any)["medal_id"] != float64(7002) {
			t.Fatal(panel)
		}
		bpExec(t, s, "DELETE FROM fan_medals WHERE ruid=? AND mid<>1", m.RUID)
		bpExec(t, s, "INSERT INTO fan_medals(medal_id,mid,ruid,intimacy,day,ctime) VALUES (990001,3,?,90,?,?)", m.RUID, medalDay(now), now.Unix())
		rooms, err := s.Store.ListLiveRoom(t.Context(), "WHERE uid=?", m.RUID)
		if err != nil || len(rooms) == 0 {
			t.Fatal(rooms, err)
		}
		rank := creatorData(t, get(t, s, fmt.Sprintf("/rankdb/v1/RoomRank/webMedalRank?roomid=%d", rooms[0].RoomID), u))
		if rank["list"].([]any)[0].(map[string]any)["uid"] != float64(3) || rank["own"].(map[string]any)["rank"] != float64(2) {
			t.Fatal(rank)
		}
	})
}
