package store

import "context"

type DandanplaySync struct {
	CID          int64  `json:"cid"`
	AID          int64  `json:"aid"`
	Media        string `json:"-"`
	Filename     string `json:"-"`
	Duration     int    `json:"-"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	EpisodeID    int64  `json:"episode_id"`
	AnimeTitle   string `json:"anime_title"`
	EpisodeTitle string `json:"episode_title"`
	Count        int    `json:"count"`
	UpdatedAt    int64  `json:"updated_at"`
}

const DandanplaySyncColumns = "cid,aid,media,filename,duration,status,message,episode_id,anime_title,episode_title,count,updated_at"

func ScanDandanplaySync(row scanner) (*DandanplaySync, error) {
	var v DandanplaySync
	err := row.Scan(&v.CID, &v.AID, &v.Media, &v.Filename, &v.Duration, &v.Status, &v.Message, &v.EpisodeID, &v.AnimeTitle, &v.EpisodeTitle, &v.Count, &v.UpdatedAt)
	return &v, err
}

func (s *Store) DandanplayComments(ctx context.Context, cid int64) ([]Danmaku, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,cid,progress,mode,fontsize,color,content,ctime,weight FROM dandanplay_comments WHERE cid=? ORDER BY progress,id", cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Danmaku{}
	for rows.Next() {
		var v Danmaku
		if err := rows.Scan(&v.ID, &v.CID, &v.Progress, &v.Mode, &v.Fontsize, &v.Color, &v.Content, &v.Ctime, &v.Weight); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
