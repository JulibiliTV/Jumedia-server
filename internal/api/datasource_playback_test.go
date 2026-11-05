package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDataSourcePlaybackMode(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		var streams atomic.Int32
		f.stream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { streams.Add(1) })
		admin := login(t, s, "jumedia", "admin123")
		id := sourceSave(t, s, admin, f, true, "playback")
		archives := sourceArchives(t, s, id)
		movie, raw := archives["movie"].Pages[0], archives["mkv"].Pages[0]
		season := pgcSeedSeason(t, s, 40000)
		ep := season.Episodes[0]
		liveExec(t, s, "UPDATE episodes SET media=? WHERE ep_id=?", raw.Media, ep.EpID)
		play := func(path, key string, direct bool, container, item string) {
			t.Helper()
			v, w := request(t, s, "GET", path, nil, nil)
			assertCode(t, v, 0)
			d := v[key].(map[string]any)
			link := d["durl"].([]any)[0].(map[string]any)["url"].(string)
			u, err := url.Parse(link)
			if err != nil {
				t.Fatal(err)
			}
			if direct {
				if u.Scheme+"://"+u.Host != f.server.URL || u.Path != "/emby/Videos/"+item+"/stream."+container || u.Query().Get("api_key") != "sensitive-token" || u.Query().Get("Static") != "true" || u.Query().Get("MediaSourceId") != item+"src" {
					t.Fatal("incorrect direct playback URL")
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("credential response must not be cached")
				}
			} else if !strings.HasPrefix(link, "http://example.com/media/emby/") || u.Query().Get("api_key") != "" {
				t.Fatal("default playback must use the proxy")
			}
			if d["format"] != container {
				t.Fatal("wrong container", d["format"])
			}
		}
		moviePath := fmt.Sprintf("/x/player/wbi/playurl?cid=%d", movie.CID)
		rawPath := fmt.Sprintf("/x/player/wbi/playurl?cid=%d", raw.CID)
		epPath := fmt.Sprintf("/pgc/player/web/playurl?ep_id=%d", ep.EpID)
		play(moviePath, "data", false, "mp4", "movie")
		params := url.Values{"id": {fmt.Sprint(id)}, "url": {f.server.URL}, "username": {"playback"}, "proxy_playback": {"false"}}
		update := func(want bool) {
			t.Helper()
			d := sourceData(t, sourcePost(t, s, admin, "/x/admin/datasource/save", params))
			if d["proxy_playback"] != want {
				t.Fatal("setting not returned", d["proxy_playback"])
			}
			sourceWait(t, s, id)
			list := sourceData(t, get(t, s, "/x/admin/datasources", admin))["list"].([]any)
			if list[0].(map[string]any)["proxy_playback"] != want {
				t.Fatal("setting not persisted")
			}
		}
		update(false)
		play(moviePath, "data", true, "mp4", "movie")
		play(rawPath, "data", true, "mkv", "mkv")
		play(epPath, "result", true, "mkv", "mkv")
		if err := s.Store.SetSetting(t.Context(), "allowGuestPlay", false); err != nil {
			t.Fatal(err)
		}
		assertCode(t, get(t, s, moviePath), -101)
		if err := s.Store.SetSetting(t.Context(), "allowGuestPlay", true); err != nil {
			t.Fatal(err)
		}
		liveExec(t, s, "UPDATE archives SET state=-100 WHERE aid=?", archives["movie"].AID)
		assertCode(t, get(t, s, moviePath), -404)
		liveExec(t, s, "UPDATE archives SET state=0 WHERE aid=?", archives["movie"].AID)
		before := f.logins.Load()
		f.expire.Store(true)
		play(moviePath, "data", true, "mp4", "movie")
		if f.logins.Load() != before+1 {
			t.Fatal("revoked token not renewed")
		}
		params.Del("proxy_playback")
		update(false)
		params.Set("proxy_playback", "true")
		update(true)
		play(moviePath, "data", false, "mp4", "movie")
		if streams.Load() != 0 {
			t.Fatal("playurl fetched video bytes")
		}
		params.Set("proxy_playback", "false")
		update(false)
		f.server.Close()
		assertCode(t, get(t, s, moviePath), -502)
	})
}

func TestPersonalDataSourcePlaybackSetting(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		f := newSourceFixture(t)
		if err := s.Store.SetSetting(t.Context(), "dataSourcePrivateNetwork", true); err != nil {
			t.Fatal(err)
		}
		owner := login(t, s, "jumedia", "admin123")
		id := sourceSave(t, s, owner, f, false, "personal")
		params := url.Values{"id": {fmt.Sprint(id)}, "url": {f.server.URL}, "username": {"personal"}, "proxy_playback": {"false"}}
		assertCode(t, post(t, s, "/x/web/datasource/save", params), -101)
		d := sourceData(t, sourcePost(t, s, owner, "/x/web/datasource/save", params))
		if d["proxy_playback"] != false {
			t.Fatal("personal source did not save direct mode")
		}
		sourceWait(t, s, id)
		list := sourceData(t, get(t, s, "/x/web/datasources", owner))["list"].([]any)
		if len(list) != 1 || list[0].(map[string]any)["proxy_playback"] != false {
			t.Fatal("personal source mode missing from list")
		}
	})
}
