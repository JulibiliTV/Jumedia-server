package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func TestPgcFollowProgress(t *testing.T) {
	srv := newTestServer(t) // SQLite only; requests use httptest without a listener.
	const sid int64 = 40011
	pgcExec(t, srv, "DELETE FROM bangumi_follow WHERE mid=?", 1)
	pgcExec(t, srv, "INSERT INTO bangumi_follow(mid,season_id,status,mtime) VALUES (?,?,?,?)", 1, sid, 3, 1234)
	pgcExec(t, srv, "UPDATE seasons SET is_finish=0,weekday=2,pub_time=? WHERE season_id=?", "19:30", sid)
	season := pgcSeedSeason(t, srv, sid)
	if len(season.Episodes) < 3 {
		t.Fatal("fixture needs at least three episodes")
	}
	// Position is the one-based list index, not the stored position or title.
	pgcExec(t, srv, "UPDATE episodes SET position=position*10+10 WHERE season_id=?", sid)
	pgcExec(t, srv, "UPDATE episodes SET title=?,long_title=?,pub_time=? WHERE ep_id=?", "特别篇", "重逢", 999, season.Episodes[1].EpID)
	latestID := season.Episodes[len(season.Episodes)-1].EpID
	pgcExec(t, srv, "UPDATE episodes SET pub_time=? WHERE ep_id=?", 200, latestID)
	season = pgcSeedSeason(t, srv, sid)
	latest := season.Episodes[len(season.Episodes)-1]
	other := pgcSeedSeason(t, srv, 40000).Episodes[0]
	history := func(ep store.Episode, mid, historySID, viewed int64, progress int) store.History {
		return store.History{MID: mid, AID: ep.AID, CID: ep.CID, EpID: ep.EpID, SID: historySID, ViewAt: viewed, Progress: progress}
	}
	for _, tc := range []struct {
		name    string
		history []store.History
		at      int
		viewed  int64
		time    int
		update  bool
	}{
		{name: "unwatched", at: -1},
		{name: "middle_before_release", history: []store.History{history(season.Episodes[1], 1, sid, 199, 42)}, at: 1, viewed: 199, time: 42, update: true},
		{name: "middle_at_release", history: []store.History{history(season.Episodes[1], 1, sid, 200, 42)}, at: 1, viewed: 200, time: 42},
		{name: "middle_after_release", history: []store.History{history(season.Episodes[1], 1, sid, 201, 42)}, at: 1, viewed: 201, time: 42},
		{name: "middle_completed", history: []store.History{history(season.Episodes[1], 1, sid, 199, -1)}, at: 1, viewed: 199, time: -1, update: true},
		{name: "latest_completed", history: []store.History{history(latest, 1, sid, 199, -1)}, at: len(season.Episodes) - 1, viewed: 199, time: -1},
		{name: "latest_partial", history: []store.History{history(latest, 1, sid, 199, 10)}, at: len(season.Episodes) - 1, viewed: 199, time: 10},
		{name: "latest_view_not_furthest_episode", history: []store.History{history(latest, 1, sid, 190, -1), history(season.Episodes[1], 1, sid, 199, 42)}, at: 1, viewed: 199, time: 42, update: true},
		{name: "ignore_other_user_and_season", history: []store.History{history(season.Episodes[1], 1, sid, 199, 42), history(latest, 2, sid, 300, -1), history(other, 1, other.SeasonID, 400, -1)}, at: 1, viewed: 199, time: 42, update: true},
		{name: "unmatched_latest_history", history: []store.History{history(season.Episodes[1], 1, sid, 190, 42), history(other, 1, sid, 199, 10)}, at: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcExec(t, srv, "DELETE FROM history")
			for _, h := range tc.history {
				if err := store.InsertHistory(t.Context(), srv.Store.DB, &h); err != nil {
					t.Fatal(err)
				}
			}
			params := url.Values{"vmid": {"1"}, "type": {pgcFollowKind(season.SeasonType)}}
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				path, body := "/x/space/bangumi/follow/list", params
				if method == http.MethodGet {
					path, body = path+"?"+params.Encode(), nil
				}
				response, _ := request(t, srv, method, path, body, nil)
				assertCode(t, response, 0)
				pgcFields(t, response, map[string]any{"message": "0", "ttl": float64(1)})
				data := pgcObject(t, response["data"])
				pgcFields(t, data, map[string]any{"pn": float64(1), "ps": float64(15), "total": float64(1)})
				rows := pgcArray(t, data["list"])
				if len(rows) != 1 {
					t.Fatalf("list length = %d", len(rows))
				}
				card := pgcObject(t, rows[0])
				pgcFields(t, card, map[string]any{"total_count": float64(len(season.Episodes)), "follow_mtime": float64(1234), "has_update": tc.update})
				newEP := pgcObject(t, card["new_ep"])
				pgcFields(t, newEP, map[string]any{"title": latest.Title, "pub_time": float64(200)})
				lastEP, present := card["last_ep"]
				if !present {
					t.Error("missing last_ep (unwatched must explicitly be null)")
				}
				progress := "尚未观看"
				if tc.at < 0 {
					if lastEP != nil {
						t.Errorf("last_ep = %#v, want null", lastEP)
					}
				} else {
					ep := season.Episodes[tc.at]
					want := map[string]any{"ep_id": float64(ep.EpID), "index": ep.Title, "title": ep.LongTitle, "position": float64(tc.at + 1), "time": float64(tc.time), "view_at": float64(tc.viewed)}
					if !reflect.DeepEqual(lastEP, want) {
						t.Errorf("last_ep = %#v, want %#v", lastEP, want)
					}
					progress = fmt.Sprintf("看到第%s话", ep.Title)
				}
				// Removing only T26 additions must reproduce the entire old card.
				for _, key := range []string{"total_count", "follow_mtime", "last_ep", "has_update"} {
					delete(card, key)
				}
				delete(newEP, "title")
				delete(newEP, "pub_time")
				encoded, err := json.Marshal(render.SeasonCard(*season, render.M{"follow_status": 3, "progress": progress, "renewal_time": "每周二 19:30"}))
				if err != nil {
					t.Fatal(err)
				}
				var legacy map[string]any
				if err := json.Unmarshal(encoded, &legacy); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(card, legacy) {
					t.Error("legacy fields changed")
				}
			}
		})
	}
	for _, query := range []string{"", "type=3", "type=1&pn=0", "type=1&ps=0", "type=1&ps=31"} {
		response := get(t, srv, "/x/space/bangumi/follow/list?"+query)
		assertCode(t, response, -400)
		pgcFields(t, response, map[string]any{"message": "请求错误"})
	}
}

func TestPgcFollowProgressEmptySeason(t *testing.T) {
	srv := newTestServer(t)
	season := store.Season{SeasonID: 99000, MediaID: 99001, SeasonType: 1, Title: "空番剧", IsFinish: true, Areas: []string{}, Styles: []string{}}
	if err := store.InsertSeason(t.Context(), srv.Store.DB, &season); err != nil {
		t.Fatal(err)
	}
	pgcExec(t, srv, "DELETE FROM bangumi_follow WHERE mid=?", 1)
	pgcExec(t, srv, "INSERT INTO bangumi_follow(mid,season_id,status,mtime) VALUES (?,?,?,?)", 1, season.SeasonID, 2, 5678)
	data := pgcPayload(t, srv, "/x/space/bangumi/follow/list?vmid=1&type=1", "data")
	card := pgcObject(t, pgcArray(t, data["list"])[0])
	pgcFields(t, card, map[string]any{"total_count": float64(0), "follow_mtime": float64(5678), "last_ep": nil, "has_update": false, "progress": "尚未观看", "renewal_time": "", "follow_status": float64(2)})
	if _, present := card["last_ep"]; !present {
		t.Error("last_ep must be present")
	}
	want := map[string]any{"index_show": render.IndexShow(season), "title": "", "pub_time": float64(0)}
	if !reflect.DeepEqual(card["new_ep"], want) {
		t.Errorf("new_ep = %#v, want %#v", card["new_ep"], want)
	}
}
