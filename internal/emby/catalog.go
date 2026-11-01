// Package emby provides the server-only Emby client and portable media catalog.
package emby

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
)

type Images struct{ Primary, Backdrop, Thumb *string }

func Image(values ...*string) string {
	for _, v := range values {
		if v != nil {
			return *v
		}
	}
	return ""
}
func (i Images) Wide() string { return Image(i.Backdrop, i.Thumb, i.Primary) }

type Episode struct {
	ID            string `json:"id"`
	SourceID      string `json:"sourceId"`
	Container     string
	Width, Height int
	Name          string
	Season, Index int
	Overview      string
	Runtime       int
	Premiere      int64
	Image         *string
}
type Series struct {
	ID, Library, Name, Overview string
	Year                        int
	Genres, Countries, Studios  []string
	Rating                      float64
	Ended                       bool
	AirDays                     []string
	AirTime                     string
	Premiere                    int64
	Images                      Images
	Episodes                    []Episode
}
type Movie struct {
	ID                      string `json:"id"`
	SourceID                string `json:"sourceId"`
	Container               string
	Width, Height           int
	Library, Name, Overview string
	Year                    int
	Genres, Countries       []string
	Rating                  float64
	Runtime                 int
	Premiere                int64
	Images                  Images
}
type Catalog struct {
	Server, Username string
	GeneratedAt      int64
	Series           []Series
	Movies           []Movie
}

func LoadCatalog(path string) (Catalog, error) {
	var c Catalog
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || path == "" {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	return c, err
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func ValidPair(item, source string) bool {
	return identifier.MatchString(item) && identifier.MatchString(source)
}
func Ref(item, source string) string { return "emby:" + item + ":" + source }
func ParseRef(ref string) (item, source string, ok bool) {
	p := strings.Split(ref, ":")
	if len(p) != 3 || p[0] != "emby" || !ValidPair(p[1], p[2]) {
		return "", "", false
	}
	return p[1], p[2], true
}
func MP4Path(item, source string) string {
	return "/media/emby/" + item + ".mp4?source=" + url.QueryEscape(source)
}
func HLSPath(item, source string) string {
	return "/media/emby/" + item + "/hls/index.m3u8?source=" + url.QueryEscape(source)
}
