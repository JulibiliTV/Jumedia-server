package seed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

// Called by BaseTx on every start. The plural marker survives source deletion
// so deleted sources are never recreated. Ciphertext is copied without a key.
func migrateDemoSourceTx(ctx context.Context, tx *db.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM settings WHERE key=?", "_demo_datasource_ids").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	values := map[string]string{}
	for _, key := range []string{"_demo_emby_url", "_demo_emby_username", "_demo_emby_password", "_demo_catalog", "_demo_datasource_id"} {
		var raw string
		err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		values[key] = raw
	}
	var p DemoPackage
	var password string
	var oldID int64
	for _, field := range []struct {
		key  string
		dest any
	}{
		{"_demo_emby_url", &p.Emby.URL}, {"_demo_emby_username", &p.Emby.Username}, {"_demo_emby_password", &password}, {"_demo_catalog", &p.Catalog}, {"_demo_datasource_id", &oldID},
	} {
		if raw := values[field.key]; raw != "" {
			if err := json.Unmarshal([]byte(raw), field.dest); err != nil {
				return err
			}
		}
	}
	// Also upgrade databases produced by the first, unified-source T30 draft.
	if p.Emby.URL == "" && oldID > 0 {
		ds, err := store.DataSourceByID(ctx, tx, oldID)
		if errors.Is(err, sql.ErrNoRows) {
			if err = store.SetSetting(ctx, tx, "_demo_datasource_ids", []int64{}); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "DELETE FROM settings WHERE key=?", "_demo_datasource_id")
			return err
		}
		if err != nil {
			return err
		}
		p.Emby.URL, p.Emby.Username, password = ds.URL, ds.Username, ds.PasswordEnc
	}
	if p.Emby.URL == "" {
		return nil
	}
	return createDemoSourcesTx(ctx, tx, p, password, oldID)
}

type demoSourceSet struct {
	official int64
	byOwner  map[int64]int64
	oldID    int64
}
type demoSourceField struct {
	table, key, column, kind, query string
	official                        bool
}
type demoSourceValue struct {
	field demoSourceField
	key   any
	value string
	owner int64
}

// Every identifier and clause is fixed schema text. Only the official owner is
// bound as a query value; no request data is interpolated into these queries.
func demoSourceFields() []demoSourceField {
	return []demoSourceField{
		{"archive_pages", "cid", "media", "media", "SELECT p.cid,p.media,a.mid FROM archive_pages p JOIN archives a ON a.aid=p.aid", false},
		{"episodes", "ep_id", "media", "media", "SELECT ep_id,media,CAST(? AS BIGINT) FROM episodes", true},
		{"rooms", "room_id", "media", "media", "SELECT room_id,media,uid FROM rooms", false},
		{"live_caches", "key", "media", "media", "SELECT c.key,c.media,COALESCE(a.mid,?) FROM live_caches c LEFT JOIN archives a ON a.aid=c.aid", true},
		{"archives", "aid", "pic", "image", "SELECT aid,pic,mid FROM archives", false},
		{"episodes", "ep_id", "cover", "image", "SELECT ep_id,cover,CAST(? AS BIGINT) FROM episodes", true},
		{"seasons", "season_id", "cover", "image", "SELECT season_id,cover,CAST(? AS BIGINT) FROM seasons", true},
		{"seasons", "season_id", "horizontal", "image", "SELECT season_id,horizontal,CAST(? AS BIGINT) FROM seasons", true},
		{"collections", "id", "cover", "image", "SELECT id,cover,mid FROM collections", false},
		{"rooms", "room_id", "cover", "image", "SELECT room_id,cover,uid FROM rooms", false},
		{"users", "mid", "face", "image", "SELECT mid,face,mid FROM users", false},
		{"users", "mid", "top_photo", "image", "SELECT mid,top_photo,mid FROM users", false},
		{"banners", "id", "img", "image", "SELECT b.id,b.img,COALESCE((SELECT a.mid FROM archives a WHERE b.url='/video/' || a.bvid),?) FROM banners b", true},
		{"rooms", "room_id", "source", "source", "SELECT room_id,source,uid FROM rooms", false},
	}
}
func demoSourceValues(ctx context.Context, tx *db.Tx, official int64) ([]demoSourceValue, error) {
	values := []demoSourceValue{}
	for _, field := range demoSourceFields() {
		var args []any
		if field.official {
			args = []any{official}
		}
		rows, err := tx.QueryContext(ctx, field.query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			v := demoSourceValue{field: field}
			if err = rows.Scan(&v.key, &v.value, &v.owner); err != nil {
				rows.Close()
				return nil, err
			}
			values = append(values, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func createDemoSourceTx(ctx context.Context, tx *db.Tx, p DemoPackage, password string) error {
	return createDemoSourcesTx(ctx, tx, p, password, 0)
}
func createDemoSourcesTx(ctx context.Context, tx *db.Tx, p DemoPackage, password string, oldID int64) error {
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", "officialMid").Scan(&raw); err != nil {
		return err
	}
	sources := demoSourceSet{byOwner: map[int64]int64{}, oldID: oldID}
	if err := json.Unmarshal([]byte(raw), &sources.official); err != nil {
		return err
	}
	values, err := demoSourceValues(ctx, tx, sources.official)
	if err != nil {
		return err
	}
	rewrite := demoSourceRewriter(p.Emby.URL, p.Catalog, oldID)
	libraries := map[string]string{}
	for _, x := range p.Catalog.Series {
		libraries[x.ID] = x.Library
		for _, e := range x.Episodes {
			libraries[e.ID] = x.Library
		}
	}
	for _, x := range p.Catalog.Movies {
		libraries[x.ID] = x.Library
	}
	owners := map[int64][]store.DataSourceLibrary{}
	collect := func(owner int64, value, kind string) error {
		if owner <= 0 {
			return nil
		}
		probe := int64(1)
		if oldID == probe {
			probe++
		}
		next, err := rewrite(probe, value, kind)
		if err != nil {
			return err
		}
		if next == value {
			return nil
		}
		if _, ok := owners[owner]; !ok {
			owners[owner] = []store.DataSourceLibrary{}
		}
		if kind == "source" {
			var src *store.StreamSource
			if err = json.Unmarshal([]byte(next), &src); err != nil {
				return err
			}
			if src != nil {
				next = src.URL
			}
		}
		_, item, _, _, ok := emby.ParseDSRef(next)
		if !ok {
			if u, e := url.Parse(next); e == nil {
				parts := strings.Split(strings.TrimPrefix(u.Path, "/media/emby/"), "/")
				if len(parts) > 0 {
					item = parts[0]
				}
			}
		}
		name := libraries[item]
		if name == "" {
			return nil
		}
		for _, l := range owners[owner] {
			if l.ID == name {
				return nil
			}
		}
		owners[owner] = append(owners[owner], store.DataSourceLibrary{ID: name, Name: name, Enabled: true})
		return nil
	}
	pages := map[int64]string{}
	for _, v := range values {
		if v.field.table == "archive_pages" {
			if cid, ok := v.key.(int64); ok {
				pages[cid] = v.value
			}
		}
	}
	for _, v := range values {
		if err = collect(v.owner, v.value, v.field.kind); err != nil {
			return err
		}
		if v.field.kind == "source" {
			var src *store.StreamSource
			if err = json.Unmarshal([]byte(v.value), &src); err != nil {
				return err
			}
			if src != nil && src.Type == "video" {
				if err = collect(v.owner, pages[src.CID], "media"); err != nil {
					return err
				}
			}
		}
	}
	mids := make([]int64, 0, len(owners))
	for mid := range owners {
		mids = append(mids, mid)
	}
	slices.Sort(mids)
	ids := []int64{}
	for _, mid := range mids {
		id, err := store.NextID(ctx, tx)
		if err != nil {
			return err
		}
		slices.SortFunc(owners[mid], func(a, b store.DataSourceLibrary) int { return strings.Compare(a.ID, b.ID) })
		ds := store.DataSource{ID: id, MID: mid, System: false, Type: "emby", Name: "演示 Emby", URL: strings.TrimRight(p.Emby.URL, "/"), Username: p.Emby.Username, PasswordEnc: password, Libraries: owners[mid], AutoSync: false, Status: "idle", Ctime: time.Now().Unix()}
		if err = store.SaveDataSource(ctx, tx, &ds); err != nil {
			return err
		}
		sources.byOwner[mid] = id
		ids = append(ids, id)
	}
	if err = applyDemoSourceValues(ctx, tx, sources, values, rewrite); err != nil {
		return err
	}
	if oldID > 0 {
		if _, err = tx.ExecContext(ctx, "DELETE FROM data_sources WHERE id=?", oldID); err != nil {
			return err
		}
	}
	if err = store.SetSetting(ctx, tx, "_demo_datasource_ids", ids); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM settings WHERE key IN (?,?,?,?)", "_demo_emby_url", "_demo_emby_username", "_demo_emby_password", "_demo_datasource_id")
	return err
}

// Load only surviving sources; dangling media still carries deleted source IDs
// and remains unavailable. The marker prevents recreation of deleted owners.
func storedDemoSourcesTx(ctx context.Context, tx *db.Tx) (demoSourceSet, string, error) {
	sources := demoSourceSet{byOwner: map[int64]int64{}}
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", "_demo_datasource_ids").Scan(&raw)
	if err != nil {
		return sources, "", err
	}
	var ids []int64
	if err = json.Unmarshal([]byte(raw), &ids); err != nil {
		return sources, "", err
	}
	if err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", "officialMid").Scan(&raw); err != nil {
		return sources, "", err
	}
	if err = json.Unmarshal([]byte(raw), &sources.official); err != nil {
		return sources, "", err
	}
	address := ""
	for _, id := range ids {
		ds, e := store.DataSourceByID(ctx, tx, id)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return sources, "", e
		}
		sources.byOwner[ds.MID] = id
		if address == "" {
			address = ds.URL
		}
	}
	return sources, address, nil
}

func demoSourceRef(id int64, ref string, oldID int64) string {
	if ds, item, source, _, ok := emby.ParseDSRef(ref); ok {
		if oldID > 0 && ds == oldID {
			if id == 0 {
				return ""
			}
			return fmt.Sprintf("embyds:%d:%s:%s:direct", id, item, source)
		}
		return ref
	}
	item, source, ok := emby.ParseRef(ref)
	if !ok {
		u, err := url.Parse(ref)
		if err != nil || (u.Query().Has("ds") && u.Query().Get("ds") != fmt.Sprint(oldID)) {
			return ref
		}
		path := strings.TrimPrefix(u.Path, "/media/emby/")
		if path == u.Path {
			return ref
		}
		switch {
		case strings.HasSuffix(path, ".mp4"):
			item = strings.TrimSuffix(path, ".mp4")
		case strings.HasSuffix(path, "/hls/index.m3u8"):
			item = strings.TrimSuffix(path, "/hls/index.m3u8")
		case strings.HasSuffix(path, "/hls/main.m3u8"):
			item = strings.TrimSuffix(path, "/hls/main.m3u8")
		default:
			return ref
		}
		source = u.Query().Get("source")
		if !emby.ValidPair(item, source) {
			return ref
		}
	}
	if id == 0 {
		return ""
	}
	return fmt.Sprintf("embyds:%d:%s:%s:direct", id, item, source)
}
func demoImagePath(id int64, item, kind, tag string) string {
	return fmt.Sprintf("/media/emby/%s/image/%s?ds=%d&tag=%s", url.PathEscape(item), kind, id, url.QueryEscape(tag))
}

func demoImageItem(path string) (string, string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+3 < len(parts); i++ {
		if strings.EqualFold(parts[i], "Items") && strings.EqualFold(parts[i+2], "Images") && emby.ValidPair(parts[i+1], "image") {
			for _, kind := range []string{"Primary", "Backdrop", "Thumb"} {
				if strings.EqualFold(parts[i+3], kind) {
					return parts[i+1], kind, true
				}
			}
		}
	}
	return "", "", false
}

// Reuse the catalog image mapping and strict legacy parser for import and both
// startup migrations; each record supplies its owning user's source ID.
func demoSourceRewriter(address string, catalog emby.Catalog, oldID int64) func(int64, string, string) (string, error) {
	images := map[string]struct{ item, kind, tag string }{}
	add := func(value *string, item, kind string) {
		if value == nil || *value == "" || !emby.ValidPair(item, "image") {
			return
		}
		tag := ""
		if u, e := url.Parse(*value); e == nil {
			tag = u.Query().Get("tag")
			// Catalog covers can reuse a parent's image: preserve the actual item
			// and kind from Emby's URL rather than guessing from the episode ID.
			if actualItem, actualKind, ok := demoImageItem(u.Path); ok {
				item, kind = actualItem, actualKind
			}
		}
		images[*value] = struct{ item, kind, tag string }{item, kind, tag}
	}
	addImages := func(im emby.Images, item string) {
		add(im.Primary, item, "Primary")
		add(im.Backdrop, item, "Backdrop")
		add(im.Thumb, item, "Thumb")
	}
	for _, x := range catalog.Series {
		addImages(x.Images, x.ID)
		for _, e := range x.Episodes {
			add(e.Image, e.ID, "Primary")
		}
	}
	for _, x := range catalog.Movies {
		addImages(x.Images, x.ID)
	}
	host, _ := url.Parse(address)
	convertImage := func(id int64, value string) string {
		if image, ok := images[value]; ok {
			if id == 0 {
				return ""
			}
			return demoImagePath(id, image.item, image.kind, image.tag)
		}
		u, e := url.Parse(value)
		if e == nil && oldID > 0 && u.Query().Get("ds") == fmt.Sprint(oldID) && strings.HasPrefix(u.Path, "/media/emby/") {
			parts := strings.Split(strings.TrimPrefix(u.Path, "/media/emby/"), "/")
			if len(parts) == 3 && parts[1] == "image" {
				if id == 0 {
					return ""
				}
				return demoImagePath(id, parts[0], parts[2], u.Query().Get("tag"))
			}
		}
		if e != nil || host == nil || !strings.EqualFold(u.Host, host.Host) || u.Host == "" {
			return value
		}
		if item, kind, ok := demoImageItem(u.Path); ok {
			if id == 0 {
				return ""
			}
			return demoImagePath(id, item, kind, u.Query().Get("tag"))
		}

		return value
	}
	return func(id int64, value, kind string) (string, error) {
		switch kind {
		case "media":
			return demoSourceRef(id, value, oldID), nil
		case "image":
			return convertImage(id, value), nil
		case "source":
			var src *store.StreamSource
			if err := json.Unmarshal([]byte(value), &src); err != nil {
				return "", err
			}
			if src != nil {
				next := demoSourceRef(id, src.URL, oldID)
				if next != src.URL {
					src.URL = next
					raw, err := json.Marshal(src)
					return string(raw), err
				}
			}
		}
		return value, nil
	}
}
func rewriteDemoSourceTx(ctx context.Context, tx *db.Tx, sources demoSourceSet, address string, catalog emby.Catalog) error {
	values, err := demoSourceValues(ctx, tx, sources.official)
	if err != nil {
		return err
	}
	return applyDemoSourceValues(ctx, tx, sources, values, demoSourceRewriter(address, catalog, sources.oldID))
}
func applyDemoSourceValues(ctx context.Context, tx *db.Tx, sources demoSourceSet, values []demoSourceValue, rewrite func(int64, string, string) (string, error)) error {
	// Cache keys, directories, states and room source.cache links are unchanged.
	// A cache shared across owners keeps the representative page owner's media.
	for _, v := range values {
		next, err := rewrite(sources.byOwner[v.owner], v.value, v.field.kind)
		if err != nil {
			return err
		}
		if next != v.value {
			if _, err = tx.ExecContext(ctx, "UPDATE "+v.field.table+" SET "+v.field.column+"=? WHERE "+v.field.key+"=?", next, v.key); err != nil {
				return err
			}
		}
	}
	return nil
}
