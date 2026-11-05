package store_test

import (
	"testing"

	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestDataSourcePlaybackMigration(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		for _, q := range []string{
			"INSERT INTO users(mid,username) VALUES (1,'fixture')",
			"INSERT INTO data_sources(id,mid,name,url,username,ctime) VALUES (1,1,'fixture','https://source.example.test','fixture',1)",
			"ALTER TABLE data_sources DROP COLUMN proxy_playback",
			"DELETE FROM schema_versions WHERE version=18",
		} {
			if _, err := s.DB.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		ds, err := store.DataSourceByID(ctx, s.DB, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !ds.ProxyPlayback {
			t.Fatal("existing source must default to proxy")
		}
		ds.ProxyPlayback = false
		if err := store.SaveDataSource(ctx, s.DB, ds); err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		ds, err = store.DataSourceByID(ctx, s.DB, 1)
		if err != nil || ds.ProxyPlayback {
			t.Fatal("migration reset direct playback", err)
		}
	})
}
