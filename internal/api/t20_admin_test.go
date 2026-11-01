package api

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestT20Moderation(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "jumedia", "admin123")
		other := creatorOther(t, s)
		for _, path := range []string{"/x/admin/replies?flagged=1", "/x/admin/danmaku?flagged=1", "/x/admin/dashboard", "/x/admin/live/caches"} {
			assertCode(t, get(t, s, path), -101)
			assertCode(t, get(t, s, path, other), -403)
		}
		bpExec(t, s, "DELETE FROM replies")
		bpExec(t, s, "DELETE FROM danmaku")
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
		texts := []string{"加V WWW.test", "正常讨论", "http://example.test", "联系1234567", "老评论加微信"}
		for i, text := range texts {
			at := now.Unix()
			if i == 4 {
				at = today - 1
			}
			bpExec(t, s, "INSERT INTO replies(rpid,oid,mid,message,ctime) VALUES (?,100100,1,?,?)", 990000+i, text, at)
			bpExec(t, s, "INSERT INTO danmaku(id,cid,mid,content,ctime,weight) VALUES (?,50001,1,?,?,10)", 990000+i, text, at)
		}
		bpExec(t, s, "INSERT INTO danmaku(id,cid,mid,content,ctime,weight) VALUES (999999,50001,1,'种子加微信',?,1)", now.Unix())
		for _, path := range []string{"/x/admin/replies", "/x/admin/danmaku"} {
			data := creatorData(t, get(t, s, path+"?flagged=1&ps=2", u))
			list := data["list"].([]any)
			stat := data["stat"].(map[string]any)
			if len(list) != 2 || data["page"].(map[string]any)["total"] != float64(4) || stat["reply_today"] != float64(4) || stat["danmaku_today"] != float64(4) {
				t.Fatal(data)
			}
			if list[0].(map[string]any)["flag"] != "敏感词「加V」" {
				t.Fatal(list)
			}
			normal := creatorData(t, get(t, s, path+"?keyword="+url.QueryEscape("正常"), u))["list"].([]any)
			if len(normal) != 1 || normal[0].(map[string]any)["flag"] != "" {
				t.Fatal(normal)
			}
			filtered := creatorData(t, get(t, s, path+"?flagged=1&keyword="+url.QueryEscape("正常"), u))
			if len(filtered["list"].([]any)) != 0 {
				t.Fatal(filtered)
			}
		}
		seeded := creatorData(t, get(t, s, "/x/admin/danmaku?flagged=1&keyword="+url.QueryEscape("种子"), u))
		if len(seeded["list"].([]any)) != 1 {
			t.Fatal(seeded)
		}
		dashboard := creatorData(t, get(t, s, "/x/admin/dashboard", u))
		if dashboard["reply_today"] != float64(4) || dashboard["danmaku_today"] != float64(4) {
			t.Fatal(dashboard)
		}
		assertCode(t, adminTestPost(t, s, u, "settings/save", url.Values{"sensitiveWords": {" 正常 \nHTTP\n"}}), 0)
		settings := creatorData(t, get(t, s, "/x/admin/settings", u))
		if settings["sensitiveWords"] != " 正常 \nHTTP\n" {
			t.Fatal(settings)
		}
		updated := creatorData(t, get(t, s, "/x/admin/replies?flagged=1&keyword="+url.QueryEscape("正常"), u))["list"].([]any)
		if len(updated) != 1 || updated[0].(map[string]any)["flag"] != "敏感词「正常」" {
			t.Fatal(updated)
		}
		if flagOf("\n HTTP \n正常", "http://x.test") != "敏感词「HTTP」" || flagOf("", "WWW.X.test") != "疑似广告" || flagOf("", "123456") != "" {
			t.Fatal("flag precedence")
		}
		assertCode(t, post(t, s, "/x/admin/settings/save", url.Values{"sensitiveWords": {""}}, u), -111)
		// The total includes all caches, not just the current page.
		for i, size := range []int{123, 456} {
			bpExec(t, s, "INSERT INTO live_caches(key,media,height,aid,cid,size,dir,ctime,mtime) VALUES (?,?,1080,100100,50001,?,'',0,0)", fmt.Sprintf("t20cache%d", i), fmt.Sprintf("/t20cache%d.mp4", i), size)
		}
		caches := creatorData(t, get(t, s, "/x/admin/live/caches?ps=1", u))
		if caches["bytes"] != float64(579) || len(caches["list"].([]any)) != 1 {
			t.Fatal(caches)
		}
	})
}

func TestT20RegionSort(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "jumedia", "admin123")
		other := creatorOther(t, s)
		roots, err := s.Store.ListRegion(t.Context(), "WHERE COALESCE(parent,0)=0 ORDER BY tid")
		if err != nil || len(roots) < 2 {
			t.Fatal(roots, err)
		}
		children, err := s.Store.ListRegion(t.Context(), "WHERE parent=? ORDER BY tid", roots[0].TID)
		if err != nil || len(children) < 2 {
			t.Fatal(children, err)
		}
		path := "/x/admin/region/sort"
		assertCode(t, post(t, s, path, nil), -101)
		assertCode(t, post(t, s, path, nil, u), -111)
		assertCode(t, bpPost(t, s, path, url.Values{"tids": {"1"}}, other), -403)
		tids := fmt.Sprintf("%d,%d", roots[1].TID, roots[0].TID)
		assertCode(t, bpPost(t, s, path, url.Values{"tids": {tids}}, u), 0)
		adminTestLog(t, s, "调整分区排序", roots[1].Name+"、"+roots[0].Name)
		for i, id := range []int64{roots[1].TID, roots[0].TID} {
			if n := adminTestCount(t, s, "SELECT sort FROM regions WHERE tid=?", id); n != i {
				t.Fatal(n)
			}
		}
		assertCode(t, bpPost(t, s, path, url.Values{"tids": {fmt.Sprintf("%d,%d", children[1].TID, children[0].TID)}}, u), 0)
		otherChildren, err := s.Store.ListRegion(t.Context(), "WHERE parent=? ORDER BY tid", roots[1].TID)
		if err != nil || len(otherChildren) == 0 {
			t.Fatal(otherChildren, err)
		}
		bpError(t, bpPost(t, s, path, url.Values{"tids": {fmt.Sprintf("%d,%d", children[0].TID, otherChildren[0].TID)}}, u), -400, "分区排序参数无效")
		before := adminTestCount(t, s, "SELECT COUNT(*) FROM logs")
		for _, bad := range []string{"", "0,NaN", "999999", fmt.Sprintf("%d,%d", roots[0].TID, children[0].TID), fmt.Sprintf("%d,%d", roots[0].TID, roots[0].TID), fmt.Sprintf("%d,999999", roots[1].TID)} {
			bpError(t, bpPost(t, s, path, url.Values{"tids": {bad}}, u), -400, "分区排序参数无效")
		}
		if before != adminTestCount(t, s, "SELECT COUNT(*) FROM logs") {
			t.Fatal("failed sort logged")
		}
		assertCode(t, bpPost(t, s, path, url.Values{"tids": {",0," + strings.Split(tids, ",")[0] + ",NaN"}}, u), 0)
	})
}
