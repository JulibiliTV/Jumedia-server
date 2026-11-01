package store

import (
	"context"
	"encoding/json"
	"jumedia-server/internal/db"
)

type ArchiveMeta struct {
	Title string `json:"title"`
	Desc  string `json:"desc"`
	TID   int64  `json:"tid"`
	Tag   string `json:"tag"`
	Pic   string `json:"pic"`
}
type ArchiveOrigin struct {
	Type    string      `json:"type"`
	Library string      `json:"library"`
	Locked  []string    `json:"locked"`
	Meta    ArchiveMeta `json:"meta"`
}
type CollectionMeta struct {
	Title string `json:"title"`
	Intro string `json:"intro"`
	Cover string `json:"cover"`
}
type CollectionOrigin struct {
	Type   string         `json:"type"`
	Locked []string       `json:"locked"`
	Meta   CollectionMeta `json:"meta"`
}
type DataSourceLibrary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	Items        int    `json:"items"`
	CollectionID int64  `json:"collection_id"`
}
type DataSource struct {
	ID          int64               `json:"id"`
	MID         int64               `json:"mid"`
	System      bool                `json:"system"`
	Type        string              `json:"type"`
	Name        string              `json:"name"`
	URL         string              `json:"url"`
	Username    string              `json:"username"`
	PasswordEnc string              `json:"-"`
	ServerName  string              `json:"server_name"`
	ServerID    string              `json:"server_id"`
	Libraries   []DataSourceLibrary `json:"libraries"`
	AutoSync    bool                `json:"auto_sync"`
	Status      string              `json:"status"`
	Message     string              `json:"message"`
	LastSync    int64               `json:"last_sync"`
	Ctime       int64               `json:"ctime"`
}

const sourceColumns = "id,mid,system,type,name,url,username,password_enc,server_name,server_id,libraries,auto_sync,status,message,last_sync,ctime"

func scanDataSource(row scanner) (*DataSource, error) {
	var v DataSource
	var libraries string
	err := row.Scan(&v.ID, &v.MID, &v.System, &v.Type, &v.Name, &v.URL, &v.Username, &v.PasswordEnc, &v.ServerName, &v.ServerID, &libraries, &v.AutoSync, &v.Status, &v.Message, &v.LastSync, &v.Ctime)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(libraries), &v.Libraries)
	return &v, err
}
func DataSourceByID(ctx context.Context, q db.Querier, id any) (*DataSource, error) {
	return scanDataSource(q.QueryRowContext(ctx, "SELECT "+sourceColumns+" FROM data_sources WHERE id=?", id))
}
func (s *Store) ListDataSource(ctx context.Context) ([]DataSource, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+sourceColumns+" FROM data_sources ORDER BY system DESC,ctime,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DataSource{}
	for rows.Next() {
		v, e := scanDataSource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
func SaveDataSource(ctx context.Context, q db.Querier, v *DataSource) error {
	b, err := json.Marshal(v.Libraries)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO data_sources ("+sourceColumns+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,url=excluded.url,username=excluded.username,password_enc=excluded.password_enc,server_name=excluded.server_name,server_id=excluded.server_id,libraries=excluded.libraries,auto_sync=excluded.auto_sync,status=excluded.status,message=excluded.message,last_sync=excluded.last_sync", v.ID, v.MID, BoolInt(v.System), v.Type, v.Name, v.URL, v.Username, v.PasswordEnc, v.ServerName, v.ServerID, string(b), BoolInt(v.AutoSync), v.Status, v.Message, v.LastSync, v.Ctime)
	return err
}

func ArchiveFrom(ctx context.Context, q db.Querier, id any) (*Archive, error) {
	a, err := scanArchive(q.QueryRowContext(ctx, "SELECT "+archiveColumns+" FROM archives WHERE aid=?", id))
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, "SELECT "+pageColumns+" FROM archive_pages WHERE aid=? ORDER BY position", a.AID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	a.Pages = []Page{}
	for rows.Next() {
		p, e := scanPage(rows)
		if e != nil {
			return nil, e
		}
		a.Pages = append(a.Pages, *p)
	}
	return a, rows.Err()
}
func CollectionFrom(ctx context.Context, q db.Querier, id any) (*Collection, error) {
	return scanCollection(q.QueryRowContext(ctx, "SELECT "+collectionColumns+" FROM collections WHERE id=?", id))
}
func SaveArchiveOrigin(ctx context.Context, q db.Querier, a *Archive) error {
	b, err := json.Marshal(a.Origin)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE archives SET title=?,description=?,tid=?,tag=?,pic=?,state=?,reason=?,origin_ds=?,origin_item=?,origin=? WHERE aid=?", a.Title, a.Desc, a.TID, a.Tag, a.Pic, a.State, a.Reason, a.OriginDS, a.OriginItem, string(b), a.AID)
	return err
}
func SaveCollectionOrigin(ctx context.Context, q db.Querier, c *Collection) error {
	b, err := json.Marshal(c.Origin)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE collections SET title=?,intro=?,cover=?,origin_ds=?,origin_library=?,origin=? WHERE id=?", c.Title, c.Intro, c.Cover, c.OriginDS, c.OriginLibrary, string(b), c.ID)
	return err
}
