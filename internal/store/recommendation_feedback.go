package store

import (
	"context"
	"time"
)

// SetRecommendationFeedback keeps video and author preferences independent.
func (s *Store) SetRecommendationFeedback(ctx context.Context, mid int64, reason int, target int64, cancel bool) error {
	if cancel {
		_, err := s.DB.ExecContext(ctx, "DELETE FROM recommendation_feedback WHERE mid=? AND reason_id=? AND target_id=?", mid, reason, target)
		return err
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO recommendation_feedback(mid,reason_id,target_id,ctime) VALUES (?,?,?,?) ON CONFLICT(mid,reason_id,target_id) DO NOTHING", mid, reason, target, time.Now().Unix())
	return err
}

// FilterRecommendations applies only to the current user's home feed candidates.
func (s *Store) FilterRecommendations(ctx context.Context, mid int64, list []Archive) ([]Archive, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT reason_id,target_id FROM recommendation_feedback WHERE mid=?", mid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	videos, authors := map[int64]bool{}, map[int64]bool{}
	for rows.Next() {
		var reason int
		var target int64
		if err = rows.Scan(&reason, &target); err != nil {
			return nil, err
		}
		if reason == 1 {
			videos[target] = true
		} else if reason == 4 {
			authors[target] = true
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := make([]Archive, 0, len(list))
	for _, a := range list {
		if !videos[a.AID] && !authors[a.MID] {
			result = append(result, a)
		}
	}
	return result, nil
}
