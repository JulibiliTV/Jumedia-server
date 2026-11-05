package api

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"jumedia-server/internal/emby"
)

func TestDataSourceManualDrafts(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		ctx := context.Background()
		f := newSourceFixture(t)
		user := login(t, s, "lumen", "demo123")
		admin := login(t, s, "jumedia", "admin123")
		for k, v := range map[string]any{"dataSourcePrivateNetwork": true, "userDataSources": true, "auditRequired": true, "dataSourceSyncRate": 50} {
			if err := s.Store.SetSetting(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
		d := sourceData(t, sourcePost(t, s, user, "/x/web/datasource/save", url.Values{"url": {f.server.URL}, "username": {"fixture"}, "auto_sync": {"false"}, "sync_now": {"false"}}))
		id := int64(d["id"].(float64))
		if d["status"] != "idle" || len(sourceArchives(t, s, id)) != 0 {
			t.Fatal("saving imported the library", d)
		}
		browse := fmt.Sprintf("/x/web/datasource/items?id=%d&library=movies&ps=1&pn=2", id)
		list := sourceData(t, get(t, s, browse, user))
		if list["page"].(map[string]any)["total"] != float64(2) || list["list"].([]any)[0].(map[string]any)["id"] != "mkv" {
			t.Fatal(list)
		}
		list = sourceData(t, get(t, s, fmt.Sprintf("/x/web/datasource/items?id=%d&library=movies&keyword=B", id), user))
		if len(list["list"].([]any)) != 1 {
			t.Fatal("search", list)
		}
		assertCode(t, get(t, s, browse, admin), -404)
		assertCode(t, get(t, s, browse), -101)
		assertCode(t, post(t, s, "/x/web/datasource/import", url.Values{"id": {fmt.Sprint(id)}}, user), -111)
		importItems := func(lib, ids string) map[string]any {
			return sourcePost(t, s, user, "/x/web/datasource/import", url.Values{"id": {fmt.Sprint(id)}, "library": {lib}, "item_ids": {ids}})
		}
		assertCode(t, importItems("unknown", `["movie"]`), -400)
		assertCode(t, importItems("movies", `[]`), -400)
		assertCode(t, importItems("movies", `["movie","missing"]`), -400)
		if len(sourceArchives(t, s, id)) != 0 {
			t.Fatal("partial import")
		}
		f.mu.Lock()
		f.items["movies"] = append(f.items["movies"], emby.Item{ID: "empty", Name: "无视频", Type: "Movie"})
		f.mu.Unlock()
		assertCode(t, importItems("movies", `["movie","empty"]`), -400)
		if len(sourceArchives(t, s, id)) != 0 {
			t.Fatal("unplayable item left partial import")
		}
		created := sourceData(t, importItems("movies", `["movie","mkv","movie"]`))["archives"].([]any)
		if len(created) != 2 {
			t.Fatal(created)
		}
		for _, v := range created {
			if row := v.(map[string]any); row["state"] != float64(-20) || row["created"] != true {
				t.Fatal(row)
			}
		}
		a := sourceArchives(t, s, id)["movie"]
		if a.Pubdate != 0 || a.MID != user.MID || len(a.Pages) != 1 {
			t.Fatal(a)
		}
		cid := a.Pages[0].CID
		edit := url.Values{"aid": {fmt.Sprint(a.AID)}, "title": {"手动编辑"}, "tid": {fmt.Sprint(a.TID)}, "desc": {"草稿简介"}, "draft": {"1"}}
		assertCode(t, sourcePost(t, s, user, "/x/vu/web/edit", edit), 0)
		a = sourceArchives(t, s, id)["movie"]
		if a.State != -20 || a.Title != "手动编辑" {
			t.Fatal(a)
		}
		reused := sourceData(t, importItems("movies", `["movie"]`))["archives"].([]any)[0].(map[string]any)
		if reused["created"] != false || reused["aid"] != float64(a.AID) {
			t.Fatal(reused)
		}
		sourceData(t, importItems("shows", `["multi"]`))
		if series := sourceArchives(t, s, id)["multi"]; len(series.Pages) != 2 || series.State != -20 {
			t.Fatal(series)
		}
		// A draft survives full sync, including an absent source item, without publication.
		sync := func() {
			assertCode(t, sourcePost(t, s, user, "/x/web/datasource/sync", url.Values{"id": {fmt.Sprint(id)}}), 0)
			sourceWait(t, s, id)
		}
		f.mu.Lock()
		f.items["movies"] = nil
		f.mu.Unlock()
		sync()
		a = sourceArchives(t, s, id)["movie"]
		if a.State != -20 || a.Title != "手动编辑" || a.Pages[0].CID != cid {
			t.Fatal("sync changed draft", a)
		}
		f.mu.Lock()
		f.items["movies"] = []emby.Item{sourceMovie("movie", "源片名", "mp4")}
		f.mu.Unlock()
		sync()
		a = sourceArchives(t, s, id)["movie"]
		if a.State != -20 || a.Title != "手动编辑" {
			t.Fatal("sync published draft", a)
		}
		edit.Del("draft")
		edit.Set("title", "")
		assertCode(t, sourcePost(t, s, user, "/x/vu/web/edit", edit), 21001)
		edit.Set("title", "正式投稿")
		edit.Set("tid", "0")
		assertCode(t, sourcePost(t, s, user, "/x/vu/web/edit", edit), 21002)
		edit.Set("tid", fmt.Sprint(a.TID))
		edit.Set("dtime", "1")
		assertCode(t, sourcePost(t, s, user, "/x/vu/web/edit", edit), 21004)
		edit.Set("dtime", fmt.Sprint(time.Now().Add(3*time.Hour).Unix()))
		result := sourceData(t, sourcePost(t, s, user, "/x/vu/web/edit", edit))
		if result["state"] != float64(-1) {
			t.Fatal("review bypassed", result)
		}
		reused = sourceData(t, importItems("movies", `["movie"]`))["archives"].([]any)[0].(map[string]any)
		if reused["created"] != false || reused["state"] != float64(-1) {
			t.Fatal("submitted archive reset", reused)
		}
		edit.Set("draft", "1")
		assertCode(t, sourcePost(t, s, user, "/x/vu/web/edit", edit), -400)
		assertCode(t, sourcePost(t, s, user, "/x/web/archive/delete", url.Values{"aid": {fmt.Sprint(a.AID)}}), 0)
		assertCode(t, importItems("movies", `["movie"]`), -400)
		if err := s.Store.SetSetting(ctx, "userDataSources", false); err != nil {
			t.Fatal(err)
		}
		assertCode(t, get(t, s, browse, user), -403)
		assertCode(t, importItems("shows", `["single"]`), -403)
	})
}

func TestDataSourceDraftRollbackAndPublication(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		ctx := context.Background()
		f := newSourceFixture(t)
		admin := login(t, s, "jumedia", "admin123")
		for k, v := range map[string]any{"dataSourcePrivateNetwork": true, "userDataSources": true, "auditRequired": true, "dataSourceSyncRate": 50} {
			if err := s.Store.SetSetting(ctx, k, v); err != nil {
				t.Fatal(err)
			}
		}
		d := sourceData(t, sourcePost(t, s, admin, "/x/web/datasource/save", url.Values{"url": {f.server.URL}, "username": {"fixture"}, "auto_sync": {"false"}, "sync_now": {"false"}, "libraries": {"movies"}}))
		id := int64(d["id"].(float64))
		params := url.Values{"id": {fmt.Sprint(id)}, "library": {"movies"}, "item_ids": {`["movie","mkv"]`}}
		// Fail the second insertion and verify the first archive/pages roll back too.
		f.mu.Lock()
		f.items["movies"][1].Name = f.items["movies"][0].Name
		f.mu.Unlock()
		if _, err := s.Store.DB.ExecContext(ctx, "CREATE UNIQUE INDEX source_draft_test_titles ON archives(title) WHERE origin_ds<>0"); err != nil {
			t.Fatal(err)
		}
		assertCode(t, sourcePost(t, s, admin, "/x/web/datasource/import", params), -500)
		if len(sourceArchives(t, s, id)) != 0 {
			t.Fatal("failed batch left archives")
		}
		if _, err := s.Store.DB.ExecContext(ctx, "DROP INDEX source_draft_test_titles"); err != nil {
			t.Fatal(err)
		}
		params.Set("library", "shows")
		params.Set("item_ids", `["multi"]`)
		assertCode(t, sourcePost(t, s, admin, "/x/web/datasource/import", params), -400)
		params.Set("library", "movies")
		params.Set("item_ids", `["movie"]`)
		sourceData(t, sourcePost(t, s, admin, "/x/web/datasource/import", params))
		a := sourceArchives(t, s, id)["movie"]
		assertCode(t, get(t, s, fmt.Sprintf("/x/web-interface/view?aid=%d", a.AID)), -404)
		edit := url.Values{"aid": {fmt.Sprint(a.AID)}, "title": {"发布草稿"}, "tid": {fmt.Sprint(a.TID)}}
		d = sourceData(t, sourcePost(t, s, admin, "/x/vu/web/edit", edit))
		if d["state"] != float64(0) {
			t.Fatal(d)
		}
		a = sourceArchives(t, s, id)["movie"]
		if a.Pubdate < time.Now().Add(-time.Minute).Unix() {
			t.Fatal("publication retained zero/source date", a.Pubdate)
		}
		params.Set("item_ids", `["mkv"]`)
		sourceData(t, sourcePost(t, s, admin, "/x/web/datasource/import", params))
		a = sourceArchives(t, s, id)["mkv"]
		edit.Set("aid", fmt.Sprint(a.AID))
		edit.Set("dtime", fmt.Sprint(time.Now().Add(3*time.Hour).Unix()))
		d = sourceData(t, sourcePost(t, s, admin, "/x/vu/web/edit", edit))
		if d["state"] != float64(-40) {
			t.Fatal("scheduled publication", d)
		}
	})
}
