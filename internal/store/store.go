package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"jumedia-server/internal/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ DB *db.DB }

func New(d *db.DB) *Store { return &Store{DB: d} }
func Open(ctx context.Context, driver, dsn string) (*Store, error) {
	d, err := db.Open(ctx, driver, dsn)
	if err != nil {
		return nil, err
	}
	s := New(d)
	if err = s.Migrate(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Migrate(ctx context.Context) error {
	return s.DB.WithTx(ctx, func(tx *db.Tx) error {
		// Serialize concurrent startup before inspecting the migration version.
		if tx.Driver == "postgres" {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?)", int64(744115)); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_versions (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)"); err != nil {
			return err
		}
		if tx.Driver == "sqlite" {
			if _, err := tx.ExecContext(ctx, "UPDATE schema_versions SET version=version"); err != nil {
				return err
			}
		}
		entries, err := migrations.ReadDir("migrations")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
			if err != nil {
				return err
			}
			var count int
			if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_versions WHERE version=?", version).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if version == 9 {
				if err = migratePublication(ctx, tx); err != nil {
					return fmt.Errorf("migration 9 data: %w", err)
				}
			}
			script, err := migrations.ReadFile("migrations/" + entry.Name())
			if err != nil {
				return err
			}
			for _, q := range strings.Split(string(script), ";") {
				if strings.TrimSpace(q) == "" {
					continue
				}
				if _, err = tx.ExecContext(ctx, q); err != nil {
					return fmt.Errorf("migration %d: %w", version, err)
				}
			}
			if version == 8 {
				if err = ExtendFanMedals(ctx, tx, time.Now().Unix()); err != nil {
					return fmt.Errorf("migration 8 data: %w", err)
				}
			}
			if version == 7 {
				if err = ExtendBPlan(ctx, tx); err != nil {
					return fmt.Errorf("migration 7 data: %w", err)
				}
			}
			if version == 10 {
				if err = ExtendDemoLive(ctx, tx); err != nil {
					return fmt.Errorf("migration 10 data: %w", err)
				}
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO schema_versions(version,applied_at) VALUES (?,?)", version, time.Now().Unix()); err != nil {
				return err
			}
		}
		return nil
	})
}
func BoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func NextID(ctx context.Context, q db.Querier) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, "UPDATE ids SET value=value+1 WHERE name=? RETURNING value", "global").Scan(&id)
	return id, err
}
func (s *Store) NextID(ctx context.Context) (int64, error) { return NextID(ctx, s.DB) }
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE mid=?", id))
}
func (s *Store) UserByUsername(ctx context.Context, name string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE username=? OR email=? ORDER BY mid LIMIT 1", name, name))
}
func (s *Store) ArchiveByID(ctx context.Context, id int64) (*Archive, error) {
	a, err := scanArchive(s.DB.QueryRowContext(ctx, "SELECT "+archiveColumns+" FROM archives WHERE aid=?", id))
	if err != nil {
		return nil, err
	}
	a.Pages, err = s.ListPage(ctx, "WHERE aid=? ORDER BY position", id)
	return a, err
}
func (s *Store) ArchiveByBVID(ctx context.Context, bvid string) (*Archive, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, "SELECT aid FROM archives WHERE bvid=?", bvid).Scan(&id); err != nil {
		return nil, err
	}
	return s.ArchiveByID(ctx, id)
}
func (s *Store) PublishedArchives(ctx context.Context) ([]Archive, error) {
	as, err := s.ListArchive(ctx, "WHERE state=0 AND mid IN (SELECT mid FROM users WHERE status=0) ORDER BY aid")
	if err != nil {
		return nil, err
	}
	for i := range as {
		as[i].Pages, err = s.ListPage(ctx, "WHERE aid=? ORDER BY position", as[i].AID)
		if err != nil {
			return nil, err
		}
	}
	return as, nil
}
func (s *Store) CollectionByID(ctx context.Context, id int64) (*Collection, error) {
	c, err := scanCollection(s.DB.QueryRowContext(ctx, "SELECT "+collectionColumns+" FROM collections WHERE id=?", id))
	if err != nil {
		return nil, err
	}
	items, err := s.ListCollectionItem(ctx, "WHERE collection_id=? ORDER BY position", id)
	c.AIDs = []int64{}
	for _, it := range items {
		c.AIDs = append(c.AIDs, it.AID)
	}
	return c, err
}
func (s *Store) SeasonByID(ctx context.Context, id int64) (*Season, error) {
	v, err := scanSeason(s.DB.QueryRowContext(ctx, "SELECT "+seasonColumns+" FROM seasons WHERE season_id=?", id))
	if err != nil {
		return nil, err
	}
	v.Episodes, err = s.ListEpisode(ctx, "WHERE season_id=? ORDER BY position", id)
	return v, err
}
func (s *Store) EpisodeByID(ctx context.Context, id int64) (*Episode, error) {
	return scanEpisode(s.DB.QueryRowContext(ctx, "SELECT "+episodeColumns+" FROM episodes WHERE ep_id=?", id))
}
func (s *Store) RoomByID(ctx context.Context, id int64) (*LiveRoom, error) {
	return scanLiveRoom(s.DB.QueryRowContext(ctx, "SELECT "+liveRoomColumns+" FROM rooms WHERE room_id=?", id))
}
func (s *Store) SessionByToken(ctx context.Context, token string) (*Session, error) {
	return scanSession(s.DB.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE token=?", token))
}
func (s *Store) SessionByAccess(ctx context.Context, access string) (*Session, error) {
	return scanSession(s.DB.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE access=?", access))
}
func (s *Store) ByAccess(ctx context.Context, access string) (*Session, error) {
	return s.SessionByAccess(ctx, access)
}
func (s *Store) FollowerCount(ctx context.Context, id int64) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM relations WHERE fid=?", id).Scan(&n)
	return n + (id*9871)%50000, err
}
func (s *Store) FollowingCount(ctx context.Context, id int64) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM relations WHERE mid=?", id).Scan(&n)
	return n, err
}
func (s *Store) RegionName(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, "SELECT name FROM regions WHERE tid=?", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "其他", nil
	}
	return name, err
}
func (s *Store) Setting(ctx context.Context, key string) (any, error) {
	var raw string
	if err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&raw); err != nil {
		return nil, err
	}
	var v any
	err := json.Unmarshal([]byte(raw), &v)
	return v, err
}
func (s *Store) Settings(ctx context.Context) (map[string]any, error) {
	rows, err := s.ListSettingRecord(ctx, "ORDER BY key")
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, r := range rows {
		var v any
		if err = json.Unmarshal([]byte(r.Value), &v); err != nil {
			return nil, err
		}
		out[r.Key] = v
	}
	return out, nil
}
func SetSetting(ctx context.Context, q db.Querier, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(raw))
	return err
}
func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	return SetSetting(ctx, s.DB, key, value)
}

// AddLogTx appends and prunes audit records using the caller's transaction.
func AddLogTx(ctx context.Context, tx *db.Tx, mid int64, action, target string, at int64) error {
	id, err := NextID(ctx, tx)
	if err != nil {
		return err
	}
	if err = InsertLog(ctx, tx, &Log{ID: id, MID: mid, Action: action, Target: target, Ctime: at}); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM logs WHERE id NOT IN (SELECT id FROM logs ORDER BY id DESC LIMIT 500)")
	return err
}
func (s *Store) AddLog(ctx context.Context, mid int64, action, target string) error {
	return s.DB.WithTx(ctx, func(tx *db.Tx) error { return AddLogTx(ctx, tx, mid, action, target, time.Now().Unix()) })
}
func (s *Store) ReplyByID(ctx context.Context, id int64) (*Reply, error) {
	v, err := scanReply(s.DB.QueryRowContext(ctx, "SELECT "+replyColumns+" FROM replies WHERE rpid=?", id))
	if err != nil {
		return nil, err
	}
	rs, err := s.ListReplyReaction(ctx, "WHERE rpid=? ORDER BY mid", id)
	v.Likers = []int64{}
	v.Haters = []int64{}
	for _, r := range rs {
		if r.Reaction == 1 {
			v.Likers = append(v.Likers, r.MID)
		} else {
			v.Haters = append(v.Haters, r.MID)
		}
	}
	return v, err
}
func (s *Store) LiveAreas(ctx context.Context) ([]LiveArea, error) {
	as, err := s.ListLiveArea(ctx, "ORDER BY position")
	if err != nil {
		return nil, err
	}
	for i := range as {
		as[i].Children, err = s.ListLiveAreaChild(ctx, "WHERE parent_id=? ORDER BY position", as[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return as, nil
}

// Reset removes data in dependency order within the caller's transaction.
// Migration history is retained. Seed.Reset repopulates in the same transaction.
func Clear(ctx context.Context, q db.Querier) error {
	tables := []string{"fan_medals", "reservations", "reply_reactions", "replies", "live_messages", "collection_items", "archive_pages", "fav_items", "history", "history_paused", "toview", "bangumi_follow", "dm_config", "dm_filter", "danmaku", "seeded_danmaku", "relations", "likes", "dislikes", "coins", "banners", "sessions", "qrcodes", "logs", "rooms", "live_area_children", "live_areas", "collections", "fav_folders", "archives", "episodes", "seasons", "regions", "data_sources", "users", "settings"}
	for _, t := range tables {
		if _, err := q.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, "UPDATE ids SET value=? WHERE name=?", 0, "sessions"); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, "UPDATE ids SET value=? WHERE name=?", 1000, "global")
	return err
}

// NullableID translates the mock's zero sentinel to SQL NULL for optional FKs.
func NullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// SessionByCredentials matches the Mock's first session satisfying either token.
func (s *Store) SessionByCredentials(ctx context.Context, token, access string) (*Session, error) {
	return scanSession(s.DB.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE (? <> '' AND token=?) OR (? <> '' AND access=?) ORDER BY position LIMIT 1", token, token, access, access))
}
