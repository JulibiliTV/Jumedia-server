package api

import (
	"context"
	"time"

	"jumedia-server/internal/store"
)

type publicationClockKey struct{}

func publicationNow(ctx context.Context) time.Time {
	if now, ok := ctx.Value(publicationClockKey{}).(func() time.Time); ok {
		return now()
	}
	return time.Now()
}

// Run before routing every request. No throttle: the first request in a new
// second observes all due archives, including space, search and ranking reads.
// An indexed read comes first so ordinary requests never take the write lock.
func (s *Server) promoteScheduled(ctx context.Context) error {
	now := publicationNow(ctx).Unix()
	var due int
	err := s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT 1 FROM archives WHERE state=-40 AND dtime<=? LIMIT 1) due", now).Scan(&due)
	if err != nil || due == 0 {
		return err
	}
	_, err = s.Store.DB.ExecContext(ctx, "UPDATE archives SET state=0,pubdate=dtime WHERE state=-40 AND dtime<=?", now)
	return err
}

type archivePublication struct {
	Dtime               int64
	NoDisturbance, NoTV int
}

func (s *Server) archivePublication(c *Ctx, aid int64) (archivePublication, error) {
	var p archivePublication
	err := s.Store.DB.QueryRowContext(c.Context(), "SELECT dtime,no_disturbance,no_tv FROM archives WHERE aid=?", aid).Scan(&p.Dtime, &p.NoDisturbance, &p.NoTV)
	return p, err
}

func (s *Server) publicationFilter(c *Ctx, list []store.Archive, tv bool) ([]store.Archive, error) {
	query := "SELECT aid FROM archives WHERE no_disturbance=1"
	if tv {
		query = "SELECT aid FROM archives WHERE no_tv=1"
	}
	rows, err := s.Store.DB.QueryContext(c.Context(), query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	excluded := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		excluded[id] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := []store.Archive{}
	for _, a := range list {
		if !excluded[a.AID] {
			out = append(out, a)
		}
	}
	return out, nil
}
