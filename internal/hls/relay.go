// Package hls ports the browser Mock's playlist parsing and wall-clock relay.
package hls

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Variant struct {
	URI, Info  string
	Bandwidth  int64
	Resolution string
}
type Segment struct {
	URI      string
	Duration float64
	Key, Map string
}
type Playlist struct {
	Kind           string
	Variants       []Variant
	Extra          []string
	Ended          bool
	TargetDuration int
	Version        int
	Segments       []Segment
	Format         string
	Duration       float64
}

var uriAttr = regexp.MustCompile(`URI="([^"]+)"`)
var bandwidthAttr = regexp.MustCompile(`BANDWIDTH=(\d+)`)
var resolutionAttr = regexp.MustCompile(`RESOLUTION=([\dx]+)`)

func absolute(uri, base string) string {
	b, e := url.Parse(base)
	if e != nil {
		return uri
	}
	u, e := url.Parse(uri)
	if e != nil {
		return uri
	}
	return b.ResolveReference(u).String()
}
func absoluteAttr(line, base string) string {
	m := uriAttr.FindStringSubmatchIndex(line)
	if m == nil {
		return line
	}
	return line[:m[2]] + absolute(line[m[2]:m[3]], base) + line[m[3]:]
}
func attr(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func ParsePlaylist(text, base string) (Playlist, error) {
	p := Playlist{}
	lines := []string{}
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 || lines[0] != "#EXTM3U" {
		return p, errors.New("不是有效的 m3u8 播放列表")
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "#EXT-X-STREAM-INF") {
			p.Kind = "master"
			break
		}
	}
	if p.Kind == "master" {
		for i := 0; i < len(lines); i++ {
			l := lines[i]
			if strings.HasPrefix(l, "#EXT-X-STREAM-INF") {
				i++
				if i >= len(lines) || strings.HasPrefix(lines[i], "#") {
					continue
				}
				bw, _ := strconv.ParseInt(attr(bandwidthAttr, l), 10, 64)
				p.Variants = append(p.Variants, Variant{absolute(lines[i], base), l, bw, attr(resolutionAttr, l)})
			} else if strings.HasPrefix(l, "#EXT-X-MEDIA") {
				p.Extra = append(p.Extra, absoluteAttr(l, base))
			}
		}
		if len(p.Variants) == 0 {
			return p, errors.New("主播放列表中没有可用的码率")
		}
		return p, nil
	}
	p.Kind = "media"
	p.Version = 3
	p.Format = "ts"
	var duration, target float64
	var key, init string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "#EXTINF:"):
			duration, _ = strconv.ParseFloat(strings.SplitN(l[8:], ",", 2)[0], 64)
		case strings.HasPrefix(l, "#EXT-X-TARGETDURATION:"):
			target, _ = strconv.ParseFloat(strings.SplitN(l, ":", 2)[1], 64)
		case strings.HasPrefix(l, "#EXT-X-VERSION:"):
			p.Version, _ = strconv.Atoi(strings.SplitN(l, ":", 2)[1])
		case strings.HasPrefix(l, "#EXT-X-KEY"):
			key = absoluteAttr(l, base)
			if strings.Contains(l, "METHOD=NONE") {
				key = ""
			}
		case strings.HasPrefix(l, "#EXT-X-MAP"):
			init = absoluteAttr(l, base)
		case l == "#EXT-X-ENDLIST" || l == "#EXT-X-PLAYLIST-TYPE:VOD":
			p.Ended = true
		case !strings.HasPrefix(l, "#"):
			if duration == 0 || math.IsNaN(duration) {
				duration = target
			}
			if duration == 0 {
				duration = 6
			}
			// Invalid non-positive/infinite timing cannot form a finite relay window.
			if duration < 0 || math.IsInf(duration, 0) || math.IsNaN(duration) {
				return p, errors.New("播放列表分片时长无效")
			}
			p.Segments = append(p.Segments, Segment{absolute(l, base), duration, key, init})
			p.Duration += duration
			duration = 0
		}
	}
	if len(p.Segments) == 0 {
		return p, errors.New("播放列表中没有分片")
	}
	if init != "" {
		p.Format = "fmp4"
	}
	for _, s := range p.Segments {
		target = math.Max(target, s.Duration)
	}
	if math.IsInf(p.Duration, 0) || math.IsInf(target, 0) || math.IsNaN(target) {
		return p, errors.New("播放列表分片时长无效")
	}
	p.TargetDuration = int(math.Ceil(target))
	return p, nil
}
func RelayMaster(p Playlist, variantURL func(int) string) string {
	lines := append([]string{"#EXTM3U", "#EXT-X-VERSION:3"}, p.Extra...)
	for i, v := range p.Variants {
		lines = append(lines, v.Info, variantURL(i))
	}
	return strings.Join(lines, "\n") + "\n"
}
func Passthrough(text, base string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") {
			lines[i] = absoluteAttr(t, base)
		} else {
			lines[i] = absolute(t, base)
		}
	}
	return strings.Join(lines, "\n")
}

type WindowOptions struct {
	Loop bool
	Size int
}
type Window struct {
	Text     string
	Ended    bool
	Position float64
}

func LiveWindow(p Playlist, startedAt, now float64, options ...WindowOptions) Window {
	opts := WindowOptions{Loop: true}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Size <= 0 {
		opts.Size = 6
	}
	n := len(p.Segments)
	size := min(opts.Size, n)
	if n == 0 || p.Duration <= 0 {
		return Window{Text: "#EXTM3U\n#EXT-X-ENDLIST\n", Ended: true}
	}
	elapsed := math.Max(0, now-startedAt)
	ended := !opts.Loop && elapsed >= p.Duration
	preroll := 0.0
	for _, s := range p.Segments[:size] {
		preroll += s.Duration
	}
	t := elapsed + preroll
	cycle := math.Floor(t / p.Duration)
	within := t - cycle*p.Duration
	j := 0
	for j < n && within >= p.Segments[j].Duration {
		within -= p.Segments[j].Duration
		j++
	}
	newest := int64(cycle)*int64(n) + int64(j) - 1
	if ended || (!opts.Loop && newest > int64(n-1)) {
		newest = int64(n - 1)
	}
	newest = max(newest, int64(size-1))
	first := newest - int64(size) + 1
	lines := []string{"#EXTM3U", fmt.Sprintf("#EXT-X-VERSION:%d", max(3, p.Version)), fmt.Sprintf("#EXT-X-TARGETDURATION:%d", p.TargetDuration), fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d", first), fmt.Sprintf("#EXT-X-DISCONTINUITY-SEQUENCE:%d", first/int64(n))}
	key, init := "", ""
	for g := first; g <= newest; g++ {
		s := p.Segments[g%int64(n)]
		if g > first && g%int64(n) == 0 {
			lines = append(lines, "#EXT-X-DISCONTINUITY")
		}
		if s.Map != "" && (s.Map != init || g%int64(n) == 0) {
			lines = append(lines, s.Map)
			init = s.Map
		}
		if s.Key != key || (g == first && s.Key != "") {
			k := s.Key
			if k == "" {
				k = "#EXT-X-KEY:METHOD=NONE"
			}
			lines = append(lines, k)
			key = s.Key
		}
		lines = append(lines, fmt.Sprintf("#EXTINF:%.3f,", s.Duration), s.URI)
	}
	if ended {
		lines = append(lines, "#EXT-X-ENDLIST")
	}
	return Window{strings.Join(lines, "\n") + "\n", ended, math.Mod(elapsed, p.Duration)}
}
