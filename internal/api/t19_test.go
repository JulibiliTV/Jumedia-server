package api

import (
	"fmt"
	"net/url"
	"testing"
)

func TestT19Routes(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		weekly := bpData(t, get(t, s, "/x/web-interface/popular/series/list"))["list"].([]any)
		if len(weekly) == 0 || len(weekly) > 30 {
			t.Fatal(weekly)
		}
		n := weekly[0].(map[string]any)["number"]
		issue := bpData(t, get(t, s, fmt.Sprintf("/x/web-interface/popular/series/one?number=%v", n)))
		if issue["config"].(map[string]any)["number"] != n || issue["reminder"] != "每周五 18:00 更新" || len(issue["list"].([]any)) != 12 {
			t.Fatal(issue)
		}
		for _, number := range []string{"0", "-1", "999999", "0x10000000000000000"} {
			bpError(t, get(t, s, "/x/web-interface/popular/series/one?number="+number), -404, "啥都木有")
		}
		panelPath := "/xlive/app-ucenter/v1/fansMedal/panel"
		bpError(t, get(t, s, panelPath), -101, "账号未登录")
		panel := bpData(t, get(t, s, panelPath, session))
		if panel["total_number"] != float64(6) || len(panel["special_list"].([]any)) != 1 || len(panel["list"].([]any)) != 5 {
			t.Fatal(panel)
		}
		medal := panel["special_list"].([]any)[0].(map[string]any)["medal"].(map[string]any)
		if medal["medal_id"] != float64(7001) || medal["level"] != float64(10) || medal["intimacy"] != float64(480) || medal["next_intimacy"] != float64(500) || medal["today_feed"] != float64(0) {
			t.Fatal(medal)
		}
		room := panel["special_list"].([]any)[0].(map[string]any)["room_info"].(map[string]any)["room_id"]
		rankPath := fmt.Sprintf("/rankdb/v1/RoomRank/webMedalRank?roomid=%v&ruid=-1", room)
		rank := bpData(t, get(t, s, rankPath, session))
		if rank["own"] == nil || rank["medal_name"] != medal["medal_name"] {
			t.Fatal(rank)
		}
		bpError(t, get(t, s, "/rankdb/v1/RoomRank/webMedalRank?roomid=-1"), 19002003, "房间不存在或已被封禁")
		for _, suffix := range []string{"wear", "take_off", "join"} {
			bpError(t, post(t, s, "/xlive/web-room/v1/fansMedal/"+suffix, nil), -101, "账号未登录")
		}
		assertCode(t, bpPost(t, s, "/xlive/web-room/v1/fansMedal/wear", url.Values{"medal_id": {"7002"}}, session), 0)
		bpError(t, bpPost(t, s, "/xlive/web-room/v1/fansMedal/wear", url.Values{"medal_id": {"999999"}}, session), -404, "勋章不存在")
		assertCode(t, bpPost(t, s, "/xlive/web-room/v1/fansMedal/take_off", url.Values{}, session), 0)
		rooms, err := s.Store.ListLiveRoom(t.Context(), "WHERE uid<>1 ORDER BY room_id")
		if err != nil {
			t.Fatal(err)
		}
		bpExec(t, s, "DELETE FROM fan_medals WHERE mid=1 AND ruid=?", rooms[len(rooms)-1].UID)
		joined := bpData(t, bpPost(t, s, "/xlive/web-room/v1/fansMedal/join", url.Values{"roomid": {fmt.Sprint(rooms[len(rooms)-1].RoomID)}}, session))
		if joined["medal"].(map[string]any)["wearing_status"] != float64(1) {
			t.Fatal(joined)
		}
		own, err := s.Store.ListLiveRoom(t.Context(), "WHERE uid=1 ORDER BY room_id")
		if err != nil || len(own) == 0 {
			t.Fatal(own, err)
		}
		bpError(t, bpPost(t, s, "/xlive/web-room/v1/fansMedal/join", url.Values{"roomid": {fmt.Sprint(own[0].RoomID)}}, session), -400, "不能加入自己的粉丝团")
		bpError(t, bpPost(t, s, "/x/v2/history/delete", url.Values{"kid": {"archive_nope"}}, session), -400, "请求错误")
		bpError(t, post(t, s, "/x/v2/history/delete", url.Values{"kid": {"archive_100100"}}), -101, "账号未登录")
		assertCode(t, bpPost(t, s, "/x/v2/history/delete", url.Values{"kid": {"archive_100100"}}, session), 0)
	})
}
