package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/hls"
	"jumedia-server/internal/store"
)

const livePlaylistLimit = 2 << 20
const liveFetchTimeout = 10 * time.Second

var livePlaylistName = regexp.MustCompile(`^(?:index|v(\d+))\.m3u8$`)
var liveRoomNumber = regexp.MustCompile(`^\d+$`)

type livePlaylist struct {
	list      hls.Playlist
	text, url string
	at        time.Time
}
type liveProbe struct {
	URL            string    `json:"url"`
	Master         bool      `json:"master"`
	Kind           string    `json:"kind"`
	Format         string    `json:"format"`
	Duration       int64     `json:"duration"`
	Segments       int       `json:"segments"`
	TargetDuration int       `json:"target_duration"`
	Encrypted      bool      `json:"encrypted"`
	Variants       []liveMap `json:"variants"`
}

func (l *liveAPI) loadPlaylist(c *Ctx, address string, maxAge time.Duration) (livePlaylist, error) {
	empty := livePlaylist{}
	base, e := url.Parse(l.s.Origin(c.R))
	if e != nil {
		return empty, Fail(-400, "m3u8 地址格式不正确")
	}
	u, e := url.Parse(strings.TrimSpace(address))
	if e != nil {
		return empty, Fail(-400, "m3u8 地址格式不正确")
	}
	u = base.ResolveReference(u)
	if u.Scheme != "http" && u.Scheme != "https" {
		return empty, Fail(-400, "仅支持 http / https 的 m3u8 地址")
	}
	if u.Host == "" {
		return empty, Fail(-400, "m3u8 地址格式不正确")
	}
	key := u.String()
	if u.Host == base.Host && u.Scheme == base.Scheme && strings.HasPrefix(u.Path, "/live-cache/") {
		parts := strings.Split(strings.TrimPrefix(u.Path, "/live-cache/"), "/")
		if len(parts) != 2 || parts[1] != "index.m3u8" {
			return empty, Fail(-404, "转码缓存不存在")
		}
		x, err := liveCacheByKey(c.Context(), l.s.Store.DB, parts[0])
		if err != nil {
			return empty, err
		}
		if x == nil || x.Status != "ready" {
			return empty, Fail(-404, "转码缓存不存在")
		}
		body, err := liveCacheRead(x, "index.m3u8")
		if err != nil {
			return empty, Fail(-404, "转码缓存不存在")
		}
		p, err := hls.ParsePlaylist(string(body), key)
		return livePlaylist{p, string(body), key, l.now()}, err
	}
	if u.Host == base.Host && u.Scheme == base.Scheme && strings.HasPrefix(u.Path, "/media/emby/") {
		return empty, Fail(-400, "无法访问该地址（网络错误或源站未开放跨域）")
	}
	l.cacheMu.Lock()
	cached, ok := l.cache[key]
	l.cacheMu.Unlock()
	if ok && cached.text != "" && l.now().Sub(cached.at) < maxAge {
		return cached, nil
	}
	ctx, cancel := context.WithTimeout(c.Context(), liveFetchTimeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, key, nil)
	if e != nil {
		return empty, Fail(-400, "m3u8 地址格式不正确")
	}
	req.Header.Set("Cache-Control", "no-cache")
	client := &http.Client{Timeout: liveFetchTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("unsupported redirect protocol")
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}}
	res, e := client.Do(req)
	if e != nil {
		return empty, Fail(-400, "无法访问该地址（网络错误或源站未开放跨域）")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return empty, Fail(-400, fmt.Sprintf("源站返回 HTTP %d", res.StatusCode))
	}
	if res.ContentLength > livePlaylistLimit {
		return empty, Fail(-400, "播放列表不能超过 2 MB")
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, livePlaylistLimit+1))
	if e != nil {
		return empty, Fail(-400, "无法访问该地址（网络错误或源站未开放跨域）")
	}
	if len(data) > livePlaylistLimit {
		return empty, Fail(-400, "播放列表不能超过 2 MB")
	}
	final := res.Request.URL.String()
	p, e := hls.ParsePlaylist(string(data), final)
	if e != nil {
		return empty, Fail(-400, e.Error())
	}
	value := livePlaylist{p, string(data), final, l.now()}
	l.cacheMu.Lock()
	// Bound cache lifetime and entry count; fresh entries retain the Mock TTLs.
	for k, v := range l.cache {
		if l.now().Sub(v.at) >= 60*time.Second {
			delete(l.cache, k)
		}
	}
	if len(l.cache) >= 128 {
		oldKey := ""
		var oldest time.Time
		for k, v := range l.cache {
			if oldKey == "" || v.at.Before(oldest) {
				oldKey = k
				oldest = v.at
			}
		}
		delete(l.cache, oldKey)
	}
	l.cache[key] = value
	l.cacheMu.Unlock()
	return value, nil
}
func (l *liveAPI) probe(c *Ctx, address string) (liveProbe, error) {
	out := liveProbe{Variants: []liveMap{}}
	if strings.TrimSpace(address) == "" {
		return out, Fail(-400, "请填写 m3u8 地址")
	}
	if l.localRelayAddress(c, address) {
		return out, Fail(-400, "请使用「转播视频」选择本站视频")
	}
	top, e := l.loadPlaylist(c, address, 0)
	if e != nil {
		return out, e
	}
	media := top
	if top.list.Kind == "master" {
		media, e = l.loadPlaylist(c, top.list.Variants[0].URI, 0)
		if e != nil {
			return out, e
		}
	}
	if media.list.Kind != "media" {
		return out, Fail(-400, "码率播放列表无效")
	}
	m := media.list
	out.URL = top.url
	out.Master = top.list.Kind == "master"
	out.Kind = "live"
	if m.Ended {
		out.Kind = "vod"
	}
	out.Format = m.Format
	out.Duration = int64(math.Round(m.Duration))
	out.Segments = len(m.Segments)
	out.TargetDuration = m.TargetDuration
	for _, s := range m.Segments {
		if s.Key != "" {
			out.Encrypted = true
		}
	}
	for _, v := range top.list.Variants {
		out.Variants = append(out.Variants, liveMap{"bandwidth": v.Bandwidth, "resolution": v.Resolution})
	}
	return out, nil
}
func (l *liveAPI) probeHandler(c *Ctx) (any, error) {
	if _, e := c.Login(); e != nil {
		return nil, e
	}
	return l.probe(c, c.P["url"])
}
func liveSource(r *store.LiveRoom) *store.StreamSource {
	if r.Source != nil {
		return r.Source
	}
	return &store.StreamSource{Type: "push", URL: "", Loop: true}
}
func (l *liveAPI) applySource(c *Ctx, q db.Querier, r *store.LiveRoom) error {
	next := &store.StreamSource{Type: "push", URL: "", Loop: true}
	if c.P["source_type"] == "m3u8" {
		info, e := l.probe(c, c.P["source_url"])
		if e != nil {
			return e
		}
		next = &store.StreamSource{Type: "m3u8", URL: strings.TrimSpace(c.P["source_url"]), Loop: c.P["loop"] != "false", Format: info.Format, Kind: info.Kind}
	}
	if c.P["source_type"] == "video" {
		var e error
		next, e = l.s.videoSource(c, q, r)
		if e != nil {
			return e
		}
	}
	if *next != *liveSource(r) && r.LiveStatus == 1 {
		r.LiveTime = l.now().Unix()
	}
	r.Source = next
	cache, e := l.s.roomCache(c.Context(), q, r)
	if e != nil {
		return e
	}
	r.PendingStart = next.Type == "video" && r.LiveStatus != 1 && BoolParam(c.P["auto_start"]) && liveCacheNotReady(cache) != ""
	return nil
}
func liveSaveSource(c *Ctx, q db.Querier, r *store.LiveRoom) error {
	if database, ok := q.(*db.DB); ok {
		return database.WithTx(c.Context(), func(tx *db.Tx) error { return liveSaveSource(c, tx, r) })
	}
	if tx, ok := q.(*db.Tx); ok {
		if e := LockTx(c.Context(), tx); e != nil {
			return e
		}
	}
	// Recheck the chosen page under the same lock as sync/delete, so a source
	// deletion cannot race this write and leave a room relaying a deleted video.
	if r.Source != nil && r.Source.Type == "video" {
		a, e := store.ArchiveFrom(c.Context(), q, r.Source.AID)
		if errors.Is(e, sql.ErrNoRows) || e == nil && (a.MID != r.UID || a.State == -100 || a.State == -6) {
			return Fail(-404, "视频不存在或不属于该直播间的主播")
		}
		if e != nil {
			return e
		}
		found := false
		for _, p := range a.Pages {
			if p.CID == r.Source.CID {
				cache, err := liveCacheByKey(c.Context(), q, r.Source.Cache)
				if err != nil {
					return err
				}
				if cache == nil || cache.Media != p.Media {
					return Fail(-400, "转播视频的转码缓存不存在，请重新选择视频")
				}
				r.Source.URL = liveCachePath(cache.Key)
				found = true
				if !liveRelayableMedia(p.Media) {
					return Fail(-400, "只能转播数据源中的视频")
				}
				break
			}
		}
		if !found {
			return Fail(-404, "视频不存在或不属于该直播间的主播")
		}
	}

	raw, e := json.Marshal(r.Source)
	if e != nil {
		return e
	}
	_, e = q.ExecContext(c.Context(), "UPDATE rooms SET source=?,live_time=?,live_status=?,online=?,pending_start=? WHERE room_id=?", string(raw), r.LiveTime, r.LiveStatus, r.Online, store.BoolInt(r.PendingStart), r.RoomID)
	return e
}
func (l *liveAPI) sourceUpdate(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	r, e := l.ownRoom(c, u.MID)
	if e != nil {
		return nil, e
	}
	if e = l.saveAppliedSource(c, r, false, 0); e != nil {
		return nil, e
	}

	source, e := l.s.liveSourceJSON(c, r)
	return liveMap{"source": source, "pending_start": r.PendingStart}, e
}
func (l *liveAPI) adminSource(c *Ctx) (any, error) {
	u, e := c.Admin()
	if e != nil {
		return nil, e
	}
	r, e := l.room(c, ParseUnicodeID(c.P["room_id"]))
	if e != nil {
		return nil, e
	}
	if e = l.saveAppliedSource(c, r, BoolParam(c.P["start"]), u.MID); e != nil {
		return nil, e
	}

	source, e := l.s.liveSourceJSON(c, r)
	return liveMap{"source": source, "live_status": r.LiveStatus, "pending_start": r.PendingStart}, e
}

func (l *liveAPI) relayedPlaylist(c *Ctx, r *store.LiveRoom, variant *int) (string, error) {
	if r.Banned || r.LiveStatus != 1 || r.Source == nil || (r.Source.Type != "m3u8" && r.Source.Type != "video") {
		return "", nil
	}
	if r.Source.Type == "video" {
		x, e := l.s.roomCache(c.Context(), l.s.Store.DB, r)
		if e != nil {
			return "", e
		}
		if x == nil || x.Status != "ready" {
			return "", nil
		}
	}
	age := 60 * time.Second
	if r.Source.Kind == "live" {
		age = time.Second
	}
	top, e := l.loadPlaylist(c, r.Source.URL, age)
	if e != nil {
		return "", e
	}
	media := top
	if top.list.Kind == "master" {
		if variant == nil {
			return hls.RelayMaster(top.list, func(i int) string { return l.s.Origin(c.R) + fmt.Sprintf("/live-relay/%d/v%d.m3u8", r.RoomID, i) }), nil
		}
		if *variant < 0 || *variant >= len(top.list.Variants) {
			return "", nil
		}
		media, e = l.loadPlaylist(c, top.list.Variants[*variant].URI, age)
		if e != nil {
			return "", e
		}
	}
	if media.list.Kind != "media" {
		return "", nil
	}
	if !media.list.Ended {
		return hls.Passthrough(media.text, media.url), nil
	}
	w := hls.LiveWindow(media.list, float64(r.LiveTime), float64(l.now().UnixMilli())/1000, hls.WindowOptions{Loop: r.Source.Loop})
	if w.Ended {
		if e := l.finishRelay(c, r); e != nil {
			return "", e
		}
	}
	return w.Text, nil
}

// finishRelay compares the decoded source, then uses the original stored text
// in an optimistic update so JSON key order does not change relay behavior.
func (l *liveAPI) finishRelay(c *Ctx, r *store.LiveRoom) error {
	var raw string
	var started int64
	err := l.s.Store.DB.QueryRowContext(c.Context(), "SELECT source,live_time FROM rooms WHERE room_id=?", r.RoomID).Scan(&raw, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var current *store.StreamSource
	if err = json.Unmarshal([]byte(raw), &current); err != nil {
		return err
	}
	if current == nil || *current != *r.Source || started != r.LiveTime {
		return nil
	}
	// An old in-flight request must not stop a newly started/reconfigured stream.
	res, err := l.s.Store.DB.ExecContext(c.Context(), "UPDATE rooms SET live_status=0 WHERE room_id=? AND live_time=? AND source=? AND live_status=1", r.RoomID, started, raw)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n > 0 {
		r.LiveStatus = 0
	}
	return err
}

func (l *liveAPI) relay(c *Ctx) (any, error) {
	roomID, name := c.R.PathValue("room"), c.R.PathValue("playlist")
	m := livePlaylistName.FindStringSubmatch(name)
	if !liveRoomNumber.MatchString(roomID) || m == nil {
		return nil, Fail(-404, "接口不存在")
	}
	r, e := l.s.Store.RoomByID(c.Context(), ParseUnicodeID(roomID))
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	var variant *int
	if m[1] != "" {
		v, err := strconv.Atoi(m[1])
		if err != nil {
			v = -1
		}
		variant = &v
	}
	text := ""
	if r != nil {
		text, e = l.relayedPlaylist(c, r, variant)
		if e != nil {
			return nil, e
		}
	}
	status := 200
	if text == "" {
		status = 404
		text = "#EXTM3U\n#EXT-X-ENDLIST\n"
	}
	return Text(text, status, "application/vnd.apple.mpegurl"), nil
}
func (l *liveAPI) play(c *Ctx) (any, error) {
	r, e := l.room(c, ParseUnicodeID(c.P["room_id"]))
	if e != nil {
		return nil, e
	}
	if r.LiveStatus == 1 && r.Source != nil && (r.Source.Type == "m3u8" || r.Source.Type == "video") && !r.Source.Loop && r.Source.Kind == "vod" {
		if _, e = l.relayedPlaylist(c, r, nil); e != nil {
			return nil, e
		}
	}
	out := liveMap{"room_id": r.RoomID, "uid": r.UID, "live_status": r.LiveStatus, "playurl_info": nil}
	if r.LiveStatus != 1 || r.Source == nil || (r.Source.Type != "m3u8" && r.Source.Type != "video") || r.Source.URL == "" {
		return out, nil
	}
	format, base, host := "fmp4", "", r.Media
	accept := []int{10000, 400}
	quality := []liveMap{{"qn": 10000, "desc": "原画"}, {"qn": 400, "desc": "蓝光"}}
	if strings.HasPrefix(r.Media, "/") {
		base = r.Media
		host = l.s.Origin(c.R)
	}
	if r.Source != nil && (r.Source.Type == "m3u8" || r.Source.Type == "video") {
		format = r.Source.Format
		if format == "" {
			format = "ts"
		}
		base = fmt.Sprintf("/live-relay/%d/index.m3u8", r.RoomID)
		host = l.s.Origin(c.R)
		accept = []int{10000}
		quality = quality[:1]
	}
	codec := liveMap{"codec_name": "avc", "current_qn": 10000, "accept_qn": accept, "base_url": base, "url_info": []liveMap{{"host": host, "extra": "", "stream_ttl": 3600}}}
	out["live_time"] = r.LiveTime
	out["playurl_info"] = liveMap{"playurl": liveMap{
		"g_qn_desc": quality,
		"stream":    []liveMap{{"protocol_name": "http_hls", "format": []liveMap{{"format_name": format, "codec": []liveMap{codec}}}}},
	}}
	return out, nil
}

func (l *liveAPI) localRelayAddress(c *Ctx, address string) bool {
	base, e := url.Parse(l.s.Origin(c.R))
	if e != nil {
		return false
	}
	u, e := url.Parse(strings.TrimSpace(address))
	if e != nil {
		return false
	}
	u = base.ResolveReference(u)
	p := path.Clean(u.Path)
	return u.Host == base.Host && u.Scheme == base.Scheme && (strings.HasPrefix(p, "/live-cache/") || strings.HasPrefix(p, "/live-relay/") || p == "/live-cache" || p == "/live-relay")
}

// Probe remote playlists before opening a transaction. Video selection, cache creation,
// room state and admin audit commit atomically under the same database write lock.
func (l *liveAPI) saveAppliedSource(c *Ctx, r *store.LiveRoom, start bool, actor int64) error {
	if c.P["source_type"] != "video" {
		if e := l.applySource(c, l.s.Store.DB, r); e != nil {
			return e
		}
	}
	return l.s.WriteTx(c, func(tx *db.Tx) error {
		if c.P["source_type"] == "video" {
			if e := l.applySource(c, tx, r); e != nil {
				return e
			}
		}
		if start && !r.Banned && r.LiveStatus != 1 {
			cache, e := l.s.roomCache(c.Context(), tx, r)
			if e != nil {
				return e
			}
			if r.Source.Type == "video" && liveCacheNotReady(cache) != "" {
				r.PendingStart = true
			} else {
				r.LiveStatus = 1
				r.LiveTime = l.now().Unix()
				r.Online = max(1, r.Online)
				r.PendingStart = false
			}
		}
		if e := liveSaveSource(c, tx, r); e != nil {
			return e
		}
		if actor != 0 {
			target := "RTMP 推流"
			if r.Source.Type == "m3u8" {
				target = r.Source.URL
			}
			if r.Source.Type == "video" {
				target = fmt.Sprintf("转播视频 av%d", r.Source.AID)
			}
			return l.s.LogTx(c.Context(), tx, actor, "配置直播源", r.Title+" · "+target, l.now().Unix())
		}
		return nil
	})
}
