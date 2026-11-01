// Package seed ports the mock catalogue and its deterministic random generator.
package seed

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

	"jumedia-server/internal/db"
	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
	"unicode/utf16"
)

func rng(seed uint32) func() float64 {
	if seed == 0 {
		seed = 1
	}
	return func() float64 { seed = seed*1664525 + 1013904223; return float64(seed) / 4294967296 }
}

// Seed only ensures base rows. Demo generation requires an explicit import.
func Seed(ctx context.Context, s *store.Store) error {
	return s.DB.WithTx(ctx, func(tx *db.Tx) error { return BaseTx(ctx, tx) })
}
func Reset(ctx context.Context, s *store.Store) error {
	return s.DB.WithTx(ctx, func(tx *db.Tx) error { return ResetTx(ctx, tx) })
}

// GenerateDemoTx writes demo content inside an existing transaction. Production
// callers must use ImportTx, which checks initial state and reserves an ID range.
// Offline contract fixtures also use this generator with their fixed ID origin.
func GenerateDemoTx(ctx context.Context, tx *db.Tx, t0 int64, catalog emby.Catalog, offset int64) error {
	r := rng(20261029)
	seq := offset + int64(1000)
	id := func() int64 { seq++; return seq }
	demoHash, err := bcrypt.GenerateFromPassword([]byte("demo123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	backdrops := catalogBackdrops(catalog)
	users := []store.User{}
	for i, p := range people {
		pw := demoHash
		role := "user"

		u := store.User{MID: offset + int64(i+1), Username: p[0], Password: string(pw), Uname: p[1], Face: fmt.Sprintf("/mock/avatars/u%d.png", i+1), Sign: p[2], Sex: []string{"男", "女", "保密"}[i%3], Birthday: "1998-06-12", Email: p[0] + "@jumedia.local", Level: 6 - i%4, Exp: 12000 - i*700, Coins: float64(120 + i*13), Role: role, Ctime: t0 - int64(86400*(400-i*20)), TopPhoto: pickString(backdrops, int64(i)), VIP: store.BoolInt(i%4 == 0)}
		if offset > 0 {
			u.Username = fmt.Sprintf("demo_%d_%s", u.MID, u.Username)
			u.Email = u.Username + "@jumedia.local"
		}
		for {
			var n int
			if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username=?", u.Username).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				break
			}
			u.Username += "_"
			u.Email = u.Username + "@jumedia.local"
		}
		if err = store.InsertUser(ctx, tx, &u); err != nil {
			return err
		}
		users = append(users, u)
	}
	cid := offset + int64(50000)
	archives := []store.Archive{}
	clips := clipsFrom(catalog)
	for i, clip := range clips[:min(40, len(clips))] {
		aid := offset + int64(100100+i*7)
		view := int64(math.Floor(2000 + r()*r()*1800000))
		pub := t0 - int64(r()*86400*60) - int64(i*3600)
		pages := []store.Page{}
		total := 0
		for j, p := range clip.Pages {
			cid++
			p.CID = cid
			p.AID = aid
			p.Position = j
			pages = append(pages, p)
			total += p.Duration
		}
		a := store.Archive{AID: aid, BVID: store.AV2BV(aid), MID: offset + int64(i%11+2), Title: clip.Title, Desc: clip.Desc, TID: clip.TID, Tag: clip.Tag, Pic: clip.Pic, Ctime: pub - 600, Pubdate: pub, Copyright: 2, Source: "转载自 Jumedia 演示媒体库", Pages: pages, Chapters: []store.Chapter{}, Subtitles: []store.Subtitle{}, Stat: store.ArchiveStat{View: view, Danmaku: int64(float64(view) * .004), Favorite: int64(float64(view) * .02), Coin: int64(float64(view) * .01), Share: int64(float64(view) * .002), Like: int64(float64(view) * .05)}}
		if i%4 == 0 && total > 120 {
			a.Chapters = []store.Chapter{{From: 0, To: math.Floor(float64(total) * .1), Content: "开场"}, {From: math.Floor(float64(total) * .1), To: math.Floor(float64(total) * .9), Content: "正片"}, {From: math.Floor(float64(total) * .9), To: float64(total), Content: "片尾"}}
		}
		archives = append(archives, a)
	}
	if len(clips) > 0 {
		clip := clips[0]
		if len(clips) > 40 {
			clip = clips[40]
		}
		a := archives[0]
		a.AID = offset + 100999
		a.BVID = store.AV2BV(a.AID)
		a.Title = prefix("待审核："+clip.Title, 80)
		a.Desc = clip.Desc
		a.TID = clip.TID
		a.Pic = clip.Pic
		a.State = -1
		a.MID = offset + 3
		a.Pubdate = t0 - 1200
		a.Ctime = t0 - 1800
		a.Stat = store.ArchiveStat{}
		a.Pages = []store.Page{}
		a.Chapters = []store.Chapter{}
		a.Subtitles = []store.Subtitle{}
		for j, p := range clip.Pages {
			cid++
			p.CID = cid
			p.AID = a.AID
			p.Position = j
			a.Pages = append(a.Pages, p)
		}
		archives = append(archives, a)
	}
	for _, a := range archives {
		if err = store.InsertArchive(ctx, tx, &a); err != nil {
			return err
		}
		for _, p := range a.Pages {
			if err = store.InsertPage(ctx, tx, &p); err != nil {
				return err
			}
		}
	}
	for _, u := range users[1:5] {
		mine := []store.Archive{}
		for _, a := range archives {
			if a.MID == u.MID && a.State == 0 {
				mine = append(mine, a)
			}
		}
		if len(mine) < 2 {
			continue
		}
		for k, kind := range []string{"season", "series"} {
			c := store.Collection{ID: id(), MID: u.MID, Kind: kind, Title: u.Uname + "的精选合集", Intro: "按发布时间整理的系列作品", Cover: mine[0].Pic, Ctime: t0 - 86400*10}
			items := mine
			if k == 1 {
				c.Title = "收藏系列"
				c.Intro = ""
				c.Cover = mine[len(mine)-1].Pic
				c.Ctime = t0 - 86400*5
				items = mine[:2]
			}
			if err = store.InsertCollection(ctx, tx, &c); err != nil {
				return err
			}
			for i, a := range items {
				if err = store.InsertCollectionItem(ctx, tx, &store.CollectionItem{CollectionID: c.ID, AID: a.AID, Position: i}); err != nil {
					return err
				}
			}
		}
	}
	seasons := catalogSeasons(catalog, t0, r, &cid)
	for i := range seasons {
		x := &seasons[i]
		x.SeasonID += offset
		x.MediaID += offset
		for j := range x.Episodes {
			e := &x.Episodes[j]
			e.EpID += offset
			e.AID += offset
			e.SeasonID += offset
		}
	}
	for _, season := range seasons {
		if err = store.InsertSeason(ctx, tx, &season); err != nil {
			return err
		}
		for _, ep := range season.Episodes {
			if err = store.InsertEpisode(ctx, tx, &ep); err != nil {
				return err
			}
		}
	}
	rooms := catalogRooms(catalog, t0, r)
	for i := range rooms {
		rooms[i].RoomID += offset
		rooms[i].UID += offset
	}
	for _, v := range rooms {
		if err = store.InsertLiveRoom(ctx, tx, &v); err != nil {
			return err
		}
	}
	for _, a := range archives[:min(24, len(archives))] {
		n := 3 + int(r()*5)
		for k := 0; k < n; k++ {
			rpid := id()
			v := store.Reply{RPID: rpid, OID: a.AID, MID: offset + int64(r()*float64(len(users))) + 1, Message: comments[int(r()*float64(len(comments)))], Ctime: a.Pubdate + int64(r()*86400*3)}
			if err = store.InsertReply(ctx, tx, &v); err != nil {
				return err
			}
			for _, u := range users {
				if r() < .3 {
					if err = store.InsertReplyReaction(ctx, tx, &store.ReplyReaction{RPID: rpid, MID: u.MID, Reaction: 1}); err != nil {
						return err
					}
				}
			}
			if r() < .45 {
				for j := 0; j < 1+int(r()*3); j++ {
					v := store.Reply{RPID: id(), OID: a.AID, MID: offset + int64(r()*float64(len(users))) + 1, Root: rpid, Parent: rpid, Message: []string{"同意！", "哈哈哈哈确实", "+1", "我也想知道", "感谢解答"}[int(r()*5)], Ctime: a.Pubdate + 86400*4 + int64(j*60)}
					if err = store.InsertReply(ctx, tx, &v); err != nil {
						return err
					}
				}
			}
		}
	}
	// Match mock extend18 after all root replies and children have been written.
	if _, err = tx.ExecContext(ctx, "UPDATE archives SET stat_reply=(SELECT COUNT(*) FROM replies WHERE replies.oid=archives.aid)"); err != nil {
		return err
	}
	for _, room := range rooms {
		for k := 0; k < 18; k++ {
			v := store.LiveMessage{ID: id(), RoomID: room.RoomID, UID: offset + int64(r()*float64(len(users))) + 1, Text: LiveChatter[int(r()*float64(len(LiveChatter)))], TS: t0 - int64((18-k)*20)}
			if err = store.InsertLiveMessage(ctx, tx, &v); err != nil {
				return err
			}
		}
	}
	price := 30.0
	if len(rooms) > 0 {
		if err = store.InsertLiveMessage(ctx, tx, &store.LiveMessage{ID: id(), RoomID: rooms[0].RoomID, UID: offset + 4, Text: "这部太好看了，主播再放一遍！", TS: t0 - 60, Price: &price}); err != nil {
			return err
		}
	}
	for i, pair := range [][2]int64{{1, 2}, {1, 3}, {1, 4}, {1, 6}, {2, 1}, {3, 1}, {3, 2}, {4, 1}, {5, 1}, {6, 2}} {
		if err = store.InsertRelation(ctx, tx, &store.Relation{MID: offset + pair[0], FID: offset + pair[1], Mtime: t0 - int64(i*3600)}); err != nil {
			return err
		}
	}
	for _, u := range users {
		f := store.FavFolder{ID: offset + 9000 + (u.MID-offset)*10, MID: u.MID, Title: "默认收藏夹", Ctime: u.Ctime}
		if err = store.InsertFavFolder(ctx, tx, &f); err != nil {
			return err
		}
		selected := []store.Archive{}
		for _, a := range archives {
			if r() < .12 {
				selected = append(selected, a)
			}
		}
		for _, a := range selected {
			if err = store.InsertFavItem(ctx, tx, &store.FavItem{FolderID: f.ID, AID: a.AID, Mtime: t0 - int64(r()*86400*20)}); err != nil {
				return err
			}
		}
	}
	if err = store.InsertFavFolder(ctx, tx, &store.FavFolder{ID: offset + 9011, MID: offset + 1, Title: "周末追剧", Intro: "慢慢看", Ctime: t0 - 86400*40, Attr: 1}); err != nil {
		return err
	}
	for _, a := range archives {
		if a.TID == 1001 {
			if err = store.InsertFavItem(ctx, tx, &store.FavItem{FolderID: offset + 9011, AID: a.AID, Mtime: t0 - 86400}); err != nil {
				return err
			}
		}
	}
	for i, a := range archives[:min(6, len(archives))] {
		progress := min(a.Pages[0].Duration-5, 60+i*30)
		if i == 0 {
			progress = -1
		}
		if err = store.InsertHistory(ctx, tx, &store.History{MID: offset + 1, AID: a.AID, CID: a.Pages[0].CID, ViewAt: t0 - int64(i*5400), Progress: progress}); err != nil {
			return err
		}
	}
	for i, a := range archives[min(8, len(archives)):min(12, len(archives))] {
		if err = store.InsertToView(ctx, tx, &store.ToView{MID: offset + 1, AID: a.AID, AddAt: t0 - int64(i*7200)}); err != nil {
			return err
		}
	}
	follows := []store.Season{}
	for _, types := range [][]int{{1, 4}, {5, 7}} {
		for _, season := range seasons {
			if season.SeasonType == types[0] || season.SeasonType == types[1] {
				follows = append(follows, season)
				break
			}
		}
	}
	for i, season := range follows {
		if err = store.InsertBangumiFollow(ctx, tx, &store.BangumiFollow{MID: offset + 1, SeasonID: season.SeasonID, Status: 2 - i, Mtime: t0 - int64((i+1)*3600)}); err != nil {
			return err
		}
	}
	for i, a := range archives[:min(4, len(archives))] {
		if err = store.InsertBanner(ctx, tx, &store.Banner{ID: offset + int64(1+i), Title: a.Title, Img: bannerImage(backdrops, i, a.Pic), URL: "/video/" + a.BVID, Position: "home", Sort: i}); err != nil {
			return err
		}
	}
	bannerSeasons := []store.Season{}
	for _, types := range [][]int{{1, 4}, {5, 7, 2}} {
		for _, season := range seasons {
			for _, typ := range types {
				if season.SeasonType == typ && season.Horizontal != "" {
					bannerSeasons = append(bannerSeasons, season)
				}
			}
		}
	}
	for i, s := range bannerSeasons[:min(4, len(bannerSeasons))] {
		sid := s.SeasonID
		if err = store.InsertBanner(ctx, tx, &store.Banner{ID: offset + int64(11+i), Title: s.Title, Img: s.Horizontal, URL: fmt.Sprintf("/bangumi/play/ss%d", sid), Position: "anime", Sort: i, SeasonID: &sid}); err != nil {
			return err
		}
	}
	if offset > 0 {
		seq = max(seq, offset+30000000)
	}
	_, err = tx.ExecContext(ctx, "UPDATE ids SET value=? WHERE name=?", seq, "global")
	if err != nil {
		return err
	}
	if err := seedBPlan(ctx, tx, t0, offset); err != nil {
		return err
	}
	if err := demoFanMedals(ctx, tx, t0, offset); err != nil {
		return err
	}
	if err := store.ExtendPublication(ctx, tx, time.Unix(t0, 0)); err != nil {
		return err
	}
	if err := demoLive(ctx, tx, offset); err != nil {
		return err
	}
	if err := seedServiceSettings(ctx, tx); err != nil {
		return err
	}
	return seedServerSettings(ctx, tx)
}

// SeedDanmaku generates a CID only once, transactionally, like db.ts.
func SeedDanmaku(ctx context.Context, s *store.Store, cid int64, duration int) error {
	return s.DB.WithTx(ctx, func(tx *db.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM archive_pages WHERE cid=? AND media LIKE 'emby:%') + (SELECT COUNT(*) FROM episodes WHERE cid=? AND media LIKE 'emby:%')", cid, cid).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		rows, err := tx.QueryContext(ctx, "SELECT mid FROM users WHERE role=? ORDER BY mid", "user")
		if err != nil {
			return err
		}
		var mids []int64
		for rows.Next() {
			var mid int64
			if err = rows.Scan(&mid); err != nil {
				rows.Close()
				return err
			}
			mids = append(mids, mid)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(mids) == 0 {
			return nil
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO seeded_danmaku(cid) VALUES (?) ON CONFLICT(cid) DO NOTHING", cid)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected == 0 {
			return err
		}
		r := rng(hash(strconv.FormatInt(cid, 10)))
		span := min(duration, 24)
		for i := 0; i < 46; i++ {
			mode := 1
			if r() >= .86 {
				if r() < .5 {
					mode = 5
				} else {
					mode = 4
				}
			}
			id, err := store.NextID(ctx, tx)
			if err != nil {
				return err
			}
			d := store.Danmaku{ID: id, CID: cid, Progress: int(r() * float64(span) * 1000), Mode: mode, Fontsize: 25, Color: colors[int(r()*float64(len(colors)))], MID: mids[int(r()*float64(len(mids)))], Content: LiveChatter[int(r()*float64(len(LiveChatter)))], Ctime: time.Now().Unix() - 86400, Weight: 1 + int(r()*10)}
			if err = store.InsertDanmaku(ctx, tx, &d); err != nil {
				return err
			}
		}
		return nil
	})
}

func hash(text string) uint32 {
	h := uint32(2166136261)
	for _, c := range utf16.Encode([]rune(text)) {
		h = (h ^ uint32(c)) * 16777619
	}
	return h
}
