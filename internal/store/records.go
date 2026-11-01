package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"jumedia-server/internal/db"
)

type scanner interface{ Scan(...any) error }

const userColumns = "mid,username,password,uname,face,sign,sex,birthday,email,level,exp,coins,role,status,ctime,top_photo,vip,notice"

func scanUser(row scanner) (*User, error) {
	var v User
	if err := row.Scan(&v.MID, &v.Username, &v.Password, &v.Uname, &v.Face, &v.Sign, &v.Sex, &v.Birthday, &v.Email, &v.Level, &v.Exp, &v.Coins, &v.Role, &v.Status, &v.Ctime, &v.TopPhoto, &v.VIP, &v.Notice); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertUser(ctx context.Context, q db.Querier, v *User) error {
	_, err := q.ExecContext(ctx, "INSERT INTO users (mid,username,password,uname,face,sign,sex,birthday,email,level,exp,coins,role,status,ctime,top_photo,vip,notice) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", v.MID, v.Username, v.Password, v.Uname, v.Face, v.Sign, v.Sex, v.Birthday, v.Email, v.Level, v.Exp, v.Coins, v.Role, v.Status, v.Ctime, v.TopPhoto, v.VIP, v.Notice)
	return err
}
func (s *Store) ListUser(ctx context.Context, clause string, args ...any) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+userColumns+" FROM users "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		v, e := scanUser(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const sessionColumns = "position,token,csrf,access,refresh,mid,ctime"

func scanSession(row scanner) (*Session, error) {
	var v Session
	if err := row.Scan(&v.Position, &v.Token, &v.CSRF, &v.Access, &v.Refresh, &v.MID, &v.Ctime); err != nil {
		return nil, err
	}
	return &v, nil
}

// InsertSession assigns an independent sequence to preserve Mock Array.find
// credential precedence, even when multiple sessions share the same ctime.
func InsertSession(ctx context.Context, q db.Querier, v *Session) error {
	if database, ok := q.(*db.DB); ok {
		return database.WithTx(ctx, func(tx *db.Tx) error { return InsertSession(ctx, tx, v) })
	}
	if err := q.QueryRowContext(ctx, "UPDATE ids SET value=value+1 WHERE name=? RETURNING value", "sessions").Scan(&v.Position); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, "INSERT INTO sessions (position,token,csrf,access,refresh,mid,ctime) VALUES (?,?,?,?,?,?,?)", v.Position, v.Token, v.CSRF, v.Access, v.Refresh, v.MID, v.Ctime)
	return err
}

func (s *Store) ListSession(ctx context.Context, clause string, args ...any) ([]Session, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+sessionColumns+" FROM sessions "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		v, e := scanSession(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const regionColumns = "tid,name,sort,visible,COALESCE(parent,0)"

func scanRegion(row scanner) (*Region, error) {
	var v Region
	if err := row.Scan(&v.TID, &v.Name, &v.Sort, &v.Visible, &v.Parent); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertRegion(ctx context.Context, q db.Querier, v *Region) error {
	_, err := q.ExecContext(ctx, "INSERT INTO regions (tid,name,sort,visible,parent) VALUES (?,?,?,?,?)", v.TID, v.Name, v.Sort, BoolInt(v.Visible), NullableID(v.Parent))
	return err
}
func (s *Store) ListRegion(ctx context.Context, clause string, args ...any) ([]Region, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+regionColumns+" FROM regions "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Region{}
	for rows.Next() {
		v, e := scanRegion(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const archiveColumns = "aid,bvid,mid,title,description,tid,tag,pic,ctime,pubdate,state,reason,copyright,source,chapters,subtitles,stat_view,stat_danmaku,stat_reply,stat_favorite,stat_coin,stat_share,stat_like,origin_ds,origin_item,origin,sub_tid"

func scanArchive(row scanner) (*Archive, error) {
	var v Archive
	var origin string
	var jChapters string
	var jSubtitles string
	if err := row.Scan(&v.AID, &v.BVID, &v.MID, &v.Title, &v.Desc, &v.TID, &v.Tag, &v.Pic, &v.Ctime, &v.Pubdate, &v.State, &v.Reason, &v.Copyright, &v.Source, &jChapters, &jSubtitles, &v.Stat.View, &v.Stat.Danmaku, &v.Stat.Reply, &v.Stat.Favorite, &v.Stat.Coin, &v.Stat.Share, &v.Stat.Like, &v.OriginDS, &v.OriginItem, &origin, &v.SubTID); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(origin), &v.Origin); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(jChapters), &v.Chapters); err != nil {
		return nil, fmt.Errorf("archives.chapters: %w", err)
	}
	if err := json.Unmarshal([]byte(jSubtitles), &v.Subtitles); err != nil {
		return nil, fmt.Errorf("archives.subtitles: %w", err)
	}
	return &v, nil
}
func InsertArchive(ctx context.Context, q db.Querier, v *Archive) error {
	jChapters, err := json.Marshal(v.Chapters)
	if err != nil {
		return err
	}
	jSubtitles, err := json.Marshal(v.Subtitles)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO archives (aid,bvid,mid,title,description,tid,tag,pic,ctime,pubdate,state,reason,copyright,source,chapters,subtitles,stat_view,stat_danmaku,stat_reply,stat_favorite,stat_coin,stat_share,stat_like,sub_tid) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", v.AID, v.BVID, v.MID, v.Title, v.Desc, v.TID, v.Tag, v.Pic, v.Ctime, v.Pubdate, v.State, v.Reason, v.Copyright, v.Source, string(jChapters), string(jSubtitles), v.Stat.View, v.Stat.Danmaku, v.Stat.Reply, v.Stat.Favorite, v.Stat.Coin, v.Stat.Share, v.Stat.Like, v.SubTID)
	return err
}
func (s *Store) ListArchive(ctx context.Context, clause string, args ...any) ([]Archive, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+archiveColumns+" FROM archives "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Archive{}
	for rows.Next() {
		v, e := scanArchive(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const pageColumns = "cid,aid,position,part,duration,media"

func scanPage(row scanner) (*Page, error) {
	var v Page
	if err := row.Scan(&v.CID, &v.AID, &v.Position, &v.Part, &v.Duration, &v.Media); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertPage(ctx context.Context, q db.Querier, v *Page) error {
	_, err := q.ExecContext(ctx, "INSERT INTO archive_pages (cid,aid,position,part,duration,media) VALUES (?,?,?,?,?,?)", v.CID, v.AID, v.Position, v.Part, v.Duration, v.Media)
	return err
}
func (s *Store) ListPage(ctx context.Context, clause string, args ...any) ([]Page, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+pageColumns+" FROM archive_pages "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Page{}
	for rows.Next() {
		v, e := scanPage(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const collectionColumns = "id,mid,kind,title,intro,cover,ctime,origin_ds,origin_library,origin"

func scanCollection(row scanner) (*Collection, error) {
	var v Collection
	var origin string
	if err := row.Scan(&v.ID, &v.MID, &v.Kind, &v.Title, &v.Intro, &v.Cover, &v.Ctime, &v.OriginDS, &v.OriginLibrary, &origin); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(origin), &v.Origin); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertCollection(ctx context.Context, q db.Querier, v *Collection) error {
	_, err := q.ExecContext(ctx, "INSERT INTO collections (id,mid,kind,title,intro,cover,ctime) VALUES (?,?,?,?,?,?,?)", v.ID, v.MID, v.Kind, v.Title, v.Intro, v.Cover, v.Ctime)
	return err
}
func (s *Store) ListCollection(ctx context.Context, clause string, args ...any) ([]Collection, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+collectionColumns+" FROM collections "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collection{}
	for rows.Next() {
		v, e := scanCollection(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const collectionItemColumns = "collection_id,aid,position"

func scanCollectionItem(row scanner) (*CollectionItem, error) {
	var v CollectionItem
	if err := row.Scan(&v.CollectionID, &v.AID, &v.Position); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertCollectionItem(ctx context.Context, q db.Querier, v *CollectionItem) error {
	_, err := q.ExecContext(ctx, "INSERT INTO collection_items (collection_id,aid,position) VALUES (?,?,?)", v.CollectionID, v.AID, v.Position)
	return err
}
func (s *Store) ListCollectionItem(ctx context.Context, clause string, args ...any) ([]CollectionItem, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+collectionItemColumns+" FROM collection_items "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectionItem{}
	for rows.Next() {
		v, e := scanCollectionItem(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const seasonColumns = "season_id,media_id,season_type,title,cover,horizontal,evaluate,areas,styles,score,is_finish,weekday,pub_time,views,follow,danmakus,ctime,state,copyright"

func scanSeason(row scanner) (*Season, error) {
	var v Season
	var jAreas string
	var jStyles string
	if err := row.Scan(&v.SeasonID, &v.MediaID, &v.SeasonType, &v.Title, &v.Cover, &v.Horizontal, &v.Evaluate, &jAreas, &jStyles, &v.Score, &v.IsFinish, &v.Weekday, &v.PubTime, &v.Views, &v.Follow, &v.Danmakus, &v.Ctime, &v.State, &v.Copyright); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(jAreas), &v.Areas); err != nil {
		return nil, fmt.Errorf("seasons.areas: %w", err)
	}
	if err := json.Unmarshal([]byte(jStyles), &v.Styles); err != nil {
		return nil, fmt.Errorf("seasons.styles: %w", err)
	}
	return &v, nil
}
func InsertSeason(ctx context.Context, q db.Querier, v *Season) error {
	jAreas, err := json.Marshal(v.Areas)
	if err != nil {
		return err
	}
	jStyles, err := json.Marshal(v.Styles)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO seasons (season_id,media_id,season_type,title,cover,horizontal,evaluate,areas,styles,score,is_finish,weekday,pub_time,views,follow,danmakus,ctime,state,copyright) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", v.SeasonID, v.MediaID, v.SeasonType, v.Title, v.Cover, v.Horizontal, v.Evaluate, string(jAreas), string(jStyles), v.Score, BoolInt(v.IsFinish), v.Weekday, v.PubTime, v.Views, v.Follow, v.Danmakus, v.Ctime, v.State, bplanCopyright(v.Copyright))
	return err
}
func (s *Store) ListSeason(ctx context.Context, clause string, args ...any) ([]Season, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+seasonColumns+" FROM seasons "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Season{}
	for rows.Next() {
		v, e := scanSeason(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const episodeColumns = "ep_id,season_id,position,aid,cid,title,long_title,cover,duration,pub_time,media,badge"

func scanEpisode(row scanner) (*Episode, error) {
	var v Episode
	if err := row.Scan(&v.EpID, &v.SeasonID, &v.Position, &v.AID, &v.CID, &v.Title, &v.LongTitle, &v.Cover, &v.Duration, &v.PubTime, &v.Media, &v.Badge); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertEpisode(ctx context.Context, q db.Querier, v *Episode) error {
	_, err := q.ExecContext(ctx, "INSERT INTO episodes (ep_id,season_id,position,aid,cid,title,long_title,cover,duration,pub_time,media,badge) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)", v.EpID, v.SeasonID, v.Position, v.AID, v.CID, v.Title, v.LongTitle, v.Cover, v.Duration, v.PubTime, v.Media, v.Badge)
	return err
}
func (s *Store) ListEpisode(ctx context.Context, clause string, args ...any) ([]Episode, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+episodeColumns+" FROM episodes "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Episode{}
	for rows.Next() {
		v, e := scanEpisode(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const liveAreaColumns = "id,name,position"

func scanLiveArea(row scanner) (*LiveArea, error) {
	var v LiveArea
	if err := row.Scan(&v.ID, &v.Name, &v.Position); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertLiveArea(ctx context.Context, q db.Querier, v *LiveArea) error {
	_, err := q.ExecContext(ctx, "INSERT INTO live_areas (id,name,position) VALUES (?,?,?)", v.ID, v.Name, v.Position)
	return err
}
func (s *Store) ListLiveArea(ctx context.Context, clause string, args ...any) ([]LiveArea, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+liveAreaColumns+" FROM live_areas "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiveArea{}
	for rows.Next() {
		v, e := scanLiveArea(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const liveAreaChildColumns = "id,parent_id,name,position"

func scanLiveAreaChild(row scanner) (*LiveAreaChild, error) {
	var v LiveAreaChild
	if err := row.Scan(&v.ID, &v.ParentID, &v.Name, &v.Position); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertLiveAreaChild(ctx context.Context, q db.Querier, v *LiveAreaChild) error {
	_, err := q.ExecContext(ctx, "INSERT INTO live_area_children (id,parent_id,name,position) VALUES (?,?,?,?)", v.ID, v.ParentID, v.Name, v.Position)
	return err
}
func (s *Store) ListLiveAreaChild(ctx context.Context, clause string, args ...any) ([]LiveAreaChild, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+liveAreaChildColumns+" FROM live_area_children "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiveAreaChild{}
	for rows.Next() {
		v, e := scanLiveAreaChild(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const liveRoomColumns = "room_id,uid,title,cover,parent_area_id,area_id,live_status,online,media,banned,live_time,tags,description,source,pending_start"

func scanLiveRoom(row scanner) (*LiveRoom, error) {
	var v LiveRoom
	var jSource string
	if err := row.Scan(&v.RoomID, &v.UID, &v.Title, &v.Cover, &v.ParentAreaID, &v.AreaID, &v.LiveStatus, &v.Online, &v.Media, &v.Banned, &v.LiveTime, &v.Tags, &v.Description, &jSource, &v.PendingStart); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(jSource), &v.Source); err != nil {
		return nil, fmt.Errorf("rooms.source: %w", err)
	}
	return &v, nil
}
func InsertLiveRoom(ctx context.Context, q db.Querier, v *LiveRoom) error {
	jSource, err := json.Marshal(v.Source)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO rooms (room_id,uid,title,cover,parent_area_id,area_id,live_status,online,media,banned,live_time,tags,description,source,pending_start) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", v.RoomID, v.UID, v.Title, v.Cover, v.ParentAreaID, v.AreaID, v.LiveStatus, v.Online, v.Media, BoolInt(v.Banned), v.LiveTime, v.Tags, v.Description, string(jSource), BoolInt(v.PendingStart))
	return err
}
func (s *Store) ListLiveRoom(ctx context.Context, clause string, args ...any) ([]LiveRoom, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+liveRoomColumns+" FROM rooms "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiveRoom{}
	for rows.Next() {
		v, e := scanLiveRoom(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const liveMessageColumns = "id,room_id,uid,text,ts,price"

func scanLiveMessage(row scanner) (*LiveMessage, error) {
	var v LiveMessage
	if err := row.Scan(&v.ID, &v.RoomID, &v.UID, &v.Text, &v.TS, &v.Price); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertLiveMessage(ctx context.Context, q db.Querier, v *LiveMessage) error {
	_, err := q.ExecContext(ctx, "INSERT INTO live_messages (id,room_id,uid,text,ts,price) VALUES (?,?,?,?,?,?)", v.ID, v.RoomID, v.UID, v.Text, v.TS, v.Price)
	return err
}
func (s *Store) ListLiveMessage(ctx context.Context, clause string, args ...any) ([]LiveMessage, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+liveMessageColumns+" FROM live_messages "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiveMessage{}
	for rows.Next() {
		v, e := scanLiveMessage(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const replyColumns = "rpid,oid,mid,root,parent,message,ctime"

func scanReply(row scanner) (*Reply, error) {
	var v Reply
	var nRoot sql.NullInt64
	var nParent sql.NullInt64
	if err := row.Scan(&v.RPID, &v.OID, &v.MID, &nRoot, &nParent, &v.Message, &v.Ctime); err != nil {
		return nil, err
	}
	v.Root = nRoot.Int64
	v.Parent = nParent.Int64
	return &v, nil
}
func InsertReply(ctx context.Context, q db.Querier, v *Reply) error {
	_, err := q.ExecContext(ctx, "INSERT INTO replies (rpid,oid,mid,root,parent,message,ctime) VALUES (?,?,?,?,?,?,?)", v.RPID, v.OID, v.MID, NullableID(v.Root), NullableID(v.Parent), v.Message, v.Ctime)
	return err
}
func (s *Store) ListReply(ctx context.Context, clause string, args ...any) ([]Reply, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+replyColumns+" FROM replies "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reply{}
	for rows.Next() {
		v, e := scanReply(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const replyReactionColumns = "rpid,mid,reaction"

func scanReplyReaction(row scanner) (*ReplyReaction, error) {
	var v ReplyReaction
	if err := row.Scan(&v.RPID, &v.MID, &v.Reaction); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertReplyReaction(ctx context.Context, q db.Querier, v *ReplyReaction) error {
	_, err := q.ExecContext(ctx, "INSERT INTO reply_reactions (rpid,mid,reaction) VALUES (?,?,?)", v.RPID, v.MID, v.Reaction)
	return err
}
func (s *Store) ListReplyReaction(ctx context.Context, clause string, args ...any) ([]ReplyReaction, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+replyReactionColumns+" FROM reply_reactions "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReplyReaction{}
	for rows.Next() {
		v, e := scanReplyReaction(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const danmakuColumns = "id,cid,progress,mode,fontsize,color,mid,content,ctime,weight"

func scanDanmaku(row scanner) (*Danmaku, error) {
	var v Danmaku
	if err := row.Scan(&v.ID, &v.CID, &v.Progress, &v.Mode, &v.Fontsize, &v.Color, &v.MID, &v.Content, &v.Ctime, &v.Weight); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertDanmaku(ctx context.Context, q db.Querier, v *Danmaku) error {
	_, err := q.ExecContext(ctx, "INSERT INTO danmaku (id,cid,progress,mode,fontsize,color,mid,content,ctime,weight) VALUES (?,?,?,?,?,?,?,?,?,?)", v.ID, v.CID, v.Progress, v.Mode, v.Fontsize, v.Color, v.MID, v.Content, v.Ctime, v.Weight)
	return err
}
func (s *Store) ListDanmaku(ctx context.Context, clause string, args ...any) ([]Danmaku, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+danmakuColumns+" FROM danmaku "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Danmaku{}
	for rows.Next() {
		v, e := scanDanmaku(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const seededDanmakuColumns = "cid"

func scanSeededDanmaku(row scanner) (*SeededDanmaku, error) {
	var v SeededDanmaku
	if err := row.Scan(&v.CID); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertSeededDanmaku(ctx context.Context, q db.Querier, v *SeededDanmaku) error {
	_, err := q.ExecContext(ctx, "INSERT INTO seeded_danmaku (cid) VALUES (?)", v.CID)
	return err
}
func (s *Store) ListSeededDanmaku(ctx context.Context, clause string, args ...any) ([]SeededDanmaku, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+seededDanmakuColumns+" FROM seeded_danmaku "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeededDanmaku{}
	for rows.Next() {
		v, e := scanSeededDanmaku(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const relationColumns = "mid,fid,mtime"

func scanRelation(row scanner) (*Relation, error) {
	var v Relation
	if err := row.Scan(&v.MID, &v.FID, &v.Mtime); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertRelation(ctx context.Context, q db.Querier, v *Relation) error {
	_, err := q.ExecContext(ctx, "INSERT INTO relations (mid,fid,mtime) VALUES (?,?,?)", v.MID, v.FID, v.Mtime)
	return err
}
func (s *Store) ListRelation(ctx context.Context, clause string, args ...any) ([]Relation, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+relationColumns+" FROM relations "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Relation{}
	for rows.Next() {
		v, e := scanRelation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const likeColumns = "mid,aid"

func scanLike(row scanner) (*Like, error) {
	var v Like
	if err := row.Scan(&v.MID, &v.AID); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertLike(ctx context.Context, q db.Querier, v *Like) error {
	_, err := q.ExecContext(ctx, "INSERT INTO likes (mid,aid) VALUES (?,?)", v.MID, v.AID)
	return err
}
func (s *Store) ListLike(ctx context.Context, clause string, args ...any) ([]Like, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+likeColumns+" FROM likes "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Like{}
	for rows.Next() {
		v, e := scanLike(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const dislikeColumns = "mid,aid"

func scanDislike(row scanner) (*Dislike, error) {
	var v Dislike
	if err := row.Scan(&v.MID, &v.AID); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertDislike(ctx context.Context, q db.Querier, v *Dislike) error {
	_, err := q.ExecContext(ctx, "INSERT INTO dislikes (mid,aid) VALUES (?,?)", v.MID, v.AID)
	return err
}
func (s *Store) ListDislike(ctx context.Context, clause string, args ...any) ([]Dislike, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+dislikeColumns+" FROM dislikes "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dislike{}
	for rows.Next() {
		v, e := scanDislike(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const coinColumns = "mid,aid,n"

func scanCoin(row scanner) (*Coin, error) {
	var v Coin
	if err := row.Scan(&v.MID, &v.AID, &v.N); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertCoin(ctx context.Context, q db.Querier, v *Coin) error {
	_, err := q.ExecContext(ctx, "INSERT INTO coins (mid,aid,n) VALUES (?,?,?)", v.MID, v.AID, v.N)
	return err
}
func (s *Store) ListCoin(ctx context.Context, clause string, args ...any) ([]Coin, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+coinColumns+" FROM coins "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Coin{}
	for rows.Next() {
		v, e := scanCoin(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const favFolderColumns = "id,mid,title,intro,privacy,ctime,attr"

func scanFavFolder(row scanner) (*FavFolder, error) {
	var v FavFolder
	if err := row.Scan(&v.ID, &v.MID, &v.Title, &v.Intro, &v.Privacy, &v.Ctime, &v.Attr); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertFavFolder(ctx context.Context, q db.Querier, v *FavFolder) error {
	_, err := q.ExecContext(ctx, "INSERT INTO fav_folders (id,mid,title,intro,privacy,ctime,attr) VALUES (?,?,?,?,?,?,?)", v.ID, v.MID, v.Title, v.Intro, v.Privacy, v.Ctime, v.Attr)
	return err
}
func (s *Store) ListFavFolder(ctx context.Context, clause string, args ...any) ([]FavFolder, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+favFolderColumns+" FROM fav_folders "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FavFolder{}
	for rows.Next() {
		v, e := scanFavFolder(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const favItemColumns = "folder_id,aid,mtime"

func scanFavItem(row scanner) (*FavItem, error) {
	var v FavItem
	if err := row.Scan(&v.FolderID, &v.AID, &v.Mtime); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertFavItem(ctx context.Context, q db.Querier, v *FavItem) error {
	_, err := q.ExecContext(ctx, "INSERT INTO fav_items (folder_id,aid,mtime) VALUES (?,?,?)", v.FolderID, v.AID, v.Mtime)
	return err
}
func (s *Store) ListFavItem(ctx context.Context, clause string, args ...any) ([]FavItem, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+favItemColumns+" FROM fav_items "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FavItem{}
	for rows.Next() {
		v, e := scanFavItem(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const historyColumns = "mid,aid,cid,epid,sid,view_at,progress"

func scanHistory(row scanner) (*History, error) {
	var v History
	var nEpID sql.NullInt64
	var nSID sql.NullInt64
	if err := row.Scan(&v.MID, &v.AID, &v.CID, &nEpID, &nSID, &v.ViewAt, &v.Progress); err != nil {
		return nil, err
	}
	v.EpID = nEpID.Int64
	v.SID = nSID.Int64
	return &v, nil
}
func InsertHistory(ctx context.Context, q db.Querier, v *History) error {
	_, err := q.ExecContext(ctx, "INSERT INTO history (mid,aid,cid,epid,sid,view_at,progress) VALUES (?,?,?,?,?,?,?)", v.MID, v.AID, v.CID, NullableID(v.EpID), NullableID(v.SID), v.ViewAt, v.Progress)
	return err
}
func (s *Store) ListHistory(ctx context.Context, clause string, args ...any) ([]History, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+historyColumns+" FROM history "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []History{}
	for rows.Next() {
		v, e := scanHistory(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const historyPausedColumns = "mid"

func scanHistoryPaused(row scanner) (*HistoryPaused, error) {
	var v HistoryPaused
	if err := row.Scan(&v.MID); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertHistoryPaused(ctx context.Context, q db.Querier, v *HistoryPaused) error {
	_, err := q.ExecContext(ctx, "INSERT INTO history_paused (mid) VALUES (?)", v.MID)
	return err
}
func (s *Store) ListHistoryPaused(ctx context.Context, clause string, args ...any) ([]HistoryPaused, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+historyPausedColumns+" FROM history_paused "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryPaused{}
	for rows.Next() {
		v, e := scanHistoryPaused(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const toViewColumns = "mid,aid,add_at"

func scanToView(row scanner) (*ToView, error) {
	var v ToView
	if err := row.Scan(&v.MID, &v.AID, &v.AddAt); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertToView(ctx context.Context, q db.Querier, v *ToView) error {
	_, err := q.ExecContext(ctx, "INSERT INTO toview (mid,aid,add_at) VALUES (?,?,?)", v.MID, v.AID, v.AddAt)
	return err
}
func (s *Store) ListToView(ctx context.Context, clause string, args ...any) ([]ToView, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+toViewColumns+" FROM toview "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ToView{}
	for rows.Next() {
		v, e := scanToView(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const bangumiFollowColumns = "mid,season_id,status,mtime"

func scanBangumiFollow(row scanner) (*BangumiFollow, error) {
	var v BangumiFollow
	if err := row.Scan(&v.MID, &v.SeasonID, &v.Status, &v.Mtime); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertBangumiFollow(ctx context.Context, q db.Querier, v *BangumiFollow) error {
	_, err := q.ExecContext(ctx, "INSERT INTO bangumi_follow (mid,season_id,status,mtime) VALUES (?,?,?,?)", v.MID, v.SeasonID, v.Status, v.Mtime)
	return err
}
func (s *Store) ListBangumiFollow(ctx context.Context, clause string, args ...any) ([]BangumiFollow, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+bangumiFollowColumns+" FROM bangumi_follow "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BangumiFollow{}
	for rows.Next() {
		v, e := scanBangumiFollow(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const dMConfigColumns = "mid,values_json"

func scanDMConfig(row scanner) (*DMConfig, error) {
	var v DMConfig
	var jValues string
	if err := row.Scan(&v.MID, &jValues); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(jValues), &v.Values); err != nil {
		return nil, fmt.Errorf("dm_config.values_json: %w", err)
	}
	return &v, nil
}
func InsertDMConfig(ctx context.Context, q db.Querier, v *DMConfig) error {
	jValues, err := json.Marshal(v.Values)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO dm_config (mid,values_json) VALUES (?,?)", v.MID, string(jValues))
	return err
}
func (s *Store) ListDMConfig(ctx context.Context, clause string, args ...any) ([]DMConfig, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+dMConfigColumns+" FROM dm_config "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DMConfig{}
	for rows.Next() {
		v, e := scanDMConfig(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const dMFilterColumns = "mid,rule,keyword,regex"

func scanDMFilter(row scanner) (*DMFilter, error) {
	var v DMFilter
	var jRule string
	var jKeyword string
	var jRegex string
	if err := row.Scan(&v.MID, &jRule, &jKeyword, &jRegex); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(jRule), &v.Rule); err != nil {
		return nil, fmt.Errorf("dm_filter.rule: %w", err)
	}
	if err := json.Unmarshal([]byte(jKeyword), &v.Keyword); err != nil {
		return nil, fmt.Errorf("dm_filter.keyword: %w", err)
	}
	if err := json.Unmarshal([]byte(jRegex), &v.Regex); err != nil {
		return nil, fmt.Errorf("dm_filter.regex: %w", err)
	}
	return &v, nil
}
func InsertDMFilter(ctx context.Context, q db.Querier, v *DMFilter) error {
	jRule, err := json.Marshal(v.Rule)
	if err != nil {
		return err
	}
	jKeyword, err := json.Marshal(v.Keyword)
	if err != nil {
		return err
	}
	jRegex, err := json.Marshal(v.Regex)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO dm_filter (mid,rule,keyword,regex) VALUES (?,?,?,?)", v.MID, string(jRule), string(jKeyword), string(jRegex))
	return err
}
func (s *Store) ListDMFilter(ctx context.Context, clause string, args ...any) ([]DMFilter, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+dMFilterColumns+" FROM dm_filter "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DMFilter{}
	for rows.Next() {
		v, e := scanDMFilter(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const bannerColumns = "id,title,img,url,position,sort,season_id,loc"

func scanBanner(row scanner) (*Banner, error) {
	var v Banner
	if err := row.Scan(&v.ID, &v.Title, &v.Img, &v.URL, &v.Position, &v.Sort, &v.SeasonID, &v.Loc); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertBanner(ctx context.Context, q db.Querier, v *Banner) error {
	_, err := q.ExecContext(ctx, "INSERT INTO banners (id,title,img,url,position,sort,season_id,loc) VALUES (?,?,?,?,?,?,?,?)", v.ID, v.Title, v.Img, v.URL, v.Position, v.Sort, v.SeasonID, v.Loc)
	return err
}
func (s *Store) ListBanner(ctx context.Context, clause string, args ...any) ([]Banner, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+bannerColumns+" FROM banners "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Banner{}
	for rows.Next() {
		v, e := scanBanner(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const settingRecordColumns = "key,value"

func scanSettingRecord(row scanner) (*SettingRecord, error) {
	var v SettingRecord
	if err := row.Scan(&v.Key, &v.Value); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertSettingRecord(ctx context.Context, q db.Querier, v *SettingRecord) error {
	_, err := q.ExecContext(ctx, "INSERT INTO settings (key,value) VALUES (?,?)", v.Key, v.Value)
	return err
}
func (s *Store) ListSettingRecord(ctx context.Context, clause string, args ...any) ([]SettingRecord, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+settingRecordColumns+" FROM settings "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SettingRecord{}
	for rows.Next() {
		v, e := scanSettingRecord(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const logColumns = "id,mid,action,target,ctime"

func scanLog(row scanner) (*Log, error) {
	var v Log
	var nMID sql.NullInt64
	if err := row.Scan(&v.ID, &nMID, &v.Action, &v.Target, &v.Ctime); err != nil {
		return nil, err
	}
	v.MID = nMID.Int64
	return &v, nil
}
func InsertLog(ctx context.Context, q db.Querier, v *Log) error {
	_, err := q.ExecContext(ctx, "INSERT INTO logs (id,mid,action,target,ctime) VALUES (?,?,?,?,?)", v.ID, NullableID(v.MID), v.Action, v.Target, v.Ctime)
	return err
}
func (s *Store) ListLog(ctx context.Context, clause string, args ...any) ([]Log, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+logColumns+" FROM logs "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Log{}
	for rows.Next() {
		v, e := scanLog(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const qRCodeColumns = "code,status,mid,ctime"

func scanQRCode(row scanner) (*QRCode, error) {
	var v QRCode
	var nMID sql.NullInt64
	if err := row.Scan(&v.Code, &v.Status, &nMID, &v.Ctime); err != nil {
		return nil, err
	}
	v.MID = nMID.Int64
	return &v, nil
}
func InsertQRCode(ctx context.Context, q db.Querier, v *QRCode) error {
	_, err := q.ExecContext(ctx, "INSERT INTO qrcodes (code,status,mid,ctime) VALUES (?,?,?,?)", v.Code, v.Status, NullableID(v.MID), v.Ctime)
	return err
}
func (s *Store) ListQRCode(ctx context.Context, clause string, args ...any) ([]QRCode, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+qRCodeColumns+" FROM qrcodes "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QRCode{}
	for rows.Next() {
		v, e := scanQRCode(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

const iDSequenceColumns = "name,value"

func scanIDSequence(row scanner) (*IDSequence, error) {
	var v IDSequence
	if err := row.Scan(&v.Name, &v.Value); err != nil {
		return nil, err
	}
	return &v, nil
}
func InsertIDSequence(ctx context.Context, q db.Querier, v *IDSequence) error {
	_, err := q.ExecContext(ctx, "INSERT INTO ids (name,value) VALUES (?,?)", v.Name, v.Value)
	return err
}
func (s *Store) ListIDSequence(ctx context.Context, clause string, args ...any) ([]IDSequence, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+iDSequenceColumns+" FROM ids "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IDSequence{}
	for rows.Next() {
		v, e := scanIDSequence(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
