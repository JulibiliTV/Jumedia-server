package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestCommunityRouteContracts(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		tests := []struct {
			path   string
			params url.Values
			code   int
			key    string
		}{
			{"/x/v2/reply", url.Values{"oid": {"1001"}}, 0, "replies"},
			{"/x/v2/reply/reply", nil, 12022, ""},
			{"/x/v2/reply/add", nil, 12025, ""},
			{"/x/v2/reply/del", nil, 12022, ""},
			{"/x/v2/reply/action", nil, 12022, ""},
			{"/x/v2/reply/hate", nil, 12022, ""},
			{"/x/v2/dm/web/config", nil, 23004, ""},
			{"/x/dm/filter/user", nil, 0, "rule"},
			{"/x/v2/dm/post", nil, 36700, ""},
			{"/x/click-interface/web/heartbeat", nil, 0, ""},
			{"/x/web-interface/history/cursor", nil, 0, "cursor"},
			{"/x/v2/history/shadow", nil, 0, ""},
			{"/x/v2/history/shadow/set", nil, 0, ""},
			{"/x/v2/history/clear", nil, 0, ""},
			{"/x/v2/history/toview", nil, 0, "list"},
			{"/x/v2/history/toview/web", nil, 0, "count"},
			{"/x/v2/history/toview/add", nil, -404, ""},
			{"/x/v2/history/toview/v2/dels", nil, 0, ""},
			{"/x/v3/fav/folder/created/list-all", url.Values{"up_mid": {"1"}}, 0, "list"},
			{"/x/v3/fav/resource/list", url.Values{"media_id": {"9010"}}, 0, "medias"},
			{"/x/v3/fav/folder/add", nil, -400, ""},
			{"/x/v3/fav/folder/edit", nil, -404, ""},
			{"/x/v3/fav/folder/del", nil, 0, ""},
		}
		for _, tt := range tests {
			t.Run(tt.path, func(t *testing.T) {
				p := tt.params
				if p == nil {
					p = url.Values{}
				}
				p.Set("csrf", session.CSRF)
				v := post(t, s, tt.path, p, session)
				assertCode(t, v, tt.code)
				if tt.key != "" {
					if _, ok := v["data"].(map[string]any)[tt.key]; !ok {
						t.Fatalf("missing %s: %+v", tt.key, v)
					}
				}
				// All routes share form parsing errors, including public endpoints without
				// a Mock-level error branch. Do not invent additional domain validation.
				req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader("bad=%zz"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, req)
				if !strings.Contains(w.Body.String(), `"code":-400`) {
					t.Fatalf("malformed form: %s", w.Body.String())
				}
			})
		}
	})
}

func TestCommunityWriteRollback(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		a := communityArchives(t, s)[0]
		var before int64
		if err := s.Store.DB.QueryRowContext(context.Background(), "SELECT value FROM ids WHERE name=?", "global").Scan(&before); err != nil {
			t.Fatal(err)
		}
		// Missing references return business errors without changing IDs or counters.
		v := communityWrite(t, s, session, "/x/v2/reply/add", url.Values{"oid": {fmt.Sprint(a.AID)}, "message": {"事务回滚"}, "parent": {"99999999"}})
		communityFailure(t, v, 12022, "评论已被删除")
		var after int64
		if err := s.Store.DB.QueryRowContext(context.Background(), "SELECT value FROM ids WHERE name=?", "global").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("failed reply consumed an ID")
		}
		current, err := s.Store.ArchiveByID(context.Background(), a.AID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Stat.Reply != a.Stat.Reply {
			t.Fatal("failed reply updated stat")
		}
		assertCode(t, communityWrite(t, s, session, "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(a.AID)}, "cid": {fmt.Sprint(a.Pages[0].CID)}, "played_time": {"17"}}), 0)
		assertCode(t, communityWrite(t, s, session, "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(a.AID)}, "epid": {"99999999"}, "sid": {"99999999"}, "played_time": {"99"}}), -404)
		var progress int
		if err := s.Store.DB.QueryRowContext(context.Background(), "SELECT progress FROM history WHERE mid=? AND aid=?", session.MID, a.AID).Scan(&progress); err != nil {
			t.Fatal(err)
		}
		if progress != 17 {
			t.Fatal("failed heartbeat overwrote history")
		}
	})
}

func TestCommunityMockParameters(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  float64
	}{{"", 20}, {" \ufeff", 0}, {"0x20", 32}, {"0b10", 2}, {"0o10", 8}, {"+.5", .5}, {"1e2", 100}, {"1_0", 20}, {"0x1p2", 20}, {"Infinity", 20}, {"NaN", 20}, {"1e999", 20}} {
		if got := Number(tc.input, 20); got != tc.want {
			t.Errorf("Number(%q)=%v, want %v", tc.input, got, tc.want)
		}
	}
	rows := []int{0, 1, 2, 3, 4}
	for _, tc := range []struct {
		pn, ps float64
		want   []int
	}{{0, 2, []int{0, 1}}, {2, 2, []int{2, 3}}, {1, -1, []int{0, 1, 2, 3}}, {1.5, 2, []int{1, 2}}, {10, 2, []int{}}} {
		if got := Page(rows, tc.pn, tc.ps); !slices.Equal(got, tc.want) {
			t.Errorf("page(%v,%v)=%v, want %v", tc.pn, tc.ps, got, tc.want)
		}
	}
}
