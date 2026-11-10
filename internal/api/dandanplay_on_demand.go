package api

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
)

// Only existing data-source media can enqueue a provider request. Imported pages and
// episodes can have no sync-owned archive, so use their original media reference.
func (s *Server) requestDandanplay(ctx context.Context, cid int64) (bool, error) {
	var aid, originDS int64
	var archiveID, episodeID sql.NullInt64
	var media string
	var duration int
	err := s.Store.DB.QueryRowContext(ctx, `SELECT p.aid,a.origin_ds,a.aid,NULL,p.media,p.duration
		FROM archive_pages p JOIN archives a ON a.aid=p.aid WHERE p.cid=? AND a.state NOT IN (-100,-6)
		UNION ALL SELECT e.aid,0,NULL,e.ep_id,e.media,e.duration
		FROM episodes e JOIN seasons s ON s.season_id=e.season_id WHERE e.cid=? AND s.state<>-100 LIMIT 1`, cid, cid).
		Scan(&aid, &originDS, &archiveID, &episodeID, &media, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ds, _, _, _, ok := emby.ParseDSRef(media)
	if !ok || (originDS != 0 && originDS != ds) {
		return false, nil
	}
	var exists int
	if err = s.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM data_sources WHERE id=? AND type IN ('emby','jellyfin','fntv')", ds).Scan(&exists); err != nil || exists == 0 {
		return false, err
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil || !Truthy(settings["dandanplayEnabled"]) {
		return false, err
	}
	if (serviceString(settings["dandanplayAppId"]) == "") != (serviceString(settings[dandanplaySecretKey]) == "") {
		return false, nil
	}
	if err = s.InitDataSources(ctx); err != nil {
		return false, err
	}
	filename, err := dandanplayMediaQuery(ctx, s.Store.DB, cid)
	if err != nil {
		return false, err
	}
	var pending bool
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if err := LockTx(ctx, tx); err != nil {
			return err
		}
		now := time.Now().Unix()
		_, err := tx.ExecContext(ctx, `INSERT INTO dandanplay_sync(cid,aid,archive_aid,source_ep_id,media,filename,duration,requested_at)
			VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(cid) DO UPDATE SET aid=excluded.aid,archive_aid=excluded.archive_aid,
			source_ep_id=excluded.source_ep_id,media=excluded.media,filename=excluded.filename,duration=excluded.duration,
			status='pending',message='',requested_at=excluded.requested_at,
			attempted_at=CASE WHEN dandanplay_sync.media<>excluded.media THEN 0 ELSE dandanplay_sync.attempted_at END
			WHERE dandanplay_sync.media<>excluded.media OR (dandanplay_sync.filename<>excluded.filename
			AND dandanplay_sync.status NOT IN ('pending','fetching') AND dandanplay_sync.attempted_at<=?)`,
			cid, aid, archiveID, episodeID, media, filename, duration, now, now-int64(dandanplayRetryInterval/time.Second))
		if err != nil {
			return err
		}
		var status string
		var attempted int64
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT status,attempted_at,count FROM dandanplay_sync WHERE cid=?", cid).Scan(&status, &attempted, &count); err != nil {
			return err
		}
		if (status == "idle" || status == "error" || status == "unmatched" || status == "ready" && count == 0) && now-attempted >= int64(dandanplayRetryInterval/time.Second) {
			status = "pending"
		}
		pending = status == "pending" || status == "fetching"
		if status == "pending" {
			_, err = tx.ExecContext(ctx, "UPDATE dandanplay_sync SET status='pending',message='',requested_at=? WHERE cid=?", now, cid)
		}
		return err
	})
	return pending, err
}

const dandanplayCurrentMedia = `EXISTS (SELECT 1 FROM data_sources ds WHERE ds.type IN ('emby','jellyfin','fntv')
	AND dandanplay_sync.media LIKE 'embyds:' || CAST(ds.id AS TEXT) || ':%')
	AND (EXISTS (SELECT 1 FROM archive_pages p JOIN archives a ON a.aid=p.aid
	WHERE p.cid=dandanplay_sync.cid AND p.media=dandanplay_sync.media AND a.aid=dandanplay_sync.archive_aid AND a.state NOT IN (-100,-6))
	OR EXISTS (SELECT 1 FROM episodes e JOIN seasons s ON s.season_id=e.season_id
	WHERE e.ep_id=dandanplay_sync.source_ep_id AND e.cid=dandanplay_sync.cid AND e.media=dandanplay_sync.media AND s.state<>-100))`
