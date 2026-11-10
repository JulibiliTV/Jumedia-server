package fntv

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"jumedia-server/internal/emby"
)

// Artwork fields can be a single path or a list of library cover paths.
type artworkPath string

func (p *artworkPath) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*p = artworkPath(single)
		return nil
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		return err
	}
	*p = ""
	for _, path := range paths {
		if path != "" {
			*p = artworkPath(path)
			break
		}
	}
	return nil
}

type entry struct {
	GUID        string      `json:"guid"`
	Title       string      `json:"title"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Category    string      `json:"category"`
	Overview    string      `json:"overview"`
	Poster      string      `json:"poster"`
	Posters     artworkPath `json:"posters"`
	Backdrops   string      `json:"backdrops"`
	FileName    string      `json:"file_name"`
	ReleaseDate string      `json:"release_date"`
	Duration    float64     `json:"duration"`
	Season      *int        `json:"season_number"`
	Episode     int         `json:"episode_number"`
	CanPlay     *int        `json:"can_play"`
}

func (v entry) item() (emby.Item, error) {
	if !emby.ValidPair(v.GUID, v.GUID) {
		return emby.Item{}, ErrResponse
	}
	kind := v.Type
	if kind == "TV" {
		kind = "Series"
	}
	name := v.Title
	if name == "" {
		name = v.FileName
	}
	if name == "" {
		name = v.Name
	}
	poster := v.Poster
	if poster == "" {
		poster = string(v.Posters)
	}
	premiere := v.ReleaseDate
	if t, err := time.Parse("2006-01-02", premiere); err == nil {
		premiere = t.Format(time.RFC3339)
	}
	out := emby.Item{ID: v.GUID, Name: name, Type: kind, Overview: v.Overview, PremiereDate: premiere, RunTimeTicks: int64(v.Duration * 1e7), ParentIndexNumber: v.Season, IndexNumber: v.Episode, ImageTags: map[string]string{"Primary": poster}}
	out.Path = v.FileName
	if v.Backdrops != "" {
		out.BackdropImageTags = []string{v.Backdrops}
	}
	if v.CanPlay != nil && *v.CanPlay == 0 {
		out.LocationType = "Virtual"
	} else if kind == "Movie" || kind == "Video" || kind == "Episode" {
		// A listing is importable; Hydrate resolves the real media ID before saving.
		out.MediaSources = []emby.MediaSource{{ID: v.GUID, Container: container(v.FileName)}}
	}
	return out, nil
}
func container(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "mp4", "m4v", "mov", "mkv", "avi", "ts", "m2ts", "webm", "flv", "wmv":
		return ext
	}
	return "mkv"
}

func (c *Client) Probe(ctx context.Context) (emby.ServerInfo, error) {
	var config struct {
		Name string `json:"server_name"`
		ID   string `json:"server_guid"`
	}
	if err := c.json(ctx, "/api/v1/sys/config", nil, &config); err != nil {
		return emby.ServerInfo{}, err
	}
	if config.ID == "" {
		return emby.ServerInfo{}, ErrResponse
	}
	libs, err := c.Libraries(ctx)
	return emby.ServerInfo{ServerName: config.Name, ServerID: config.ID, Libraries: libs}, err
}
func (c *Client) Libraries(ctx context.Context) ([]emby.Library, error) {
	var raw []entry
	if err := c.json(ctx, "/api/v1/mediadb/list", nil, &raw); err != nil {
		return nil, err
	}
	out := []emby.Library{}
	for _, v := range raw {
		if !emby.ValidPair(v.GUID, v.GUID) {
			return nil, ErrResponse
		}
		name := v.Title
		if name == "" {
			name = v.Name
		}
		kind := "mixed"
		switch v.Category {
		case "Movie":
			kind = "movies"
		case "TV":
			kind = "tvshows"
		case "Music", "Photo", "Audio":
			continue
		}
		out = append(out, emby.Library{ID: v.GUID, Name: name, Type: kind})
	}
	return out, nil
}

type Page struct {
	Items            []emby.Item
	TotalRecordCount int
}

func (c *Client) page(ctx context.Context, library string, start, limit int) (Page, error) {
	var raw struct {
		List  []entry `json:"list"`
		Total *int    `json:"total"`
	}
	body := map[string]any{"ancestor_guid": library, "tags": map[string]any{"type": []string{"Movie", "TV", "Video"}}, "exclude_grouped_video": 1, "sort_column": "title", "sort_type": "ASC", "page": start/limit + 1, "page_size": limit}
	if err := c.json(ctx, "/api/v1/item/list", body, &raw); err != nil {
		return Page{}, err
	}
	if raw.Total == nil || *raw.Total < 0 || (len(raw.List) < limit && start+len(raw.List) < *raw.Total) {
		return Page{}, ErrResponse
	}
	out := Page{Items: []emby.Item{}, TotalRecordCount: *raw.Total}
	for _, v := range raw.List {
		item, err := v.item()
		if err != nil {
			return Page{}, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (c *Client) Browse(ctx context.Context, library, keyword string, start, limit int, ids []string) (Page, error) {
	if !emby.ValidPair(library, library) || start < 0 || limit < 1 || limit > 100 {
		return Page{}, ErrResponse
	}
	if keyword == "" && len(ids) == 0 {
		return c.page(ctx, library, start, limit)
	}
	// Filter only a library-scoped listing. Explicit IDs cannot bypass library membership.
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	matched := []emby.Item{}
	for offset := 0; ; offset += 100 {
		page, err := c.page(ctx, library, offset, 100)
		if err != nil {
			return Page{}, err
		}
		for _, item := range page.Items {
			if (len(ids) == 0 || wanted[item.ID]) && strings.Contains(strings.ToLower(item.Name), strings.ToLower(keyword)) {
				matched = append(matched, item)
				delete(wanted, item.ID)
			}
		}
		if offset+100 >= page.TotalRecordCount || (len(ids) > 0 && len(wanted) == 0) {
			break
		}
	}
	total := len(matched)
	return Page{matched[min(start, total):min(start+limit, total)], total}, nil
}

func (c *Client) Hydrate(ctx context.Context, item emby.Item) (emby.Item, error) {
	var detail entry
	if err := c.json(ctx, "/api/v1/item/"+item.ID, nil, &detail); err != nil {
		return item, err
	}
	if detail.GUID != item.ID {
		return item, ErrResponse
	}
	if detail.Type == "" {
		detail.Type = item.Type
	}
	full, err := detail.item()
	if err != nil {
		return item, err
	}
	if full.Name == "" {
		full.Name = item.Name
	}
	if full.ImageTags["Primary"] == "" {
		full.ImageTags = item.ImageTags
	}
	if full.ParentIndexNumber == nil {
		full.ParentIndexNumber = item.ParentIndexNumber
	}
	if full.IndexNumber == 0 {
		full.IndexNumber = item.IndexNumber
	}
	if full.RunTimeTicks == 0 {
		full.RunTimeTicks = item.RunTimeTicks
	}
	if full.Type == "Series" || full.LocationType == "Virtual" {
		return full, nil
	}
	var play struct {
		Media string `json:"media_guid"`
		Item  entry  `json:"item"`
	}
	if err = c.json(ctx, "/api/v1/play/info", map[string]string{"item_guid": item.ID}, &play); err != nil {
		return item, err
	}
	if !emby.ValidPair(item.ID, play.Media) {
		return item, ErrResponse
	}
	name := detail.FileName
	if name == "" {
		name = play.Item.FileName
	}
	if name == "" {
		name = item.Path
	}
	if full.RunTimeTicks == 0 {
		full.RunTimeTicks = int64(play.Item.Duration * 1e7)
	}
	if name == "" || full.RunTimeTicks == 0 {
		// Stream discovery returns file metadata; playback/transcoding is a separate API.
		var stream struct {
			File struct {
				Name     string  `json:"file_name"`
				Duration float64 `json:"duration"`
			} `json:"file_stream"`
		}
		nonce, err := randomNonce()
		if err != nil {
			return item, err
		}
		body := map[string]any{"media_guid": play.Media, "level": 1, "ip": fmt.Sprintf("%x", md5.Sum([]byte(c.username))), "nonce": nonce, "header": map[string][]string{"User-Agent": {"Jumedia"}}}
		if err = c.json(ctx, "/api/v1/stream", body, &stream); err != nil {
			return item, err
		}
		if name == "" {
			name = stream.File.Name
		}
		if full.RunTimeTicks == 0 {
			full.RunTimeTicks = int64(stream.File.Duration * 1e7)
		}
	}
	full.Path = name
	full.MediaSources = []emby.MediaSource{{ID: play.Media, Container: container(name)}}
	return full, nil
}

func (c *Client) Episodes(ctx context.Context, id string) ([]emby.Item, error) {
	if !emby.ValidPair(id, id) {
		return nil, ErrResponse
	}
	var seasons []entry
	if err := c.json(ctx, "/api/v1/season/list/"+id, nil, &seasons); err != nil {
		return nil, err
	}
	out := []emby.Item{}
	for _, season := range seasons {
		if !emby.ValidPair(season.GUID, season.GUID) {
			return nil, ErrResponse
		}
		if season.Season != nil && *season.Season == 0 {
			continue
		}
		var episodes []entry
		if err := c.json(ctx, "/api/v1/episode/list/"+season.GUID, nil, &episodes); err != nil {
			return nil, err
		}
		for _, ep := range episodes {
			if ep.Type == "" {
				ep.Type = "Episode"
			}
			if ep.Season == nil {
				ep.Season = season.Season
			}
			item, err := ep.item()
			if err != nil {
				return nil, err
			}
			item, err = c.Hydrate(ctx, item)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	return out, nil
}
