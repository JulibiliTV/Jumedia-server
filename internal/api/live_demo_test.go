package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func TestDemoLiveStartupAndContracts(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprint("upgrade=", upgrade), func(t *testing.T) {
			testServers(t, func(t *testing.T, s *Server) {
				ctx := t.Context()
				if upgrade {
					liveExec(t, s, "UPDATE rooms SET source=?,live_status=1,pending_start=0", `{"type":"m3u8","url":"/media/emby/old/hls/index.m3u8","loop":true}`)
					liveExec(t, s, "DELETE FROM schema_versions WHERE version=?", 10)
					if err := s.Store.Migrate(ctx); err != nil {
						t.Fatal(err)
					}
				}
				// Same ordering as cmd/jumedia startup, including the older media migration.
				if err := seed.MigrateMedia(ctx, s.Store, testdata.Catalog()); err != nil {
					t.Fatal(err)
				}
				if err := s.recoverLiveCaches(ctx); err != nil {
					t.Fatal(err)
				}
				caches, err := liveCacheList(ctx, s.Store.DB)
				if err != nil || len(caches) != 2 {
					t.Fatal("expected exactly two queued caches", caches, err)
				}
				for _, id := range []int64{21000, 21018} {
					r := liveRoom(t, s, id)
					if r.LiveStatus != 0 || !r.PendingStart || r.Source.Type != "video" || r.Source.Cache == "" || r.Source.URL != liveCachePath(r.Source.Cache) {
						t.Fatalf("pending room=%+v source=%+v", r, r.Source)
					}
					a, e := s.Store.ArchiveByID(ctx, r.Source.AID)
					if e != nil || a.MID != r.UID || r.Source.CID != a.Pages[0].CID {
						t.Fatal(a, e)
					}
					x, e := s.roomCache(ctx, s.Store.DB, r)
					if e != nil || x.Status != "pending" || x.Media != a.Pages[0].Media || x.AID != a.AID || x.CID != a.Pages[0].CID {
						t.Fatal(x, e)
					}
					data := liveData(t, get(t, s, fmt.Sprintf("/xlive/web-room/v2/index/getRoomPlayInfo?room_id=%d", id)))
					liveWant(t, data, float64(0), "live_status")
					liveWant(t, data, nil, "playurl_info")
					if w := sourceHTTP(s, fmt.Sprintf("/live-relay/%d/index.m3u8", id)); w.Code != 404 {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				liveWant(t, get(t, s, "/room/v1/area/getRoomList"), []any{}, "data")
				before, e := s.Store.ListLiveRoom(ctx, "ORDER BY room_id")
				if e != nil {
					t.Fatal(e)
				}
				for i := 0; i < 2; i++ {
					if e = s.recoverLiveCaches(ctx); e != nil {
						t.Fatal(e)
					}
				}
				after, e := s.Store.ListLiveRoom(ctx, "ORDER BY room_id")
				if e != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("recovery not idempotent", e)
				}
				again, e := liveCacheList(ctx, s.Store.DB)
				if e != nil || !reflect.DeepEqual(caches, again) {
					t.Fatal("recovery duplicated or changed jobs", again, e)
				}
				for _, x := range caches {
					cacheReady(t, s, &x)
					if e = s.finishLiveCache(ctx, &x, 20); e != nil {
						t.Fatal(e)
					}
				}
				for _, id := range []int64{21000, 21018} {
					r := liveRoom(t, s, id)
					if r.LiveStatus != 1 || r.PendingStart {
						t.Fatal(r)
					}
					data := liveData(t, get(t, s, fmt.Sprintf("/xlive/web-room/v2/index/getRoomPlayInfo?room_id=%d", id)))
					liveWant(t, data, float64(id), "room_id")
					liveWant(t, data, "http_hls", "playurl_info", "playurl", "stream", 0, "protocol_name")
					liveWant(t, data, "ts", "playurl_info", "playurl", "stream", 0, "format", 0, "format_name")
					liveWant(t, data, fmt.Sprintf("/live-relay/%d/index.m3u8", id), "playurl_info", "playurl", "stream", 0, "format", 0, "codec", 0, "base_url")
					w := sourceHTTP(s, fmt.Sprintf("/live-relay/%d/index.m3u8", id))
					if w.Code != 200 || !strings.Contains(w.Body.String(), "#EXTM3U") || !strings.Contains(w.Body.String(), "/live-cache/"+r.Source.Cache+"/seg00000.ts") {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				list := liveData(t, get(t, s, "/room/v1/area/getRoomList?page_size=100")).([]any)
				if len(list) != 2 {
					t.Fatal(list)
				}
				for _, v := range list {
					id := liveField(t, v, "roomid")
					if id != float64(21000) && id != float64(21018) {
						t.Fatal(v)
					}
					liveField(t, v, "title")
					liveField(t, v, "cover")
					liveField(t, v, "uid")
				}
				liveWant(t, get(t, s, "/room/v1/area/getRoomList?area_id=99999"), []any{}, "data")
				liveFailure(t, get(t, s, "/xlive/web-room/v2/index/getRoomPlayInfo?room_id=99999"), 19002003, "房间不存在或已被封禁")
				if w := sourceHTTP(s, "/live-relay/99999/index.m3u8"); w.Code != 404 {
					t.Fatal(w.Code)
				}
			})
		})
	}
}

func TestDemoLiveReadyCacheReuse(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		ctx := t.Context()
		if err := s.recoverLiveCaches(ctx); err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{21000, 21018} {
			r := liveRoom(t, s, id)
			x, e := s.roomCache(ctx, s.Store.DB, r)
			if e != nil {
				t.Fatal(e)
			}
			cacheReady(t, s, x)
			r.Source.Cache = ""
			r.Source.URL = ""
			raw, e := json.Marshal(r.Source)
			if e != nil {
				t.Fatal(e)
			}
			liveExec(t, s, "UPDATE rooms SET source=?,live_status=1,pending_start=0,banned=? WHERE room_id=?", string(raw), store.BoolInt(id == 21018), id)
		}
		if err := s.recoverLiveCaches(ctx); err != nil {
			t.Fatal(err)
		}
		r := liveRoom(t, s, 21000)
		if r.LiveStatus != 1 || r.PendingStart {
			t.Fatal("ready cache must start immediately", r)
		}
		r = liveRoom(t, s, 21018)
		if r.LiveStatus != 0 || r.PendingStart {
			t.Fatal("banned room must stay offline", r)
		}
		xs, e := liveCacheList(ctx, s.Store.DB)
		if e != nil || len(xs) != 2 {
			t.Fatal(xs, e)
		}
	})
}

func TestDemoLiveAdminResetQueuesCaches(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		u := login(t, s, "jumedia", "admin123")
		assertCode(t, post(t, s, "/x/admin/settings/save", url.Values{"reset": {"true"}}), -101)
		assertCode(t, sourcePost(t, s, u, "/x/admin/settings/save", url.Values{"reset": {"true"}}), 0)
		caches, e := liveCacheList(t.Context(), s.Store.DB)
		if e != nil || len(caches) != 2 {
			t.Fatal("reset must queue demo caches", caches, e)
		}
		for _, id := range []int64{21000, 21018} {
			r := liveRoom(t, s, id)
			if r.LiveStatus != 0 || !r.PendingStart || r.Source.Cache == "" {
				t.Fatal(r)
			}
		}
	})
}
