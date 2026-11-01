package seed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

var baseSubRegions = []struct {
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

func BaseTx(ctx context.Context, tx *db.Tx) error {
	for name, value := range map[string]int64{"global": 1000, "sessions": 0} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO ids(name,value) VALUES (?,?) ON CONFLICT(name) DO NOTHING", name, value); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE ids SET value=value WHERE name=?", "global"); err != nil {
		return err
	}
	for _, v := range regions {
		if _, err := tx.ExecContext(ctx, "INSERT INTO regions(tid,name,sort,visible,parent) VALUES (?,?,?,?,?) ON CONFLICT(tid) DO NOTHING", v.TID, v.Name, v.Sort, store.BoolInt(v.Visible), store.NullableID(v.Parent)); err != nil {
			return err
		}
	}
	for _, g := range baseSubRegions {
		for i, name := range g.names {
			if _, err := tx.ExecContext(ctx, "INSERT INTO regions(tid,name,sort,visible,parent) VALUES (?,?,?,1,?) ON CONFLICT(tid) DO NOTHING", g.parent*100+int64(i)+1, name, i, g.parent); err != nil {
				return err
			}
		}
	}
	for _, a := range liveAreas {
		if _, err := tx.ExecContext(ctx, "INSERT INTO live_areas(id,name,position) VALUES (?,?,?) ON CONFLICT(id) DO NOTHING", a.ID, a.Name, a.Position); err != nil {
			return err
		}
		for _, c := range a.Children {
			if _, err := tx.ExecContext(ctx, "INSERT INTO live_area_children(id,parent_id,name,position) VALUES (?,?,?,?) ON CONFLICT(id) DO NOTHING", c.ID, c.ParentID, c.Name, c.Position); err != nil {
				return err
			}
		}
	}
	var official int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MIN(mid),0) FROM users WHERE role=?", "admin").Scan(&official); err != nil {
		return err
	}
	settings := map[string]any{"siteName": "Jumedia", "allowRegister": true, "auditRequired": true, "maxUploadMB": 2048, "announcement": "欢迎来到 Jumedia 在线媒体平台。", "allowGuestPlay": true, "defaultQuality": 80, "userDataSources": true, "dataSourcePrivateNetwork": false, "dataSourceSyncHours": 6, "dataSourceSyncRate": 5, "dataSourceSyncConcurrency": 2, "officialMid": official, "liveCacheDir": "", "liveTranscodeHeight": 1080, "imageCacheEnabled": true, "imageCacheDir": "", "imageCacheMaxMB": 1024}
	for k, v := range settings {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO NOTHING", k, string(raw)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO NOTHING", "sensitiveWords", `"加微信\n加V\n代刷\n免费领取\n私聊我\n刷粉"`); err != nil {
		return err
	}
	if err := seedServiceSettings(ctx, tx); err != nil {
		return err
	}
	return seedServerSettings(ctx, tx)
}

// Only administrators survive a reset. Keep their rows and signing identity intact.
func ResetTx(ctx context.Context, tx *db.Tx) error {
	if _, err := tx.ExecContext(ctx, "UPDATE ids SET value=value WHERE name=?", "global"); err != nil {
		return err
	}
	for _, table := range businessTables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM users WHERE role<>?", "admin"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key<>?", "_live_chat_secret"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE ids SET value=? WHERE name=?", 0, "sessions"); err != nil {
		return err
	}
	return BaseTx(ctx, tx)
}

var businessTables = []string{"live_caches", "fan_medals", "reservations", "reply_reactions", "replies", "live_messages", "collection_items", "archive_pages", "fav_items", "history", "history_paused", "toview", "bangumi_follow", "dm_config", "dm_filter", "danmaku", "seeded_danmaku", "relations", "likes", "dislikes", "coins", "banners", "sessions", "qrcodes", "logs", "rooms", "live_area_children", "live_areas", "collections", "fav_folders", "archives", "episodes", "seasons", "regions", "data_sources"}

func BootstrapAdmin(ctx context.Context, s *store.Store, username, password string) error {
	return s.DB.WithTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE ids SET value=value WHERE name=?", "global"); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role=?", "admin").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		if username == "" && password == "" {
			slog.Warn("没有管理员，请设置 JUMEDIA_ADMIN_USERNAME/JUMEDIA_ADMIN_PASSWORD 或运行 jumedia admin")
			return nil
		}
		if username == "" || password == "" {
			return errors.New("JUMEDIA_ADMIN_USERNAME 和 JUMEDIA_ADMIN_PASSWORD 必须同时设置")
		}
		return adminTx(ctx, tx, username, password, false)
	})
}
func Admin(ctx context.Context, s *store.Store, username, password string) error {
	return s.DB.WithTx(ctx, func(tx *db.Tx) error { return adminTx(ctx, tx, username, password, true) })
}
func adminTx(ctx context.Context, tx *db.Tx, username, password string, recover bool) error {
	username = strings.TrimSpace(username)
	if username == "" || utf8.RuneCountInString(password) < 6 || len(password) > 72 {
		return errors.New("管理员用户名不能为空，密码至少 6 位且不超过 72 字节")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE ids SET value=value WHERE name=?", "global"); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	var mid int64
	err = tx.QueryRowContext(ctx, "SELECT mid FROM users WHERE username=?", username).Scan(&mid)
	if err == nil {
		if !recover {
			return fmt.Errorf("管理员引导用户名已存在，请使用 jumedia admin 找回")
		}
		if _, err = tx.ExecContext(ctx, "UPDATE users SET role=?,password=?,status=0 WHERE mid=?", "admin", string(hash), mid); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE settings SET value=? WHERE key=? AND value=?", fmt.Sprint(mid), "officialMid", "0")
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE ids SET value=CASE WHEN value<(SELECT COALESCE(MAX(mid),0) FROM users) THEN (SELECT COALESCE(MAX(mid),0) FROM users) ELSE value END WHERE name=?", "global"); err != nil {
		return err
	}
	mid, err = store.NextID(ctx, tx)
	if err != nil {
		return err
	}
	if err = store.InsertUser(ctx, tx, &store.User{MID: mid, Username: username, Password: string(hash), Uname: username, Role: "admin", Ctime: time.Now().Unix(), Sex: "保密"}); err != nil {
		return err
	}
	// A previously empty official account follows the first real administrator.
	_, err = tx.ExecContext(ctx, "UPDATE settings SET value=? WHERE key=? AND value=?", fmt.Sprint(mid), "officialMid", "0")
	return err
}
