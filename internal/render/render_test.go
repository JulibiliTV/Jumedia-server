package render_test

import (
	"context"
	"encoding/json"
	seedfixture "jumedia-server/internal/seed/testdata"
	"reflect"
	"testing"

	"jumedia-server/internal/emby/testdata"
	"jumedia-server/internal/render"
	contract "jumedia-server/internal/render/testdata"
	"jumedia-server/internal/store"
	"jumedia-server/internal/testutil"
)

func normalize(v any, base float64, key string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, w := range x {
			x[k] = normalize(w, base, k)
		}
		return x
	case []any:
		for i, w := range x {
			x[i] = normalize(w, base, "")
		}
		return x
	case float64:
		if key == "ctime" || key == "pubdate" || key == "created" || key == "live_time" {
			return x - base
		}
	}
	return v
}
func TestMockRenderFixtures(t *testing.T) {
	testutil.Drivers(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		if err := seedfixture.Seed(ctx, s, testdata.Catalog()); err != nil {
			t.Fatal(err)
		}
		var err error
		var want map[string]any
		if err = json.Unmarshal([]byte(contract.Render), &want); err != nil {
			t.Fatal(err)
		}
		var base float64
		if err = s.DB.QueryRowContext(ctx, "SELECT ctime FROM logs WHERE id=1").Scan(&base); err != nil {
			t.Fatal(err)
		}
		r := render.New(s)
		a, e := s.ArchiveByID(ctx, 100100)
		if e != nil {
			t.Fatal(e)
		}
		other, e := s.ArchiveByID(ctx, 100170)
		if e != nil {
			t.Fatal(e)
		}
		c, e := s.CollectionByID(ctx, 1001)
		if e != nil {
			t.Fatal(e)
		}
		season, e := s.SeasonByID(ctx, 40000)
		if e != nil {
			t.Fatal(e)
		}
		movie, e := s.SeasonByID(ctx, 40088)
		if e != nil {
			t.Fatal(e)
		}
		room, e := s.RoomByID(ctx, 20000)
		if e != nil {
			t.Fatal(e)
		}
		reply, e := s.ReplyByID(ctx, 1009)
		if e != nil {
			t.Fatal(e)
		}
		u, e := s.UserByID(ctx, 1)
		if e != nil {
			t.Fatal(e)
		}
		got := map[string]any{}
		add := func(k string, v any, e error) {
			t.Helper()
			if e != nil {
				t.Fatal(e)
			}
			got[k] = v
		}
		v, e := r.ArcView(ctx, *a)
		add("arc", v, e)
		v, e = r.ArcView(ctx, *other)
		add("noCollection", v, e)
		v, e = r.UgcSeason(ctx, *c)
		add("ugc", v, e)
		v, e = r.SpaceArc(ctx, *a)
		add("space", v, e)
		v, e = r.FeedCard(ctx, *a)
		add("feed", v, e)
		got["season"] = render.SeasonCard(*season)
		got["movie"] = render.SeasonCard(*movie)
		v, e = r.RoomCard(ctx, *room)
		add("room", v, e)
		v, e = r.ReplyJSON(ctx, *reply, 1)
		add("reply", v, e)
		v, e = r.MemberJSON(ctx, 1)
		add("member", v, e)
		v, e = r.MemberJSON(ctx, 999)
		add("missingMember", v, e)
		v, e = r.Owner(ctx, 1)
		add("owner", v, e)
		v, e = r.Owner(ctx, 999)
		add("missingOwner", v, e)
		got["level"] = render.LevelInfo(*u)
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err = json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		normalize(decoded, base, "")
		// T18 adds fields, while all pre-v11 fields must remain byte-for-byte equivalent.
		for _, key := range []string{"arc", "noCollection"} {
			v := decoded[key].(map[string]any)
			if _, ok := v["sub_tid"]; !ok {
				t.Fatal("missing sub_tid")
			}
			delete(v, "sub_tid")
			delete(v, "sub_tname")
		}
		for _, key := range []string{"season", "movie"} {
			v := decoded[key].(map[string]any)
			if v["season_status"] != float64(1) {
				t.Fatal(v)
			}
			for _, field := range []string{"copyright", "season_status", "duration", "year"} {
				delete(v, field)
			}
		}
		for k, w := range want {
			if !reflect.DeepEqual(decoded[k], w) {
				a, _ := json.MarshalIndent(decoded[k], "", "  ")
				b, _ := json.MarshalIndent(w, "", "  ")
				t.Errorf("%s differs\ngot %s\nwant %s", k, a, b)
			}
		}
	})
}
func TestSmallHelpers(t *testing.T) {
	if render.CompactCount(10000) != "1万" || render.CompactCount(11500) != "1.1万" || render.CompactCount(12500) != "1.3万" || render.FormatDuration(3661) != "1:01:01" || render.FormatDuration(-1) != "00:00" {
		t.Fatal("formatting")
	}
}
