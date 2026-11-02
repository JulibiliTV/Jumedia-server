package seed_test

import (
	"golang.org/x/crypto/bcrypt"
	"jumedia-server/internal/db"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
	"os"
	"strings"
	"testing"
)

func demoPackage(t *testing.T) seed.DemoPackage {
	t.Helper()
	raw, err := os.ReadFile("testdata/demo.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := seed.ParsePackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestBaseStartupAndAdminRecovery(t *testing.T) {
	s := testutil.Open(t, "sqlite")
	ctx := t.Context()
	if err := seed.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	if catalog, err := seed.StoredCatalog(ctx, s); err != nil || len(catalog.Series)+len(catalog.Movies) != 0 {
		t.Fatal("empty startup catalog", err)
	}
	for _, table := range []string{"users", "archives", "seasons", "rooms", "logs", "reservations", "fan_medals", "fav_folders"} {
		var n int
		if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM regions").Scan(&n); err != nil || n != 67 {
		t.Fatal(n, err)
	}
	if err := seed.BootstrapAdmin(ctx, s, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"owner", "12345"}, {"owner", ""}, {"", "secret123"}} {
		if err := seed.BootstrapAdmin(ctx, s, pair[0], pair[1]); err == nil {
			t.Fatal("invalid bootstrap accepted")
		}
	}
	if err := seed.BootstrapAdmin(ctx, s, "owner", "secret123"); err != nil {
		t.Fatal(err)
	}
	owner, err := s.UserByUsername(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = seed.BootstrapAdmin(ctx, s, "replacement", "short"); err != nil {
		t.Fatal(err)
	}
	if err = seed.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := s.UserByUsername(ctx, "owner")
	if owner.Password != unchanged.Password || owner.Role != "admin" {
		t.Fatal("bootstrap changed administrator")
	}
	if err = s.SetSetting(ctx, "siteName", "keep"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, "DELETE FROM regions WHERE tid=?", 100101); err != nil {
		t.Fatal(err)
	}
	if err = seed.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	name, _ := s.Setting(ctx, "siteName")
	if name != "keep" {
		t.Fatal(name)
	}
	if _, err = s.DB.ExecContext(ctx, "UPDATE users SET role=? WHERE mid=?", "user", owner.MID); err != nil {
		t.Fatal(err)
	}
	if err = seed.Admin(ctx, s, "owner", "new-secret"); err != nil {
		t.Fatal(err)
	}
	owner, err = s.UserByUsername(ctx, "owner")
	if err != nil || owner.Role != "admin" || bcrypt.CompareHashAndPassword([]byte(owner.Password), []byte("new-secret")) != nil {
		t.Fatal("recovery failed", err)
	}
}

func TestDemoPackageImportAndReset(t *testing.T) {
	s := testutil.Open(t, "sqlite")
	ctx := t.Context()
	if err := seed.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := seed.Admin(ctx, s, "owner", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := seed.Admin(ctx, s, "second", "secret456"); err != nil {
		t.Fatal(err)
	}
	owner, _ := s.UserByUsername(ctx, "owner")
	before, _ := s.Settings(ctx)
	if err := s.SetSetting(ctx, "_live_chat_secret", "keep-signing"); err != nil {
		t.Fatal(err)
	}
	p := demoPackage(t)
	importDemo := func() error {
		return s.DB.WithTx(ctx, func(tx *db.Tx) error { return seed.ImportTx(ctx, tx, p, "test-ciphertext") })
	}
	if err := importDemo(); err != nil {
		t.Fatal(err)
	}
	users, err := s.ListUser(ctx, "")
	if err != nil || len(users) != 14 {
		t.Fatal(len(users), err)
	}
	for _, u := range users {
		if u.Role == "admin" {
			continue
		}
		if u.MID <= owner.MID || bcrypt.CompareHashAndPassword([]byte(u.Password), []byte("demo123")) != nil {
			t.Fatal(u.MID, u.Role)
		}
	}
	var officialDemo, reservations int64
	if err = s.DB.QueryRowContext(ctx, "SELECT MIN(mid) FROM users WHERE role=?", "user").Scan(&officialDemo); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM reservations WHERE mid=?", officialDemo).Scan(&reservations); err != nil || reservations != 1 {
		t.Fatal("official demo reservations", reservations, err)
	}
	after, _ := s.Settings(ctx)
	for k, v := range before {
		if after[k] != v {
			t.Fatal("import changed setting", k)
		}
	}
	if err = importDemo(); err != seed.ErrNotInitial {
		t.Fatal(err)
	}
	var cid int64
	if err = s.DB.QueryRowContext(ctx, "SELECT MIN(cid) FROM archive_pages").Scan(&cid); err != nil {
		t.Fatal(err)
	}
	if err = seed.SeedDanmaku(ctx, s, cid, 1200); err != nil {
		t.Fatal(err)
	}
	if err = seed.SeedDanmaku(ctx, s, cid, 1200); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM danmaku WHERE cid=?", cid).Scan(&n); err != nil || n != 46 {
		t.Fatal(n, err)
	}
	if _, err = s.DB.ExecContext(ctx, "UPDATE archive_pages SET media=? WHERE cid=?", "/upload/local.mp4", cid); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, "DELETE FROM danmaku"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, "DELETE FROM seeded_danmaku"); err != nil {
		t.Fatal(err)
	}
	if err = seed.SeedDanmaku(ctx, s, cid, 1200); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM danmaku").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = seed.Reset(ctx, s); err != nil {
		t.Fatal(err)
	}
	users, err = s.ListUser(ctx, "")
	if err != nil || len(users) != 2 {
		t.Fatal(len(users), err)
	}
	for _, table := range []string{"archives", "seasons", "rooms", "fan_medals", "reservations", "logs", "data_sources", "live_caches"} {
		if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	secret, _ := s.Setting(ctx, "_live_chat_secret")
	if secret != "keep-signing" {
		t.Fatal(secret)
	}
	settings, _ := s.Settings(ctx)
	for k := range settings {
		if strings.HasPrefix(k, "_demo_") {
			t.Fatal("retained demo secret")
		}
	}
	current, _ := s.UserByUsername(ctx, "owner")
	if *current != *owner {
		t.Fatal("administrator row changed")
	}
	if err = importDemo(); err != nil {
		t.Fatal("reimport", err)
	}
}
func TestDemoPackageValidationAndRollback(t *testing.T) {
	for _, raw := range []string{`{`, `{}`, `{"format":"jumedia-demo","version":9}`, `{"format":"jumedia-demo","version":1,"catalog":{"series":[],"movies":[]}}`} {
		if _, err := seed.ParsePackage([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	s := testutil.Open(t, "sqlite")
	ctx := t.Context()
	if err := seed.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, "CREATE TRIGGER fail_demo BEFORE INSERT ON archives BEGIN SELECT RAISE(ABORT, 'test rollback'); END"); err != nil {
		t.Fatal(err)
	}
	err := s.DB.WithTx(ctx, func(tx *db.Tx) error { return seed.ImportTx(ctx, tx, demoPackage(t), "test-ciphertext") })
	if err == nil {
		t.Fatal("expected failure")
	}
	var n int
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = store.InsertUser(ctx, s.DB, &store.User{MID: 42, Username: "ordinary", Role: "user"}); err != nil {
		t.Fatal(err)
	}
	err = s.DB.WithTx(ctx, func(tx *db.Tx) error { return seed.ImportTx(ctx, tx, demoPackage(t), "test-ciphertext") })
	if err != seed.ErrNotInitial {
		t.Fatal(err)
	}
}

func TestModerationResetPreservesAdminsWithoutTheirReportsOrBlocks(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := t.Context()
		if err := seed.Seed(ctx, s); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"first", "second"} {
			if err := seed.Admin(ctx, s, name, "fixture-password"); err != nil {
				t.Fatal(err)
			}
		}
		first, _ := s.UserByUsername(ctx, "first")
		second, _ := s.UserByUsername(ctx, "second")
		if _, err := s.DB.ExecContext(ctx, "INSERT INTO comment_reports(mid,rpid,reason,content,status,ctime) VALUES (?,?,?,'fixture','pending',1)", first.MID, 999, "7"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, "INSERT INTO user_blocks(mid,fid,ctime) VALUES (?,?,1)", first.MID, second.MID); err != nil {
			t.Fatal(err)
		}
		if err := seed.Reset(ctx, s); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"comment_reports", "user_blocks"} {
			var count int
			if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("reset retained %s: %d (%v)", table, count, err)
			}
		}
		users, err := s.ListUser(ctx, "")
		if err != nil || len(users) != 2 {
			t.Fatal("admins changed", err)
		}
	})
}
