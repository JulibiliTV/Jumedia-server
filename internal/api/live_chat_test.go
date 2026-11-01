package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"jumedia-server/internal/store"
)

func chatHTTP(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	t.Cleanup(s.CloseLiveChat)
	return server
}

func chatDial(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/sub", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://tv-client.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func chatWrite(t *testing.T, c *websocket.Conn, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, data); err != nil {
		t.Fatal(err)
	}
}

func chatRead(t *testing.T, c *websocket.Conn, operation uint32) livePacket {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	kind, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.MessageBinary || binary.BigEndian.Uint16(data[6:]) != 0 {
		t.Fatalf("non-v0/binary response: %x", data)
	}
	packets, err := decodeLivePackets(data)
	if err != nil || len(packets) != 1 || packets[0].operation != operation {
		t.Fatalf("packets %v: %v", packets, err)
	}
	return packets[0]
}

func chatJSON(t *testing.T, packet livePacket) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(packet.body, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func chatClosed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected peer close, got %v", err)
	}
}

func chatToken(t *testing.T, s *Server, rid int64, session *store.Session) string {
	t.Helper()
	value := liveData(t, get(t, s, "/xlive/web-room/v1/index/getDanmuInfo?id="+liveStringID(rid), session))
	return liveField(t, value, "token").(string)
}

func chatAuthPacket(token string, roomID, uid int64) []byte {
	body, _ := json.Marshal(liveMap{"uid": uid, "roomid": roomID, "protover": 3, "platform": "web", "type": 2, "key": token, "buvid": "test"})
	packet := encodeLivePacket(7, body)
	binary.BigEndian.PutUint16(packet[6:], 1)
	return packet
}

func chatConnect(t *testing.T, s *Server, server *httptest.Server, rid int64, session *store.Session) *websocket.Conn {
	t.Helper()
	c := chatDial(t, server)
	var uid int64
	if session != nil {
		uid = session.MID
	}
	chatWrite(t, c, chatAuthPacket(chatToken(t, s, rid, session), rid, uid))
	assertCode(t, chatJSON(t, chatRead(t, c, 8)), 0)
	return c
}

func TestLiveChatTokensAndHosts(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		server := chatHTTP(t, s)
		admin := login(t, s, "jumedia", "admin123")
		token := chatToken(t, s, 21000, admin)
		if !s.liveChat.tokens.valid(token, 21000, admin.MID, time.Now()) {
			t.Fatal("issued token invalid")
		}
		if s.liveChat.tokens.valid(token, 21001, admin.MID, time.Now()) || s.liveChat.tokens.valid(token, 21000, 0, time.Now()) || s.liveChat.tokens.valid(token, 21000, admin.MID, time.Now().Add(time.Hour+time.Second)) {
			t.Fatal("token binding/expiry")
		}
		// A new Server over the same database represents restart; old tokens survive.
		restarted := New(s.Store, s.Config)
		defer restarted.CloseLiveChat()
		if err := restarted.InitLiveChat(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !restarted.liveChat.tokens.valid(token, 21000, admin.MID, time.Now()) {
			t.Fatal("token lost after restart")
		}
		settings := liveData(t, get(t, s, "/x/admin/settings", admin)).(map[string]any)
		if _, ok := settings[liveChatSecretKey]; ok {
			t.Fatal("secret exposed")
		}
		assertCode(t, post(t, s, "/x/admin/settings/save", url.Values{"csrf": {admin.CSRF}, liveChatSecretKey: {"changed"}}, admin), 0)
		if err := New(s.Store, s.Config).InitLiveChat(context.Background()); err != nil {
			t.Fatal("secret overwritten", err)
		}
		raw, _ := base64.RawURLEncoding.DecodeString(token)
		raw[len(raw)-1] ^= 1
		bad := base64.RawURLEncoding.EncodeToString(raw)
		cases := []struct {
			name, token string
			roomID, uid int64
		}{
			{"valid", token, 21000, admin.MID},
			{"anonymous", chatToken(t, s, 21000, nil), 21000, 0},
			{"tampered", bad, 21000, admin.MID},
			{"garbage", "invalid", 21000, 0},
			{"wrong uid", token, 21000, 0},
			{"wrong room", token, 20000, admin.MID},
			{"expired", s.liveChat.tokens.issue(21000, 0, time.Now().Add(-time.Second)), 21000, 0},
			{"missing room", s.liveChat.tokens.issue(999999, 0, time.Now().Add(time.Hour)), 999999, 0},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				c := chatDial(t, server)
				chatWrite(t, c, chatAuthPacket(tt.token, tt.roomID, tt.uid))
				want := -101
				if tt.name == "valid" || tt.name == "anonymous" {
					want = 0
				}
				assertCode(t, chatJSON(t, chatRead(t, c, 8)), want)
				if want != 0 {
					chatClosed(t, c)
				}
			})
		}
		for _, state := range []struct{ banned, status int }{{1, 1}, {0, 0}} {
			liveExec(t, s, "UPDATE rooms SET banned=?,live_status=? WHERE room_id=?", state.banned, state.status, 21000)
			c := chatDial(t, server)
			chatWrite(t, c, chatAuthPacket(token, 21000, admin.MID))
			assertCode(t, chatJSON(t, chatRead(t, c, 8)), -101)
			chatClosed(t, c)
		}
		// No background WebSocket handlers read Config; only these synchronous calls do.
		for _, tt := range []struct {
			origin    string
			port, wss float64
		}{
			{"https://tv.example:8443", 8443, 8443}, {"https://tv.example", 443, 443},
			{"http://tv.example:8080", 8080, 443}, {"http://tv.example", 80, 443},
		} {
			s.Config.PublicOrigin = tt.origin
			v := liveData(t, get(t, s, "/xlive/web-room/v1/index/getDanmuInfo?id=21000"))
			liveWant(t, v, "tv.example", "host_list", 0, "host")
			liveWant(t, v, tt.port, "host_list", 0, "port")
			liveWant(t, v, tt.port, "host_list", 0, "ws_port")
			liveWant(t, v, tt.wss, "host_list", 0, "wss_port")
		}
		s.Config.PublicOrigin = ""
		req := httptest.NewRequest("GET", "/xlive/web-room/v1/index/getDanmuInfo?id=21000", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "proxy.example:9443")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		var v map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		liveWant(t, v, "proxy.example", "data", "host_list", 0, "host")
		liveWant(t, v, float64(9443), "data", "host_list", 0, "wss_port")
	})
}

func TestLiveChatBroadcastContract(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		server := chatHTTP(t, s)
		session := login(t, s, "lumen", "demo123")
		// Since T21 the demo official room is offline; chat needs both rooms live.
		liveExec(t, s, "UPDATE rooms SET live_status=1,banned=0 WHERE room_id IN (21000,20000)")
		c1 := chatConnect(t, s, server, 21000, session)
		c2 := chatConnect(t, s, server, 21000, nil)
		other := chatConnect(t, s, server, 20000, nil)
		liveExec(t, s, "UPDATE rooms SET online=? WHERE room_id=?", 4242, 21000)
		chatWrite(t, c1, append(encodeLivePacket(2, nil), encodeLivePacket(2, []byte("anything"))...))
		for i := 0; i < 2; i++ {
			if got := binary.BigEndian.Uint32(chatRead(t, c1, 3).body); got != 4242 {
				t.Fatal(got)
			}
		}
		p := url.Values{"csrf": {session.CSRF}, "roomid": {"21000"}, "msg": {"  直播测试 😀  "}}
		// Real HTTP send, not a direct invocation of the handler.
		req, err := http.NewRequest("POST", server.URL+"/msg/send", strings.NewReader(p.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result map[string]any
		if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		assertCode(t, result, 0)
		liveWant(t, result, float64(0), "data", "mode_info", "mode")
		messages, err := s.Store.ListLiveMessage(context.Background(), "WHERE room_id=? ORDER BY id DESC LIMIT 1", 21000)
		if err != nil || len(messages) != 1 {
			t.Fatalf("%v %v", messages, err)
		}
		u, err := s.Store.UserByID(context.Background(), session.MID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []*websocket.Conn{c1, c2} {
			v := chatJSON(t, chatRead(t, c, 5))
			liveWant(t, v, "DANMU_MSG", "cmd")
			liveWant(t, v, "直播测试 😀", "info", 1)
			liveWant(t, v, float64(u.MID), "info", 2, 0)
			liveWant(t, v, u.Uname, "info", 2, 1)
			liveWant(t, v, float64(1), "info", 0, 1)
			liveWant(t, v, float64(25), "info", 0, 2)
			liveWant(t, v, float64(0xffffff), "info", 0, 3)
			liveWant(t, v, float64(messages[0].TS*1000), "info", 0, 4)
			if len(liveField(t, v, "info", 0, 13).(map[string]any)) != 0 {
				t.Fatal("ordinary emoticon metadata")
			}
			liveWant(t, v, float64(u.MID), "info", 0, 15, "user", "uid")
			liveWant(t, v, u.Uname, "info", 0, 15, "user", "base", "name")
			liveWant(t, v, liveChatFace(server.URL, u.Face), "info", 0, 15, "user", "base", "face")
			extra := liveField(t, v, "info", 0, 15, "extra").(string)
			var parsed map[string]any
			if err = json.Unmarshal([]byte(extra), &parsed); err != nil {
				t.Fatal(err)
			}
			liveWant(t, parsed, liveStringID(messages[0].ID), "id_str")
			liveWant(t, parsed, "直播测试 😀", "content")
		}
		// Heartbeat markers establish that neither other-room messages, failed sends,
		// nor randomly appended history messages have leaked into the socket stream.
		chatWrite(t, other, encodeLivePacket(2, nil))
		chatRead(t, other, 3)
		assertCode(t, post(t, s, "/msg/send", url.Values{"csrf": {session.CSRF}, "roomid": {"21000"}, "msg": {" "}}, session), -400)
		for i := 0; i < 8; i++ {
			assertCode(t, get(t, s, "/xlive/web-room/v1/dM/gethistory?roomid=21000"), 0)
		}
		chatWrite(t, c1, encodeLivePacket(2, nil))
		chatRead(t, c1, 3)
		chatWrite(t, c2, encodeLivePacket(2, nil))
		chatRead(t, c2, 3)
		// SC payload has the exact shape of getMessageList.
		price := float64(30)
		sc := store.LiveMessage{ID: 999999, RoomID: 21000, UID: u.MID, Text: "SC", TS: time.Now().Unix(), Price: &price}
		if err = store.InsertLiveMessage(context.Background(), s.Store.DB, &sc); err != nil {
			t.Fatal(err)
		}
		if err = s.BroadcastLiveSuperChat(context.Background(), sc, "http://example.com"); err != nil {
			t.Fatal(err)
		}
		history := liveData(t, get(t, s, "/av/v1/SuperChat/getMessageList?room_id=21000"))
		var expected any
		for _, item := range liveField(t, history, "list").([]any) {
			if liveField(t, item, "id") == float64(sc.ID) {
				expected = item
			}
		}
		for _, c := range []*websocket.Conn{c1, c2} {
			v := chatJSON(t, chatRead(t, c, 5))
			liveWant(t, v, "SUPER_CHAT_MESSAGE", "cmd")
			liveWant(t, v, expected, "data")
		}
		s.BroadcastLiveSuperChatDelete(21000, []int64{sc.ID})
		for _, c := range []*websocket.Conn{c1, c2} {
			v := chatJSON(t, chatRead(t, c, 5))
			liveWant(t, v, "SUPER_CHAT_MESSAGE_DELETE", "cmd")
			liveWant(t, v, float64(sc.ID), "data", "ids", 0)
		}
	})
}

func TestLiveChatPreparingAndShutdown(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		server := chatHTTP(t, s)
		admin := login(t, s, "jumedia", "admin123")
		owner := login(t, s, "lumen", "demo123")
		for _, action := range []string{"ban", "stop"} {
			liveExec(t, s, "UPDATE rooms SET live_status=1,banned=0 WHERE room_id=?", 21000)
			a := chatConnect(t, s, server, 21000, nil)
			b := chatConnect(t, s, server, 21000, owner)
			if action == "ban" {
				assertCode(t, post(t, s, "/x/admin/live/room/ban", url.Values{"csrf": {admin.CSRF}, "room_id": {"21000"}, "banned": {"true"}}, admin), 0)
			} else {
				assertCode(t, post(t, s, "/room/v1/Room/stopLive", url.Values{"csrf": {owner.CSRF}}, owner), 0)
			}
			for _, c := range []*websocket.Conn{a, b} {
				v := chatJSON(t, chatRead(t, c, 5))
				liveWant(t, v, "PREPARING", "cmd")
				liveWant(t, v, float64(21000), "roomid")
				chatClosed(t, c)
			}
		}
		liveExec(t, s, "UPDATE rooms SET live_status=1,banned=0 WHERE room_id=?", 20000)
		authenticated := chatConnect(t, s, server, 20000, nil)
		pending := chatDial(t, server)
		s.CloseLiveChat()
		chatClosed(t, authenticated)
		chatClosed(t, pending)
		after := chatDial(t, server)
		chatClosed(t, after)
	})
}

func TestLiveChatRejectsInvalidInputAndTimeouts(t *testing.T) {
	s := newTestServer(t)
	s.liveChat.authTimeout = 80 * time.Millisecond
	s.liveChat.readTimeout = 120 * time.Millisecond
	server := chatHTTP(t, s)
	t.Run("no auth", func(t *testing.T) { c := chatDial(t, server); chatClosed(t, c) })
	t.Run("first heartbeat", func(t *testing.T) {
		c := chatDial(t, server)
		chatWrite(t, c, encodeLivePacket(2, nil))
		assertCode(t, chatJSON(t, chatRead(t, c, 8)), -101)
		chatClosed(t, c)
	})
	t.Run("idle", func(t *testing.T) { c := chatConnect(t, s, server, 21000, nil); chatClosed(t, c) })
	t.Run("heartbeat resets idle", func(t *testing.T) {
		c := chatConnect(t, s, server, 21000, nil)
		for i := 0; i < 5; i++ {
			time.Sleep(40 * time.Millisecond)
			chatWrite(t, c, encodeLivePacket(2, nil))
			chatRead(t, c, 3)
		}
	})
	for _, tt := range []struct {
		name string
		kind websocket.MessageType
		data []byte
	}{
		{"text", websocket.MessageText, []byte("hello")},
		{"oversize", websocket.MessageBinary, bytes.Repeat([]byte("a"), liveChatMaxMessage+1)},
		{"bad length", websocket.MessageBinary, []byte{0, 0, 0, 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := chatDial(t, server)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = c.Write(ctx, tt.kind, tt.data)
			chatClosed(t, c)
		})
	}
	t.Run("repeat auth", func(t *testing.T) {
		c := chatConnect(t, s, server, 21000, nil)
		chatWrite(t, c, chatAuthPacket(chatToken(t, s, 21000, nil), 21000, 0))
		chatClosed(t, c)
	})
	req := httptest.NewRequest("GET", "/sub", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusUpgradeRequired {
		t.Fatal("non-upgrade request", w.Code)
	}
	req = httptest.NewRequest("POST", "/sub", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code == http.StatusSwitchingProtocols {
		t.Fatal("POST upgraded")
	}
}

// A real WebSocket over a deliberately blocked server transport makes the
// queue-overflow test independent of OS socket-buffer sizes and machine speed.
type chatBlockedConn struct {
	net.Conn
	blocked              atomic.Bool
	entered              chan struct{}
	closed               chan struct{}
	enterOnce, closeOnce sync.Once
}

func (c *chatBlockedConn) Write(p []byte) (int, error) {
	if c.blocked.Load() {
		c.enterOnce.Do(func() { close(c.entered) })
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}
func (c *chatBlockedConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type chatBlockedListener struct {
	net.Listener
	accepted chan *chatBlockedConn
}

func (l *chatBlockedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &chatBlockedConn{Conn: c, entered: make(chan struct{}), closed: make(chan struct{})}
	l.accepted <- wrapped
	return wrapped, nil
}

func TestLiveChatSlowClient(t *testing.T) {
	s := newTestServer(t)
	s.liveChat.queueSize = 2
	server := httptest.NewUnstartedServer(s)
	listener := &chatBlockedListener{Listener: server.Listener, accepted: make(chan *chatBlockedConn, 1)}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	t.Cleanup(s.CloseLiveChat)
	c := chatConnect(t, s, server, 21000, nil)
	transport := <-listener.accepted
	transport.blocked.Store(true)
	packet := encodeLivePacket(5, []byte(`{"cmd":"test"}`))
	s.liveChat.broadcast(21000, packet, false)
	select {
	case <-transport.entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not block")
	}
	for i := 0; i < 3; i++ {
		s.liveChat.broadcast(21000, packet, false)
	}
	select {
	case <-transport.closed:
	case <-time.After(time.Second):
		t.Fatal("full queue did not disconnect slow client")
	}
	chatClosed(t, c)
}

func TestLiveChatDanmuInfoFailure(t *testing.T) {
	s := newTestServer(t)
	// Invalid persistent key fails closed; getDanmuInfo does not issue a weak token.
	if err := s.Store.SetSetting(context.Background(), liveChatSecretKey, "bad"); err != nil {
		t.Fatal(err)
	}
	assertCode(t, get(t, s, "/xlive/web-room/v1/index/getDanmuInfo?id=21000"), -500)
}

func TestLiveChatAuthAndHeartbeatInOneMessage(t *testing.T) {
	s := newTestServer(t)
	server := chatHTTP(t, s)
	c := chatDial(t, server)
	data := append(chatAuthPacket(chatToken(t, s, 21000, nil), 21000, 0), encodeLivePacket(2, nil)...)
	chatWrite(t, c, data)
	assertCode(t, chatJSON(t, chatRead(t, c, 8)), 0)
	if got := binary.BigEndian.Uint32(chatRead(t, c, 3).body); got != uint32(liveRoom(t, s, 21000).Online) {
		t.Fatal(got)
	}
}

func TestLiveChatSecretSurvivesSettingsReset(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		before := chatToken(t, s, 21000, nil)
		assertCode(t, post(t, s, "/x/admin/settings/save", url.Values{"csrf": {session.CSRF}, "reset": {"true"}}, session), 0)
		after := chatToken(t, s, 21000, nil)
		restarted := New(s.Store, s.Config)
		defer restarted.CloseLiveChat()
		if err := restarted.InitLiveChat(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, token := range []string{before, after} {
			if !restarted.liveChat.tokens.valid(token, 21000, 0, time.Now()) {
				t.Fatal("reset lost persisted signing secret")
			}
		}
	})
}

func TestLiveChatConcurrentAuthenticationAndBan(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		server := chatHTTP(t, s)
		admin := login(t, s, "jumedia", "admin123")
		token := chatToken(t, s, 21000, nil)
		for attempt := 0; attempt < 4; attempt++ {
			liveExec(t, s, "UPDATE rooms SET banned=0,live_status=1 WHERE room_id=?", 21000)
			var clients []*websocket.Conn
			for i := 0; i < 8; i++ {
				clients = append(clients, chatDial(t, server))
			}
			start := make(chan struct{})
			var writers sync.WaitGroup
			for _, c := range clients {
				writers.Add(1)
				go func(c *websocket.Conn) {
					defer writers.Done()
					<-start
					chatWrite(t, c, chatAuthPacket(token, 21000, 0))
				}(c)
			}
			close(start)
			assertCode(t, post(t, s, "/x/admin/live/room/ban", url.Values{"csrf": {admin.CSRF}, "room_id": {"21000"}, "banned": {"true"}}, admin), 0)
			writers.Wait()
			for _, c := range clients {
				auth := chatJSON(t, chatRead(t, c, 8))
				if auth["code"] == float64(0) {
					v := chatJSON(t, chatRead(t, c, 5))
					liveWant(t, v, "PREPARING", "cmd")
				} else {
					assertCode(t, auth, -101)
				}
				chatClosed(t, c)
			}
			s.liveChat.mu.Lock()
			count := len(s.liveChat.rooms[21000])
			s.liveChat.mu.Unlock()
			if count != 0 {
				t.Fatalf("%d subscriptions survived ban", count)
			}
		}
	})
}
