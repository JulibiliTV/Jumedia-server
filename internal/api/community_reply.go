package api

import (
	"database/sql"
	"errors"
	"sort"
	"time"

	"jumedia-server/internal/db"
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) communityReplies(c *Ctx) (any, error) {
	oid := ParseID(c.P["oid"])
	pn, ps := Number(c.P["pn"], 1), Number(c.P["ps"], 20)
	all, err := s.Store.ListReply(c.Context(), "WHERE oid=? ORDER BY rpid", oid)
	if err != nil {
		return nil, err
	}
	all, err = s.Store.VisibleReplies(c.Context(), ActorID(c), all)
	if err != nil {
		return nil, err
	}
	top := []store.Reply{}
	for _, r := range all {
		if r.Root == 0 {
			v, e := s.Store.ReplyByID(c.Context(), r.RPID)
			if e != nil {
				return nil, e
			}
			top = append(top, *v)
		}
	}
	sort.SliceStable(top, func(i, j int) bool {
		if c.P["sort"] != "0" && len(top[i].Likers) != len(top[j].Likers) {
			return len(top[i].Likers) > len(top[j].Likers)
		}
		return top[i].Ctime > top[j].Ctime
	})
	replies := []render.M{}
	for _, r := range Page(top, pn, ps) {
		v, e := s.Render.ReplyJSON(c.Context(), r, ActorID(c))
		if e != nil {
			return nil, e
		}
		replies = append(replies, v)
	}
	var mid int64
	a, err := s.Store.ArchiveByID(c.Context(), oid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if a != nil {
		mid = a.MID
	}
	return render.M{"page": render.M{"num": pn, "size": ps, "count": len(top), "acount": len(all)}, "replies": replies, "upper": render.M{"mid": mid}}, nil
}
func (s *Server) communityReply(c *Ctx, id int64) (*store.Reply, error) {
	r, e := s.Store.ReplyByID(c.Context(), id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, Fail(12022, "评论已被删除")
	}
	return r, e
}
func (s *Server) communityReplyChildren(c *Ctx) (any, error) {
	root, e := s.communityReply(c, ParseID(c.P["root"]))
	if e != nil {
		return nil, e
	}
	visible, e := s.Store.VisibleReplies(c.Context(), ActorID(c), []store.Reply{*root})
	if e != nil {
		return nil, e
	}
	if len(visible) == 0 {
		return nil, Fail(12022, "评论已被删除")
	}
	if oid := ParseID(c.P["oid"]); oid != 0 && root.OID != oid {
		return nil, Fail(12022, "评论已被删除")
	}
	children, e := s.Store.ListReply(c.Context(), "WHERE root=? ORDER BY ctime,rpid", root.RPID)
	if e != nil {
		return nil, e
	}
	children, e = s.Store.VisibleReplies(c.Context(), ActorID(c), children)
	if e != nil {
		return nil, e
	}
	pn := Number(c.P["pn"], 1)
	replies := []render.M{}
	for _, r := range Page(children, pn, 20) {
		full, e := s.Store.ReplyByID(c.Context(), r.RPID)
		if e != nil {
			return nil, e
		}
		v, e := s.Render.ReplyJSON(c.Context(), *full, ActorID(c), false)
		if e != nil {
			return nil, e
		}
		replies = append(replies, v)
	}
	v, e := s.Render.ReplyJSON(c.Context(), *root, ActorID(c), false)
	if e != nil {
		return nil, e
	}
	return render.M{"page": render.M{"num": pn, "size": 20, "count": len(children)}, "root": v, "replies": replies}, nil
}
func (s *Server) communityReplyAdd(c *Ctx) (any, error) {
	if c.Me != nil && c.Me.Status != 0 {
		return nil, Fail(-102, "账号已被封停")
	}
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	if c.R.Method != "POST" {
		return nil, Fail(-400, "请使用 POST")
	}
	if c.P["type"] != "1" {
		return nil, Fail(12009, "评论主体的type不合法")
	}
	message := Trim(c.P["message"])
	if message == "" || UTF16Length(message) > 1000 {
		return nil, Fail(12025, "评论字数需在 1–1000 字之间")
	}
	if err := s.validateCommentText(c, message); err != nil {
		return nil, err
	}
	r := store.Reply{OID: ParseID(c.P["oid"]), MID: u.MID, Root: ParseID(c.P["root"]), Parent: ParseID(c.P["parent"]), Message: message, Ctime: time.Now().Unix()}
	for _, key := range []string{"root", "parent"} {
		if value := c.P[key]; value != "" && value != "0" && ParseID(value) <= 0 {
			return nil, Fail(12006, "没有该评论")
		}
	}
	settings, e := s.Store.Settings(c.Context())
	if e != nil {
		return nil, e
	}
	e = s.WriteTx(c, func(tx *db.Tx) error {
		r.Ctime = time.Now().Unix()
		if err := replyAddPolicy(c, tx, r, settings); err != nil {
			return err
		}
		var e error
		r.RPID, e = store.NextID(c.Context(), tx)
		if e != nil {
			return e
		}
		if e = store.InsertReply(c.Context(), tx, &r); e != nil {
			return e
		}
		return communitySyncReplyCount(c, tx, r.OID)
	})
	if e != nil {
		return nil, e
	}
	v, e := s.Render.ReplyJSON(c.Context(), r, u.MID)
	if e != nil {
		return nil, e
	}
	return replyAddResponse(r, v), nil
}
func (s *Server) communityReplyDelete(c *Ctx) (any, error) {
	u, e := c.CSRF()
	if e != nil {
		return nil, e
	}
	r, e := s.communityReply(c, ParseID(c.P["rpid"]))
	if e != nil {
		return nil, e
	}
	if r.MID != u.MID && u.Role != "admin" {
		a, e := s.Store.ArchiveByID(c.Context(), r.OID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if a == nil || a.MID != u.MID {
			return nil, Fail(-403, "没有权限")
		}
	}
	e = s.WriteTx(c, func(tx *db.Tx) error {
		return communityDeleteReplies(c, tx, r.RPID)
	})
	return nil, e
}

// Call within the reply mutation's transaction. Non-archive oids are a no-op,
// matching mock syncReplyCount; children count just like root replies.
func communitySyncReplyCount(c *Ctx, tx *db.Tx, oid int64) error {
	_, err := tx.ExecContext(c.Context(), "UPDATE archives SET stat_reply=(SELECT COUNT(*) FROM replies WHERE oid=?) WHERE aid=?", oid, oid)
	return err
}

func communityDeleteReplies(c *Ctx, tx *db.Tx, rpid int64) error {
	// Existing root references may cross archives or nest. Include every row
	// reached by ON DELETE CASCADE before deleting, while WriteTx holds the lock.
	rows, err := tx.QueryContext(c.Context(), `WITH RECURSIVE deleted_replies(rpid,oid) AS (
		SELECT rpid,oid FROM replies WHERE rpid=?
		UNION
		SELECT r.rpid,r.oid FROM replies r JOIN deleted_replies d ON r.root=d.rpid
	) SELECT DISTINCT oid FROM deleted_replies ORDER BY oid`, rpid)
	if err != nil {
		return err
	}
	var oids []int64
	for rows.Next() {
		var oid int64
		if err := rows.Scan(&oid); err != nil {
			rows.Close()
			return err
		}
		oids = append(oids, oid)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(c.Context(), "DELETE FROM replies WHERE rpid=? OR root=?", rpid, rpid); err != nil {
		return err
	}
	for _, oid := range oids {
		if err := communitySyncReplyCount(c, tx, oid); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(c.Context(), "UPDATE comment_reports SET status='removed' WHERE status='pending' AND NOT EXISTS (SELECT 1 FROM replies r WHERE r.rpid=comment_reports.rpid)"); err != nil {
		return err
	}
	return nil
}

func (s *Server) communityReplyReact(reaction int) Handler {
	return func(c *Ctx) (any, error) {
		u, e := c.CSRF()
		if e != nil {
			return nil, e
		}
		r, e := s.communityReply(c, ParseID(c.P["rpid"]))
		if e != nil {
			return nil, e
		}
		// A single UPSERT preserves mutual exclusion and makes simultaneous
		// repeats idempotent even when the reaction row did not exist yet.
		if c.P["action"] == "1" {
			_, e = s.Store.DB.ExecContext(c.Context(), "INSERT INTO reply_reactions (rpid,mid,reaction) VALUES (?,?,?) ON CONFLICT (rpid,mid) DO UPDATE SET reaction=excluded.reaction", r.RPID, u.MID, reaction)
		} else {
			_, e = s.Store.DB.ExecContext(c.Context(), "DELETE FROM reply_reactions WHERE rpid=? AND mid=?", r.RPID, u.MID)
		}
		return nil, e
	}
}
