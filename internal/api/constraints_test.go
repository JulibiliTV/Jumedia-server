package api

import (
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

func TestMissingReferencesReturnBusinessErrors(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		archives := communityArchives(t, s)
		aid := fmt.Sprint(archives[0].AID)
		episodes, err := s.Store.ListEpisode(t.Context(), "ORDER BY ep_id LIMIT 1")
		if err != nil {
			t.Fatal(err)
		}
		ep := episodes[0]
		cases := []struct {
			name, path string
			p          url.Values
			code       int
			message    string
		}{
			{"reply root", "/x/v2/reply/add", url.Values{"oid": {aid}, "root": {"99999999"}, "message": {"评论"}}, 12022, "评论已被删除"},
			{"reply parent", "/x/v2/reply/add", url.Values{"oid": {aid}, "parent": {"99999999"}, "message": {"评论"}}, 12022, "评论已被删除"},
			{"reply archive", "/x/v2/reply/add", url.Values{"oid": {"99999999"}, "message": {"评论"}}, -404, "啥都木有"},
			{"reply fractional root", "/x/v2/reply/add", url.Values{"oid": {aid}, "root": {"1.5"}, "message": {"评论"}}, 12022, "评论已被删除"},
			{"history episode", "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(ep.AID)}, "sid": {fmt.Sprint(ep.SeasonID)}, "epid": {"99999999"}}, -404, "剧集不存在"},
			{"history season", "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(ep.AID)}, "sid": {"99999999"}, "epid": {fmt.Sprint(ep.EpID)}}, -404, "番剧不存在"},
			{"history fractional episode", "/x/click-interface/web/heartbeat", url.Values{"aid": {aid}, "epid": {"1.5"}}, -404, "剧集不存在"},
			{"history fractional season", "/x/click-interface/web/heartbeat", url.Values{"aid": {aid}, "sid": {"1.5"}}, -404, "番剧不存在"},
			{"favorite archive", "/x/v3/fav/resource/deal", url.Values{"rid": {"99999999"}, "add_media_ids": {"1"}}, -404, "啥都木有"},
			{"follow season", "/pgc/web/follow/add", url.Values{"season_id": {"99999999"}}, -404, "番剧不存在"},
			{"dislike archive", "/x/v2/view/dislike", url.Values{"aid": {"99999999"}, "dislike": {"0"}}, -404, "啥都木有"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				before := transactionSnapshot(t, s)
				communityFailure(t, communityWrite(t, s, session, tc.path, tc.p), tc.code, tc.message)
				if !reflect.DeepEqual(before, transactionSnapshot(t, s)) {
					t.Fatal("invalid reference changed data")
				}
			})
		}
		// Optional history references remain optional, and an existing episode works.
		assertCode(t, communityWrite(t, s, session, "/x/click-interface/web/heartbeat", url.Values{"aid": {aid}, "epid": {"0"}, "sid": {"0"}}), 0)
		assertCode(t, communityWrite(t, s, session, "/x/click-interface/web/heartbeat", url.Values{"aid": {fmt.Sprint(ep.AID)}, "sid": {fmt.Sprint(ep.SeasonID)}, "epid": {fmt.Sprint(ep.EpID)}}), 0)
	})
}

func TestDuplicateEpisodeIdentifiers(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		session := login(t, s, "jumedia", "admin123")
		seasons, err := s.Store.ListSeason(t.Context(), "ORDER BY season_id LIMIT 2")
		if err != nil {
			t.Fatal(err)
		}
		original, err := s.Store.SeasonByID(t.Context(), seasons[0].SeasonID)
		if err != nil {
			t.Fatal(err)
		}
		other, err := s.Store.SeasonByID(t.Context(), seasons[1].SeasonID)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"ep_id", "aid", "cid"} {
			for _, across := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/across=%v", field, across), func(t *testing.T) {
					value := int64(99999999)
					if across {
						switch field {
						case "ep_id":
							value = other.Episodes[0].EpID
						case "aid":
							value = other.Episodes[0].AID
						case "cid":
							value = other.Episodes[0].CID
						}
					}
					eps := []any{map[string]any{field: value}}
					if !across {
						eps = append(eps, map[string]any{field: value})
					}
					before := transactionSnapshot(t, s)
					logs := adminTestCount(t, s, "SELECT COUNT(*) FROM logs")
					adminTestError(t, adminTestSeasonSave(t, s, session, map[string]any{"season_id": original.SeasonID, "title": "must roll back", "episodes": eps}), -400, "剧集 ID 重复（"+field+"）")
					if !reflect.DeepEqual(before, transactionSnapshot(t, s)) || logs != adminTestCount(t, s, "SELECT COUNT(*) FROM logs") {
						t.Fatal("duplicate episode changed season, references, sequence or logs")
					}
				})
			}
		}
		// Keeping all IDs while editing the same season must remain valid.
		adminTestData(t, adminTestSeasonSave(t, s, session, map[string]any{"season_id": original.SeasonID, "title": original.Title, "episodes": original.Episodes}))
	})
}
