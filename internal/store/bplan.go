package store

import (
	"context"
	"fmt"
	"strconv"

	"jumedia-server/internal/db"
)

// Keep the order within each parent identical to mock/db.ts SUB_REGIONS.
var bplanSubRegions = []struct {
	parent int64
	names  []string
}{
	{1001, []string{"影视杂谈", "影视剪辑", "影视整活", "小剧场", "预告·资讯", "短片"}},
	{1002, []string{"综艺", "明星综合", "粉丝创作"}},
	{1003, []string{"原创音乐", "翻唱", "演奏", "音乐现场"}},
	{1004, []string{"宅舞", "街舞", "舞蹈教程"}},
	{1005, []string{"短片·手书", "MAD·AMV", "动画综合"}},
	{1007, []string{"鬼畜调教", "音MAD", "人力VOCALOID"}},
	{1008, []string{"单机游戏", "网络游戏", "游戏杂谈"}},
	{1010, []string{"科学科普", "社科·人文", "财经商业"}},
	{1012, []string{"数码", "软件应用", "计算机技术"}},
	{1013, []string{"赛车", "改装玩车", "购车攻略"}},
	{1014, []string{"美妆护肤", "穿搭", "时尚潮流"}},
	{1018, []string{"篮球", "足球", "健身"}},
	{1020, []string{"美食制作", "美食侦探", "美食测评"}},
	{1024, []string{"喵星人", "汪星人", "动物综合"}},
	{1030, []string{"手工", "绘画", "出行"}},
	{1031, []string{"生活经验", "技能分享"}},
}

func bplanHash(v string) uint32 {
	// Inputs are decimal IDs, or "ex" + a decimal ID: all ASCII.
	h := uint32(2166136261)
	for i := 0; i < len(v); i++ {
		h = (h ^ uint32(v[i])) * 16777619
	}
	return h
}
func bplanCopyright(v int) int {
	if v == 0 {
		return 1
	}
	return v
}

// ExtendBPlan runs only for migration 007 or freshly seeded data, inside the
// caller's transaction. Reopening an upgraded store must not reassign user edits.
func ExtendBPlan(ctx context.Context, tx *db.Tx) error {
	for _, group := range bplanSubRegions {
		for i, name := range group.names {
			if _, err := tx.ExecContext(ctx, "INSERT INTO regions(tid,name,sort,visible,parent) SELECT ?,?,?,1,? WHERE EXISTS (SELECT 1 FROM regions WHERE tid=? AND COALESCE(parent,0)=0) ON CONFLICT(tid) DO NOTHING", group.parent*100+int64(i)+1, name, i, group.parent, group.parent); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT tid,parent FROM regions WHERE parent IS NOT NULL AND parent<>0 AND visible=1 ORDER BY sort,tid")
	if err != nil {
		return err
	}
	subs := map[int64][]int64{}
	for rows.Next() {
		var tid, parent int64
		if err = rows.Scan(&tid, &parent); err != nil {
			rows.Close()
			return err
		}
		subs[parent] = append(subs[parent], tid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, "SELECT aid,tid FROM archives ORDER BY aid")
	if err != nil {
		return err
	}
	type pair struct{ id, tid int64 }
	arcs := []pair{}
	for rows.Next() {
		var a pair
		if err = rows.Scan(&a.id, &a.tid); err != nil {
			rows.Close()
			return err
		}
		arcs = append(arcs, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range arcs {
		var sub int64
		if list := subs[a.tid]; len(list) > 0 {
			sub = list[int(bplanHash(strconv.FormatInt(a.id, 10)))%len(list)]
		}
		if _, err = tx.ExecContext(ctx, "UPDATE archives SET sub_tid=? WHERE aid=?", sub, a.id); err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, "SELECT season_id FROM seasons ORDER BY season_id")
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		copyright := 1
		if bplanHash("ex"+strconv.FormatInt(id, 10))%4 == 0 {
			copyright = 3
		}
		if _, err = tx.ExecContext(ctx, "UPDATE seasons SET copyright=? WHERE season_id=?", copyright, id); err != nil {
			return err
		}
	}
	return bplanChannelBanners(ctx, tx)
}
func bplanChannelBanners(ctx context.Context, tx *db.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM banners WHERE position=?", "channel").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var id int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM banners").Scan(&id); err != nil {
		return err
	}
	banners := []Banner{}
	for _, kind := range []int{2, 3, 5, 7} {
		rows, err := tx.QueryContext(ctx, "SELECT season_id,title,horizontal FROM seasons WHERE state=0 AND season_type=? AND horizontal<>'' ORDER BY season_id LIMIT 3", kind)
		if err != nil {
			return err
		}
		index := 0
		for rows.Next() {
			var sid int64
			var title, img string
			if err = rows.Scan(&sid, &title, &img); err != nil {
				rows.Close()
				return err
			}
			loc := int64(10 + kind)
			id++
			banners = append(banners, Banner{ID: id, Title: title, Img: img, URL: fmt.Sprintf("/bangumi/play/ss%d", sid), Position: "channel", Sort: index, SeasonID: &sid, Loc: &loc})
			index++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT parent FROM regions WHERE parent IS NOT NULL AND parent<>0 ORDER BY parent")
	if err != nil {
		return err
	}
	parents := []int64{}
	for rows.Next() {
		var p int64
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		parents = append(parents, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, parent := range parents {
		rows, err := tx.QueryContext(ctx, "SELECT bvid,title,pic FROM archives WHERE state=0 AND tid=? ORDER BY aid LIMIT 3", parent)
		if err != nil {
			return err
		}
		index := 0
		for rows.Next() {
			var bv, title, img string
			if err = rows.Scan(&bv, &title, &img); err != nil {
				rows.Close()
				return err
			}
			loc := parent
			id++
			banners = append(banners, Banner{ID: id, Title: title, Img: img, URL: "/video/" + bv, Position: "channel", Sort: index, Loc: &loc})
			index++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for i := range banners {
		if err := InsertBanner(ctx, tx, &banners[i]); err != nil {
			return err
		}
	}
	// Prevent a future global nextID from colliding with migrated banners.
	_, err = tx.ExecContext(ctx, "UPDATE ids SET value=CASE WHEN value<? THEN ? ELSE value END WHERE name=?", id, id, "global")
	return err
}
