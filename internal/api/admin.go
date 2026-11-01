package api

import (
	"encoding/json"

	"strings"

	"jumedia-server/internal/db"
)

func (s *Server) registerAdmin() {
	s.handle("/x/jumedia/service", s.jumediaService)
	routes := map[string]Handler{
		"dashboard": s.adminDashboard, "users": s.adminUsers, "user/update": s.adminUserUpdate,
		"archives": s.adminArchives, "archive/audit": s.adminArchiveAudit, "archive/delete": s.adminArchiveDelete,
		"seasons": s.adminSeasons, "season/save": s.adminSeasonSave, "season/delete": s.adminSeasonDelete,
		"live/rooms": s.adminRooms, "live/room/ban": s.adminRoomBan,
		"replies": s.adminReplies, "reply/delete": s.adminReplyDelete, "danmaku": s.adminDanmaku, "danmaku/delete": s.adminDanmakuDelete,
		"regions": s.adminRegions, "region/save": s.adminRegionSave, "region/sort": s.adminRegionSort, "region/delete": s.adminRegionDelete,
		"live/areas": s.adminAreas, "live/area/save": s.adminAreaSave,
		"banners": s.adminBanners, "banner/save": s.adminBannerSave, "banner/delete": s.adminBannerDelete,
		"server": s.adminServer, "settings": s.adminSettings, "settings/save": s.adminSettingsSave, "logs": s.adminLogs,
	}
	for path, handler := range routes {
		s.handle("/x/admin/"+path, func(c *Ctx) (any, error) {
			if _, err := c.Admin(); err != nil {
				return nil, err
			}
			return handler(c)
		})
	}
}

func adminKey(v string) any { return DBID(NumberUnicodeGo(v)) }

func adminMatch(text, keyword string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(keyword))
}
func adminDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func adminObject(v any) map[string]any {
	// Only database models with JSON-safe primitive fields reach this helper.
	raw, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// Business changes and audit logs commit or roll back together.
func (s *Server) adminWrite(c *Ctx, action, target string, write func(*db.Tx) error) (any, error) {
	return nil, s.WriteTx(c, func(tx *db.Tx) error {
		if err := write(tx); err != nil {
			return err
		}
		return s.LogTx(c.Context(), tx, ActorID(c), action, target)
	})
}
