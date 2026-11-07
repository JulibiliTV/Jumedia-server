package api

import (
	"database/sql"
	"errors"
	"strings"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

func (s *Server) playbackMediaURL(c *Ctx, media string) (string, error) {
	id, item, source, mode, ok := emby.ParseDSRef(media)
	if !ok {
		return discoverAbsoluteMedia(s.Origin(c.R), media)
	}
	ds, err := store.DataSourceByID(c.Context(), s.Store.DB, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Fail(-404, "数据源不存在")
	}
	if err != nil {
		return "", err
	}
	if ds.ProxyPlayback {
		return discoverAbsoluteMedia(s.Origin(c.R), media)
	}
	// Direct playback grants the same media access as the proxy route.
	allowed, err := s.sourceMediaAllowed(c.Context(), id, item, source, mode)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", Fail(-404, "视频不存在")
	}
	client, err := s.sourceClient(c.Context(), ds)
	if err != nil {
		return "", err
	}
	container := "mp4"
	if strings.HasPrefix(mode, "raw-") {
		container = strings.TrimPrefix(mode, "raw-")
	}
	c.W.Header().Set("Cache-Control", "no-store")
	link, err := client.StreamURL(c.Context(), item, source, container)
	if err != nil {
		return "", Fail(-502, "无法获取数据源播放地址，请检查数据源连接")
	}
	return link, nil
}
