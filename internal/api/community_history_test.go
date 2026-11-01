package api

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"testing"

	"jumedia-server/internal/store"
)

func TestCommunityHistory(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		user := login(t, s, "jumedia", "admin123")
		other := login(t, s, "lumen", "demo123")
		arcs := communityArchives(t, s)
		a := arcs[0]
		assertCode(t, communityWrite(t, s, user, "/x/v2/history/clear", nil), 0)
		heartbeat := func(session *store.Session, aid, cid int64, progress int) {
			t.Helper()
			v := communityWrite(t, s, session, "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(aid)}, "cid": {fmt.Sprint(cid)}, "played_time": {fmt.Sprint(progress)}})
			assertCode(t, v, 0)
			if v["data"] != nil {
				t.Fatal(v)
			}
		}
		heartbeat(user, a.AID, a.Pages[0].CID, 4)
		heartbeat(other, a.AID, a.Pages[0].CID, 12)
		history := communityData(t, get(t, s, "/x/web-interface/history/cursor", user))
		list := history["list"].([]any)
		if len(list) != 1 {
			t.Fatal(history)
		}
		row := list[0].(map[string]any)
		h := row["history"].(map[string]any)
		if h["oid"] != float64(a.AID) || h["bvid"] != a.BVID || h["cid"] != float64(a.Pages[0].CID) || h["business"] != "archive" || h["dt"] != float64(2) || row["progress"] != float64(4) || row["uri"] != "/video/"+a.BVID || row["author_mid"] != float64(a.MID) {
			t.Fatal(row)
		}
		assertCode(t, communityWrite(t, s, user, "/x/v2/history/shadow/set", url.Values{"switch": {"true"}}), 0)
		shadow := get(t, s, "/x/v2/history/shadow", user)
		assertCode(t, shadow, 0)
		if shadow["data"] != true {
			t.Fatal(shadow)
		}
		heartbeat(user, a.AID, a.Pages[0].CID, 19)
		history = communityData(t, get(t, s, "/x/web-interface/history/cursor", user))
		if history["list"].([]any)[0].(map[string]any)["progress"] != float64(4) {
			t.Fatal("paused heartbeat changed progress")
		}
		assertCode(t, communityWrite(t, s, user, "/x/v2/history/shadow/set", url.Values{"switch": {"false"}}), 0)
		shadow = get(t, s, "/x/v2/history/shadow", user)
		assertCode(t, shadow, 0)
		if shadow["data"] != false {
			t.Fatal(shadow)
		}
		heartbeat(user, a.AID, 0, -1)
		history = communityData(t, get(t, s, "/x/web-interface/history/cursor", user))
		list = history["list"].([]any)
		if len(list) != 1 || list[0].(map[string]any)["progress"] != float64(-1) || list[0].(map[string]any)["history"].(map[string]any)["cid"] != float64(a.Pages[0].CID) {
			t.Fatal("heartbeat upsert or page fallback", history)
		}
		eps, e := s.Store.ListEpisode(context.Background(), "ORDER BY ep_id LIMIT 1")
		if e != nil {
			t.Fatal(e)
		}
		ep := eps[0]
		v := communityWrite(t, s, user, "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(ep.AID)}, "cid": {fmt.Sprint(ep.CID)}, "epid": {fmt.Sprint(ep.EpID)}, "sid": {fmt.Sprint(ep.SeasonID)}, "played_time": {"15"}})
		assertCode(t, v, 0)
		history = communityData(t, get(t, s, "/x/web-interface/history/cursor", user))
		found := false
		for _, r := range history["list"].([]any) {
			row := r.(map[string]any)
			h := row["history"].(map[string]any)
			if h["epid"] == float64(ep.EpID) {
				found = true
				if h["business"] != "pgc" || row["author_name"] != "番剧" || row["uri"] != fmt.Sprintf("/bangumi/play/ep%d", ep.EpID) || row["kid"] != float64(ep.SeasonID) || row["duration"] != float64(ep.Duration) {
					t.Fatal(row)
				}
			}
		}
		if !found {
			t.Fatal("missing PGC history")
		}
		assertCode(t, communityWrite(t, s, user, "/x/v2/history/clear", nil), 0)
		otherHistory := communityData(t, get(t, s, "/x/web-interface/history/cursor", other))
		if len(otherHistory["list"].([]any)) != 1 {
			t.Fatal("clear affected another user")
		}
		// Same-second records use max as the secondary cursor; older rows remain.
		sort.Slice(arcs, func(i, j int) bool { return arcs[i].AID > arcs[j].AID })
		for i, a := range arcs[:3] {
			at := int64(100)
			if i == 2 {
				at = 99
			}
			if e := store.InsertHistory(context.Background(), s.Store.DB, &store.History{MID: user.MID, AID: a.AID, CID: a.Pages[0].CID, ViewAt: at}); e != nil {
				t.Fatal(e)
			}
		}
		first := communityData(t, get(t, s, "/x/web-interface/history/cursor?ps=1", user))
		cursor := first["cursor"].(map[string]any)
		next := communityData(t, get(t, s, fmt.Sprintf("/x/web-interface/history/cursor?ps=2&max=%v&view_at=%v", cursor["max"], cursor["view_at"]), user))
		if len(next["list"].([]any)) != 2 || next["list"].([]any)[0].(map[string]any)["history"].(map[string]any)["oid"] != float64(arcs[1].AID) {
			t.Fatal(next)
		}
		empty := communityData(t, get(t, s, "/x/web-interface/history/cursor?view_at=1&max=0", user))
		if len(empty["list"].([]any)) != 0 || empty["cursor"].(map[string]any)["max"] != float64(0) {
			t.Fatal(empty)
		}
		// Mock slices before filtering deleted archives, so this page is empty.
		if e := store.InsertHistory(context.Background(), s.Store.DB, &store.History{MID: user.MID, AID: 999999, ViewAt: 101}); e != nil {
			t.Fatal(e)
		}
		empty = communityData(t, get(t, s, "/x/web-interface/history/cursor?ps=1", user))
		if len(empty["list"].([]any)) != 0 {
			t.Fatal(empty)
		}
	})
}

func TestCommunityToView(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		user := login(t, s, "jumedia", "admin123")
		other := login(t, s, "lumen", "demo123")
		arcs := communityArchives(t, s)
		communityExec(t, s, "DELETE FROM toview WHERE mid=?", user.MID)
		for i, a := range arcs[:3] {
			v := communityWrite(t, s, user, "/x/v2/history/toview/add", url.Values{"aid": {fmt.Sprint(a.AID)}})
			assertCode(t, v, 0)
			if v["data"] != nil {
				t.Fatal(v)
			}
			communityExec(t, s, "UPDATE toview SET add_at=? WHERE mid=? AND aid=?", 100+i, user.MID, a.AID)
		}
		communityFailure(t, communityWrite(t, s, user, "/x/v2/history/toview/add", url.Values{"aid": {fmt.Sprint(arcs[0].AID)}}), 90001, "已经在稍后再看里了")
		communityFailure(t, communityWrite(t, s, user, "/x/v2/history/toview/add", url.Values{"aid": {"999999"}}), -404, "啥都木有")
		assertCode(t, communityWrite(t, s, user, "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(arcs[2].AID)}, "cid": {fmt.Sprint(arcs[2].Pages[0].CID)}, "played_time": {"18"}}), 0)
		list := communityData(t, get(t, s, "/x/v2/history/toview", user))
		rows := list["list"].([]any)
		if list["count"] != float64(3) || rows[0].(map[string]any)["aid"] != float64(arcs[2].AID) || rows[0].(map[string]any)["progress"] != float64(18) || rows[0].(map[string]any)["add_at"] != float64(102) || rows[0].(map[string]any)["bvid"] != arcs[2].BVID {
			t.Fatal(list)
		}
		for _, asc := range []string{"true", "false", "1"} {
			list = communityData(t, get(t, s, "/x/v2/history/toview/web?pn=1&ps=1&asc="+asc, user))
			expected := arcs[2].AID
			if asc == "true" {
				expected = arcs[0].AID
			}
			if list["count"] != float64(3) || len(list["list"].([]any)) != 1 || list["list"].([]any)[0].(map[string]any)["aid"] != float64(expected) {
				t.Fatal(list)
			}
		}
		list = communityData(t, get(t, s, "/x/v2/history/toview/web?pn=2&ps=2", user))
		if len(list["list"].([]any)) != 1 || list["count"] != float64(3) {
			t.Fatal(list)
		}
		assertCode(t, communityWrite(t, s, other, "/x/v2/history/toview/add", url.Values{"aid": {fmt.Sprint(arcs[0].AID)}}), 0)
		assertCode(t, communityWrite(t, s, user, "/x/v2/history/toview/v2/dels", url.Values{"resources": {fmt.Sprintf("%d:2,%d:2", arcs[0].AID, arcs[1].AID)}}), 0)
		list = communityData(t, get(t, s, "/x/v2/history/toview", user))
		if list["count"] != float64(1) {
			t.Fatal(list)
		}
		list = communityData(t, get(t, s, "/x/v2/history/toview", other))
		if list["count"] != float64(1) {
			t.Fatal("delete affected other user")
		}
		// archiveOf respects unpublished visibility, including aid alias precedence.
		a := arcs[3]
		communityExec(t, s, "UPDATE archives SET state=-1 WHERE aid=?", a.AID)
		foreign := other
		if foreign.MID == a.MID {
			foreign = login(t, s, "oolong", "demo123")
		}
		communityFailure(t, communityWrite(t, s, foreign, "/x/v2/history/toview/add", url.Values{"oid": {fmt.Sprint(a.AID)}}), -404, "啥都木有")
		assertCode(t, communityWrite(t, s, user, "/x/v2/history/toview/add", url.Values{"avid": {fmt.Sprint(a.AID)}}), 0)
	})
}

func TestCommunityHistoryWithoutPages(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		user := login(t, s, "jumedia", "admin123")
		a := communityArchives(t, s)[0]
		communityExec(t, s, "DELETE FROM history WHERE mid=?", user.MID)
		communityExec(t, s, "DELETE FROM archive_pages WHERE aid=?", a.AID)
		assertCode(t, communityWrite(t, s, user, "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(a.AID)}}), 0)
		// Mock historyRow fails when a retained archive has no fallback page.
		assertCode(t, get(t, s, "/x/web-interface/history/cursor", user), -500)
	})
}
