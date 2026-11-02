package api

import (
	"database/sql"
	"errors"
	"math"
	"strconv"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func replyAddResponse(r store.Reply, reply render.M) render.M {
	return render.M{
		"success_action": 0, "success_toast": "发送成功", "need_captcha": false, "url": "",
		"rpid": r.RPID, "rpid_str": strconv.FormatInt(r.RPID, 10),
		"dialog": r.Parent, "dialog_str": strconv.FormatInt(r.Parent, 10),
		"root": r.Root, "root_str": strconv.FormatInt(r.Root, 10),
		"parent": r.Parent, "parent_str": strconv.FormatInt(r.Parent, 10),
		"emote": render.M{}, "reply": reply,
	}
}

// These checks share the insertion transaction and its lock, including across
// processes. Failed submissions neither insert a reply nor consume a time slot.
func replyAddPolicy(c *Ctx, tx *db.Tx, r store.Reply, settings map[string]any) error {
	var owner int64
	var state int
	err := tx.QueryRowContext(c.Context(), "SELECT mid,state FROM archives WHERE aid=?", r.OID).Scan(&owner, &state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && state != 0) {
		return Fail(-404, "啥都木有")
	}
	if err != nil {
		return err
	}
	if settings["commentEnabled"] == false {
		return Fail(12002, "评论区已关闭")
	}
	if (r.Root != 0 || r.Parent != 0) && settings["commentReplyEnabled"] == false {
		return Fail(12003, "禁止回复")
	}
	blocked, err := Count(c, tx, "SELECT COUNT(*) FROM user_blocks WHERE mid=? AND fid=?", owner, r.MID)
	if err != nil {
		return err
	}
	if blocked > 0 {
		return Fail(12035, "该账号被UP主列入评论黑名单")
	}
	if r.Root != 0 || r.Parent != 0 {
		if r.Root <= 0 || r.Parent <= 0 {
			return Fail(12006, "没有该评论")
		}
		valid, err := Count(c, tx, `SELECT COUNT(*) FROM replies root JOIN replies parent ON parent.rpid=?
			WHERE root.rpid=? AND root.oid=? AND COALESCE(root.root,0)=0 AND parent.oid=root.oid
			AND (parent.rpid=root.rpid OR parent.root=root.rpid)`, r.Parent, r.Root, r.OID)
		if err != nil {
			return err
		}
		if valid == 0 {
			return Fail(12006, "没有该评论")
		}
	}
	if seconds, ok := settings["commentDuplicateWindow"].(float64); ok && seconds > 0 {
		n, err := Count(c, tx, "SELECT COUNT(*) FROM replies WHERE mid=? AND oid=? AND COALESCE(root,0)=? AND COALESCE(parent,0)=? AND message=? AND ctime>?", r.MID, r.OID, r.Root, r.Parent, r.Message, r.Ctime-int64(seconds))
		if err != nil {
			return err
		}
		if n > 0 {
			return Fail(12051, "重复评论，请勿刷屏")
		}
	}
	if seconds, ok := settings["commentMinInterval"].(float64); ok && seconds > 0 {
		n, err := Count(c, tx, "SELECT COUNT(*) FROM replies WHERE mid=? AND ctime>?", r.MID, r.Ctime-int64(seconds))
		if err != nil {
			return err
		}
		if n > 0 {
			return Fail(-509, "请求过于频繁，请稍后再试")
		}
	}
	return nil
}

func validateReplySettings(c *Ctx) error {
	for _, key := range []string{"commentMinInterval", "commentDuplicateWindow"} {
		if value, ok := c.P[key]; ok {
			n, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 86400 || math.Trunc(n) != n {
				return Fail(-400, "评论防刷时间应为 0–86400 秒的整数")
			}
		}
	}
	for _, key := range []string{"commentEnabled", "commentReplyEnabled"} {
		if value, ok := c.P[key]; ok && value != "true" && value != "false" {
			return Fail(-400, "评论开关参数无效")
		}
	}
	return nil
}
