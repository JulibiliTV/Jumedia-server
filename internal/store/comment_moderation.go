package store

import "context"

// Apply before pagination and before rendering child previews. Reported content
// remains hidden for its reporter even if an administrator dismisses the report.
func (s *Store) VisibleReplies(ctx context.Context, viewer int64, replies []Reply) ([]Reply, error) {

	if viewer == 0 {
		return replies, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.rpid FROM replies r WHERE
		EXISTS (SELECT 1 FROM user_blocks b WHERE b.mid=? AND b.fid=r.mid)
		OR EXISTS (SELECT 1 FROM comment_reports p WHERE p.mid=? AND p.rpid=r.rpid)`, viewer, viewer)
	if err != nil {
		return nil, err
	}
	hidden := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		hidden[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	visible := make([]Reply, 0, len(replies))
	for _, r := range replies {
		if !hidden[r.RPID] && !hidden[r.Root] {
			visible = append(visible, r)
		}
	}
	return visible, nil
}
