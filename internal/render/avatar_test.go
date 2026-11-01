package render_test

import (
	"testing"

	"jumedia-server/internal/render"
	"jumedia-server/internal/seed"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func TestDefaultUserAvatars(t *testing.T) {
	s := testutil.Open(t, "sqlite")
	ctx := t.Context()
	for _, u := range []store.User{{MID: 1, Username: "empty"}, {MID: 2, Username: "custom", Face: "/upload/keep.png"}, {MID: 3, Username: "demo", Face: "/mock/avatars/u3.png"}} {
		if err := store.InsertUser(ctx, s.DB, &u); err != nil {
			t.Fatal(err)
		}
	}
	r := render.New(s)
	for _, mid := range []int64{1, 2, 3, 999, -9, 0} {
		want := seed.Avatar(mid)
		if mid == 2 {
			want = "/upload/keep.png"
		}
		if mid == 3 {
			want = "/mock/avatars/u3.png"
		}
		owner, err := r.Owner(ctx, mid)
		if err != nil {
			t.Fatal(err)
		}
		member, err := r.MemberJSON(ctx, mid)
		if err != nil {
			t.Fatal(err)
		}
		if owner["face"] != want || member["avatar"] != want {
			t.Errorf("mid %d owner=%v member=%v", mid, owner, member)
		}
		if mid == 999 && owner["name"] != "已注销用户" {
			t.Fatal(owner)
		}
	}
	if _, err := s.DB.ExecContext(ctx, "DROP TABLE users"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Owner(ctx, 1); err == nil {
		t.Fatal("owner swallowed storage failure")
	}
	if _, err := r.MemberJSON(ctx, 1); err == nil {
		t.Fatal("member swallowed storage failure")
	}
}
