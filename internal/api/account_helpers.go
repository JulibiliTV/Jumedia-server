package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"jumedia-server/internal/db"
	"jumedia-server/internal/store"
)

func accountRandom(n int) (string, error) {
	limit := new(big.Int).Exp(big.NewInt(36), big.NewInt(int64(n)), nil)
	v, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", err
	}
	text := v.Text(36)
	return strings.Repeat("0", n-len(text)) + text, nil
}

func accountNewSession(ctx context.Context, q db.Querier, mid int64) (*store.Session, error) {
	parts := make([]string, 5)
	for i, n := range []int{10, 6, 32, 32, 32} {
		var err error
		parts[i], err = accountRandom(n)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().Unix()
	s := &store.Session{Token: parts[0] + "-" + strconv.FormatInt(now+SessionMaxAge, 10) + "-" + parts[1], CSRF: parts[2], Access: parts[3], Refresh: parts[4], MID: mid, Ctime: now}
	return s, store.InsertSession(ctx, q, s)
}

func (s *Server) accountTokenPayload(c *Ctx, session *store.Session, flat bool) map[string]any {
	token := map[string]any{"mid": session.MID, "access_token": session.Access, "refresh_token": session.Refresh, "expires_in": SessionMaxAge}
	cookies := make([]map[string]any, 0, 3)
	for _, v := range []struct{ name, value string }{{"SESSDATA", session.Token}, {"bili_jct", session.CSRF}, {"DedeUserID", strconv.FormatInt(session.MID, 10)}} {
		httpOnly := 0
		if v.name == "SESSDATA" {
			httpOnly = 1
		}
		cookies = append(cookies, map[string]any{"name": v.name, "value": v.value, "http_only": httpOnly, "expires": session.Ctime + SessionMaxAge, "secure": 0})
	}
	origin, _ := url.Parse(s.Origin(c.R))
	domain := c.R.URL.Hostname()
	if origin != nil {
		domain = origin.Hostname()
	}
	data := map[string]any{"token_info": token, "cookie_info": map[string]any{"cookies": cookies, "domains": []string{domain}}}
	if flat {
		for k, v := range token {
			data[k] = v
		}
	}
	return data
}

func accountQRCode(ctx context.Context, q db.Querier, code string, now int64) (*store.QRCode, error) {
	var qr store.QRCode
	var mid sql.NullInt64
	err := q.QueryRowContext(ctx, "SELECT code,status,mid,ctime FROM qrcodes WHERE code=?", code).Scan(&qr.Code, &qr.Status, &mid, &qr.Ctime)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && now-qr.Ctime > 180) {
		return nil, Fail(86038, "二维码已失效")
	}
	qr.MID = mid.Int64
	return &qr, err
}

// Mock has no password maximum. bcrypt accepts at most 72 bytes; retain all
// bytes of longer passwords using an explicitly tagged SHA-256 prehash. Plain
// bcrypt remains compatible with T1 seeds and other domains' password writers.
const accountPrehashPrefix = "bcrypt-sha256:"

func accountPasswordDigest(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	return []byte(base64.StdEncoding.EncodeToString(sum[:]))
}
func accountHashPassword(password string) (string, error) {
	input, prefix := []byte(password), ""
	if len(input) > 72 {
		input, prefix = accountPasswordDigest(password), accountPrehashPrefix
	}
	hash, err := bcrypt.GenerateFromPassword(input, bcrypt.DefaultCost)
	return prefix + string(hash), err
}
func accountCheckPassword(hash, password string) bool {
	input := []byte(password)
	if strings.HasPrefix(hash, accountPrehashPrefix) {
		hash = strings.TrimPrefix(hash, accountPrehashPrefix)
		input = accountPasswordDigest(password)
	} else if len(input) > 72 {
		// CompareHashAndPassword itself truncates; do not authenticate a suffix.
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), input) == nil
}
