package api

import (
	"fmt"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestT19ChatMedalsAndFeed(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		rooms, err := s.Store.ListLiveRoom(t.Context(), "WHERE uid=2 ORDER BY room_id")
		if err != nil || len(rooms) == 0 {
			t.Fatal(rooms, err)
		}
		room := rooms[0]
		bpExec(t, s, "UPDATE rooms SET live_status=1 WHERE room_id=?", room.RoomID)
		bpExec(t, s, "UPDATE fan_medals SET wearing=0 WHERE mid=1")
		bpExec(t, s, "UPDATE fan_medals SET wearing=1,intimacy=95,today=0,day=? WHERE mid=1 AND ruid=2", medalDay(time.Now()))
		server := chatHTTP(t, s)
		socket := chatConnect(t, s, server, room.RoomID, session)
		bpExec(t, s, "UPDATE rooms SET live_status=0 WHERE room_id=?", room.RoomID)
		send := func(msg string) map[string]any {
			return bpPost(t, s, "/msg/send", url.Values{"roomid": {fmt.Sprint(room.RoomID)}, "msg": {msg}}, session)
		}
		assertCode(t, send("第一条"), 0)
		packet := chatJSON(t, chatRead(t, socket, 5))
		medal := liveField(t, packet, "info", 0, 15, "user", "medal").(map[string]any)
		if medal["level"] != float64(2) || medal["ruid"] != float64(2) || medal["v2_medal_color_start"] != "#5c968e" {
			t.Fatal(medal)
		}
		legacy := liveField(t, packet, "info", 3).([]any)
		user, err := s.Store.UserByID(t.Context(), 2)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(legacy, []any{float64(2), medal["name"], user.Uname, float64(room.RoomID), float64(0x5c968e)}) {
			t.Fatal(legacy)
		}
		hist := bpData(t, get(t, s, fmt.Sprintf("/xlive/web-room/v1/dM/gethistory?roomid=%d", room.RoomID)))["room"].([]any)
		last := hist[len(hist)-1].(map[string]any)
		if !reflect.DeepEqual(last["user"].(map[string]any)["medal"], medal) {
			t.Fatal(last, medal)
		}
		messages, err := s.Store.ListLiveMessage(t.Context(), "WHERE room_id=? ORDER BY id DESC LIMIT 1", room.RoomID)
		if err != nil {
			t.Fatal(err)
		}
		m := messages[0]
		sender, err := s.Store.UserByID(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		delete(last["user"].(map[string]any), "medal")
		expected := map[string]any{"text": m.Text, "uid": float64(1), "nickname": sender.Uname, "timeline": liveDate(m.TS), "id_str": fmt.Sprint(m.ID), "check_info": map[string]any{"ts": float64(m.TS)}, "user": map[string]any{"uid": float64(1), "base": map[string]any{"name": sender.Uname, "face": liveChatFace("http://example.com", sender.Face)}}, "medal_info": nil}
		if !reflect.DeepEqual(last, expected) {
			t.Fatal("gethistory changed", last, expected)
		}
		for i := 0; i < 22; i++ {
			assertCode(t, send("增加亲密度"), 0)
			chatRead(t, socket, 5)
		}
		check := func(intimacy, today int) {
			t.Helper()
			var a, b int
			if err := s.Store.DB.QueryRowContext(t.Context(), "SELECT intimacy,today FROM fan_medals WHERE mid=1 AND ruid=2").Scan(&a, &b); err != nil || a != intimacy || b != today {
				t.Fatal(a, b, err)
			}
		}
		check(195, 100)
		bpError(t, send(" "), -400, "弹幕需在 1–40 字之间")
		check(195, 100)
		bpExec(t, s, "UPDATE fan_medals SET day=? WHERE mid=1 AND ruid=2", medalDay(time.Now().Add(-24*time.Hour)))
		assertCode(t, send("新的一天"), 0)
		chatRead(t, socket, 5)
		check(200, 5)
		assertCode(t, bpPost(t, s, "/xlive/web-room/v1/fansMedal/take_off", url.Values{}, session), 0)
		assertCode(t, send("没有佩戴"), 0)
		packet = chatJSON(t, chatRead(t, socket, 5))
		if liveField(t, packet, "info", 0, 15, "user", "medal") != nil || len(liveField(t, packet, "info", 3).([]any)) != 0 {
			t.Fatal(packet)
		}
		hist = bpData(t, get(t, s, fmt.Sprintf("/xlive/web-room/v1/dM/gethistory?roomid=%d", room.RoomID)))["room"].([]any)
		if hist[len(hist)-1].(map[string]any)["user"].(map[string]any)["medal"] != nil {
			t.Fatal(hist)
		}
	})
}

func TestT19LogFilters(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		bpExec(t, s, "DELETE FROM logs")
		actions := []string{"审核稿件", "封禁用户", "直播转码", "同步数据源元信息", "修改站点设置", "修改资料", "删除评论弹幕", "图片缓存", "初始化"}
		now := time.Now().Unix()
		for i, action := range actions {
			bpExec(t, s, "INSERT INTO logs(id,mid,action,target,ctime) VALUES (?,?,?,?,?)", 9000+i, 1+i%2, action, "target", now-int64(i)*86400)
		}
		bpError(t, get(t, s, "/x/admin/logs?kind=live"), -101, "账号未登录")
		for kind, want := range map[string]int{"archive": 2, "user": 2, "live": 1, "datasource": 2, "settings": 2, "unknown": 9} {
			d := bpData(t, get(t, s, "/x/admin/logs?kind="+kind+"&ps=1", session))
			if d["page"].(map[string]any)["total"] != float64(want) || len(d["list"].([]any)) != 1 || len(d["operators"].([]any)) != 2 {
				t.Fatal(kind, d)
			}
		}
		d := bpData(t, get(t, s, "/x/admin/logs?kind=user&days=3&mid=2", session))
		if d["page"].(map[string]any)["total"] != float64(1) || d["list"].([]any)[0].(map[string]any)["action"] != "封禁用户" {
			t.Fatal(d)
		}
		d = bpData(t, get(t, s, "/x/admin/logs?mid=999", session))
		if len(d["list"].([]any)) != 0 || len(d["operators"].([]any)) != 2 {
			t.Fatal(d)
		}
	})
}
