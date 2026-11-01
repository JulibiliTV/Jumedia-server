package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestT20Draft(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "jumedia", "admin123")
		v := post(t, s, "/x/vu/web/add/v3", url.Values{"csrf": {u.CSRF}, "draft": {"1"}}, u)
		assertCode(t, v, 0)
		if v["data"].(map[string]any)["state"] != float64(-20) {
			t.Fatal(v)
		}
	})
}

func TestT20PublicationWorkflow(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		admin := login(t, s, "jumedia", "admin123")
		u := creatorOther(t, s)
		logCount := adminTestCount(t, s, "SELECT COUNT(*) FROM logs")
		draft := creatorData(t, post(t, s, "/x/vu/web/add/v3", url.Values{"csrf": {u.CSRF}, "draft": {"1"}, "no_tv": {"1"}, "no_disturbance": {"true"}}, u))
		aid := fmt.Sprint(draft["aid"])
		row := creatorData(t, get(t, s, "/x/web/archive/view?aid="+aid, u))["Archive"].(map[string]any)
		if row["title"] != "未命名草稿" || row["state_desc"] != "草稿" || row["ptime"] != float64(0) || row["no_tv"] != float64(1) || row["no_disturbance"] != float64(1) {
			t.Fatal(row)
		}
		if adminTestCount(t, s, "SELECT COUNT(*) FROM logs") != logCount {
			t.Fatal("draft wrote log")
		}
		listing := creatorData(t, get(t, s, "/x/web/archives?status=draft", u))
		if listing["class"].(map[string]any)["draft"] != float64(1) || len(listing["arc_audits"].([]any)) != 1 {
			t.Fatal(listing)
		}
		adminList := creatorData(t, get(t, s, "/x/admin/archives?ps=1000", admin))["list"].([]any)
		for _, v := range adminList {
			if fmt.Sprint(v.(map[string]any)["aid"]) == aid {
				t.Fatal("admin default exposed draft")
			}
		}
		allDrafts := creatorData(t, get(t, s, "/x/admin/archives?state=-20", admin))
		if len(allDrafts["list"].([]any)) != 1 {
			t.Fatal(allDrafts)
		}
		// Draft title truncation and edits remain unlogged.
		assertCode(t, post(t, s, "/x/vu/web/edit", url.Values{"csrf": {u.CSRF}, "aid": {aid}, "draft": {"true"}, "title": {strings.Repeat("草", 90)}}, u), 0)
		row = creatorData(t, get(t, s, "/x/web/archive/view?aid="+aid, u))["Archive"].(map[string]any)
		if UTF16Length(row["title"].(string)) != 80 || adminTestCount(t, s, "SELECT COUNT(*) FROM logs") != logCount {
			t.Fatal(row)
		}
		if err := s.Store.SetSetting(t.Context(), "auditRequired", true); err != nil {
			t.Fatal(err)
		}
		p := creatorParams(u)
		p.Set("aid", aid)
		for _, d := range []int64{time.Now().Unix() + 60, time.Now().Unix() + 16*86400} {
			p.Set("dtime", fmt.Sprint(d))
			bpError(t, post(t, s, "/x/vu/web/edit", p, u), 21004, "定时发布时间需在 2 小时后、15 天内")
		}
		due := time.Now().Unix() + 3*3600
		p.Set("dtime", fmt.Sprint(due))
		submitted := creatorData(t, post(t, s, "/x/vu/web/edit", p, u))
		if submitted["state"] != float64(-1) {
			t.Fatal(submitted)
		}
		adminTestLog(t, s, "投稿", "T8 Test 视频")
		bpError(t, post(t, s, "/x/vu/web/edit", url.Values{"csrf": {u.CSRF}, "aid": {aid}, "draft": {"1"}}, u), -400, "已投稿的稿件不能存为草稿")
		assertCode(t, adminTestPost(t, s, admin, "archive/audit", url.Values{"aid": {aid}, "state": {"0"}}), 0)
		row = creatorData(t, get(t, s, "/x/web/archive/view?aid="+aid, u))["Archive"].(map[string]any)
		if row["state"] != float64(-40) || row["ptime"] != float64(due) || row["dtime"] != float64(due) {
			t.Fatal(row)
		}
		timer := creatorData(t, get(t, s, "/x/web/archives?status=timer", u))
		if timer["class"].(map[string]any)["timer"] != float64(1) || timer["class"].(map[string]any)["draft"] != float64(0) || len(timer["arc_audits"].([]any)) != 1 {
			t.Fatal(timer)
		}
		before := creatorData(t, get(t, s, "/x/web-interface/wbi/index/top/feed/rcmd?ps=1000"))["item"].([]any)
		for _, v := range before {
			if fmt.Sprint(v.(map[string]any)["aid"]) == aid {
				t.Fatal("scheduled archive leaked")
			}
		}
		// Exact due second: the request pipeline publishes before a public list reads.
		response := t20At(t, s, "/x/web-interface/wbi/index/top/feed/rcmd?ps=1000", due)
		after := creatorData(t, response)["item"].([]any)
		found := false
		for _, v := range after {
			if fmt.Sprint(v.(map[string]any)["aid"]) == aid {
				found = true
				if v.(map[string]any)["pubdate"] != float64(due) {
					t.Fatal(v)
				}
			}
		}
		if !found {
			t.Fatal("not promoted")
		}
		if err := s.Store.SetSetting(t.Context(), "auditRequired", false); err != nil {
			t.Fatal(err)
		}
		p.Set("dtime", "1")
		edited := creatorData(t, post(t, s, "/x/vu/web/edit", p, u))
		if edited["state"] != float64(0) {
			t.Fatal(edited)
		}
		row = creatorData(t, get(t, s, "/x/web/archive/view?aid="+aid, u))["Archive"].(map[string]any)
		if row["dtime"] != float64(0) || row["ptime"] != float64(due) {
			t.Fatal(row)
		}
		bpError(t, post(t, s, "/x/vu/web/edit", url.Values{"csrf": {u.CSRF}, "aid": {"-1"}, "draft": {"1"}}, u), -404, "稿件不存在")
	})
}

func t20At(t *testing.T, s *Server, path string, at int64) map[string]any {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r = r.WithContext(context.WithValue(r.Context(), publicationClockKey{}, func() time.Time { return time.Unix(at, 0) }))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestT20FeedFlagsAndStats(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "jumedia", "admin123")
		viewer := creatorOther(t, s)
		p := creatorParams(u)
		p.Set("no_tv", "1")
		p.Set("no_disturbance", "1")
		a := creatorData(t, post(t, s, "/x/vu/web/add/v3", p, u))
		aid := a["aid"]
		// Restrict fixtures to two archives so both recommendation feeds are exhaustive.
		bpExec(t, s, "UPDATE archives SET state=-2 WHERE aid<>? AND aid<>100100", aid)
		bpExec(t, s, "INSERT INTO relations(mid,fid,mtime) VALUES (?,1,0) ON CONFLICT(mid,fid) DO NOTHING", viewer.MID)
		tv := creatorData(t, get(t, s, "/x/v2/feed/index"))["items"].([]any)
		for _, v := range tv {
			if v.(map[string]any)["param"] == fmt.Sprint(aid) {
				t.Fatal("no_tv leaked")
			}
		}
		web := creatorData(t, get(t, s, "/x/web-interface/wbi/index/top/feed/rcmd", viewer))
		found := false
		for _, v := range web["item"].([]any) {
			m := v.(map[string]any)
			if m["aid"] == aid {
				found = true
				if m["id"] != aid || m["goto"] != "av" || m["uri"] != "/video/"+a["bvid"].(string) || m["is_followed"] != float64(1) {
					t.Fatal(m)
				}
			}
		}
		if !found || web["mid"] != float64(viewer.MID) {
			t.Fatal(web)
		}
		dynamic := creatorData(t, get(t, s, "/x/polymer/web-dynamic/v1/feed/all", viewer))
		raw, _ := json.Marshal(dynamic)
		if strings.Contains(string(raw), a["bvid"].(string)) {
			t.Fatal("no_disturbance leaked")
		}
		p.Set("aid", fmt.Sprint(aid))
		p.Del("no_disturbance")
		assertCode(t, post(t, s, "/x/vu/web/edit", p, u), 0)
		raw, _ = json.Marshal(creatorData(t, get(t, s, "/x/polymer/web-dynamic/v1/feed/all", viewer)))
		if !strings.Contains(string(raw), a["bvid"].(string)) {
			t.Fatal("dynamic not restored")
		}
		bpExec(t, s, "UPDATE archives SET stat_view=4000 WHERE aid=?", aid)
		bpExec(t, s, "INSERT INTO replies(rpid,oid,mid,message,ctime) VALUES (990001,?,2,'今日',?)", aid, time.Now().Unix())
		bpExec(t, s, "INSERT INTO replies(rpid,oid,mid,message,ctime) VALUES (990002,?,2,'昨日',?)", aid, time.Now().Add(-48*time.Hour).Unix())
		stat := creatorData(t, get(t, s, "/x/web/data/stat", u))
		total := stat["total_click"].(float64)
		prev := 0.0
		for i := 0; i < 14; i++ {
			prev += math.Floor(total / 400 * (0.6 + math.Abs(math.Sin(float64(i-14)*1.7+1))*0.9))
		}
		if stat["prev_view"] != prev || stat["reply_today"].(float64) < 1 || len(stat["series"].([]any)) != 14 {
			t.Fatal(stat)
		}
		info := creatorData(t, get(t, s, "/x/space/wbi/acc/info?mid=1"))
		usr, err := s.Store.UserByID(t.Context(), 1)
		if err != nil || info["jointime"] != float64(usr.Ctime) {
			t.Fatal(info, err)
		}
		bpError(t, get(t, s, "/x/space/wbi/acc/info?mid=-1"), -404, "啥都木有")
		assertCode(t, get(t, s, "/x/web/data/stat"), -101)
		assertCode(t, get(t, s, "/x/polymer/web-dynamic/v1/feed/all"), -101)
		assertCode(t, get(t, s, "/x/web-interface/wbi/index/top/feed/rcmd?fresh_idx=1.1&ps=12"), -500)
	})
}

func TestT20ScheduleBoundariesAndRollback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "jumedia", "admin123")
		now := time.Now().Unix()
		submit := func(p url.Values) map[string]any {
			r := httptest.NewRequest("POST", "/x/vu/web/add/v3", strings.NewReader(p.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(&http.Cookie{Name: "SESSDATA", Value: u.Token})
			r = r.WithContext(context.WithValue(r.Context(), publicationClockKey{}, func() time.Time { return time.Unix(now, 0) }))
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			var v map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		for _, tc := range []struct {
			delta int64
			code  int
		}{{7139, 21004}, {7140, 0}, {15 * 86400, 0}, {15*86400 + 1, 21004}} {
			p := creatorParams(u)
			p.Set("dtime", fmt.Sprint(now+tc.delta))
			v := submit(p)
			assertCode(t, v, tc.code)
			if tc.code == 0 && v["data"].(map[string]any)["state"] != float64(-40) {
				t.Fatal(v)
			}
		}
		for _, raw := range []string{"0.5", fmt.Sprintf("%.1f", float64(now+15*86400)+0.5)} {
			p := creatorParams(u)
			p.Set("dtime", raw)
			bpError(t, submit(p), 21004, "定时发布时间需在 2 小时后、15 天内")
		}
		immediate := creatorData(t, submit(creatorParams(u)))
		if immediate["state"] != float64(0) {
			t.Fatal(immediate)
		}
		row := creatorData(t, get(t, s, fmt.Sprintf("/x/web/archive/view?aid=%v", immediate["aid"]), u))["Archive"].(map[string]any)
		if row["ptime"] != float64(now) {
			t.Fatal(row)
		}
		bpExec(t, s, "UPDATE archives SET state=-1,dtime=? WHERE aid=?", now-30, immediate["aid"])
		assertCode(t, adminTestPost(t, s, u, "archive/audit", url.Values{"aid": {fmt.Sprint(immediate["aid"])}, "state": {"0"}}), 0)
		row = creatorData(t, get(t, s, fmt.Sprintf("/x/web/archive/view?aid=%v", immediate["aid"]), u))["Archive"].(map[string]any)
		if row["state"] != float64(0) || row["ptime"].(float64) < float64(now) {
			t.Fatal(row)
		}
		assertCode(t, adminTestPost(t, s, u, "archive/audit", url.Values{"aid": {fmt.Sprint(immediate["aid"])}, "state": {"-2"}}), 0)
		adminTestLog(t, s, "退回稿件", "T8 Test 视频")
		// A non-array draft video payload is an internal JSON/map error, not 21003.
		bpError(t, submit(url.Values{"csrf": {u.CSRF}, "draft": {"1"}, "videos": {"false"}}), -500, "服务器内部错误")
		bpError(t, post(t, s, "/x/vu/web/edit", url.Values{"csrf": {u.CSRF}, "aid": {fmt.Sprint(immediate["aid"]) + ".5"}, "draft": {"1"}}, u), -404, "稿件不存在")
		before := adminTestCount(t, s, "SELECT COUNT(*) FROM archives")
		bpExec(t, s, "ALTER TABLE logs RENAME TO t20_unavailable_logs")
		p := creatorParams(u)
		p.Set("dtime", fmt.Sprint(now+3*3600))
		assertCode(t, submit(p), -500)
		if adminTestCount(t, s, "SELECT COUNT(*) FROM archives") != before {
			t.Fatal("failed scheduled save partially committed")
		}
		// Drafts have no log dependency.
		assertCode(t, submit(url.Values{"csrf": {u.CSRF}, "draft": {"1"}}), 0)
		bpExec(t, s, "ALTER TABLE t20_unavailable_logs RENAME TO logs")
	})
}
