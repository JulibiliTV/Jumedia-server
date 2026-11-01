package api

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"jumedia-server/internal/store"
)

func TestCommunityFavorites(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		user := login(t, s, "jumedia", "admin123")
		other := login(t, s, "lumen", "demo123")
		arcs := communityArchives(t, s)
		add := func(session *store.Session, title, privacy string) float64 {
			v := communityData(t, communityWrite(t, s, session, "/x/v3/fav/folder/add", url.Values{"title": {title}, "privacy": {privacy}, "intro": {"说明"}}))
			if v["title"] != Trim(title) {
				t.Fatal(v)
			}
			return v["id"].(float64)
		}
		id := add(user, "  AbC 收藏  ", "0")
		private := add(user, "私密夹", "1")
		foreign := add(other, "别人的夹", "0")
		communityFailure(t, communityWrite(t, s, user, "/x/v3/fav/folder/add", url.Values{"title": {" \ufeff"}}), -400, "请填写收藏夹名称")
		// Pin all three sort modes and the invalid-video rendering independently.
		for i, a := range arcs[:3] {
			if e := store.InsertFavItem(context.Background(), s.Store.DB, &store.FavItem{FolderID: int64(id), AID: a.AID, Mtime: int64(100 + i)}); e != nil {
				t.Fatal(e)
			}
			communityExec(t, s, "UPDATE archives SET title=?,stat_view=?,pubdate=? WHERE aid=?", fmt.Sprintf("AbC %d", i), 300-i, 200+(i+1)%3, a.AID)
		}
		communityExec(t, s, "UPDATE archives SET state=-2 WHERE aid=?", arcs[2].AID)
		if e := store.InsertFavItem(context.Background(), s.Store.DB, &store.FavItem{FolderID: int64(private), AID: arcs[0].AID, Mtime: 999}); e != nil {
			t.Fatal(e)
		}
		guest := communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/folder/created/list-all?up_mid=1&rid=%d", arcs[0].AID)))
		owner := communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/folder/created/list-all?up_mid=1&rid=%d", arcs[0].AID), user))
		if owner["count"].(float64) != guest["count"].(float64)+1 {
			t.Fatal(owner, guest)
		}
		found := false
		for _, x := range guest["list"].([]any) {
			f := x.(map[string]any)
			if f["id"] == private {
				t.Fatal("private leaked")
			}
			if f["id"] == id {
				found = true
				if f["fid"] != id || f["fav_state"] != float64(1) || f["media_count"] != float64(3) || f["intro"] != "说明" || f["privacy"] != float64(0) || f["cover"] == "" {
					t.Fatal(f)
				}
			}
		}
		if !found {
			t.Fatal("missing public folder")
		}
		noRID := communityData(t, get(t, s, "/x/v3/fav/folder/created/list-all?up_mid=1"))
		for _, x := range noRID["list"].([]any) {
			if x.(map[string]any)["fav_state"] != float64(0) {
				t.Fatal(x)
			}
		}
		for _, session := range []*store.Session{nil, other} {
			communityFailure(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f", private), session), -403, "收藏夹不存在或已设为私密")
		}
		privateData := communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f", private), user))
		if len(privateData["medias"].([]any)) != 1 {
			t.Fatal(privateData)
		}
		communityFailure(t, get(t, s, "/x/v3/fav/resource/list?media_id=999999"), -403, "收藏夹不存在或已设为私密")
		for _, tc := range []struct {
			order string
			first int
		}{{"mtime", 2}, {"view", 0}, {"pubtime", 1}, {"view; DROP TABLE users", 2}} {
			data := communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f&order=%s&ps=1", id, url.QueryEscape(tc.order))))
			media := data["medias"].([]any)[0].(map[string]any)
			a := arcs[tc.first]
			if media["id"] != float64(a.AID) || media["type"] != float64(2) || media["bvid"] != a.BVID || media["bv_id"] != a.BVID || media["ugc"].(map[string]any)["first_cid"] != float64(a.Pages[0].CID) || data["has_more"] != true || data["info"].(map[string]any)["media_count"] != float64(3) {
				t.Fatal(data)
			}
			if tc.first == 2 && (media["title"] != "已失效视频" || media["attr"] != float64(9)) {
				t.Fatal(media)
			}
			if media["upper"].(map[string]any)["mid"] != float64(a.MID) || media["cnt_info"].(map[string]any)["collect"] != float64(a.Stat.Favorite) {
				t.Fatal(media)
			}
		}
		data := communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f&keyword=abc&ps=2&pn=2", id)))
		if len(data["medias"].([]any)) != 1 || data["has_more"] != false {
			t.Fatal(data)
		}
		data = communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f&keyword=%s", id, url.QueryEscape("' OR 1=1 --"))))
		if len(data["medias"].([]any)) != 0 || data["info"].(map[string]any)["cover"] != "" || data["info"].(map[string]any)["media_count"] != float64(3) {
			t.Fatal(data)
		}
		communityFailure(t, communityWrite(t, s, other, "/x/v3/fav/folder/edit", url.Values{"media_id": {fmt.Sprint(id)}, "title": {"hacked"}}), -404, "收藏夹不存在")
		edited := communityData(t, communityWrite(t, s, user, "/x/v3/fav/folder/edit", url.Values{"media_id": {fmt.Sprint(id)}, "title": {"  不修剪  "}, "intro": {"新说明"}, "privacy": {"1"}}))
		if edited["id"] != id {
			t.Fatal(edited)
		}
		communityFailure(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f", id)), -403, "收藏夹不存在或已设为私密")
		data = communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f", id), user))
		info := data["info"].(map[string]any)
		if info["title"] != "  不修剪  " || info["intro"] != "新说明" || info["privacy"] != float64(1) {
			t.Fatal(info)
		}
		// Mock edit accepts an explicitly empty title, unlike folder/add.
		assertCode(t, communityWrite(t, s, user, "/x/v3/fav/folder/edit", url.Values{"media_id": {fmt.Sprint(id)}, "title": {""}}), 0)
		communityFailure(t, communityWrite(t, s, user, "/x/v3/fav/folder/del", url.Values{"media_ids": {fmt.Sprintf("%.0f,9010", id)}}), -400, "默认收藏夹不能删除")
		communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f", id), user))
		deleted := communityWrite(t, s, user, "/x/v3/fav/folder/del", url.Values{"media_ids": {fmt.Sprintf("%.0f,%.0f,%.0f", id, private, foreign)}})
		assertCode(t, deleted, 0)
		if deleted["data"] != float64(0) {
			t.Fatal(deleted)
		}
		communityFailure(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f", id)), -403, "收藏夹不存在或已设为私密")
		communityData(t, get(t, s, fmt.Sprintf("/x/v3/fav/resource/list?media_id=%.0f", foreign), other))
		var count int
		if e := s.Store.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM fav_items WHERE folder_id=?", int64(id)).Scan(&count); e != nil || count != 0 {
			t.Fatalf("orphan items %d %v", count, e)
		}
	})
}
