package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

func (s *Server) registerSourceMedia() {
	s.Mux.HandleFunc("GET /media/emby/{path...}", s.serveSourceMedia)
}

// Escape LIKE metacharacters in literal identifiers. '!' works with both DBs.
func sourceLikeLiteral(value string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value)
}

func sourceMediaFilter(id int64, item, source, mode string) (string, string) {
	prefix := "embyds:" + strconv.FormatInt(id, 10) + ":" + item + ":"
	if mode == "image" {
		return " LIKE ? ESCAPE '!'", sourceLikeLiteral(prefix) + "%"
	}
	return " = ?", prefix + source + ":" + mode
}

func sourceRefMatches(ref string, id int64, item, source, mode string) bool {
	ds, it, src, refMode, ok := emby.ParseDSRef(ref)
	return ok && ds == id && it == item && (mode == "image" || src == source && refMode == mode)
}

// Only effective originals grant access. Assembled demo pages have origin_ds=0;
// sync-owned pages must also agree with the requested datasource identity.
// aid/cid bind a transcode job to its original page (zero means any page).
func (s *Server) sourceOriginalMediaAllowed(ctx context.Context, id int64, item, source, mode string, aid, cid int64) (bool, error) {
	op, value := sourceMediaFilter(id, item, source, mode)
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT p.media FROM archive_pages p JOIN archives a ON a.aid=p.aid
 WHERE (a.origin_ds=0 OR a.origin_ds=?) AND (?=1 OR a.state<>-100)
 AND (?=0 OR (p.aid=? AND p.cid=?)) AND p.media`+op+`
 UNION ALL SELECT e.media FROM episodes e JOIN seasons s ON s.season_id=e.season_id
 WHERE s.state<>-100 AND (?=0 OR (e.aid=? AND e.cid=?)) AND e.media`+op,
		id, store.BoolInt(mode == "image"), aid, aid, cid, value, aid, aid, cid, value)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return false, err
		}
		if sourceRefMatches(ref, id, item, source, mode) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Server) sourceMediaAllowed(ctx context.Context, id int64, item, source, mode string) (bool, error) {
	image := mode == "image"
	if !emby.ValidPair(item, "image") || (!image && !emby.ValidPair(item, source)) {
		return false, nil
	}
	if image {
		var n int
		err := s.Store.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM archives WHERE origin_ds=? AND origin_item=?) + (SELECT COUNT(*) FROM collections WHERE origin_ds=? AND origin_library=?)", id, item, id, item).Scan(&n)
		if err != nil || n > 0 {
			return n > 0, err
		}
	}
	if allowed, err := s.sourceOriginalMediaAllowed(ctx, id, item, source, mode, 0, 0); err != nil || allowed {
		return allowed, err
	}
	// Standalone room media may grant access; a video room is only a pointer
	// to its page and cannot resurrect a deleted/replaced/mismatched original.
	// A live_caches row never grants public access, regardless of its status.
	op, value := sourceMediaFilter(id, item, source, mode)
	needle := value
	if image {
		needle = strings.TrimSuffix(needle, "%")
	} else {
		needle = sourceLikeLiteral(needle)
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT media,source FROM rooms WHERE media`+op+` OR source LIKE ? ESCAPE '!'`, value, "%"+needle+"%")
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var media, raw string
		if err = rows.Scan(&media, &raw); err != nil {
			break
		}
		var src store.StreamSource
		if raw != "" && json.Unmarshal([]byte(raw), &src) != nil {
			continue
		}
		if src.Type == "video" || src.AID != 0 || src.CID != 0 {
			continue
		}
		if sourceRefMatches(media, id, item, source, mode) || sourceRefMatches(src.URL, id, item, source, mode) {
			rows.Close()
			return true, nil
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || !image {
		return false, err
	}
	return s.sourceImageReferenced(ctx, id, item)
}

func (s *Server) sourceImageReferenced(ctx context.Context, id int64, item string) (bool, error) {
	// All identifiers below are fixed schema names, never request values.
	fields := []struct{ field, from, condition string }{
		{"pic", "archives", "(origin_ds=0 OR origin_ds=?)"},
		{"cover", "collections", "(origin_ds=0 OR origin_ds=?)"},
		{"e.cover", "episodes e JOIN seasons s ON s.season_id=e.season_id", "s.state<>-100"},
		{"cover", "seasons", "state<>-100"}, {"horizontal", "seasons", "state<>-100"},
		{"cover", "rooms", "1=1"}, {"face", "users", "1=1"},
		{"top_photo", "users", "1=1"}, {"img", "banners", "1=1"},
	}
	queries := make([]string, 0, len(fields))
	var args []any
	prefix := "%" + sourceLikeLiteral("/media/emby/"+item+"/image/") + "%"
	ds := strconv.FormatInt(id, 10)
	for _, f := range fields {
		queries = append(queries, "SELECT "+f.field+" FROM "+f.from+" WHERE "+f.condition+" AND "+f.field+" LIKE ? ESCAPE '!' AND ("+f.field+" LIKE ? ESCAPE '!' OR "+f.field+" LIKE ? ESCAPE '!' OR "+f.field+" LIKE ? ESCAPE '!' OR "+f.field+" LIKE ? ESCAPE '!')")
		if strings.Contains(f.condition, "?") {
			args = append(args, id)
		}
		args = append(args, prefix, "%?ds="+ds, "%?ds="+ds+"&%", "%&ds="+ds, "%&ds="+ds+"&%")
	}
	rows, err := s.Store.DB.QueryContext(ctx, strings.Join(queries, " UNION ALL "), args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return false, err
		}
		u, err := url.Parse(ref)
		if err == nil && pathImageKind(u.Path) != "" && u.Path == "/media/emby/"+item+"/image/"+pathImageKind(u.Path) && u.Query().Get("ds") == ds {
			return true, nil
		}
	}
	return false, rows.Err()
}

func pathImageKind(path string) string {
	for _, kind := range []string{"Primary", "Backdrop", "Thumb"} {
		if strings.HasSuffix(path, "/image/"+kind) {
			return kind
		}
	}
	return ""
}

func (s *Server) serveSourceMedia(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("ds"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	ds, err := store.DataSourceByID(r.Context(), s.Store.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(r.PathValue("path"), "/")
	item, kind, mode, container := "", "", "", ""
	image := false
	if len(parts) == 1 && strings.HasSuffix(parts[0], ".mp4") {
		item = strings.TrimSuffix(parts[0], ".mp4")
		kind = "mp4"
		mode = "direct"
	} else if len(parts) == 2 && strings.HasPrefix(parts[1], "file.") {
		item = parts[0]
		container = strings.TrimPrefix(parts[1], "file.")
		kind = "file"
		mode = "raw-" + container

	} else if len(parts) == 3 && parts[1] == "image" {
		item = parts[0]
		kind = parts[2]
		image = kind == "Primary" || kind == "Backdrop" || kind == "Thumb"
		mode = "image"
		if !image {
			http.NotFound(w, r)
			return
		}
	} else {
		http.NotFound(w, r)
		return
	}
	source := r.URL.Query().Get("source")
	allowed, err := s.sourceMediaAllowed(r.Context(), id, item, source, mode)
	if err != nil {
		http.Error(w, "media lookup failed", 500)
		return
	}
	if !allowed {
		http.NotFound(w, r)
		return
	}
	if image && s.serveCachedSourceImage(w, r, ds, item, kind) {
		return
	}
	client, err := s.sourceClient(r.Context(), ds)
	if err != nil {
		http.Error(w, "Emby unavailable", 502)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	if image {
		cancel()
		ctx, cancel = context.WithTimeout(r.Context(), 20*time.Second)
	}
	defer cancel()
	var res *http.Response
	if image {
		res, err = client.Image(ctx, item, kind, r.URL.Query().Get("tag"))
	} else if r.Method == http.MethodHead {
		if kind == "mp4" {
			container = "mp4"
		}
		res, err = client.HeadStream(ctx, item, source, container, r.Header)
	} else if kind == "file" {
		res, err = client.RawStream(ctx, item, source, container, r.Header)
	} else if kind == "mp4" {
		res, err = client.Stream(ctx, item, source, r.Header)
	} else {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Emby unavailable", 502)
		return
	}
	defer res.Body.Close()
	if image {
		if res.StatusCode != 200 || !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "image/") {
			http.Error(w, "Emby image unavailable", 502)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
		http.Error(w, "Emby unavailable", 502)
		return
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, res.Body)
}
