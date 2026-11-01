package seed_test

import (
	"math"
	"testing"

	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestAvatarSelection(t *testing.T) {
	for _, tc := range []struct {
		mid  int64
		want string
	}{
		{0, "default1.png"}, {1, "default2.png"}, {7, "default8.png"}, {8, "default1.png"}, {-1, "default2.png"}, {-9, "default2.png"}, {math.MinInt64, "default1.png"},
	} {
		if got := seed.Avatar(tc.mid); got != "/mock/avatars/"+tc.want {
			t.Errorf("Avatar(%d) = %q", tc.mid, got)
		}
	}
}

func TestDefaultAvatarsStartupAndReset(t *testing.T) {
	s := testutil.Open(t, "sqlite")
	ctx := t.Context()
	// Model an older nullable face column without changing production migrations.
	for _, q := range []string{"ALTER TABLE users RENAME COLUMN face TO legacy_face", "ALTER TABLE users ADD COLUMN face TEXT"} {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []store.User{
		{MID: 1, Username: "empty-admin", Role: "admin"},
		{MID: 2, Username: "null-user", Role: "user"},
		{MID: 3, Username: "custom-admin", Role: "admin", Face: "/upload/custom.png"},
		{MID: 4, Username: "demo-user", Role: "user", Face: "/mock/avatars/u1.png"},
	} {
		if err := store.InsertUser(ctx, s.DB, &u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET face=NULL WHERE mid=?", 2); err != nil {
		t.Fatal(err)
	}
	check := func(mid int64, want string) {
		t.Helper()
		u, err := s.UserByID(ctx, mid)
		if err != nil {
			t.Fatal(err)
		}
		if u.Face != want {
			t.Errorf("mid %d face = %q, want %q", mid, u.Face, want)
		}
	}
	for range 2 {
		if err := seed.Seed(ctx, s); err != nil {
			t.Fatal(err)
		}
		check(1, seed.Avatar(1))
		check(2, seed.Avatar(2))
		check(3, "/upload/custom.png")
		check(4, "/mock/avatars/u1.png")
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE users SET face=NULL WHERE mid=?", 1); err != nil {
		t.Fatal(err)
	}
	if err := seed.Reset(ctx, s); err != nil {
		t.Fatal(err)
	}
	check(1, seed.Avatar(1))
	check(3, "/upload/custom.png")
}

func TestDefaultAvatarsAdmin(t *testing.T) {
	s := testutil.Open(t, "sqlite")
	ctx := t.Context()
	if err := seed.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := seed.BootstrapAdmin(ctx, s, "bootstrap", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := seed.Admin(ctx, s, "command", "secret456"); err != nil {
		t.Fatal(err)
	}
	for _, username := range []string{"bootstrap", "command"} {
		u, err := s.UserByUsername(ctx, username)
		if err != nil {
			t.Fatal(err)
		}
		if u.Face != seed.Avatar(u.MID) {
			t.Errorf("%s face = %q", username, u.Face)
		}
	}
	for i, face := range []string{"", "/upload/keep.png"} {
		u := store.User{MID: int64(20 + i), Username: []string{"empty", "custom"}[i], Role: "user", Face: face}
		if err := store.InsertUser(ctx, s.DB, &u); err != nil {
			t.Fatal(err)
		}
		if err := seed.Admin(ctx, s, u.Username, "promote123"); err != nil {
			t.Fatal(err)
		}
		got, err := s.UserByID(ctx, u.MID)
		if err != nil {
			t.Fatal(err)
		}
		want := face
		if want == "" {
			want = seed.Avatar(u.MID)
		}
		if got.Face != want || got.Role != "admin" {
			t.Errorf("promotion: %+v", got)
		}
	}
}
