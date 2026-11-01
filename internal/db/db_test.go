package db

import "testing"

func TestRebind(t *testing.T) {
	q := "SELECT ?, '?', \"?\", $$?$$, $tag$?$tag$ /* ? */ -- ?\n, ?"
	want := "SELECT $1, '?', \"?\", $$?$$, $tag$?$tag$ /* ? */ -- ?\n, $2"
	if got := Rebind("postgres", q); got != want {
		t.Fatalf("%s", got)
	}
	if got := Rebind("sqlite", q); got != q {
		t.Fatal(got)
	}
}

func TestRebindQuotedAndComments(t *testing.T) {
	cases := []struct{ in, want string }{{"SELECT 'it''s ?', ?", "SELECT 'it''s ?', $1"}, {"/* outer /* ? */ ? */ SELECT ?", "/* outer /* ? */ ? */ SELECT $1"}, {"SELECT $1, ?, 'unfinished ?", "SELECT $1, $1, 'unfinished ?"}}
	for _, c := range cases {
		if got := Rebind("postgres", c.in); got != c.want {
			t.Fatalf("%q => %q", c.in, got)
		}
	}
}
