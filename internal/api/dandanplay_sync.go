package api

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"jumedia-server/internal/dandanplay"
	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

const dandanplayRetryInterval = 30 * time.Minute

func queueDandanplay(ctx context.Context, tx *db.Tx, p store.Page, filename string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO dandanplay_sync(cid,aid,archive_aid,media,filename,duration) VALUES (?,?,?,?,?,?)
		ON CONFLICT(cid) DO UPDATE SET media=excluded.media,filename=excluded.filename,duration=excluded.duration,status='pending',message='',
		attempted_at=CASE WHEN dandanplay_sync.media<>excluded.media THEN 0 ELSE dandanplay_sync.attempted_at END
		WHERE dandanplay_sync.media<>excluded.media OR (dandanplay_sync.status NOT IN ('pending','fetching') AND dandanplay_sync.attempted_at<=?)`,
		p.CID, p.AID, p.AID, p.Media, filename, p.Duration, time.Now().Add(-dandanplayRetryInterval).Unix())
	return err
}

func pruneDandanplay(ctx context.Context, tx *db.Tx, aid int64) error {
	var removed int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(count),0) FROM dandanplay_sync WHERE archive_aid=? AND cid NOT IN (SELECT cid FROM archive_pages WHERE aid=?)", aid, aid).Scan(&removed); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM dandanplay_sync WHERE archive_aid=? AND cid NOT IN (SELECT cid FROM archive_pages WHERE aid=?)", aid, aid); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE archives SET stat_danmaku=CASE WHEN stat_danmaku>=? THEN stat_danmaku-? ELSE 0 END WHERE aid=?", removed, removed, aid)
	return err
}

// The durable queue runs independently of media synchronization, one request at a time.
func (s *Server) runDandanplay(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.syncNextDandanplay(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Warn("danmaku background sync failed", "error", err)
			}
		}
	}
}

func (s *Server) syncNextDandanplay(ctx context.Context) error {
	settings, err := s.Store.Settings(ctx)
	if err != nil || !Truthy(settings["dandanplayEnabled"]) {
		return err
	}
	secret, err := s.decryptPassword(serviceString(settings[dandanplaySecretKey]))
	if err != nil {
		return err
	}
	if (secret == "") != (serviceString(settings["dandanplayAppId"]) == "") {
		return nil
	}
	var job *store.DandanplaySync
	err = s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if e := LockTx(ctx, tx); e != nil {
			return e
		}
		var e error
		job, e = store.ScanDandanplaySync(tx.QueryRowContext(ctx, "SELECT "+store.DandanplaySyncColumns+" FROM dandanplay_sync WHERE status='pending' AND "+dandanplayCurrentMedia+" ORDER BY requested_at DESC,cid LIMIT 1"))
		if errors.Is(e, sql.ErrNoRows) {
			job = nil
			return nil
		}
		if e != nil {
			return e
		}
		job.Filename, e = dandanplayMediaQuery(ctx, tx, job.CID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE dandanplay_sync SET filename=?,status='fetching',attempted_at=? WHERE cid=?", job.Filename, time.Now().Unix(), job.CID)
		return e
	})
	if err != nil || job == nil {
		return err
	}
	client := dandanplay.New(serviceString(settings["dandanplayApi"]), serviceString(settings["dandanplayAppId"]), secret)
	match, fetchErr := client.Match(ctx, job.Filename, job.Duration)
	var comments []store.Danmaku
	if fetchErr == nil && match != nil {
		comments, fetchErr = client.Comments(ctx, match.EpisodeID)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if err := LockTx(ctx, tx); err != nil {
			return err
		}
		var oldCount int
		err := tx.QueryRowContext(ctx, "SELECT count FROM dandanplay_sync WHERE cid=? AND media=? AND filename=? AND status='fetching' AND "+dandanplayCurrentMedia, job.CID, job.Media, job.Filename).Scan(&oldCount)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if fetchErr != nil {
			// A failed refresh is not a new cached result. Restore the last normal
			// outcome while retaining attempted_at for the request throttle.
			status, message := "idle", ""
			if job.EpisodeID > 0 || oldCount > 0 {
				status = "ready"
			} else if job.UpdatedAt > 0 {
				status, message = "unmatched", "没有唯一匹配结果"
			}
			_, err = tx.ExecContext(ctx, "UPDATE dandanplay_sync SET status=?,message=? WHERE cid=?", status, message, job.CID)
			return err
		}
		if match == nil {
			if oldCount > 0 {
				_, err = tx.ExecContext(ctx, "UPDATE dandanplay_sync SET status='ready',message='' WHERE cid=?", job.CID)
			} else {
				_, err = tx.ExecContext(ctx, "UPDATE dandanplay_sync SET status='unmatched',message='没有唯一匹配结果',episode_id=0,anime_title='',episode_title='',updated_at=? WHERE cid=?", time.Now().Unix(), job.CID)
			}
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM dandanplay_comments WHERE cid=?", job.CID); err != nil {
			return err
		}
		for _, d := range comments {
			id, e := store.NextID(ctx, tx)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, "INSERT INTO dandanplay_comments(id,cid,progress,mode,fontsize,color,content,ctime,weight) VALUES (?,?,?,?,?,?,?,?,?)", id, job.CID, d.Progress, d.Mode, d.Fontsize, d.Color, d.Content, d.Ctime, d.Weight)
			if e != nil {
				return e
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE dandanplay_sync SET status='ready',message='',episode_id=?,anime_title=?,episode_title=?,count=?,updated_at=? WHERE cid=?", match.EpisodeID, match.AnimeTitle, match.EpisodeTitle, len(comments), time.Now().Unix(), job.CID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE archives SET stat_danmaku=stat_danmaku+? WHERE aid=(SELECT archive_aid FROM dandanplay_sync WHERE cid=?)", len(comments)-oldCount, job.CID)
		return err
	})
}

func (s *Server) adminArchiveDanmaku(c *Ctx) (any, error) {
	aid := ParseID(c.P["aid"])
	pages, err := s.Store.ListPage(c.Context(), "WHERE aid=? ORDER BY position", aid)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, Fail(-404, "稿件不存在")
	}
	cid := ParseID(c.P["cid"])
	if cid == 0 {
		cid = pages[0].CID
	}
	matched := false
	pageInfo := []map[string]any{}
	var selected *store.DandanplaySync
	for _, p := range pages {
		job, e := store.ScanDandanplaySync(s.Store.DB.QueryRowContext(c.Context(), "SELECT "+store.DandanplaySyncColumns+" FROM dandanplay_sync WHERE cid=?", p.CID))
		if errors.Is(e, sql.ErrNoRows) {
			job = &store.DandanplaySync{CID: p.CID, AID: aid, Status: "idle"}
		} else if e != nil {
			return nil, e
		}
		pageInfo = append(pageInfo, map[string]any{"cid": p.CID, "part": p.Part, "sync": job})
		if p.CID == cid {
			matched, selected = true, job
		}
	}
	if !matched {
		return nil, Fail(-404, "分集不存在")
	}
	items, err := s.Store.DandanplayComments(c.Context(), cid)
	if err != nil {
		return nil, err
	}
	list, page := PageResult(items, c, 20)
	return map[string]any{"pages": pageInfo, "sync": selected, "list": list, "page": page}, nil
}
