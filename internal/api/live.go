package api

import (
	"database/sql"
	"errors"

	"net/url"
	"strconv"

	"sync"
	"time"

	"jumedia-server/internal/store"
)

type liveAPI struct {
	s       *Server
	writes  sync.Mutex
	cacheMu sync.Mutex
	cache   map[string]livePlaylist
	now     func() time.Time
}
type liveMap = map[string]any

func (s *Server) registerLive() {
	s.Mux.HandleFunc("GET /sub", s.liveChat.serve)
	l := &liveAPI{s: s, cache: map[string]livePlaylist{}, now: time.Now}
	s.handle("/xlive/app-ucenter/v1/fansMedal/panel", l.medalPanel)
	s.handle("/rankdb/v1/RoomRank/webMedalRank", l.medalRank)
	s.handle("/xlive/web-room/v1/fansMedal/wear", l.medalWear)
	s.handle("/xlive/web-room/v1/fansMedal/take_off", l.medalTakeOff)
	s.handle("/xlive/web-room/v1/fansMedal/join", l.medalJoin)
	for path, h := range map[string]Handler{
		"/xlive/web-interface/v1/index/getWebAreaList":    l.areas,
		"/room/v1/area/getRoomList":                       l.rooms,
		"/xlive/web-interface/v1/second/getUserRecommend": l.recommend,
		"/xlive/web-ucenter/user/following":               l.following,
		"/room/v1/Room/get_info":                          l.info,
		"/room/v1/Room/room_init":                         l.init,
		"/live_user/v1/Master/info":                       l.master,
		"/xlive/web-room/v2/index/getRoomPlayInfo":        l.play,
		"/xlive/web-room/v1/index/getDanmuInfo":           l.danmu,
		"/xlive/web-room/v1/dM/gethistory":                l.history,
		"/av/v1/SuperChat/getMessageList":                 l.superChat,
		"/msg/send":                                       l.send,
		"/xlive/app-blink/v1/streamSource/probe":          l.probeHandler,
	} {
		s.handle(path, h)
	}
	for path, h := range map[string]Handler{
		"/xlive/app-blink/v1/live/getWebUpStreamAddr": l.webUpStreamAddr,
		"/xlive/app-blink/v1/room/GetInfo":            l.creatorInfo,
		"/xlive/app-blink/v1/streamSource/update":     l.sourceUpdate,
		"/xlive/app-blink/v1/streamSource/retry":      l.sourceRetry,
		"/room/v1/Room/update":                        l.update,
		"/room/v1/Room/startLive":                     l.start,
		"/room/v1/Room/stopLive":                      l.stop,
		"/x/admin/live/room/source":                   l.adminSource,
	} {
		s.handle(path, func(c *Ctx) (any, error) {
			l.writes.Lock()
			defer l.writes.Unlock()
			s.liveCaches.mu.Lock()
			defer s.liveCaches.mu.Unlock()
			return h(c)
		})
	}
	s.handle("/live-relay/{room}/{playlist}", l.relay)
}

func liveDate(ts int64) string { return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04:05") }
func liveHost(c *Ctx) string {
	u, e := url.Parse(c.Server.Origin(c.R))
	if e != nil {
		return c.R.URL.Hostname()
	}
	return u.Hostname()
}
func liveStringID(id int64) string { return strconv.FormatInt(id, 10) }

func (l *liveAPI) room(c *Ctx, id int64) (*store.LiveRoom, error) {
	r, e := l.s.Store.RoomByID(c.Context(), id)
	if errors.Is(e, sql.ErrNoRows) || (e == nil && r.Banned && (c.Me == nil || (c.Me.Role != "admin" && c.Me.MID != r.UID))) {
		return nil, Fail(19002003, "房间不存在或已被封禁")
	}
	return r, e
}
func (l *liveAPI) ownRoom(c *Ctx, uid int64) (*store.LiveRoom, error) {
	rooms, e := l.s.Store.ListLiveRoom(c.Context(), "WHERE uid=? ORDER BY room_id LIMIT 1", uid)
	if e != nil {
		return nil, e
	}
	if len(rooms) == 0 {
		return nil, Fail(-404, "尚未开通直播间")
	}
	return &rooms[0], nil
}
func (l *liveAPI) cards(c *Ctx, rooms []store.LiveRoom) ([]liveMap, error) {
	out := []liveMap{}
	for _, r := range rooms {
		v, e := l.s.Render.RoomCard(c.Context(), r)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
