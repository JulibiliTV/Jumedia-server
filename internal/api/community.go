package api

import (
	"database/sql"
	"errors"

	"jumedia-server/internal/store"
)

func (s *Server) registerCommunity() {
	s.handle("/x/v2/history/delete", s.communityHistoryDelete)
	for path, h := range map[string]Handler{
		"/x/v2/reply":                       s.communityReplies,
		"/x/v2/reply/reply":                 s.communityReplyChildren,
		"/x/v2/reply/add":                   s.communityReplyAdd,
		"/x/v2/reply/del":                   s.communityReplyDelete,
		"/x/v2/reply/action":                s.communityReplyReact(1),
		"/x/v2/reply/hate":                  s.communityReplyReact(2),
		"/x/v2/reply/report":                s.communityReport,
		"/x/relation/blacks":               s.communityBlocks,
		"/x/v2/dm/list/seg.so":              s.communityDMSegment,
		"/x/v2/dm/web/view":                 s.communityDMView,
		"/x/v2/dm/web/config":               s.communityDMConfig,
		"/x/dm/filter/user":                 s.communityDMFilter,
		"/x/v2/dm/post":                     s.communityDMPost,
		"/x/click-interface/web/heartbeat":  s.communityHeartbeat,
		"/x/web-interface/history/cursor":   s.communityHistory,
		"/x/v2/history/shadow":              s.communityShadow,
		"/x/v2/history/shadow/set":          s.communityShadowSet,
		"/x/v2/history/clear":               s.communityHistoryClear,
		"/x/v2/history/toview":              s.communityToView,
		"/x/v2/history/toview/web":          s.communityToView,
		"/x/v2/history/toview/add":          s.communityToViewAdd,
		"/x/v2/history/toview/v2/dels":      s.communityToViewDelete,
		"/x/v3/fav/folder/created/list-all": s.communityFolders,
		"/x/v3/fav/resource/list":           s.communityResources,
		"/x/v3/fav/folder/add":              s.communityFolderAdd,
		"/x/v3/fav/folder/edit":             s.communityFolderEdit,
		"/x/v3/fav/folder/del":              s.communityFolderDelete,
	} {
		s.handle(path, h)
	}
}

func (s *Server) communityArchive(c *Ctx) (*store.Archive, error) {
	value := ""
	for _, key := range []string{"aid", "oid", "rid", "avid"} {
		if v, ok := c.P[key]; ok {
			value = v
			break
		}
	}
	a, err := s.Store.ArchiveByID(c.Context(), ParseID(value))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "啥都木有")
	}
	if err != nil {
		return nil, err
	}
	if a.State != 0 && a.MID != ActorID(c) && (c.Me == nil || c.Me.Role != "admin") {
		return nil, Fail(-404, "啥都木有")
	}
	return a, nil
}
