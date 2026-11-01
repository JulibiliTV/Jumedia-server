package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

// MigrateMedia replaces only legacy resources, preserving user-uploaded media.
// The same transaction and deterministic row selection make restarts idempotent.
func MigrateMedia(ctx context.Context, s *store.Store, catalog emby.Catalog) error {
	pictures, backdrops, media, hls := []string{}, catalogBackdrops(catalog), []string{}, []string{}
	for _, v := range catalog.Series {
		if image := v.Images.Wide(); image != "" {
			pictures = append(pictures, image)
		}
		for _, e := range v.Episodes {
			media = append(media, emby.Ref(e.ID, e.SourceID))
			hls = append(hls, emby.HLSPath(e.ID, e.SourceID))
		}
	}
	for _, v := range catalog.Movies {
		if image := v.Images.Wide(); image != "" {
			pictures = append(pictures, image)
		}
		media = append(media, emby.Ref(v.ID, v.SourceID))
		hls = append(hls, emby.HLSPath(v.ID, v.SourceID))
	}
	return s.DB.WithTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE ids SET value=value WHERE name=?", "global"); err != nil {
			return err
		}
		for _, field := range []struct{ table, id, column, kind string }{
			{"users", "mid", "face", "avatar"}, {"users", "mid", "top_photo", "backdrop"},
			{"archives", "aid", "pic", "image"}, {"collections", "id", "cover", "image"},
			{"seasons", "season_id", "cover", "image"}, {"seasons", "season_id", "horizontal", "backdrop"},
			{"episodes", "ep_id", "cover", "image"}, {"rooms", "room_id", "cover", "image"}, {"banners", "id", "img", "image"},
			{"archive_pages", "cid", "media", "media"}, {"episodes", "ep_id", "media", "media"}, {"rooms", "room_id", "media", "media"},
		} {
			// Identifiers come only from the fixed schema list above.
			rows, err := tx.QueryContext(ctx, "SELECT "+field.id+","+field.column+" FROM "+field.table)
			if err != nil {
				return err
			}
			type replacement struct {
				id    int64
				value string
			}
			changes := []replacement{}
			for rows.Next() {
				var id int64
				var value string
				if err = rows.Scan(&id, &value); err != nil {
					rows.Close()
					return err
				}
				if !legacyResource(value) {
					continue
				}
				next := pickString(pictures, id)
				switch field.kind {
				case "avatar":
					next = Avatar(id)
				case "backdrop":
					next = pickString(backdrops, id)
				case "media":
					next = pickString(media, id)
				}
				changes = append(changes, replacement{id, next})
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, v := range changes {
				if _, err = tx.ExecContext(ctx, "UPDATE "+field.table+" SET "+field.column+"=? WHERE "+field.id+"=?", v.value, v.id); err != nil {
					return err
				}
			}
		}
		rows, err := tx.QueryContext(ctx, "SELECT room_id,source FROM rooms")
		if err != nil {
			return err
		}
		type roomUpdate struct {
			id     int64
			source string
			stop   bool
		}
		changes := []roomUpdate{}
		for rows.Next() {
			var id int64
			var raw string
			if err = rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var source *store.StreamSource
			if err = json.Unmarshal([]byte(raw), &source); err != nil {
				rows.Close()
				return err
			}
			if source != nil && source.URL != "" && !legacyResource(source.URL) {
				continue
			}
			// Video references may await their startup cache URL. They and explicit
			// push rooms are valid choices, not missing legacy sources.
			if source != nil && (source.Type == "video" || (source.Type == "push" && source.URL == "")) {
				continue
			}
			next := pickString(hls, id)
			source = &store.StreamSource{Type: "m3u8", URL: next, Loop: true, Format: "ts", Kind: "vod"}
			if next == "" {
				source = &store.StreamSource{Type: "push", Loop: true}
			}
			encoded, _ := json.Marshal(source)
			changes = append(changes, roomUpdate{id, string(encoded), next == ""})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, v := range changes {
			if _, err = tx.ExecContext(ctx, "UPDATE rooms SET source=? WHERE room_id=?", v.source, v.id); err != nil {
				return err
			}
			if v.stop {
				if _, err = tx.ExecContext(ctx, "UPDATE rooms SET live_status=0 WHERE room_id=?", v.id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
func Avatar(mid int64) string {
	n := mid % 8
	if n < 0 {
		n = -n
	}
	return fmt.Sprintf("/mock/avatars/default%d.png", n+1)
}
func legacyResource(v string) bool {
	return strings.HasPrefix(strings.ToLower(v), "data:") || strings.HasPrefix(v, "/mock/media/sample") || strings.HasPrefix(v, "/mock/hls/demo")
}
