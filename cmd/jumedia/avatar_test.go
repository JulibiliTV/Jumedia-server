package main

import (
	"os"
	"path/filepath"
	"testing"

	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
)

func TestAdminCommandDefaultAvatar(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "app.db")
	t.Setenv("JUMEDIA_DB_DRIVER", "sqlite")
	t.Setenv("JUMEDIA_DB_DSN", dsn)
	t.Setenv("JUMEDIA_DATA_DIR", dir)
	before := os.Args
	t.Cleanup(func() { os.Args = before })
	os.Args = []string{"jumedia", "admin", "owner", "secret123"}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		s, err := store.Open(t.Context(), "sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		u, err := s.UserByUsername(t.Context(), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if want == "" {
			want = seed.Avatar(u.MID)
		}
		if u.Face != want {
			t.Fatalf("face = %q, want %q", u.Face, want)
		}
	}
	check("")
	for _, face := range []string{"", "/upload/custom.png"} {
		s, err := store.Open(t.Context(), "sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.DB.ExecContext(t.Context(), "UPDATE users SET role=?,face=? WHERE username=?", "user", face, "owner")
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err = run(); err != nil {
			t.Fatal(err)
		}
		check(face)
	}
}
