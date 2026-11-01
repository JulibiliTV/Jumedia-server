package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type liveChatHub struct {
	s                                      *Server
	tokens                                 liveChatTokens
	mu                                     sync.Mutex
	clients                                map[*liveChatClient]struct{}
	rooms                                  map[int64]map[*liveChatClient]struct{}
	closed                                 bool
	wg                                     sync.WaitGroup
	authTimeout, readTimeout, writeTimeout time.Duration
	queueSize                              int
}

type liveChatOutput struct {
	packet []byte
	final  bool
}

type liveChatClient struct {
	hub        *liveChatHub
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	send       chan liveChatOutput
	writerDone chan struct{}
	roomID     int64 // protected by hub.mu
	closing    bool  // protected by hub.mu
}

func newLiveChatHub(s *Server) *liveChatHub {
	return &liveChatHub{s: s, clients: make(map[*liveChatClient]struct{}), rooms: make(map[int64]map[*liveChatClient]struct{}), authTimeout: 10 * time.Second, readTimeout: 70 * time.Second, writeTimeout: 5 * time.Second, queueSize: 64}
}

// CloseLiveChat also closes sockets still awaiting authentication. HTTP Shutdown
// does not own hijacked connections, so the process must call this explicitly.
func (s *Server) CloseLiveChat() {
	h := s.liveChat
	h.mu.Lock()
	h.closed = true
	for c := range h.clients {
		c.cancel()
	}
	h.mu.Unlock()
	h.wg.Wait()
}

func (h *liveChatHub) serve(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(liveChatMaxMessage)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &liveChatClient{hub: h, conn: conn, ctx: ctx, cancel: cancel, send: make(chan liveChatOutput, h.queueSize), writerDone: make(chan struct{})}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.clients[c] = struct{}{}
	h.wg.Add(1)
	h.mu.Unlock()
	defer func() {
		cancel()
		<-c.writerDone
		h.mu.Lock()
		h.removeRoom(c)
		delete(h.clients, c)
		h.mu.Unlock()
		_ = conn.CloseNow()
		h.wg.Done()
	}()
	go c.writeLoop()
	c.readLoop()
}

// All queue and membership operations run under mu. No network I/O holds it.
func (h *liveChatHub) enqueue(c *liveChatClient, output liveChatOutput) {
	if c.closing || c.ctx.Err() != nil {
		return
	}
	select {
	case c.send <- output:
		if output.final {
			c.closing = true
			h.removeRoom(c)
		}
	default:
		c.closing = true
		h.removeRoom(c)
		c.cancel() // cancels an in-flight write as well as the reader
	}
}

func (h *liveChatHub) removeRoom(c *liveChatClient) {
	if room := h.rooms[c.roomID]; room != nil {
		delete(room, c)
		if len(room) == 0 {
			delete(h.rooms, c.roomID)
		}
	}
}

func (h *liveChatHub) broadcast(roomID int64, packet []byte, final bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.rooms[roomID] {
		h.enqueue(c, liveChatOutput{packet, final})
	}
}

func (h *liveChatHub) preparing(roomID int64) {
	body, _ := json.Marshal(liveMap{"cmd": "PREPARING", "roomid": roomID})
	h.broadcast(roomID, encodeLivePacket(5, body), true)
}

func (c *liveChatClient) writeLoop() {
	defer close(c.writerDone)
	defer c.cancel()
	for {
		select {
		case <-c.ctx.Done():
			return
		case output := <-c.send:
			ctx, cancel := context.WithTimeout(c.ctx, c.hub.writeTimeout)
			err := c.conn.Write(ctx, websocket.MessageBinary, output.packet)
			cancel()
			if err != nil || output.final {
				return
			}
		}
	}
}

func (c *liveChatClient) authenticate(packet livePacket, deadline time.Time) bool {
	ctx, cancel := context.WithDeadline(c.ctx, deadline)
	defer cancel()
	var auth struct {
		UID    int64  `json:"uid"`
		RoomID int64  `json:"roomid"`
		Key    string `json:"key"`
	}
	h := c.hub
	valid := packet.operation == 7 && json.Unmarshal(packet.body, &auth) == nil
	if valid {
		valid = h.s.InitLiveChat(ctx) == nil && h.tokens.valid(auth.Key, auth.RoomID, auth.UID, time.Now())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Holding mu through the room read closes the subscribe-vs-stop/ban race:
	// a completed stop either precedes this read or removes this subscription.
	if valid && !h.closed && !c.closing && c.ctx.Err() == nil {
		room, err := h.s.Store.RoomByID(ctx, auth.RoomID)
		valid = err == nil && !room.Banned && room.LiveStatus == 1
	} else {
		valid = false
	}
	if !valid {
		h.enqueue(c, liveChatOutput{encodeLivePacket(8, []byte(`{"code":-101}`)), true})
		return false
	}
	c.roomID = auth.RoomID
	if h.rooms[c.roomID] == nil {
		h.rooms[c.roomID] = make(map[*liveChatClient]struct{})
	}
	h.rooms[c.roomID][c] = struct{}{}
	h.enqueue(c, liveChatOutput{packet: encodeLivePacket(8, []byte(`{"code":0}`))})
	return true
}

func (c *liveChatClient) readLoop() {
	deadline := time.Now().Add(c.hub.authTimeout)
	authenticated := false
	for {
		ctx, cancel := context.WithDeadline(c.ctx, deadline)
		kind, data, err := c.conn.Read(ctx)
		cancel()
		if err != nil || kind != websocket.MessageBinary {
			return
		}
		packets, err := decodeLivePackets(data)
		if err != nil {
			return
		}
		for _, packet := range packets {
			if !authenticated {
				if !c.authenticate(packet, deadline) {
					<-c.writerDone
					return
				}
				authenticated = true
			} else {
				if packet.operation != 2 {
					return
				}
				room, err := c.hub.s.Store.RoomByID(c.ctx, c.roomID)
				if err != nil {
					return
				}
				if room.Banned || room.LiveStatus != 1 {
					c.hub.preparing(c.roomID)
					<-c.writerDone
					return
				}
				body := make([]byte, 4)
				binary.BigEndian.PutUint32(body, uint32(room.Online))
				c.hub.mu.Lock()
				c.hub.enqueue(c, liveChatOutput{packet: encodeLivePacket(3, body)})
				c.hub.mu.Unlock()
			}
			deadline = time.Now().Add(c.hub.readTimeout)
		}
	}
}
