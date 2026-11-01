package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"jumedia-server/internal/store"
)

type communityProtoField struct {
	number int
	value  uint64
	data   []byte
}

func communityDecode(t *testing.T, b []byte) []communityProtoField {
	t.Helper()
	fields := []communityProtoField{}
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			t.Fatal("invalid tag")
		}
		b = b[n:]
		f := communityProtoField{number: int(tag >> 3)}
		switch tag & 7 {
		case 0:
			f.value, n = binary.Uvarint(b)
			if n <= 0 {
				t.Fatal("invalid varint")
			}
			b = b[n:]
		case 2:
			size, n := binary.Uvarint(b)
			if n <= 0 || size > uint64(len(b)-n) {
				t.Fatal("invalid length")
			}
			b = b[n:]
			f.data = b[:size]
			b = b[size:]
		case 5:
			if len(b) < 4 {
				t.Fatal("invalid float")
			}
			f.data = b[:4]
			b = b[4:]
		default:
			t.Fatal("invalid wire type")
		}
		fields = append(fields, f)
	}
	return fields
}
func communityFields(t *testing.T, b []byte) map[int]communityProtoField {
	t.Helper()
	m := map[int]communityProtoField{}
	for _, f := range communityDecode(t, b) {
		m[f.number] = f
	}
	return m
}
func communityBinary(t *testing.T, s *Server, path string, session *store.Session) []byte {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if session != nil {
		r.AddCookie(&http.Cookie{Name: "SESSDATA", Value: session.Token})
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("binary response %d %s", w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}
func TestCommunityDanmaku(t *testing.T) {
	testServers(t, func(t *testing.T, s *Server) {
		user := login(t, s, "jumedia", "admin123")
		a := communityArchives(t, s)[0]
		cid := a.Pages[0].CID
		path := fmt.Sprintf("/x/v2/dm/list/seg.so?oid=%d", cid)
		first := communityBinary(t, s, path, nil)
		entries := communityDecode(t, first)
		if len(entries) != 46 {
			t.Fatalf("seed count=%d", len(entries))
		}
		if !bytes.Equal(first, communityBinary(t, s, path, nil)) {
			t.Fatal("seeding duplicated or changed")
		}
		// Both protobuf routes report malformed forms through the JSON failure envelope.
		for _, p := range []string{"/x/v2/dm/list/seg.so", "/x/v2/dm/web/view"} {
			r := httptest.NewRequest("POST", p, strings.NewReader("x=%zz"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			var v map[string]any
			if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
				t.Fatal(e)
			}
			communityFailure(t, v, -400, "请求参数错误")
		}
		// Posting at the exact segment boundary belongs to segment two, not one.
		payload := url.Values{"oid": {fmt.Sprint(cid)}, "aid": {fmt.Sprint(a.AID)}, "msg": {" \ufeff分段边界 "}, "progress": {"360000"}, "mode": {"5"}, "color": {"0xffd302"}}
		posted := communityData(t, communityWrite(t, s, user, "/x/v2/dm/post", payload))
		id := posted["dmid"]
		if posted["dmid_str"] != fmt.Sprint(id) || posted["action"] != "" {
			t.Fatal(posted)
		}
		if !bytes.Equal(first, communityBinary(t, s, path, nil)) {
			t.Fatal("segment one contains boundary")
		}
		segment := communityDecode(t, communityBinary(t, s, path+"&segment_index=2", nil))
		if len(segment) != 1 {
			t.Fatal(segment)
		}
		f := communityFields(t, segment[0].data)
		if f[1].value != uint64(id.(float64)) || f[2].value != 360000 || f[3].value != 5 || f[4].value != 25 || f[5].value != 0xffd302 || string(f[6].data) != "1" || string(f[7].data) != "分段边界" || f[9].value != 10 || string(f[12].data) != fmt.Sprint(id) {
			t.Fatal(f)
		}
		updated, e := s.Store.ArchiveByID(context.Background(), a.AID)
		if e != nil || updated.Stat.Danmaku != a.Stat.Danmaku+1 {
			t.Fatalf("stat update: %+v %v", updated, e)
		}
		for _, msg := range []string{" ", strings.Repeat("😀", 51)} {
			communityFailure(t, communityWrite(t, s, user, "/x/v2/dm/post", url.Values{"msg": {msg}}), 36700, "弹幕内容需在 1–100 字之间")
		}
		assertCode(t, communityWrite(t, s, user, "/x/v2/dm/post", url.Values{"msg": {strings.Repeat("😀", 50)}, "oid": {fmt.Sprint(cid)}}), 0)
		defaults := communityFields(t, communityBinary(t, s, "/x/v2/dm/web/view", nil))
		cfg := communityFields(t, defaults[10].data)
		if defaults[1].value != 1 || cfg[1].value != 1 || cfg[2].value != 0 || cfg[3].value != 1 || cfg[12].value != 50 || math.Float32frombits(binary.LittleEndian.Uint32(cfg[11].data)) != .8 {
			t.Fatal(cfg)
		}
		settings := url.Values{"dm_switch": {"false"}, "opacity": {"0.3"}, "unknown": {"ignored"}}
		assertCode(t, communityWrite(t, s, user, "/x/v2/dm/web/config", settings), 0)
		communityFailure(t, communityWrite(t, s, user, "/x/v2/dm/web/config", settings), 23004, "设置未变更")
		// Comparing only an already equal subset still succeeds in the Mock, since
		// omitted previous keys differ from undefined in the submitted object.
		assertCode(t, communityWrite(t, s, user, "/x/v2/dm/web/config", url.Values{"opacity": {"0.3"}}), 0)
		cfg = communityFields(t, communityFields(t, communityBinary(t, s, "/x/v2/dm/web/view", user))[10].data)
		if cfg[1].value != 0 || math.Float32frombits(binary.LittleEndian.Uint32(cfg[11].data)) != .3 {
			t.Fatal(cfg)
		}
		guestCfg := communityFields(t, communityFields(t, communityBinary(t, s, "/x/v2/dm/web/view", nil))[10].data)
		if guestCfg[1].value != 1 {
			t.Fatal("config leaked to guest")
		}
		filter := communityData(t, get(t, s, "/x/dm/filter/user", user))
		if len(filter["rule"].([]any)) != 0 || len(filter) != 3 {
			t.Fatal(filter)
		}
		communityExec(t, s, "INSERT INTO dm_filter (mid,rule,keyword,regex) VALUES (?,?,?,?)", user.MID, `["42"]`, `["广告"]`, `["spam.*"]`)
		filter = communityData(t, get(t, s, "/x/dm/filter/user", user))
		if filter["keyword"].([]any)[0] != "广告" || filter["regex"].([]any)[0] != "spam.*" {
			t.Fatal(filter)
		}
		// Unknown CIDs do not generate fake danmaku.
		unknown := communityDecode(t, communityBinary(t, s, "/x/v2/dm/list/seg.so?oid=999999", nil))
		if len(unknown) != 0 {
			t.Fatal(len(unknown))
		}
		for _, v := range unknown {
			if communityFields(t, v.data)[2].value >= 24000 {
				t.Fatal("default duration")
			}
		}
		if len(communityBinary(t, s, path+"&segment_index=0", nil)) != 0 {
			t.Fatal("segment zero must be empty")
		}
		// Resolve durations from both archive pages and PGC episodes.
		eps, e := s.Store.ListEpisode(context.Background(), "ORDER BY ep_id LIMIT 1")
		if e != nil {
			t.Fatal(e)
		}
		for _, target := range []struct {
			cid   int64
			table string
		}{{cid + 1000000, "archive_pages"}, {eps[0].CID, "episodes"}} {
			if target.table == "archive_pages" {
				p := store.Page{CID: target.cid, AID: a.AID, Position: 99, Duration: 2, Media: "/upload/local.mp4"}
				if e := store.InsertPage(context.Background(), s.Store.DB, &p); e != nil {
					t.Fatal(e)
				}
			} else {
				communityExec(t, s, "UPDATE episodes SET duration=2 WHERE cid=?", target.cid)
			}
			values := communityDecode(t, communityBinary(t, s, fmt.Sprintf("/x/v2/dm/list/seg.so?oid=%d", target.cid), nil))
			want := 46
			if target.table == "archive_pages" {
				want = 0
			}
			if len(values) != want {
				t.Fatal(len(values))
			}
			for _, v := range values {
				if communityFields(t, v.data)[2].value >= 2000 {
					t.Fatal("CID duration not respected")
				}
			}
		}
		// Concurrent first requests must see exactly one committed batch.
		var wg sync.WaitGroup
		results := make(chan *httptest.ResponseRecorder, 6)
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest("GET", "/x/v2/dm/list/seg.so?oid=888888", nil))
				results <- w
			}()
		}
		wg.Wait()
		close(results)
		var expected []byte
		for w := range results {
			if w.Header().Get("Content-Type") != "application/octet-stream" {
				t.Fatal(w.Body.String())
			}
			if expected == nil {
				expected = w.Body.Bytes()
			}
			if !bytes.Equal(expected, w.Body.Bytes()) {
				t.Fatal("concurrent seed differs")
			}
		}
		if len(communityDecode(t, expected)) != 0 {
			t.Fatal("concurrent duplicate seeds")
		}
	})
}
