package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func liveChatFace(origin, face string) string {
	if face == "" {
		return ""
	}
	base, err := url.Parse(strings.TrimRight(origin, "/") + "/")
	if err != nil {
		return ""
	}
	ref, err := url.Parse(face)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

func liveChatDanmu(message *store.LiveMessage, user *store.User, origin string, medal liveMap, legacy []any) []byte {
	extra, _ := json.Marshal(liveMap{"id_str": liveStringID(message.ID), "content": message.Text})
	meta := make([]any, 16)
	for i := range meta {
		meta[i] = 0
	}
	meta[1], meta[2], meta[3], meta[4] = 1, 25, 0xffffff, message.TS*1000
	meta[13] = liveMap{}
	meta[15] = liveMap{"user": liveMap{"uid": user.MID, "base": liveMap{"name": user.Uname, "face": liveChatFace(origin, render.Face(user.MID, user.Face))}, "medal": medal}, "extra": string(extra)}
	body, _ := json.Marshal(liveMap{"cmd": "DANMU_MSG", "info": []any{meta, message.Text, []any{user.MID, user.Uname, 0, 0, 0, 10000, 1, ""}, legacy, []any{0, 0, 0, ">50000", 0}}})
	return encodeLivePacket(5, body)
}

func liveChatSuperChat(message store.LiveMessage, name, face string) liveMap {
	price := float64(0)
	if message.Price != nil {
		price = *message.Price
	}
	return liveMap{"id": message.ID, "uid": message.UID, "message": message.Text, "price": price, "background_bottom_color": "#2A60B2", "background_color": "#EDF5FF", "start_time": message.TS, "end_time": float64(message.TS) + 120 + price*4, "time": 120, "user_info": liveMap{"uname": name, "face": face}}
}

// BroadcastLiveSuperChat broadcasts an already committed SC using the same
// fields as getMessageList. This task adds no SC purchase/write endpoint.
func (s *Server) BroadcastLiveSuperChat(ctx context.Context, message store.LiveMessage, origin string) error {
	user, err := s.Store.UserByID(ctx, message.UID)
	if err != nil {
		return err
	}
	body, err := json.Marshal(liveMap{"cmd": "SUPER_CHAT_MESSAGE", "data": liveChatSuperChat(message, user.Uname, liveChatFace(origin, render.Face(user.MID, user.Face)))})
	if err != nil {
		return err
	}
	s.liveChat.broadcast(message.RoomID, encodeLivePacket(5, body), false)
	return nil
}

// BroadcastLiveSuperChatDelete is the post-commit hook for future SC deletion.
func (s *Server) BroadcastLiveSuperChatDelete(roomID int64, ids []int64) {
	if ids == nil {
		ids = []int64{}
	}
	body, _ := json.Marshal(liveMap{"cmd": "SUPER_CHAT_MESSAGE_DELETE", "data": liveMap{"ids": ids}})
	s.liveChat.broadcast(roomID, encodeLivePacket(5, body), false)
}
