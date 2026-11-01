package api

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestFoundationRoutes(t *testing.T) {
	testServers(t, func(t *testing.T, srv *Server) {
		v := get(t, srv, "/x/web-interface/nav")
		assertCode(t, v, -101)
		if v["message"] != "账号未登录" || v["ttl"] != float64(1) {
			t.Fatal(v)
		}
		d := v["data"].(map[string]any)
		if d["isLogin"] != false || !strings.Contains(d["wbi_img"].(map[string]any)["img_url"].(string), "7cd084941338484aae1ad9425b84077c.png") {
			t.Fatal(d)
		}
		session := login(t, srv, "jumedia", "admin123")
		for _, v := range []map[string]any{get(t, srv, "/x/web-interface/nav", session), get(t, srv, "/x/web-interface/nav?access_key="+session.Access), post(t, srv, "/x/web-interface/nav", url.Values{"access_key": {session.Access}})} {
			assertCode(t, v, 0)
			d = v["data"].(map[string]any)
			if d["mid"] != float64(1) || d["role"] != "admin" || d["uname"] != "聚媒官方" || d["isLogin"] != true || d["level_info"].(map[string]any)["next_exp"] != "--" {
				t.Fatal(d)
			}
		}
		v = get(t, srv, "/x/jumedia/site")
		assertCode(t, v, 0)
		d = v["data"].(map[string]any)
		if d["siteName"] != "Jumedia" || d["allowRegister"] != true || len(d["banners"].([]any)) != 4 {
			t.Fatal(d)
		}
		b := d["banners"].([]any)[0].(map[string]any)
		if b["position"] != "home" || b["url"] == "" || b["sort"] != float64(0) {
			t.Fatal(b)
		}
		if _, ok := b["season_id"]; ok {
			t.Fatal("optional season_id must be absent")
		}
		v = post(t, srv, "/x/web-interface/region/list", nil)
		assertCode(t, v, 0)
		rows := v["data"].([]any)
		if len(rows) != 16 || rows[0].(map[string]any)["tid"] != float64(1005) || rows[0].(map[string]any)["id"] != float64(1005) {
			t.Fatal(rows)
		}
		if _, err := srv.Store.DB.ExecContext(context.Background(), "UPDATE regions SET visible=0 WHERE tid=?", 1005); err != nil {
			t.Fatal(err)
		}
		v = get(t, srv, "/x/web-interface/region/list")
		assertCode(t, v, 0)
		if len(v["data"].([]any)) != 15 {
			t.Fatal(v)
		}
		// These public routes have no business-error branch in the mock. A closed
		// database exercises their shared internal-error response without inventing one.
		if err := srv.Store.Close(); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/x/web-interface/nav?access_key=" + session.Access, "/x/jumedia/site", "/x/web-interface/region/list"} {
			v = get(t, srv, path)
			assertCode(t, v, -500)
			if v["message"] != "服务器内部错误" {
				t.Fatal(v)
			}
		}
	})
}
