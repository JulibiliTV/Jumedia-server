package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func creatorData(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	assertCode(t, v, 0)
	d, ok := v["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected object: %v", v)
	}
	return d
}

func creatorParams(session *store.Session) url.Values {
	return url.Values{"csrf": {session.CSRF}, "title": {"  T8 Test 视频  "}, "tid": {"1005"}, "videos": {`[{"filename":"/upload/test.mp4","title":"","duration":2.5}]`}, "chapters": {`[{"from":0,"to":3,"content":"开场"}]`}}
}

func creatorOther(t *testing.T, srv *Server) *store.Session {
	t.Helper()
	u, err := srv.Store.UserByID(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	return login(t, srv, u.Username, "demo123")
}

func TestCreatorRouteAuthentication(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		reads := []string{"/x/web/archives", "/x/web/archive/view", "/x/web/data/stat", "/x/web/seasons"}
		writes := []string{"/x/upload/web/video", "/x/upload/web/image", "/x/vu/web/add/v3", "/x/vu/web/edit", "/x/web/archive/delete", "/x/web/season/save", "/x/web/season/delete"}
		for _, path := range append(reads, writes...) {
			assertCode(t, get(t, srv, path), -101)
		}
		session := login(t, srv, "jumedia", "admin123")
		for _, path := range writes {
			v := post(t, srv, path, nil, session)
			assertCode(t, v, -111)
			if v["message"] != "csrf 校验失败" {
				t.Fatal(v)
			}
		}
	})
}

func creatorUpload(t *testing.T, srv *Server, session *store.Session, path, name string, data []byte) map[string]any {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("csrf", session.CSRF); err != nil {
		t.Fatal(err)
	}
	f, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
	res := httptest.NewRecorder()
	srv.ServeHTTP(res, r)
	var v map[string]any
	if err = json.Unmarshal(res.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if res.Code != http.StatusOK {
		t.Fatal(res.Code)
	}
	return v
}

func TestCreatorUploads(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := login(t, srv, "jumedia", "admin123")
		if err := srv.Store.SetSetting(context.Background(), "maxUploadMB", 1); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ path, field, name, missing string }{
			{"/x/upload/web/video", "filename", "video.mp4", "请选择视频文件"},
			{"/x/upload/web/image", "url", "cover.png", "请选择图片"},
		} {
			t.Run(tc.field, func(t *testing.T) {
				v := post(t, srv, tc.path, url.Values{"csrf": {session.CSRF}}, session)
				assertCode(t, v, -400)
				if v["message"] != tc.missing {
					t.Fatal(v)
				}
				payload := bytes.Repeat([]byte{42}, 1<<20)
				if tc.field == "url" {
					copy(payload, []byte("\x89PNG\r\n\x1a\n"))
				}
				d := creatorData(t, creatorUpload(t, srv, session, tc.path, tc.name, payload))
				ref := d[tc.field].(string)
				if !strings.HasPrefix(ref, "http://example.com/upload/") || filepath.Base(ref) == tc.name {
					t.Fatal(d)
				}
				base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(ref), "media-"), filepath.Ext(ref))
				random, err := hex.DecodeString(base)
				if err != nil || len(random) < 16 || filepath.Ext(ref) != filepath.Ext(tc.name) {
					t.Fatalf("insecure name or changed extension: %s", ref)
				}
				got, err := os.ReadFile(filepath.Join(srv.Config.UploadDir(), filepath.Base(ref)))
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("saved bytes: %v", err)
				}
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, ref, nil))
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), payload) {
					t.Fatal("upload URL cannot be read")
				}
				if tc.field == "filename" && (d["name"] != tc.name || d["size"] != float64(len(payload))) {
					t.Fatal(d)
				}
				d2 := creatorData(t, creatorUpload(t, srv, session, tc.path, tc.name, payload[:32]))
				if d2[tc.field] == ref {
					t.Fatal("filename collision")
				}
				v = creatorUpload(t, srv, session, tc.path, tc.name, append(payload, 0))
				assertCode(t, v, -400)
				if v["message"] != "文件超过 1MB 上限" {
					t.Fatal(v)
				}
			})
		}
		files, err := os.ReadDir(srv.Config.UploadDir())
		if err != nil || len(files) != 4 {
			t.Fatalf("unexpected files after rejected upload: %d %v", len(files), err)
		}
	})
}

func TestCreatorSubmitAndEdit(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := creatorOther(t, srv)
		admin := login(t, srv, "jumedia", "admin123")
		for _, path := range []string{"/x/vu/web/add/v3", "/x/vu/web/edit"} {
			for _, tc := range []struct {
				key, value string
				code       int
				message    string
			}{
				{"title", " ", 21001, "标题需为 1–80 个字符"},
				{"title", strings.Repeat("😀", 41), 21001, "标题需为 1–80 个字符"},
				{"tid", "-1", 21002, "请选择分区"},
				{"videos", "[]", 21003, "请至少上传一个视频"},
				{"videos", "{}", 21003, "请至少上传一个视频"},
				{"videos", "null", -500, "服务器内部错误"},
				{"videos", "[null]", -500, "服务器内部错误"},
				{"videos", "{", -500, "服务器内部错误"},
				{"chapters", "{", -500, "服务器内部错误"},
				{"aid", "999999999", -404, "稿件不存在"},
			} {
				p := creatorParams(session)
				p.Set(tc.key, tc.value)
				v := post(t, srv, path, p, session)
				assertCode(t, v, tc.code)
				if v["message"] != tc.message {
					t.Fatal(v)
				}
			}
		}
		p := creatorParams(session)
		d := creatorData(t, post(t, srv, "/x/vu/web/add/v3", p, session))
		aid := int64(d["aid"].(float64))
		if d["state"] != float64(-1) || d["bvid"] != store.AV2BV(aid) {
			t.Fatal(d)
		}
		view := creatorData(t, get(t, srv, fmt.Sprintf("/x/web/archive/view?aid=%d", aid), session))
		a := view["Archive"].(map[string]any)
		video := view["Videos"].([]any)[0].(map[string]any)
		if a["title"] != "T8 Test 视频" || a["state_desc"] != "审核中" || a["duration"] != float64(3) || a["copyright"] != float64(1) || a["tname"] == "" || a["cover"] != "" || video["title"] != a["title"] || video["filename"] != "http://example.com/upload/test.mp4" || len(view["chapters"].([]any)) != 1 {
			t.Fatal(view)
		}
		p.Set("aid", fmt.Sprint(aid))
		p.Set("title", "编辑后")
		p.Del("chapters")
		p.Set("videos", `[{"filename":"/upload/test.mp4","title":"保留","duration":0},{"filename":"/upload/new.mp4","title":"新增","duration":-3}]`)
		creatorData(t, post(t, srv, "/x/vu/web/edit", p, session))
		view = creatorData(t, get(t, srv, fmt.Sprintf("/x/web/archive/view?aid=%d", aid), session))
		vs := view["Videos"].([]any)
		after := view["Archive"].(map[string]any)
		if vs[0].(map[string]any)["cid"] != video["cid"] || vs[1].(map[string]any)["cid"] == video["cid"] || after["duration"] != float64(2) || after["ctime"] != a["ctime"] || after["ptime"] != a["ptime"] || len(view["chapters"].([]any)) != 0 {
			t.Fatal(view)
		}
		p.Set("csrf", admin.CSRF)
		assertCode(t, post(t, srv, "/x/vu/web/edit", p, admin), -404)
		assertCode(t, get(t, srv, fmt.Sprintf("/x/web/archive/view?aid=%d", aid), admin), -404)
		ad := creatorData(t, post(t, srv, "/x/vu/web/add/v3", creatorParams(admin), admin))
		if ad["state"] != float64(0) {
			t.Fatal(ad)
		}
		if err := srv.Store.SetSetting(context.Background(), "auditRequired", false); err != nil {
			t.Fatal(err)
		}
		p.Set("csrf", session.CSRF)
		if d = creatorData(t, post(t, srv, "/x/vu/web/edit", p, session)); d["state"] != float64(0) {
			t.Fatal(d)
		}
	})
}

func creatorExec(t *testing.T, srv *Server, query string, args ...any) {
	t.Helper()
	if _, err := srv.Store.DB.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func creatorAdd(t *testing.T, srv *Server, session *store.Session, title string) int64 {
	t.Helper()
	p := creatorParams(session)
	p.Set("title", title)
	d := creatorData(t, post(t, srv, "/x/vu/web/add/v3", p, session))
	return int64(d["aid"].(float64))
}

func TestCreatorManagement(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := creatorOther(t, srv)
		other := login(t, srv, "jumedia", "admin123")
		creatorExec(t, srv, "UPDATE archives SET state=? WHERE mid=?", -100, session.MID)
		ids := []int64{}
		for i, state := range []int{0, -1, -2, 0, 7} {
			aid := creatorAdd(t, srv, session, fmt.Sprintf("Case_%d ' OR 1=1 --", i))
			ids = append(ids, aid)
			creatorExec(t, srv, "UPDATE archives SET state=?,ctime=?,reason=? WHERE aid=?", state, 100+i, "理由", aid)
		}
		for _, tc := range []struct {
			query       string
			count, rows int
		}{
			{"", 5, 5}, {"status=pubed", 2, 2}, {"status=is_pubing", 1, 1}, {"status=not_pubed", 1, 1},
			{"status=unknown", 5, 5}, {"keyword=case_1", 1, 1}, {"keyword=%25", 0, 0},
			{"pn=2&ps=2", 5, 2}, {"pn=100&ps=2", 5, 0}, {"pn=0&ps=2", 5, 2},
			{"ps=0", 5, 0}, {"ps=-2", 5, 3}, {"pn=1.5&ps=2", 5, 2}, {"pn=no&ps=bad", 5, 5},
		} {
			d := creatorData(t, get(t, srv, "/x/web/archives?"+tc.query, session))
			page := d["page"].(map[string]any)
			class := d["class"].(map[string]any)
			rows := d["arc_audits"].([]any)
			if page["count"] != float64(tc.count) || len(rows) != tc.rows || class["pubed"] != float64(2) || class["is_pubing"] != float64(1) || class["not_pubed"] != float64(1) {
				t.Fatalf("%s: %v", tc.query, d)
			}
			if tc.query == "" && rows[0].(map[string]any)["Archive"].(map[string]any)["aid"] != float64(ids[4]) {
				t.Fatal("ctime order", rows)
			}
		}
		p := url.Values{"csrf": {session.CSRF}, "aid": {fmt.Sprint(ids[0])}}
		wrong := url.Values{"csrf": {other.CSRF}, "aid": {fmt.Sprint(ids[0])}}
		assertCode(t, post(t, srv, "/x/web/archive/delete", wrong, other), -404)
		for _, id := range []string{"999999999", fmt.Sprint(ids[0]) + ".5", "1e100"} {
			assertCode(t, get(t, srv, "/x/web/archive/view?aid="+id, session), -404)
		}
		for i := 0; i < 2; i++ {
			v := post(t, srv, "/x/web/archive/delete", p, session)
			assertCode(t, v, 0)
			if v["data"] != nil {
				t.Fatal(v)
			}
		}
		assertCode(t, get(t, srv, "/x/web/archive/view?aid="+fmt.Sprint(ids[0]), session), -404)
		d := creatorData(t, post(t, srv, "/x/web/archives", nil, session))
		if d["page"].(map[string]any)["count"] != float64(4) || d["class"].(map[string]any)["pubed"] != float64(1) {
			t.Fatal(d)
		}
		var n int
		if err := srv.Store.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM logs WHERE mid=? AND action=? AND target=?", session.MID, "删除稿件", "Case_0 ' OR 1=1 --").Scan(&n); err != nil || n != 2 {
			t.Fatalf("delete log: %d %v", n, err)
		}
	})
}

func TestCreatorStatistics(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := creatorOther(t, srv)
		creatorExec(t, srv, "UPDATE archives SET state=? WHERE mid=?", -100, session.MID)
		empty := creatorData(t, get(t, srv, "/x/web/data/stat", session))
		if empty["total_click"] != float64(0) || len(empty["top"].([]any)) != 0 || len(empty["series"].([]any)) != 14 {
			t.Fatal(empty)
		}
		var last int64
		for i := 1; i <= 8; i++ {
			aid := creatorAdd(t, srv, session, fmt.Sprintf("统计 %d", i))
			last = aid
			state := 0
			if i == 8 {
				state = -1
			}
			creatorExec(t, srv, "UPDATE archives SET state=?,stat_view=?,stat_like=?,stat_coin=?,stat_favorite=?,stat_reply=?,stat_danmaku=?,stat_share=? WHERE aid=?", state, i*100, i, i*2, i*3, i*4, i*5, i*6, aid)
		}
		d := creatorData(t, get(t, srv, "/x/web/data/stat", session))
		for key, want := range map[string]float64{"total_click": 2800, "total_like": 28, "total_coin": 56, "total_fav": 84, "total_reply": 112, "total_dm": 140, "total_share": 168, "archive_count": 7, "incr_fans": 6, "incr_click": 33} {
			if d[key] != want {
				t.Fatalf("%s: %v != %v", key, d[key], want)
			}
		}
		user, err := srv.Store.UserByID(context.Background(), session.MID)
		if err != nil {
			t.Fatal(err)
		}
		fans, err := srv.Store.FollowerCount(context.Background(), session.MID)
		if err != nil {
			t.Fatal(err)
		}
		if d["coins"] != user.Coins || d["total_fans"] != float64(fans) {
			t.Fatal(d)
		}
		top := d["top"].([]any)
		if len(top) != 5 {
			t.Fatal(top)
		}
		for i, raw := range top {
			a := raw.(map[string]any)
			if a["aid"] != float64(last-1-int64(i)) || a["stat"].(map[string]any)["view"] != float64((7-i)*100) || a["owner"].(map[string]any)["mid"] != float64(session.MID) || len(a["pages"].([]any)) != 1 || a["bvid"] == "" {
				t.Fatal(a)
			}
		}
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		for i, raw := range d["series"].([]any) {
			day := raw.(map[string]any)
			wantView := math.Floor(7 * (0.6 + math.Abs(math.Sin(float64(i)*1.7+3))*0.9))
			wantFans := math.Floor(1 + math.Abs(math.Cos(float64(i)+3))*6)
			if day["date"] != today.Add(-time.Duration(13-i)*24*time.Hour).Format("1-2") || day["view"] != wantView || day["fans"] != wantFans {
				t.Fatal(day)
			}
		}
	})
}

func creatorFindSeason(t *testing.T, srv *Server, session *store.Session, title string) map[string]any {
	t.Helper()
	d := creatorData(t, get(t, srv, "/x/web/seasons", session))
	for _, raw := range d["seasons"].([]any) {
		row := raw.(map[string]any)
		if row["title"] == title {
			return row
		}
	}
	t.Fatalf("missing collection %q", title)
	return nil
}

func TestCreatorCollections(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := creatorOther(t, srv)
		other := login(t, srv, "jumedia", "admin123")
		creatorExec(t, srv, "DELETE FROM collections WHERE mid=?", session.MID)
		if d := creatorData(t, get(t, srv, "/x/web/seasons", session)); len(d["seasons"].([]any)) != 0 {
			t.Fatal(d)
		}
		first := creatorAdd(t, srv, session, "first")
		second := creatorAdd(t, srv, session, "second")
		foreign := creatorAdd(t, srv, other, "foreign")
		p := url.Values{"csrf": {session.CSRF}, "title": {" "}}
		v := post(t, srv, "/x/web/season/save", p, session)
		assertCode(t, v, -400)
		if v["message"] != "请填写名称" {
			t.Fatal(v)
		}
		p.Set("title", " 合集 A ")
		p.Set("kind", "season")
		p.Set("intro", "介绍")
		p.Set("aids", fmt.Sprintf("%d,%d,%d,999999999,%d,1.5", second, foreign, first, second))
		v = post(t, srv, "/x/web/season/save", p, session)
		assertCode(t, v, 0)
		if v["data"] != nil {
			t.Fatal(v)
		}
		a := creatorFindSeason(t, srv, session, "合集 A")
		if !reflect.DeepEqual(a["aids"], []any{float64(second), float64(first)}) || a["ep_count"] != float64(2) || a["kind"] != "season" || a["intro"] != "介绍" || a["ctime"].(float64) <= 0 {
			t.Fatal(a)
		}
		arc := a["archives"].([]any)[0].(map[string]any)
		if arc["aid"] != float64(second) || arc["title"] != "second" || arc["state"] != float64(-1) || a["cover"] != arc["pic"] {
			t.Fatal(a)
		}
		p.Set("title", "系列")
		p.Set("kind", "series")
		p.Set("aids", fmt.Sprint(first))
		assertCode(t, post(t, srv, "/x/web/season/save", p, session), 0)
		if got := creatorFindSeason(t, srv, session, "合集 A"); got["ep_count"] != float64(2) {
			t.Fatal(got)
		}
		p.Set("title", "合集 B")
		p.Set("kind", "season")
		p.Set("cover", "/upload/cover.jpg")
		// Registered paths accept GET as well as POST, just like the mock.
		assertCode(t, get(t, srv, "/x/web/season/save?"+p.Encode(), session), 0)
		b := creatorFindSeason(t, srv, session, "合集 B")
		if got := creatorFindSeason(t, srv, session, "合集 A"); !reflect.DeepEqual(got["aids"], []any{float64(second)}) {
			t.Fatal(got)
		}
		if got := creatorFindSeason(t, srv, session, "系列"); got["ep_count"] != float64(1) {
			t.Fatal(got)
		}
		view := creatorData(t, get(t, srv, "/x/web/archive/view?aid="+fmt.Sprint(first), session))
		if view["season_id"] != b["id"] {
			t.Fatal(view)
		}
		p.Set("season_id", fmt.Sprint(b["id"]))
		p.Set("title", "B 编辑")
		p.Set("kind", "series")
		p.Set("cover", "")
		p.Set("aids", "")
		assertCode(t, post(t, srv, "/x/web/season/save", p, session), 0)
		b2 := creatorFindSeason(t, srv, session, "B 编辑")
		if b2["kind"] != "season" || b2["cover"] != b["cover"] || b2["ctime"] != b["ctime"] || b2["ep_count"] != float64(0) || len(b2["archives"].([]any)) != 0 {
			t.Fatal(b2)
		}
		edit := creatorParams(session)
		edit.Set("aid", fmt.Sprint(first))
		edit.Set("season_id", fmt.Sprint(a["id"]))
		creatorData(t, post(t, srv, "/x/vu/web/edit", edit, session))
		if got := creatorFindSeason(t, srv, session, "合集 A"); !reflect.DeepEqual(got["aids"], []any{float64(second), float64(first)}) {
			t.Fatal(got)
		}
		edit.Del("season_id")
		creatorData(t, post(t, srv, "/x/vu/web/edit", edit, session))
		if got := creatorFindSeason(t, srv, session, "合集 A"); !reflect.DeepEqual(got["aids"], []any{float64(second)}) {
			t.Fatal(got)
		}
		assertCode(t, post(t, srv, "/x/web/archive/delete", url.Values{"csrf": {session.CSRF}, "aid": {fmt.Sprint(second)}}, session), 0)
		if got := creatorFindSeason(t, srv, session, "合集 A"); got["archives"].([]any)[0].(map[string]any)["state"] != float64(-100) {
			t.Fatal(got)
		}
		for _, actor := range []*store.Session{other, session, session} {
			assertCode(t, post(t, srv, "/x/web/season/delete", url.Values{"csrf": {actor.CSRF}, "season_id": {fmt.Sprint(a["id"])}}, actor), 0)
			if actor == other {
				creatorFindSeason(t, srv, session, "合集 A")
			}
		}
		assertCode(t, get(t, srv, "/x/web/archive/view?aid="+fmt.Sprint(second), session), -404)
		var count int
		if err := srv.Store.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM collection_items WHERE collection_id=?", a["id"]).Scan(&count); err != nil || count != 0 {
			t.Fatalf("orphan collection items: %d %v", count, err)
		}
		p.Set("season_id", "999999999")
		p.Set("title", "不存在则新建")
		p.Del("kind")
		assertCode(t, post(t, srv, "/x/web/season/save", p, session), 0)
		if got := creatorFindSeason(t, srv, session, "不存在则新建"); got["kind"] != "season" || got["cover"] != "" {
			t.Fatal(got)
		}
	})
}

func TestCreatorTransactionRollback(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := creatorOther(t, srv)
		aid := creatorAdd(t, srv, session, "原始稿件")
		p := url.Values{"csrf": {session.CSRF}, "title": {"回滚合集"}, "kind": {"season"}, "aids": {fmt.Sprint(aid)}}
		assertCode(t, post(t, srv, "/x/web/season/save", p, session), 0)
		path := "/x/web/archive/view?aid=" + fmt.Sprint(aid)
		before := creatorData(t, get(t, srv, path, session))
		// An error at the final log write must undo earlier archive, page and
		// collection writes. DROP TABLE is portable and confined to this test DB.
		creatorExec(t, srv, "DROP TABLE logs")
		edit := creatorParams(session)
		edit.Set("aid", fmt.Sprint(aid))
		edit.Set("title", "不应保存")
		edit.Set("videos", `[{"filename":"/upload/replacement.mp4","duration":18}]`)
		assertCode(t, post(t, srv, "/x/vu/web/edit", edit, session), -500)
		if after := creatorData(t, get(t, srv, path, session)); !reflect.DeepEqual(before, after) {
			t.Fatalf("edit failed to roll back: %v", after)
		}
		assertCode(t, post(t, srv, "/x/web/archive/delete", url.Values{"csrf": {session.CSRF}, "aid": {fmt.Sprint(aid)}}, session), -500)
		if after := creatorData(t, get(t, srv, path, session)); !reflect.DeepEqual(before, after) {
			t.Fatalf("delete failed to roll back: %v", after)
		}
		assertCode(t, post(t, srv, "/x/vu/web/add/v3", creatorParams(session), session), -500)
		assertCode(t, get(t, srv, "/x/web/archive/view?aid="+fmt.Sprint(aid+1), session), -404)
	})
}

func TestCreatorRepeatedMediaAndPreservedFields(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := creatorOther(t, srv)
		p := creatorParams(session)
		p.Set("title", strings.Repeat("😀", 40))
		p.Set("videos", `[{"filename":"/upload/same.mp4","title":"第一段","duration":1},{"filename":"/upload/same.mp4","title":"第二段","duration":2}]`)
		// A hidden region still exists: the mock does not filter on visibility.
		creatorExec(t, srv, "UPDATE regions SET visible=? WHERE tid=?", 0, 1005)
		d := creatorData(t, post(t, srv, "/x/vu/web/add/v3", p, session))
		aid := int64(d["aid"].(float64))
		path := "/x/web/archive/view?aid=" + fmt.Sprint(aid)
		before := creatorData(t, get(t, srv, path, session))
		vs := before["Videos"].([]any)
		if vs[0].(map[string]any)["cid"] == vs[1].(map[string]any)["cid"] {
			t.Fatal(vs)
		}
		creatorExec(t, srv, "UPDATE archives SET state=?,reason=?,stat_view=?,subtitles=? WHERE aid=?", -2, "退回理由", 123, `[{"lan":"zh","lan_doc":"中文","body":[]}]`, aid)
		p.Set("aid", fmt.Sprint(aid))
		creatorData(t, post(t, srv, "/x/vu/web/edit", p, session))
		after := creatorData(t, get(t, srv, path, session))
		newVideos := after["Videos"].([]any)
		if newVideos[0].(map[string]any)["cid"] != vs[0].(map[string]any)["cid"] || newVideos[0].(map[string]any)["cid"] == newVideos[1].(map[string]any)["cid"] {
			t.Fatal(newVideos)
		}
		a := after["Archive"].(map[string]any)
		if a["state"] != float64(-1) || a["reject_reason"] != "" || after["stat"].(map[string]any)["view"] != float64(123) {
			t.Fatal(after)
		}
		stored, err := srv.Store.ArchiveByID(context.Background(), aid)
		if err != nil || len(stored.Subtitles) != 1 || stored.Subtitles[0].Lan != "zh" {
			t.Fatalf("subtitles changed: %+v %v", stored, err)
		}
	})
}

func TestCreatorConcurrentSubmissions(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		session := creatorOther(t, srv)
		var start int64
		if err := srv.Store.DB.QueryRowContext(context.Background(), "SELECT MAX(aid) FROM archives").Scan(&start); err != nil {
			t.Fatal(err)
		}
		const count = 8
		results := make([]map[string]any, count)
		errors := make([]error, count)
		var group sync.WaitGroup
		for i := range count {
			group.Add(1)
			go func(i int) {
				defer group.Done()
				p := creatorParams(session)
				p.Set("title", fmt.Sprintf("并发投稿 %d", i))
				r := httptest.NewRequest(http.MethodPost, "/x/vu/web/add/v3", strings.NewReader(p.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				errors[i] = json.Unmarshal(w.Body.Bytes(), &results[i])
			}(i)
		}
		group.Wait()
		aids := map[float64]bool{}
		for i, v := range results {
			if errors[i] != nil {
				t.Fatal(errors[i])
			}
			d := creatorData(t, v)
			aid := d["aid"].(float64)
			if aids[aid] || aid <= float64(start) || aid > float64(start+count) {
				t.Fatal("invalid concurrent ID", aid)
			}
			aids[aid] = true
			stored, err := srv.Store.ArchiveByID(context.Background(), int64(aid))
			if err != nil || len(stored.Pages) != 1 {
				t.Fatalf("incomplete concurrent archive: %v", err)
			}
		}
	})
}
