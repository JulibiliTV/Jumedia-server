package api

import (
	"context"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

// LockTx serializes read/check/write operations across server instances on both
// SQL engines using the existing global sequence row.
func LockTx(ctx context.Context, tx *db.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE ids SET value=value WHERE name=?", "global")
	return err
}
func (s *Server) WriteTx(c *Ctx, write func(*db.Tx) error) error {
	return s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		if err := LockTx(c.Context(), tx); err != nil {
			return err
		}
		return write(tx)
	})
}

// Count executes a fixed SQL query; request values must be bound arguments.
func Count(c *Ctx, q db.Querier, query string, args ...any) (int64, error) {
	var n int64
	err := q.QueryRowContext(c.Context(), query, args...).Scan(&n)
	return n, err
}

// LogTx joins an existing business transaction. An explicit actor also supports
// login/registration before c.Me is set. at preserves an injected event clock.
func (s *Server) LogTx(ctx context.Context, tx *db.Tx, mid int64, action, target string, at ...int64) error {
	now := time.Now().Unix()
	if len(at) > 0 {
		now = at[0]
	}
	return store.AddLogTx(ctx, tx, mid, action, target, now)
}

// RequireReference checks a fixed COUNT query before inserting a foreign key.
// It must use the transaction that will perform the write.
func RequireReference(c *Ctx, q db.Querier, query string, id any, code int, message string) error {
	n, err := Count(c, q, query, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return Fail(code, message)
	}
	return nil
}
