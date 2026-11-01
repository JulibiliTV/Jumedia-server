package seed

import (
	"context"
	"jumedia-server/internal/db"
)

// Match mock v17 SERVER_KEYS, preserving every existing supported override.
func seedServerSettings(ctx context.Context, q db.Querier) error {
	for _, key := range []string{"serverPublicOrigin", "serverFfmpeg", "serverFfprobe", "serverVideoEncoder"} {
		if _, err := q.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO NOTHING", key, `""`); err != nil {
			return err
		}
	}
	return nil
}
