// Package store contains persistent models matching the browser mock.
package store

type User struct {
	Notice   string  `json:"notice"`
	MID      int64   `json:"mid"`
	Username string  `json:"username"`
	Password string  `json:"-"`
	Uname    string  `json:"uname"`
	Face     string  `json:"face"`
	Sign     string  `json:"sign"`
	Sex      string  `json:"sex"`
	Birthday string  `json:"birthday"`
	Email    string  `json:"email"`
	Level    int     `json:"level"`
	Exp      int     `json:"exp"`
	Coins    float64 `json:"coins"`
	Role     string  `json:"role"`
	Status   int     `json:"status"`
	Ctime    int64   `json:"ctime"`
	TopPhoto string  `json:"top_photo"`
	VIP      int     `json:"vip"`
}
type Session struct {
	Position int64  `json:"-"`
	Token    string `json:"token"`
	CSRF     string `json:"csrf"`
	Access   string `json:"access"`
	Refresh  string `json:"refresh"`
	MID      int64  `json:"mid"`
	Ctime    int64  `json:"ctime"`
}
type Region struct {
	Parent  int64  `json:"parent"`
	TID     int64  `json:"tid"`
	Name    string `json:"name"`
	Sort    int    `json:"sort"`
	Visible bool   `json:"visible"`
}
type Archive struct {
	SubTID     int64          `json:"sub_tid"`
	OriginDS   int64          `json:"-"`
	OriginItem string         `json:"-"`
	Origin     *ArchiveOrigin `json:"-"`
	AID        int64          `json:"aid"`
	BVID       string         `json:"bvid"`
	MID        int64          `json:"mid"`
	Title      string         `json:"title"`
	Desc       string         `json:"desc"`
	TID        int64          `json:"tid"`
	Tag        string         `json:"tag"`
	Pic        string         `json:"pic"`
	Ctime      int64          `json:"ctime"`
	Pubdate    int64          `json:"pubdate"`
	State      int            `json:"state"`
	Reason     string         `json:"reason"`
	Copyright  int            `json:"copyright"`
	Source     string         `json:"source"`
	Chapters   []Chapter      `json:"chapters"`
	Subtitles  []Subtitle     `json:"subtitles"`
	Pages      []Page         `json:"pages"`
	Stat       ArchiveStat    `json:"stat"`
}
type Page struct {
	CID      int64  `json:"cid"`
	AID      int64  `json:"-"`
	Position int    `json:"-"`
	Part     string `json:"part"`
	Duration int    `json:"duration"`
	Media    string `json:"media"`
}
type Collection struct {
	OriginDS      int64             `json:"-"`
	OriginLibrary string            `json:"-"`
	Origin        *CollectionOrigin `json:"-"`
	ID            int64             `json:"id"`
	MID           int64             `json:"mid"`
	Kind          string            `json:"kind"`
	Title         string            `json:"title"`
	Intro         string            `json:"intro"`
	Cover         string            `json:"cover"`
	Ctime         int64             `json:"ctime"`
	AIDs          []int64           `json:"aids"`
}
type CollectionItem struct {
	CollectionID int64 `json:"collection_id"`
	AID          int64 `json:"aid"`
	Position     int   `json:"-"`
}
type Season struct {
	Copyright  int       `json:"copyright"`
	SeasonID   int64     `json:"season_id"`
	MediaID    int64     `json:"media_id"`
	SeasonType int       `json:"season_type"`
	Title      string    `json:"title"`
	Cover      string    `json:"cover"`
	Horizontal string    `json:"horizontal"`
	Evaluate   string    `json:"evaluate"`
	Areas      []string  `json:"areas"`
	Styles     []string  `json:"styles"`
	Score      float64   `json:"score"`
	IsFinish   bool      `json:"is_finish"`
	Weekday    int       `json:"weekday"`
	PubTime    string    `json:"pub_time"`
	Views      int64     `json:"views"`
	Follow     int64     `json:"follow"`
	Danmakus   int64     `json:"danmakus"`
	Ctime      int64     `json:"ctime"`
	State      int       `json:"state"`
	Episodes   []Episode `json:"episodes"`
}
type Episode struct {
	EpID      int64  `json:"ep_id"`
	SeasonID  int64  `json:"-"`
	Position  int    `json:"-"`
	AID       int64  `json:"aid"`
	CID       int64  `json:"cid"`
	Title     string `json:"title"`
	LongTitle string `json:"long_title"`
	Cover     string `json:"cover"`
	Duration  int    `json:"duration"`
	PubTime   int64  `json:"pub_time"`
	Media     string `json:"media"`
	Badge     string `json:"badge"`
}
type LiveArea struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Position int             `json:"-"`
	Children []LiveAreaChild `json:"children"`
}
type LiveAreaChild struct {
	ID       int64  `json:"id"`
	ParentID int64  `json:"-"`
	Name     string `json:"name"`
	Position int    `json:"-"`
}
type LiveRoom struct {
	PendingStart bool          `json:"pending_start"`
	RoomID       int64         `json:"room_id"`
	UID          int64         `json:"uid"`
	Title        string        `json:"title"`
	Cover        string        `json:"cover"`
	ParentAreaID int64         `json:"parent_area_id"`
	AreaID       int64         `json:"area_id"`
	LiveStatus   int           `json:"live_status"`
	Online       int64         `json:"online"`
	Media        string        `json:"media"`
	Banned       bool          `json:"banned"`
	LiveTime     int64         `json:"live_time"`
	Tags         string        `json:"tags"`
	Description  string        `json:"desc"`
	Source       *StreamSource `json:"source,omitempty"`
}
type LiveMessage struct {
	ID     int64    `json:"id"`
	RoomID int64    `json:"room_id"`
	UID    int64    `json:"uid"`
	Text   string   `json:"text"`
	TS     int64    `json:"ts"`
	Price  *float64 `json:"price,omitempty"`
}
type Reply struct {
	RPID    int64   `json:"rpid"`
	OID     int64   `json:"oid"`
	MID     int64   `json:"mid"`
	Root    int64   `json:"root"`
	Parent  int64   `json:"parent"`
	Message string  `json:"message"`
	Ctime   int64   `json:"ctime"`
	Likers  []int64 `json:"likers"`
	Haters  []int64 `json:"haters"`
}
type ReplyReaction struct {
	RPID     int64 `json:"rpid"`
	MID      int64 `json:"mid"`
	Reaction int   `json:"reaction"`
}
type Danmaku struct {
	ID       int64  `json:"id"`
	CID      int64  `json:"cid"`
	Progress int    `json:"progress"`
	Mode     int    `json:"mode"`
	Fontsize int    `json:"fontsize"`
	Color    int    `json:"color"`
	MID      int64  `json:"mid"`
	Content  string `json:"content"`
	Ctime    int64  `json:"ctime"`
	Weight   int    `json:"weight"`
}
type SeededDanmaku struct {
	CID int64 `json:"cid"`
}
type Relation struct {
	MID   int64 `json:"mid"`
	FID   int64 `json:"fid"`
	Mtime int64 `json:"mtime"`
}
type Like struct {
	MID int64 `json:"mid"`
	AID int64 `json:"aid"`
}
type Dislike struct {
	MID int64 `json:"mid"`
	AID int64 `json:"aid"`
}
type Coin struct {
	MID int64 `json:"mid"`
	AID int64 `json:"aid"`
	N   int   `json:"n"`
}
type FavFolder struct {
	ID      int64     `json:"id"`
	MID     int64     `json:"mid"`
	Title   string    `json:"title"`
	Intro   string    `json:"intro"`
	Privacy int       `json:"privacy"`
	Ctime   int64     `json:"ctime"`
	Attr    int       `json:"attr"`
	Items   []FavItem `json:"items"`
}
type FavItem struct {
	FolderID int64 `json:"folder_id"`
	AID      int64 `json:"aid"`
	Mtime    int64 `json:"mtime"`
}
type History struct {
	MID      int64 `json:"mid"`
	AID      int64 `json:"aid"`
	CID      int64 `json:"cid"`
	EpID     int64 `json:"epid"`
	SID      int64 `json:"sid"`
	ViewAt   int64 `json:"view_at"`
	Progress int   `json:"progress"`
}
type HistoryPaused struct {
	MID int64 `json:"mid"`
}
type ToView struct {
	MID   int64 `json:"mid"`
	AID   int64 `json:"aid"`
	AddAt int64 `json:"add_at"`
}
type BangumiFollow struct {
	MID      int64 `json:"mid"`
	SeasonID int64 `json:"season_id"`
	Status   int   `json:"status"`
	Mtime    int64 `json:"mtime"`
}
type DMConfig struct {
	MID    int64             `json:"mid"`
	Values map[string]string `json:"values"`
}
type DMFilter struct {
	MID     int64    `json:"mid"`
	Rule    []string `json:"rule"`
	Keyword []string `json:"keyword"`
	Regex   []string `json:"regex"`
}
type Banner struct {
	Loc      *int64 `json:"loc,omitempty"`
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Img      string `json:"img"`
	URL      string `json:"url"`
	Position string `json:"position"`
	Sort     int    `json:"sort"`
	SeasonID *int64 `json:"season_id,omitempty"`
}
type SettingRecord struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type Log struct {
	ID     int64  `json:"id"`
	MID    int64  `json:"mid"`
	Action string `json:"action"`
	Target string `json:"target"`
	Ctime  int64  `json:"ctime"`
}
type QRCode struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	MID    int64  `json:"mid"`
	Ctime  int64  `json:"ctime"`
}
type IDSequence struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}
type Chapter struct {
	From    float64 `json:"from"`
	To      float64 `json:"to"`
	Content string  `json:"content"`
}
type Subtitle struct {
	Lan    string    `json:"lan"`
	LanDoc string    `json:"lan_doc"`
	Body   []Chapter `json:"body"`
}
type ArchiveStat struct {
	View     int64 `json:"view"`
	Danmaku  int64 `json:"danmaku"`
	Reply    int64 `json:"reply"`
	Favorite int64 `json:"favorite"`
	Coin     int64 `json:"coin"`
	Share    int64 `json:"share"`
	Like     int64 `json:"like"`
}
type StreamSource struct {
	Cache  string `json:"cache,omitempty"`
	AID    int64  `json:"aid,omitempty"`
	CID    int64  `json:"cid,omitempty"`
	Type   string `json:"type"`
	URL    string `json:"url"`
	Loop   bool   `json:"loop"`
	Format string `json:"format,omitempty"`
	Kind   string `json:"kind,omitempty"`
}
