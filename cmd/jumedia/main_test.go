package main

import (
	"golang.org/x/crypto/bcrypt"
	"jumedia-server/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestAdminAndResetCommands(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "app.db")
	t.Setenv("JUMEDIA_DB_DRIVER", "sqlite")
	t.Setenv("JUMEDIA_DB_DSN", dsn)
	t.Setenv("JUMEDIA_DATA_DIR", dir)
	t.Setenv("JUMEDIA_ADMIN_USERNAME", "bootstrap")
	t.Setenv("JUMEDIA_ADMIN_PASSWORD", "bootstrap123")
	previous := os.Args
	t.Cleanup(func() { os.Args = previous })
	command := func(args ...string) error { os.Args = append([]string{"jumedia"}, args...); return run() }
	if err := command("admin", "owner", "first123"); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), "sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(t.Context(), "UPDATE users SET role=? WHERE username=?", "user", "owner"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = command("admin", "owner", "second123"); err != nil {
		t.Fatal(err)
	}
	if err = command("demo", "import", "../../internal/seed/testdata/demo.json"); err != nil {
		t.Fatal(err)
	}
	if err = command("reset"); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(t.Context(), "sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, err := s.UserByUsername(t.Context(), "owner")
	if err != nil || u.Role != "admin" || bcrypt.CompareHashAndPassword([]byte(u.Password), []byte("second123")) != nil {
		t.Fatal("admin recovery", err)
	}
	var n int
	if err = s.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM archives").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = s.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM users").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
