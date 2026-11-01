package seed_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func mediaSnapshot(t *testing.T, s *store.Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"users", "archives", "archive_pages", "collections", "seasons", "episodes", "rooms", "banners"} {
		rows, err := s.DB.QueryContext(t.Context(), "SELECT * FROM "+table)
		if err != nil {
			t.Fatal(err)
		}
		var records [][]any
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			records = append(records, values)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		raw, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		out[table] = string(raw)
	}
	return out
}
func TestMediaMigration(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		catalog := testdata.Catalog()
		if err := seed.Seed(t.Context(), s, catalog); err != nil {
			t.Fatal(err)
		}
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := s.DB.ExecContext(t.Context(), q, args...); err != nil {
				t.Fatal(err)
			}
		}
		for _, v := range []struct{ table, column string }{{"users", "face"}, {"users", "top_photo"}, {"archives", "pic"}, {"collections", "cover"}, {"seasons", "cover"}, {"seasons", "horizontal"}, {"episodes", "cover"}, {"rooms", "cover"}, {"banners", "img"}} {
			exec("UPDATE "+v.table+" SET "+v.column+"=?", "data:image/svg+xml,legacy")
		}
		oldMP4 := "/mock/media/sample1.mp4"
		oldHLS := "/mock/hls/demo/index.m3u8"
		exec("UPDATE archive_pages SET media=?", oldMP4)
		exec("UPDATE episodes SET media=?", oldMP4)
		exec("UPDATE rooms SET source=?,media=?", "null", oldMP4)
		source, _ := json.Marshal(store.StreamSource{Type: "m3u8", URL: oldHLS})
		exec("UPDATE rooms SET source=? WHERE room_id=?", string(source), 20000)
		exec("UPDATE archives SET pic=? WHERE aid=?", "/upload/keep.png", 100100)
		exec("UPDATE archive_pages SET media=? WHERE cid=?", "/upload/keep.mp4", 50001)
		if err := seed.MigrateMedia(t.Context(), s, catalog); err != nil {
			t.Fatal(err)
		}
		snapshot := mediaSnapshot(t, s)
		for table, value := range snapshot {
			if strings.Contains(value, "data:") || strings.Contains(value, oldMP4) || strings.Contains(value, oldHLS) {
				t.Fatalf("unmigrated %s", table)
			}
		}
		u, err := s.UserByID(t.Context(), 1)
		if err != nil || u.Face != "/mock/avatars/default2.png" || !strings.HasPrefix(u.TopPhoto, "https://") {
			t.Fatal(u, err)
		}
		a, err := s.ArchiveByID(t.Context(), 100100)
		if err != nil || a.Pic != "/upload/keep.png" || a.Pages[0].Media != "/upload/keep.mp4" {
			t.Fatal(a, err)
		}
		rooms, err := s.ListLiveRoom(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rooms {
			if r.Source == nil || r.Source.Type != "m3u8" || !strings.HasPrefix(r.Source.URL, "/media/emby/") {
				t.Fatal(r)
			}
		}
		if err = seed.MigrateMedia(t.Context(), s, catalog); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snapshot, mediaSnapshot(t, s)) {
			t.Fatal("migration not idempotent")
		}
		// No catalog still clears legacy resources and leaves unplayable rooms stopped.
		exec("UPDATE users SET face=?,top_photo=?", "data:image/png,legacy", "data:image/png,legacy")
		exec("UPDATE rooms SET source=?,cover=?,live_status=1", "null", "data:image/png,legacy")
		if err = seed.MigrateMedia(t.Context(), s, emby.Catalog{}); err != nil {
			t.Fatal(err)
		}
		rooms, err = s.ListLiveRoom(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rooms {
			if r.LiveStatus != 0 || r.Source.Type != "push" || r.Cover != "" {
				t.Fatal(r)
			}
		}
		snapshot = mediaSnapshot(t, s)
		if err = seed.MigrateMedia(t.Context(), s, emby.Catalog{}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snapshot, mediaSnapshot(t, s)) {
			t.Fatal("empty-catalog migration not idempotent")
		}
	})
}
func TestMissingAndSmallCatalog(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		c, err := emby.LoadCatalog(filepath.Join(t.TempDir(), "missing.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = seed.Seed(t.Context(), s, c); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"archives", "seasons", "rooms", "banners", "replies", "history"} {
			var n int
			if err = s.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != 0 {
				t.Fatalf("%s=%d %v", table, n, err)
			}
		}
		// One item exercises short slices, missing images, optional metadata, and reset.
		c = testdata.Catalog()
		c.Series = c.Series[:1]
		c.Series[0].Episodes = c.Series[0].Episodes[:1]
		c.Movies = nil
		c.Series[0].Images = emby.Images{}
		path := filepath.Join(t.TempDir(), "catalog.json")
		raw, _ := json.Marshal(c)
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		c, err = emby.LoadCatalog(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = seed.Reset(t.Context(), s, c); err != nil {
			t.Fatal(err)
		}
		for table, want := range map[string]int{"archives": 2, "seasons": 1, "rooms": 1, "episodes": 1, "collections": 0, "live_messages": 19} {
			var got int
			if err = s.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil || got != want {
				t.Fatal(fmt.Sprintf("%s=%d want %d", table, got, want), err)
			}
		}
	})
}
