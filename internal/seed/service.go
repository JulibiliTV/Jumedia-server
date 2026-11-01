package seed

import (
	"context"
	"encoding/json"

	"jumedia-server/internal/db"
)

// Match extend15: seed may run after migration, or with preexisting settings.
func seedServiceSettings(ctx context.Context, q db.Querier) error {
	for key, value := range map[string]any{
		"serviceApi": "", "serviceLive": "", "serviceApp": "", "servicePassport": "", "serviceCdns": "[]", "serviceMtime": 0,
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO NOTHING", key, string(raw)); err != nil {
			return err
		}
	}
	return nil
}
