package api

import (
	"net/http"
	"strings"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
)

// Jumedia /x/v2/reply/report reason codes, not display strings.
var commentReportReasons = map[string]string{"9": "违法违规", "2": "色情", "10": "低俗", "12": "赌博诈骗", "23": "违法信息外链", "19": "涉政谣言", "22": "虚假不实信息", "20": "涉社会事件谣言", "7": "人身攻击", "15": "侵犯隐私", "1": "垃圾广告", "4": "引战", "5": "剧透", "3": "刷屏", "8": "视频不相关", "18": "违规抽奖", "17": "青少年不良信息", "0": "其他"}

func (s *Server) communityReport(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	if c.R.Method != http.MethodPost {
		return nil, Fail(-400, "请使用 POST")
	}
	if c.P["type"] != "1" {
		return nil, Fail(-400, "评论区类型无效")
	}
	r, err := s.communityReply(c, ParseID(c.P["rpid"]))
	if err != nil {
		return nil, err
	}
	if r.OID != ParseID(c.P["oid"]) {
		return nil, Fail(12022, "评论已被删除")
	}
	if commentReportReasons[c.P["reason"]] == "" {
		return nil, Fail(-400, "请选择举报原因")
	}
	content := Trim(c.P["content"])
	if UTF16Length(content) > 1000 || ((c.P["reason"] == "0" || c.P["reason"] == "22") && content == "") {
		return nil, Fail(-400, "请填写举报说明（1–1000 字）")
	}
	if r.MID == u.MID {
		return nil, Fail(-400, "不能举报自己的评论")
	}
	return nil, s.WriteTx(c, func(tx *db.Tx) error {
		if err := RequireReference(c, tx, "SELECT COUNT(*) FROM replies WHERE rpid=?", r.RPID, 12022, "评论已被删除"); err != nil {
			return err
		}
		_, err := tx.ExecContext(c.Context(), "INSERT INTO comment_reports(mid,rpid,reason,content,status,ctime) VALUES (?,?,?,?,'pending',?) ON CONFLICT (mid,rpid) DO NOTHING", u.MID, r.RPID, c.P["reason"], content, time.Now().Unix())
		if err == nil && BoolParam(c.P["add_blacklist"]) {
			err = blockCommentAuthor(c, tx, u.MID, r.MID)
		}
		return err
	})
}

func (s *Server) communityBlock(c *Ctx) (any, error) {
	u, err := c.CSRF()
	if err != nil {
		return nil, err
	}
	if c.R.Method != http.MethodPost {
		return nil, Fail(-400, "请使用 POST")
	}
	fid := ParseID(c.P["fid"])
	if fid == u.MID {
		return nil, Fail(22001, "不能拉黑自己")
	}
	if c.P["act"] != "5" && c.P["act"] != "6" {
		return nil, Fail(-400, "拉黑参数无效")
	}
	return nil, s.WriteTx(c, func(tx *db.Tx) error {
		if err := RequireReference(c, tx, "SELECT COUNT(*) FROM users WHERE mid=?", fid, 22003, "用户不存在"); err != nil {
			return err
		}
		if c.P["act"] == "6" {
			_, err := tx.ExecContext(c.Context(), "DELETE FROM user_blocks WHERE mid=? AND fid=?", u.MID, fid)
			return err
		}
		return blockCommentAuthor(c, tx, u.MID, fid)
	})
}

func blockCommentAuthor(c *Ctx, tx *db.Tx, mid, fid int64) error {
	if _, err := tx.ExecContext(c.Context(), "DELETE FROM relations WHERE mid=? AND fid=?", mid, fid); err != nil {
		return err
	}
	_, err := tx.ExecContext(c.Context(), "INSERT INTO user_blocks(mid,fid,ctime) VALUES (?,?,?) ON CONFLICT (mid,fid) DO NOTHING", mid, fid, time.Now().Unix())
	return err
}

func (s *Server) communityBlocks(c *Ctx) (any, error) {
	u, err := c.Login()
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.DB.QueryContext(c.Context(), "SELECT b.fid,u.uname,u.face,u.sign,b.ctime FROM user_blocks b JOIN users u ON u.mid=b.fid WHERE b.mid=? ORDER BY b.ctime DESC,b.fid", u.MID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var mid, ctime int64
		var name, face, sign string
		if err := rows.Scan(&mid, &name, &face, &sign, &ctime); err != nil {
			return nil, err
		}
		list = append(list, map[string]any{"mid": mid, "uname": name, "face": render.Face(mid, face), "sign": sign, "mtime": ctime, "attribute": 128, "tag": nil, "special": 0})
	}
	return map[string]any{"list": Page(list, Number(c.P["pn"], 1), min(50, Number(c.P["ps"], 50))), "total": len(list), "re_version": 0}, rows.Err()
}

func (s *Server) adminDismissCommentReports(c *Ctx) (any, error) {
	if _, err := c.CSRF(); err != nil {
		return nil, err
	}
	if c.R.Method != http.MethodPost {
		return nil, Fail(-400, "请使用 POST")
	}
	return s.adminWrite(c, "驳回评论举报", c.P["rpid"], func(tx *db.Tx) error {
		_, err := tx.ExecContext(c.Context(), "UPDATE comment_reports SET status='dismissed' WHERE rpid=? AND status='pending'", ParseID(c.P["rpid"]))
		return err
	})
}

func (s *Server) commentReportSummary(c *Ctx, rpid int64) (int, []string, error) {
	rows, err := s.Store.DB.QueryContext(c.Context(), "SELECT reason,content FROM comment_reports WHERE rpid=? AND status='pending' ORDER BY ctime,mid", rpid)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	count, reasons, seen := 0, []string{}, map[string]bool{}
	for rows.Next() {
		var reason, content string
		if err := rows.Scan(&reason, &content); err != nil {
			return 0, nil, err
		}
		count++
		reason = commentReportReasons[reason]
		if content != "" {
			reason += "：" + content
		}
		if !seen[reason] {
			reasons = append(reasons, reason)
			seen[reason] = true
		}
	}
	return count, reasons, rows.Err()
}

func (s *Server) validateCommentText(c *Ctx, message string) error {
	words, err := s.sensitiveWords(c)
	if err != nil {
		return err
	}
	for _, word := range strings.Split(words, "\n") {
		if word = Trim(word); word != "" && strings.Contains(strings.ToLower(message), strings.ToLower(word)) {
			return Fail(12016, "评论内容包含敏感信息，请修改后重试")
		}
	}
	return nil
}
