package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"jumedia-server/internal/emby"
)

// embyAllowed checks persisted references on every request, including live-only
// references; unreferenced sources cannot use this server as an open proxy.
func (s *Server) embyAllowed(ctx context.Context, item, source string) (bool, error) {
	if !emby.ValidPair(item, source) {
		return false, nil
	}
	ref := emby.Ref(item, source)
	var n int
	if err := s.Store.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM archive_pages WHERE media=?) + (SELECT COUNT(*) FROM episodes WHERE media=?) + (SELECT COUNT(*) FROM rooms WHERE media=?)", ref, ref, ref).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	rooms, err := s.Store.ListLiveRoom(ctx, "")
	if err != nil {
		return false, err
	}
	for _, room := range rooms {
		if room.Source == nil || (room.Source.Type != "m3u8" && room.Source.Type != "video") {
			continue
		}
		u, err := url.Parse(room.Source.URL)
		if err != nil {
			continue
		}
		if u.Path == "/media/emby/"+item+"/hls/index.m3u8" && u.Query().Get("source") == source {
			return true, nil
		}
	}
	return false, nil
}
func (s *Server) embyPlaylist(ctx context.Context, path, source, origin string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/media/emby/"), "/")
	if len(parts) != 3 || parts[1] != "hls" || (parts[2] != "index.m3u8" && parts[2] != "main.m3u8") {
		return "", emby.ErrUnavailable
	}
	allowed, err := s.embyAllowed(ctx, parts[0], source)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", emby.ErrUnavailable
	}
	return s.embyClient().Playlist(ctx, parts[0], source, parts[2], origin)
}
func (s *Server) registerEmby() { s.Mux.HandleFunc("GET /media/emby/{path...}", s.serveEmby) }
func (s *Server) serveEmby(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("ds") {
		s.serveSourceMedia(w, r)
		return
	}
	path := r.PathValue("path")
	parts := strings.Split(path, "/")
	item, kind := "", ""
	if len(parts) == 1 && strings.HasSuffix(parts[0], ".mp4") {
		item = strings.TrimSuffix(parts[0], ".mp4")
		kind = "mp4"
	} else if len(parts) >= 3 && parts[1] == "hls" {
		item = parts[0]
		kind = strings.Join(parts[2:], "/")
	}
	if kind != "mp4" && kind != "index.m3u8" && kind != "main.m3u8" && !emby.SegmentPath.MatchString(kind) {
		http.NotFound(w, r)
		return
	}
	source := r.URL.Query().Get("source")
	allowed, err := s.embyAllowed(r.Context(), item, source)
	if err != nil {
		http.Error(w, "media lookup failed", 500)
		return
	}
	if !allowed {
		http.NotFound(w, r)
		return
	}
	if kind == "index.m3u8" || kind == "main.m3u8" {
		text, err := s.embyClient().Playlist(r.Context(), item, source, kind, s.Origin(r))
		if err != nil {
			http.Error(w, "Emby unavailable", 502)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.WriteString(w, text)
		return
	}
	var res *http.Response
	if kind == "mp4" {
		res, err = s.embyClient().Stream(r.Context(), item, source, r.Header)
	} else {
		res, err = s.embyClient().Segment(r.Context(), item, source, kind)
	}
	if err != nil {
		http.Error(w, "Emby unavailable", 502)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
		http.Error(w, "Emby unavailable", 502)
		return
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := res.Header.Get(h); value != "" {
			w.Header().Set(h, value)
		}
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}
