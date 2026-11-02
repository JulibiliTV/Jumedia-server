package api

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"jumedia-server/internal/store"
)

func TestLiveCacheRecoveryAssociatedCache(t *testing.T) {
	for _, tc := range []struct {
		name, status, wantStatus         string
		banned, stopped, missingPlaylist bool
		online, wantOnline               int64
		wantStart, wantPending           bool
	}{
		{name: "ready", status: "ready", wantStatus: "ready", wantStart: true, wantOnline: 1},
		{name: "ready preserves online", status: "ready", wantStatus: "ready", online: 17, wantOnline: 17, wantStart: true},
		{name: "banned", status: "ready", wantStatus: "ready", banned: true, online: 17, wantOnline: 17},
		{name: "pending", status: "pending", wantStatus: "pending", wantPending: true},
		{name: "running", status: "running", wantStatus: "pending", wantPending: true},
		{name: "failed", status: "failed", wantStatus: "failed", wantPending: true},
		{name: "missing playlist", status: "ready", wantStatus: "failed", missingPlaylist: true, wantPending: true},
		{name: "missing cache", status: "missing", wantPending: true},
		{name: "stopped", status: "ready", wantStatus: "ready", stopped: true, online: 17, wantOnline: 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testServers(t, func(t *testing.T, s *Server) {
				ctx := context.Background()
				u, p, rid := cacheSetup(t, s)
				x := cacheChoose(t, s, u, p, true)
				if tc.status == "ready" {
					cacheReady(t, s, x)
					if tc.missingPlaylist {
						if err := os.Remove(filepath.Join(x.Dir, "index.m3u8")); err != nil {
							t.Fatal(err)
						}
					}
				} else if tc.status == "missing" {
					liveExec(t, s, "DELETE FROM live_caches WHERE key=?", x.Key)
				} else {
					liveExec(t, s, "UPDATE live_caches SET status=? WHERE key=?", tc.status, x.Key)
				}
				const oldLiveTime = 123
				liveExec(t, s, "UPDATE rooms SET banned=?,pending_start=?,live_status=0,live_time=?,online=? WHERE room_id=?", store.BoolInt(tc.banned), store.BoolInt(!tc.stopped), oldLiveTime, tc.online, rid)
				before := liveRoom(t, s, rid)
				start := time.Now().Unix()
				if err := s.recoverLiveCaches(ctx); err != nil {
					t.Fatal(err)
				}
				end := time.Now().Unix()
				r := liveRoom(t, s, rid)
				if r.LiveStatus != store.BoolInt(tc.wantStart) || r.PendingStart != tc.wantPending || r.Online != tc.wantOnline {
					t.Fatalf("unexpected room after recovery: %+v", r)
				}
				if tc.wantStart {
					if r.LiveTime < start || r.LiveTime > end {
						t.Fatalf("live_time=%d outside recovery interval [%d, %d]", r.LiveTime, start, end)
					}
				} else if r.LiveTime != oldLiveTime {
					t.Fatalf("recovery changed live_time without starting: %d", r.LiveTime)
				}
				if r.Banned != before.Banned || !reflect.DeepEqual(r.Source, before.Source) {
					t.Fatalf("recovery changed ban or associated source: before=%+v after=%+v", before, r)
				}
				cache, err := liveCacheByKey(ctx, s.Store.DB, x.Key)
				if err != nil {
					t.Fatal(err)
				}
				if tc.status == "missing" {
					if cache != nil {
						t.Fatalf("recovery recreated missing cache: %+v", cache)
					}
				} else if cache == nil || cache.Status != tc.wantStatus {
					t.Fatalf("cache after recovery: %+v; want status %q", cache, tc.wantStatus)
				} else if tc.missingPlaylist && cache.Message != "缓存文件丢失，请重试" {
					t.Fatalf("missing playlist message: %q", cache.Message)
				}
			})
		})
	}
}
