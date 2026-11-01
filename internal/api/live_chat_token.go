package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const liveChatSecretKey = "_live_chat_secret"

type liveChatTokens struct {
	mu     sync.Mutex
	secret []byte
}

// InitLiveChat creates the signing secret before listening. Lazy calls also
// support embedded Servers. ON CONFLICT preserves the first process's secret.
func (s *Server) InitLiveChat(ctx context.Context) error {
	return s.liveChat.tokens.init(ctx, s)
}

func (t *liveChatTokens) init(ctx context.Context, s *Server) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.secret) != 0 {
		return nil
	}
	candidate := make([]byte, 32)
	if _, err := rand.Read(candidate); err != nil {
		return err
	}
	raw, _ := json.Marshal(hex.EncodeToString(candidate))
	if _, err := s.Store.DB.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO NOTHING", liveChatSecretKey, string(raw)); err != nil {
		return err
	}
	value, err := s.Store.Setting(ctx, liveChatSecretKey)
	if err != nil {
		return err
	}
	encoded, ok := value.(string)
	secret, err := hex.DecodeString(encoded)
	if !ok || err != nil || len(secret) != 32 {
		return errors.New("invalid live chat signing secret")
	}
	t.secret = secret
	return nil
}

func (t *liveChatTokens) issue(roomID, uid int64, expires time.Time) string {
	payload := fmt.Sprintf("%d:%d:%d", roomID, uid, expires.Unix())
	t.mu.Lock()
	defer t.mu.Unlock()
	mac := hmac.New(sha256.New, t.secret)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + ":" + hex.EncodeToString(mac.Sum(nil))))
}

func (t *liveChatTokens) valid(token string, roomID, uid int64, now time.Time) bool {
	if len(token) > 512 || roomID <= 0 || uid < 0 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	fields := strings.Split(string(raw), ":")
	if len(fields) != 4 {
		return false
	}
	rid, e1 := strconv.ParseInt(fields[0], 10, 64)
	user, e2 := strconv.ParseInt(fields[1], 10, 64)
	expiry, e3 := strconv.ParseInt(fields[2], 10, 64)
	signature, e4 := hex.DecodeString(fields[3])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || rid != roomID || user != uid || expiry <= now.Unix() {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.secret) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, t.secret)
	_, _ = mac.Write([]byte(strings.Join(fields[:3], ":")))
	return hmac.Equal(signature, mac.Sum(nil))
}

func (l *liveAPI) danmu(c *Ctx) (any, error) {
	if err := l.s.InitLiveChat(c.Context()); err != nil {
		return nil, err
	}
	origin, err := url.Parse(l.s.Origin(c.R))
	if err != nil {
		return nil, err
	}
	port := 80
	if origin.Scheme == "https" {
		port = 443
	}
	if origin.Port() != "" {
		port, err = strconv.Atoi(origin.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid public origin port")
		}
	}
	wssPort := 443
	if origin.Scheme == "https" {
		wssPort = port
	}
	var uid int64
	if c.Me != nil {
		uid = c.Me.MID
	}
	rid := ParseUnicodeID(c.P["id"])
	token := l.s.liveChat.tokens.issue(rid, uid, l.now().Add(time.Hour))
	return liveMap{"group": "live", "business_id": 0, "refresh_row_factor": 0.125, "max_delay": 5000, "token": token, "host_list": []liveMap{{"host": origin.Hostname(), "port": port, "ws_port": port, "wss_port": wssPort}}, "room_id": NumberUnicodeGoFloat(c.P["id"], 0)}, nil
}
