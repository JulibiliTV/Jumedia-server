// Package dandanplay implements the server-side 弹弹play API client.
package dandanplay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"jumedia-server/internal/store"
)

const DefaultAPI = "https://api.dandanplay.net"

type Client struct {
	base, appID, secret string
	http                *http.Client
}

type Match struct {
	EpisodeID    int64  `json:"episodeId"`
	AnimeTitle   string `json:"animeTitle"`
	EpisodeTitle string `json:"episodeTitle"`
}

func New(base, appID, secret string) *Client {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultAPI
	}
	if !strings.HasSuffix(base, "/api/v2") {
		base += "/api/v2"
	}
	return &Client{base, appID, secret, &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("弹弹play 重定向过多")
			}
			for _, h := range []string{"X-AppId", "X-Signature", "X-Timestamp"} {
				req.Header.Del(h)
			}
			return nil
		},
	}}
}

func (c *Client) request(ctx context.Context, method, path, query string, body any, out any) error {
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path+query, input)
	if err != nil {
		return errors.New("弹弹play 接口地址无效")
	}
	if c.appID != "" && c.secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		h := sha256.Sum256([]byte(c.appID + ts + req.URL.Path + c.secret))
		req.Header.Set("X-AppId", c.appID)
		req.Header.Set("X-Timestamp", ts)
		req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(h[:]))
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("弹弹play 网络请求失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("弹弹play 请求失败（HTTP %d）", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out); err != nil {
		return errors.New("弹弹play 响应格式无效")
	}
	return nil
}

// Match only accepts a unique result; ambiguous candidates need human selection.
func (c *Client) Match(ctx context.Context, filename string, duration int) (*Match, error) {
	var result struct {
		Success bool
		Matches []Match
	}
	err := c.request(ctx, "POST", "/match", "", map[string]any{"fileName": filename, "videoDuration": duration, "matchMode": "fileNameOnly"}, &result)
	if err != nil {
		return nil, err
	}
	if !result.Success {
		return nil, errors.New("弹弹play 匹配请求失败")
	}
	if len(result.Matches) != 1 || result.Matches[0].EpisodeID <= 0 {
		return nil, nil
	}
	return &result.Matches[0], nil
}

func (c *Client) Comments(ctx context.Context, episodeID int64) ([]store.Danmaku, error) {
	var result struct {
		Count    *int `json:"count"`
		Comments []struct {
			CID int64  `json:"cid"`
			P   string `json:"p"`
			M   string `json:"m"`
		} `json:"comments"`
	}
	err := c.request(ctx, "GET", "/comment/"+strconv.FormatInt(episodeID, 10), "?withRelated=true&format=json&segmentflag=false", nil, &result)
	if err != nil {
		return nil, err
	}
	if result.Comments == nil {
		if result.Count != nil && *result.Count == 0 {
			return []store.Danmaku{}, nil
		}
		return nil, errors.New("弹弹play 弹幕列表缺失")
	}
	items := []store.Danmaku{}
	seen := map[int64]bool{}
	for _, comment := range result.Comments {
		p := strings.Split(comment.P, ",")
		if len(p) < 3 || strings.TrimSpace(comment.M) == "" || seen[comment.CID] {
			continue
		}
		seconds, e1 := strconv.ParseFloat(p[0], 64)
		mode, e2 := strconv.Atoi(p[1])
		color, e3 := strconv.Atoi(p[2])
		if e1 != nil || e2 != nil || e3 != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > math.MaxInt32/1000 || color < 0 || color > 0xffffff || (mode != 1 && mode != 4 && mode != 5) {
			continue
		}
		seen[comment.CID] = true
		items = append(items, store.Danmaku{Progress: int(math.Round(seconds * 1000)), Mode: mode, Fontsize: 25, Color: color, Content: comment.M, Weight: 5, Ctime: time.Now().Unix()})
	}
	return items, nil
}
