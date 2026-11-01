package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

var regionRules = []struct {
	pattern string
	tid     int64
}{
	{`(?i)美食|烹饪|Food|Cooking`, 1020}, {`(?i)音乐|Music`, 1003}, {`(?i)纪录|Documentary`, 1010}, {`(?i)体育|运动|Sport`, 1018}, {`(?i)旅行|旅游|Travel`, 1030}, {`(?i)动画|动漫|番剧|Anime|Animation`, 1005}, {`(?i)综艺|真人秀|脱口秀|Reality|Talk|Game Show`, 1002},
}

func autoRegion(regions []store.Region, lib store.DataSourceLibrary, genres []string) int64 {
	g := strings.Join(genres, " ")
	tid := int64(1001)
	for _, rule := range regionRules {
		hit := regexp.MustCompile(rule.pattern).MatchString(g)
		if rule.tid == 1003 && lib.Type == "musicvideos" {
			hit = true
		}
		if rule.tid == 1005 && regexp.MustCompile(`动漫|动画|番`).MatchString(lib.Name) {
			hit = true
		}
		if rule.tid == 1002 && strings.Contains(lib.Name, "综艺") {
			hit = true
		}
		if hit {
			tid = rule.tid
			break
		}
	}
	for _, r := range regions {
		if r.TID == tid {
			return tid
		}
	}
	for _, r := range regions {
		if r.TID == 1001 {
			return 1001
		}
	}
	if len(regions) > 0 {
		return regions[0].TID
	}
	return 0
}
func sourceImage(ds int64, item, kind, tag string) string {
	return fmt.Sprintf("/media/emby/%s/image/%s?ds=%d&tag=%s", url.PathEscape(item), kind, ds, url.QueryEscape(tag))
}
func sourceItemImage(ds int64, item emby.Item) string {
	for _, kind := range []string{"Backdrop", "Thumb", "Primary"} {
		tag := item.ImageTags[kind]
		if kind == "Backdrop" && len(item.BackdropImageTags) > 0 {
			tag = item.BackdropImageTags[0]
		}
		if tag != "" {
			return sourceImage(ds, item.ID, kind, tag)
		}
	}
	return ""
}
func sourceUnix(s string) int64 {
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		return 0
	}
	return t.Unix()
}
func sourceSeason(e emby.Item) int {
	if e.ParentIndexNumber == nil {
		return 1
	}
	return *e.ParentIndexNumber
}

var bareEpisode = regexp.MustCompile(`^(第\s*)?\d+\s*[集话期]?$`)

type sourceMirror struct {
	item           string
	meta           store.ArchiveMeta
	pages          []store.Page
	ctime, pubdate int64
}

func mirrorSource(ctx context.Context, client interface {
	Episodes(context.Context, string) ([]emby.Item, error)
}, ds *store.DataSource, regions []store.Region, lib store.DataSourceLibrary, item emby.Item) (*sourceMirror, error) {
	pages := []store.Page{}
	page := func(e emby.Item, part string) store.Page {
		return store.Page{Part: sourceClip(part, 80), Duration: max(1, int(math.Round(float64(e.RunTimeTicks)/1e7))), Media: emby.DSRef(ds.ID, e.ID, e.MediaSources[0])}
	}
	if item.Type == "Series" {
		eps, err := client.Episodes(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		valid := []emby.Item{}
		seasons := map[int]bool{}
		for _, ep := range eps {
			if sourceSeason(ep) > 0 && ep.LocationType != "Virtual" && len(ep.MediaSources) > 0 {
				valid = append(valid, ep)
				seasons[sourceSeason(ep)] = true
			}
		}
		sort.SliceStable(valid, func(i, j int) bool {
			a, b := valid[i], valid[j]
			if sourceSeason(a) != sourceSeason(b) {
				return sourceSeason(a) < sourceSeason(b)
			}
			return a.IndexNumber < b.IndexNumber
		})
		for _, ep := range valid {
			label := fmt.Sprintf("第%d集", ep.IndexNumber)
			if len(seasons) > 1 {
				label = fmt.Sprintf("S%02dE%02d", sourceSeason(ep), ep.IndexNumber)
			}
			if !bareEpisode.MatchString(strings.TrimSpace(ep.Name)) {
				label += " " + ep.Name
			}
			pages = append(pages, page(ep, label))
		}
	} else if len(item.MediaSources) > 0 {
		pages = append(pages, page(item, item.Name))
	}
	if len(pages) == 0 {
		return nil, nil
	}
	tags := []string{}
	if lib.Name != "" {
		tags = append(tags, lib.Name)
	}
	for _, g := range item.Genres[:min(3, len(item.Genres))] {
		if g != "" {
			tags = append(tags, g)
		}
	}
	ctime := sourceUnix(item.DateCreated)
	if ctime == 0 {
		ctime = time.Now().Unix()
	}
	pub := sourceUnix(item.PremiereDate)
	if pub == 0 {
		pub = ctime
	}
	return &sourceMirror{item.ID, store.ArchiveMeta{Title: sourceClip(item.Name, 80), Desc: sourceClip(item.Overview, 2000), TID: autoRegion(regions, lib, item.Genres), Tag: strings.Join(tags, ","), Pic: sourceItemImage(ds.ID, item)}, pages, ctime, pub}, nil
}
func applyArchiveMeta(a *store.Archive, m store.ArchiveMeta, fields []string) {
	for _, f := range fields {
		switch f {
		case "title":
			a.Title = m.Title
		case "desc":
			a.Desc = m.Desc
		case "tid":
			a.TID = m.TID
		case "tag":
			a.Tag = m.Tag
		case "pic":
			a.Pic = m.Pic
		}
	}
}

var archiveFields = []string{"title", "desc", "tid", "tag", "pic"}
var collectionFields = []string{"title", "intro", "cover"}

func unlocked(fields, locked []string) []string {
	out := []string{}
	for _, f := range fields {
		if !slices.Contains(locked, f) {
			out = append(out, f)
		}
	}
	return out
}
func sourceItemOf(ref string) string {
	_, item, _, _, ok := emby.ParseDSRef(ref)
	if ok {
		return item
	}
	return ref
}

// A partial sync is a completed run with recoverable per-library/item errors.
type sourceSyncPartialError struct {
	failures, count int
	last            error
}

func (e *sourceSyncPartialError) Error() string {
	return fmt.Sprintf("部分内容同步失败（%d 处）：%s；已同步 %d 个视频", e.failures, e.last, e.count)
}

func (s *Server) sourceSyncTx(ctx context.Context, ds *store.DataSource, write func(*db.Tx) error) error {
	return s.Store.DB.WithTx(ctx, func(tx *db.Tx) error {
		if err := LockTx(ctx, tx); err != nil {
			return err
		}
		// Shares the deletion lock: no page or collection can be recreated after deletion.
		if _, err := store.DataSourceByID(ctx, tx, ds.ID); err != nil {
			return err
		}
		return write(tx)
	})
}
func sourceSyncProgress(ctx context.Context, tx *db.Tx, ds *store.DataSource) error {
	b, err := json.Marshal(ds.Libraries)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE data_sources SET libraries=?,message=? WHERE id=?", string(b), ds.Message, ds.ID)
	return err
}
func (s *Server) syncDataSource(ctx context.Context, ds *store.DataSource, settings map[string]any) (int, error) {
	// Settings are the launch-time snapshot, including authentication/network policy.
	client, err := s.sourceSyncClient(ctx, ds, settings)
	if err != nil {
		return 0, err
	}
	defer client.close()
	found, err := client.Libraries(ctx)
	if err != nil {
		return 0, err
	}
	libs := []store.DataSourceLibrary{}
	for _, l := range found {
		lib := store.DataSourceLibrary{ID: l.ID, Name: l.Name, Type: l.Type, Enabled: true}
		for _, old := range ds.Libraries {
			if old.ID == l.ID {
				lib.Enabled = old.Enabled
				lib.Items = old.Items
				lib.CollectionID = old.CollectionID
			}
		}
		libs = append(libs, lib)
	}
	ds.Libraries = libs
	if err = s.sourceSyncTx(ctx, ds, func(tx *db.Tx) error { return sourceSyncProgress(ctx, tx, ds) }); err != nil {
		return 0, err
	}
	regions, err := s.Store.ListRegion(ctx, "WHERE COALESCE(parent,0)=0 ORDER BY sort,tid")
	if err != nil {
		return 0, err
	}
	owner, err := s.Store.UserByID(ctx, ds.MID)
	if err != nil {
		return 0, err
	}
	audit := Truthy(settings["auditRequired"]) && !ds.System && owner.Role != "admin"
	seen := map[int64]bool{}
	incomplete := map[string]bool{}
	failures := 0
	var last error
	for i := range ds.Libraries {
		lib := &ds.Libraries[i]
		if !lib.Enabled {
			continue
		}
		aids := []int64{}
		done := 0
		ds.Message = fmt.Sprintf("正在同步「%s」：0 / 0", lib.Name)
		if err = s.sourceSyncTx(ctx, ds, func(tx *db.Tx) error { return sourceSyncProgress(ctx, tx, ds) }); err != nil {
			return len(seen), err
		}
		for start := 0; ; start += sourceSyncPage {
			page, e := client.listPage(ctx, lib.ID, start)
			if ctx.Err() != nil {
				return len(seen), ctx.Err()
			}
			if e != nil {
				if sourceSyncLoginFailed(e) {
					return len(seen), e
				}
				s.Logger.Warn("datasource library listing failed", "id", ds.ID, "library", lib.ID, "error", e)
				failures++
				last = e
				incomplete[lib.ID] = true
				break
			}
			results := sourceSyncMirrors(ctx, client, ds, regions, *lib, page.Items)
			if ctx.Err() != nil {
				return len(seen), ctx.Err()
			}
			for _, result := range results {
				if result.err != nil {
					if sourceSyncLoginFailed(result.err) {
						return len(seen), result.err
					}
					s.Logger.Warn("datasource item failed", "id", ds.ID, "item", result.item, "error", result.err)
					failures++
					last = result.err
				}
			}
			pageAids := []int64{}
			complete := len(page.Items) == 0 || start+sourceSyncPage >= page.TotalRecordCount
			err = s.sourceSyncTx(ctx, ds, func(tx *db.Tx) error {
				pageSeen := map[int64]bool{}
				itemsSeen := map[string]bool{}
				for _, result := range results {
					if itemsSeen[result.item] {
						continue
					}
					itemsSeen[result.item] = true
					aid, e := saveSourceSyncArchive(ctx, tx, ds, lib, result, audit, seen)
					if e != nil {
						return e
					}
					if aid != 0 && !pageSeen[aid] {
						pageAids = append(pageAids, aid)
						pageSeen[aid] = true
					}
				}
				all := append(append([]int64{}, aids...), pageAids...)
				if e := upsertSourceCollection(ctx, tx, ds, lib, all, complete, pageAids); e != nil {
					return e
				}
				lib.Items = len(all)
				ds.Message = fmt.Sprintf("正在同步「%s」：%d / %d", lib.Name, done+len(page.Items), page.TotalRecordCount)
				return sourceSyncProgress(ctx, tx, ds)
			})
			if err != nil {
				return len(seen), err
			}
			for _, aid := range pageAids {
				seen[aid] = true
			}
			aids = append(aids, pageAids...)
			done += len(page.Items)
			if complete {
				break
			}
		}
	}
	if ctx.Err() != nil {
		return len(seen), ctx.Err()
	}
	err = s.sourceSyncTx(ctx, ds, func(tx *db.Tx) error {
		rows, e := tx.QueryContext(ctx, "SELECT aid,origin FROM archives WHERE origin_ds=? AND state<>-100", ds.ID)
		if e != nil {
			return e
		}
		gone := []int64{}
		for rows.Next() {
			var aid int64
			var raw string
			var origin store.ArchiveOrigin
			if e = rows.Scan(&aid, &raw); e != nil {
				rows.Close()
				return e
			}
			if e = json.Unmarshal([]byte(raw), &origin); e != nil {
				rows.Close()
				return e
			}
			if !seen[aid] && !incomplete[origin.Library] {
				gone = append(gone, aid)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, aid := range gone {
			if _, e = tx.ExecContext(ctx, "UPDATE archives SET state=-6 WHERE aid=?", aid); e != nil {
				return e
			}
		}
		rows, e = tx.QueryContext(ctx, "SELECT id,origin_library FROM collections WHERE origin_ds=?", ds.ID)
		if e != nil {
			return e
		}
		remove := []int64{}
		for rows.Next() {
			var id int64
			var library string
			if e = rows.Scan(&id, &library); e != nil {
				rows.Close()
				return e
			}
			live := false
			for _, l := range ds.Libraries {
				if l.ID == library && l.Enabled {
					live = true
				}
			}
			if !live {
				remove = append(remove, id)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range remove {
			if _, e = tx.ExecContext(ctx, "DELETE FROM collections WHERE id=?", id); e != nil {
				return e
			}
		}
		for i := range ds.Libraries {
			if !ds.Libraries[i].Enabled {
				ds.Libraries[i].CollectionID = 0
			}
		}
		return sourceSyncProgress(ctx, tx, ds)
	})
	if err != nil {
		return len(seen), err
	}
	if failures > 0 {
		return len(seen), &sourceSyncPartialError{failures, len(seen), last}
	}
	return len(seen), nil
}

func saveSourceSyncArchive(ctx context.Context, tx *db.Tx, ds *store.DataSource, lib *store.DataSourceLibrary, result sourceSyncMirror, audit bool, seen map[int64]bool) (int64, error) {
	m := result.mirror
	if m == nil && result.err == nil {
		return 0, nil
	}
	var aid int64
	e := tx.QueryRowContext(ctx, "SELECT aid FROM archives WHERE origin_ds=? AND origin_item=?", ds.ID, result.item).Scan(&aid)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return 0, e
	}
	var a *store.Archive
	if e == nil {
		a, e = store.ArchiveFrom(ctx, tx, aid)
		if e != nil {
			return 0, e
		}
		if a.State == -100 || seen[aid] {
			return 0, nil
		}
		if result.err != nil {
			return aid, nil
		}
		if a.State == -6 {
			a.State = 0
		}
	} else {
		if result.err != nil {
			return 0, nil
		}
		if e = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(aid),100000)+1 FROM archives").Scan(&aid); e != nil {
			return 0, e
		}
		aid = max(aid, 100001)
		state := 0
		if audit {
			state = -1
		}
		name := ds.ServerName
		if name == "" {
			name = ds.Name
		}
		a = &store.Archive{AID: aid, BVID: store.AV2BV(aid), MID: ds.MID, Ctime: m.ctime, Pubdate: m.pubdate, State: state, Copyright: 2, Source: "Emby · " + name, Chapters: []store.Chapter{}, Subtitles: []store.Subtitle{}, OriginDS: ds.ID, OriginItem: result.item, Origin: &store.ArchiveOrigin{Type: "emby", Locked: []string{}}}
		// The FK requires a configured region even before the origin update.
		a.TID = m.meta.TID
		if e = store.InsertArchive(ctx, tx, a); e != nil {
			return 0, e
		}
	}
	applyArchiveMeta(a, m.meta, unlocked(archiveFields, a.Origin.Locked))
	a.Origin.Library = lib.ID
	a.Origin.Meta = m.meta
	if e = store.SaveArchiveOrigin(ctx, tx, a); e != nil {
		return 0, e
	}
	old := map[string]int64{}
	for _, p := range a.Pages {
		old[sourceItemOf(p.Media)] = p.CID
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM archive_pages WHERE aid=?", aid); e != nil {
		return 0, e
	}
	used := map[int64]bool{}
	for position, p := range m.pages {
		cid := old[sourceItemOf(p.Media)]
		if cid == 0 || used[cid] {
			cid, e = store.NextID(ctx, tx)
			if e != nil {
				return 0, e
			}
		}
		used[cid] = true
		p.CID = cid
		p.AID = aid
		p.Position = position
		if e = store.InsertPage(ctx, tx, &p); e != nil {
			return 0, e
		}
	}
	return aid, nil
}
func upsertSourceCollection(ctx context.Context, tx *db.Tx, ds *store.DataSource, lib *store.DataSourceLibrary, aids []int64, complete bool, claimed []int64) error {
	name := ds.ServerName
	if name == "" {
		name = ds.Name
	}
	meta := store.CollectionMeta{Title: sourceClip(lib.Name, 40), Intro: "同步自 Emby「" + name + "」的媒体库", Cover: sourceImage(ds.ID, lib.ID, "Primary", "")}
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM collections WHERE origin_ds=? AND origin_library=?", ds.ID, lib.ID).Scan(&id)
	var c *store.Collection
	if errors.Is(err, sql.ErrNoRows) {
		id, err = store.NextID(ctx, tx)
		if err != nil {
			return err
		}
		c = &store.Collection{ID: id, MID: ds.MID, Kind: "season", Ctime: time.Now().Unix(), OriginDS: ds.ID, OriginLibrary: lib.ID, Origin: &store.CollectionOrigin{Type: "emby", Locked: []string{}}}
		if err = store.InsertCollection(ctx, tx, c); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		c, err = store.CollectionFrom(ctx, tx, id)
		if err != nil {
			return err
		}
	}
	if !slices.Contains(c.Origin.Locked, "title") {
		c.Title = meta.Title
	}
	if !slices.Contains(c.Origin.Locked, "intro") {
		c.Intro = meta.Intro
	}
	if !slices.Contains(c.Origin.Locked, "cover") {
		c.Cover = meta.Cover
	}
	c.Origin.Meta = meta
	if err = store.SaveCollectionOrigin(ctx, tx, c); err != nil {
		return err
	}
	// Keep only IDs/positions, never archive metadata, for the collection merge.
	// Unchanged prefix members need no writes on later pages.
	positions := map[int64]int{}
	previous := []int64{}
	rows, e := tx.QueryContext(ctx, "SELECT aid,position FROM collection_items WHERE collection_id=? ORDER BY position", id)
	if e != nil {
		return e
	}
	for rows.Next() {
		var aid int64
		var position int
		if e = rows.Scan(&aid, &position); e != nil {
			rows.Close()
			return e
		}
		positions[aid] = position
		previous = append(previous, aid)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	present := make(map[int64]bool, len(aids))
	for _, aid := range aids {
		present[aid] = true
	}
	for _, aid := range previous {
		if !present[aid] {
			if complete {
				if _, err = tx.ExecContext(ctx, "DELETE FROM collection_items WHERE collection_id=? AND aid=?", id, aid); err != nil {
					return err
				}
			} else {
				aids = append(aids, aid)
			}
		}
	}
	for _, aid := range claimed {
		if _, err = tx.ExecContext(ctx, "DELETE FROM collection_items WHERE aid=? AND collection_id<>? AND collection_id IN (SELECT id FROM collections WHERE kind=?)", aid, id, "season"); err != nil {
			return err
		}
	}
	// The schema also has UNIQUE(collection_id,position). Stage changed positions
	// in a disjoint negative range before assigning the new ordered prefix.
	for i, aid := range aids {
		if old, exists := positions[aid]; exists && old != i {
			if _, err = tx.ExecContext(ctx, "UPDATE collection_items SET position=? WHERE collection_id=? AND aid=?", -i-1, id, aid); err != nil {
				return err
			}
		}
	}
	for i, aid := range aids {
		old, exists := positions[aid]
		if !exists {
			err = store.InsertCollectionItem(ctx, tx, &store.CollectionItem{CollectionID: id, AID: aid, Position: i})
		} else if old != i {
			_, err = tx.ExecContext(ctx, "UPDATE collection_items SET position=? WHERE collection_id=? AND aid=?", i, id, aid)
		}
		if err != nil {
			return err
		}
	}

	lib.CollectionID = id
	return nil
}
