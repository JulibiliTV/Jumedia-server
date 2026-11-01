package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) creatorSubmit(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	title := Trim(c.P["title"])
	var target *store.Archive
	if c.P["aid"] != "" {
		target, err = store.ArchiveFrom(c.Context(), s.Store.DB, ParseGoID(c.P["aid"]))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if target == nil || target.MID != u.MID || target.State == -100 {
			return nil, Fail(-404, "稿件不存在")
		}
		if target.Origin != nil {
			return s.editArchiveOrigin(c, title)
		}
	}
	draft := BoolParam(c.P["draft"])
	tid, subTID := NumberGo(c.P["tid"], 0), Number(c.P["sub_tid"], 0)
	if draft {
		if target != nil && target.State != -20 {
			return nil, Fail(-400, "已投稿的稿件不能存为草稿")
		}
		title = UTF16PrefixNormalized(title, 80)
		if title == "" {
			title = "未命名草稿"
		}
	} else {
		if title == "" || UTF16Length(title) > 80 {
			return nil, Fail(21001, "标题需为 1–80 个字符")
		}
		var exists int
		if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM regions WHERE tid=? AND COALESCE(parent,0)=0", ParseGoID(c.P["tid"])).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			return nil, Fail(21002, "请选择分区")
		}
		if subTID != 0 {
			if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM regions WHERE tid=? AND parent=?", DBID(subTID), DBID(tid)).Scan(&exists); err != nil {
				return nil, err
			}
			if exists == 0 {
				return nil, Fail(21002, "子分区不属于所选分区")
			}
		}
	}
	var videos []*struct {
		Filename string  `json:"filename"`
		Title    string  `json:"title"`
		Duration float64 `json:"duration"`
	}
	videoJSON := c.P["videos"]
	if videoJSON == "" {
		videoJSON = "[]"
	}
	// The mock checks .length before calling .map. Preserve its business
	// error for objects/scalars without a length and its internal error for null.
	var payload any
	if err = json.Unmarshal([]byte(videoJSON), &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, errors.New("null videos")
	}
	if !draft {
		switch value := payload.(type) {
		case []any:
		case string:
			if value == "" {
				return nil, Fail(21003, "请至少上传一个视频")
			}
		case map[string]any:
			if !Truthy(value["length"]) {
				return nil, Fail(21003, "请至少上传一个视频")
			}
		default:
			return nil, Fail(21003, "请至少上传一个视频")
		}
	}
	if err = json.Unmarshal([]byte(videoJSON), &videos); err != nil {
		return nil, err
	}
	if !draft && len(videos) == 0 {
		return nil, Fail(21003, "请至少上传一个视频")
	}
	requestedDtime := NumberWide(c.P["dtime"], 0)
	if !draft && target != nil && target.State == 0 {
		requestedDtime = 0
	}
	now := publicationNow(c.Context()).Unix()
	if !draft && requestedDtime != 0 && (requestedDtime < float64(now+2*3600-60) || requestedDtime > float64(now+15*86400)) {
		return nil, Fail(21004, "定时发布时间需在 2 小时后、15 天内")
	}
	dtime := int64(requestedDtime)
	var pubdate int64
	chapters := []store.Chapter{}
	chapterJSON := c.P["chapters"]
	if chapterJSON == "" {
		chapterJSON = "[]"
	}
	if err = json.Unmarshal([]byte(chapterJSON), &chapters); err != nil {
		return nil, err
	}
	var aid int64
	var bvid string
	var state int
	err = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		ctx := c.Context()
		if err := LockTx(ctx, tx); err != nil {
			return err
		}
		var setting string
		if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", "auditRequired").Scan(&setting); err != nil {
			return err
		}
		var audit any
		if err := json.Unmarshal([]byte(setting), &audit); err != nil {
			return err
		}

		oldPages := map[string]int64{}
		editing := c.P["aid"] != ""
		oldState := 0
		var oldPubdate int64
		if editing {
			if err := tx.QueryRowContext(ctx, "SELECT aid,bvid,state,pubdate FROM archives WHERE aid=? AND mid=? AND state<>-100", ParseGoID(c.P["aid"]), u.MID).Scan(&aid, &bvid, &oldState, &oldPubdate); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return Fail(-404, "稿件不存在")
				}
				return err
			}
			if draft && oldState != -20 {
				return Fail(-400, "已投稿的稿件不能存为草稿")
			}
			rows, err := tx.QueryContext(ctx, "SELECT cid,media FROM archive_pages WHERE aid=? ORDER BY position", aid)
			if err != nil {
				return err
			}
			for rows.Next() {
				var cid int64
				var media string
				if err = rows.Scan(&cid, &media); err != nil {
					rows.Close()
					return err
				}
				if _, ok := oldPages[media]; !ok {
					oldPages[media] = cid
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		} else {
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(aid),0)+1 FROM archives").Scan(&aid); err != nil {
				return err
			}
			bvid = store.AV2BV(aid)
		}
		if !draft && editing && oldState == 0 {
			dtime = 0
		}
		state = 0
		if draft {
			state = -20
		} else if Truthy(audit) && u.Role != "admin" {
			state = -1
		} else if dtime != 0 {
			state = -40
		}
		if draft {
			pubdate = oldPubdate
		} else if dtime != 0 {
			pubdate = dtime
		} else if editing && oldState != -20 {
			pubdate = oldPubdate
		} else {
			pubdate = now
		}
		pic := c.P["cover"]
		if editing {
			if _, err := tx.ExecContext(ctx, "UPDATE archives SET title=?,description=?,tid=?,sub_tid=?,tag=?,pic=?,copyright=?,source=?,chapters=?,state=?,reason=? WHERE aid=?", title, c.P["desc"], int64(tid), int64(subTID), c.P["tag"], pic, int(NumberGo(c.P["copyright"], 1)), c.P["source"], chapterJSON, state, "", aid); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM archive_pages WHERE aid=?", aid); err != nil {
				return err
			}
		} else {
			a := &store.Archive{AID: aid, BVID: bvid, MID: u.MID, Title: title, Desc: c.P["desc"], TID: int64(tid), SubTID: int64(subTID), Tag: c.P["tag"], Pic: pic, Copyright: int(NumberGo(c.P["copyright"], 1)), Source: c.P["source"], Chapters: chapters, Subtitles: []store.Subtitle{}, State: state, Ctime: now, Pubdate: pubdate}
			if err := store.InsertArchive(ctx, tx, a); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE archives SET dtime=?,no_disturbance=?,no_tv=?,pubdate=? WHERE aid=?", dtime, store.BoolInt(BoolParam(c.P["no_disturbance"])), store.BoolInt(BoolParam(c.P["no_tv"])), pubdate, aid); err != nil {
			return err
		}
		used := map[int64]bool{}
		for i, v := range videos {
			if v == nil {
				return errors.New("null video")
			}
			cid := oldPages[v.Filename]
			// A CID is a primary key. Repeated media still gets distinct pages.
			if cid == 0 || used[cid] {
				var err error
				cid, err = store.NextID(ctx, tx)
				if err != nil {
					return err
				}
			}
			used[cid] = true
			part := v.Title
			if part == "" {
				part = title
			}
			p := &store.Page{CID: cid, AID: aid, Position: i, Part: part, Media: v.Filename, Duration: int(math.Max(1, math.Floor(v.Duration+0.5)))}
			if err := store.InsertPage(ctx, tx, p); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM collection_items WHERE aid=? AND collection_id IN (SELECT id FROM collections WHERE kind=?)", aid, "season"); err != nil {
			return err
		}
		var collectionID int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM collections WHERE id=? AND mid=?", ParseGoID(c.P["season_id"]), u.MID).Scan(&collectionID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var position int
			if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(position),-1)+1 FROM collection_items WHERE collection_id=?", collectionID).Scan(&position); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO collection_items (collection_id,aid,position) VALUES (?,?,?) ON CONFLICT(collection_id,aid) DO NOTHING", collectionID, aid, position); err != nil {
				return err
			}
		}
		if draft {
			return nil
		}
		action := "投稿"
		if editing && oldState != -20 {
			action = "编辑稿件"
		}
		return s.LogTx(ctx, tx, u.MID, action, title)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"aid": aid, "bvid": bvid, "state": state}, nil
}

func (s *Server) creatorRow(c *Ctx, a store.Archive) (map[string]any, error) {
	tname, err := s.Store.RegionName(c.Context(), a.TID)
	if err != nil {
		return nil, err
	}
	if a.Pages == nil {
		a.Pages, err = s.Store.ListPage(c.Context(), "WHERE aid=? ORDER BY position", a.AID)
		if err != nil {
			return nil, err
		}
	}
	var seasonID int64
	err = s.Store.DB.QueryRowContext(c.Context(), "SELECT id FROM collections WHERE kind=? AND id IN (SELECT collection_id FROM collection_items WHERE aid=?) ORDER BY id LIMIT 1", "season", a.AID).Scan(&seasonID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	videos := []map[string]any{}
	for _, p := range a.Pages {
		videos = append(videos, map[string]any{"cid": p.CID, "title": p.Part, "duration": p.Duration, "filename": p.Media})
	}
	origin, err := s.archiveOriginJSON(c.Context(), a)
	if err != nil {
		return nil, err
	}
	publication, err := s.archivePublication(c, a.AID)
	if err != nil {
		return nil, err
	}
	states := map[int]string{-20: "草稿", -40: "定时发布", -6: "源已移除", 0: "已通过", -1: "审核中", -2: "已退回", -100: "已删除"}
	return map[string]any{
		"Archive": map[string]any{"aid": a.AID, "bvid": a.BVID, "title": a.Title, "cover": a.Pic, "desc": a.Desc, "tid": a.TID, "sub_tid": a.SubTID, "tname": tname, "tag": a.Tag, "state": a.State, "state_desc": states[a.State], "reject_reason": a.Reason, "ptime": a.Pubdate, "ctime": a.Ctime, "duration": render.TotalDuration(a), "copyright": a.Copyright, "source": a.Source, "dtime": publication.Dtime, "no_disturbance": publication.NoDisturbance, "no_tv": publication.NoTV},
		"origin":  origin, "stat": a.Stat, "Videos": videos, "chapters": a.Chapters, "season_id": seasonID,
	}, nil
}

func (s *Server) creatorArchives(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	mine, err := s.Store.ListArchive(c.Context(), "WHERE mid=? AND state<>? ORDER BY ctime DESC,aid DESC", u.MID, -100)
	if err != nil {
		return nil, err
	}
	class := map[string]int{"pubed": 0, "is_pubing": 0, "not_pubed": 0, "missing": 0, "timer": 0, "draft": 0}
	states := map[string]int{"pubed": 0, "is_pubing": -1, "not_pubed": -2, "missing": -6, "timer": -40, "draft": -20}
	wanted, filter := states[c.P["status"]]
	list := []store.Archive{}
	for _, a := range mine {
		if (c.P["origin"] == "upload" && a.Origin != nil) || (c.P["origin"] == "datasource" && a.Origin == nil) {
			continue
		}
		for name, state := range states {
			if a.State == state {
				class[name]++
			}
		}
		if (!filter || a.State == wanted) && strings.Contains(strings.ToLower(a.Title), strings.ToLower(c.P["keyword"])) {
			list = append(list, a)
		}
	}
	pn, ps := NumberGo(c.P["pn"], 1), NumberGo(c.P["ps"], 10)
	rows := []map[string]any{}
	for _, a := range Page(list, pn, ps) {
		row, err := s.creatorRow(c, a)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return map[string]any{"class": class, "arc_audits": rows, "page": map[string]any{"pn": pn, "ps": ps, "count": len(list)}}, nil
}

func (s *Server) creatorView(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	// Query with the original numeric value, so fractional IDs do not alias an ID.
	var aid int64
	err = s.Store.DB.QueryRowContext(c.Context(), "SELECT aid FROM archives WHERE aid=? AND mid=? AND state<>-100", ParseGoID(c.P["aid"]), u.MID).Scan(&aid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(-404, "稿件不存在")
	}
	if err != nil {
		return nil, err
	}
	a, err := s.Store.ArchiveByID(c.Context(), aid)
	if err != nil {
		return nil, err
	}
	return s.creatorRow(c, *a)
}

func (s *Server) creatorDelete(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	err = s.Store.DB.WithTx(c.Context(), func(tx *db.Tx) error {
		ctx := c.Context()
		if err := LockTx(ctx, tx); err != nil {
			return err
		}
		var aid int64
		var title string
		if err := tx.QueryRowContext(ctx, "SELECT aid,title FROM archives WHERE aid=? AND mid=?", ParseGoID(c.P["aid"]), u.MID).Scan(&aid, &title); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Fail(-404, "稿件不存在")
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE archives SET state=? WHERE aid=?", -100, aid); err != nil {
			return err
		}
		return s.LogTx(ctx, tx, u.MID, "删除稿件", title)
	})
	return nil, err
}

func (s *Server) creatorStat(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	mine, err := s.Store.ListArchive(c.Context(), "WHERE mid=? AND state=? ORDER BY aid", u.MID, 0)
	if err != nil {
		return nil, err
	}
	fans, err := s.Store.FollowerCount(c.Context(), u.MID)
	if err != nil {
		return nil, err
	}
	var total store.ArchiveStat
	for _, a := range mine {
		total.View += a.Stat.View
		total.Like += a.Stat.Like
		total.Coin += a.Stat.Coin
		total.Favorite += a.Stat.Favorite
		total.Reply += a.Stat.Reply
		total.Danmaku += a.Stat.Danmaku
		total.Share += a.Stat.Share
	}
	now := publicationNow(c.Context())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayView := func(i int) float64 {
		return math.Floor(float64(total.View) / 400 * (0.6 + math.Abs(math.Sin(float64(i)*1.7+float64(u.MID)))*0.9))
	}
	var prevView float64
	for i := 0; i < 14; i++ {
		prevView += dayView(i - 14)
	}
	var replyToday int64
	if err = s.Store.DB.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM replies WHERE ctime>=? AND oid IN (SELECT aid FROM archives WHERE mid=? AND state=0)", today.Unix(), u.MID).Scan(&replyToday); err != nil {
		return nil, err
	}
	series := []map[string]any{}
	for i := 0; i < 14; i++ {
		d := today.Add(-time.Duration(13-i) * 24 * time.Hour)
		series = append(series, map[string]any{"date": d.Format("1-2"), "view": dayView(i), "fans": math.Floor(1 + math.Abs(math.Cos(float64(i)+float64(u.MID)))*6)})
	}
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].Stat.View > mine[j].Stat.View })
	top := []map[string]any{}
	for _, a := range mine[:min(5, len(mine))] {
		a.Pages, err = s.Store.ListPage(c.Context(), "WHERE aid=? ORDER BY position", a.AID)
		if err != nil {
			return nil, err
		}
		row, err := s.Render.ArcView(c.Context(), a)
		if err != nil {
			return nil, err
		}
		top = append(top, row)
	}
	return map[string]any{"total_fans": fans, "total_click": total.View, "total_like": total.Like, "total_coin": total.Coin, "total_fav": total.Favorite, "total_reply": total.Reply, "total_dm": total.Danmaku, "total_share": total.Share, "incr_fans": 3 + u.MID%20, "incr_click": math.Floor(float64(total.View) * 0.012), "archive_count": len(mine), "coins": u.Coins, "series": series, "top": top, "prev_view": prevView, "reply_today": replyToday}, nil
}
