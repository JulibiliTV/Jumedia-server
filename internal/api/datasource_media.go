package api

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

func (s *Server) sourceMediaAllowed(ctx context.Context, id int64, item, source, mode string) (bool, error) {
	image := mode == "image"
	if !emby.ValidPair(item, "image") || (!image && !emby.ValidPair(item, source)) {
		return false, nil
	}
	var n int
	if image {
		err := s.Store.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM archives WHERE origin_ds=? AND origin_item=?) + (SELECT COUNT(*) FROM collections WHERE origin_ds=? AND origin_library=?)", id, item, id, item).Scan(&n)
		if err != nil || n > 0 {
			return n > 0, err
		}
	}
	pages, err := s.Store.ListPage(ctx, "WHERE aid IN (SELECT aid FROM archives WHERE origin_ds=? AND (?=1 OR state<>-100))", id, store.BoolInt(image))
	if err != nil {
		return false, err
	}
	for _, p := range pages {
		ds, it, src, refMode, ok := emby.ParseDSRef(p.Media)
		if ok && ds == id && it == item && (image || src == source && refMode == mode) {
			return true, nil
		}
	}
	return false, nil
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
	_, _ = io.Copy(w, res.Body)
}
