package api

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func bplanDurationIn(sec int, d float64) bool {
	switch d {
	case 1:
		return sec <= 600
	case 2:
		return sec > 600 && sec <= 1800
	case 3:
		return sec > 1800 && sec <= 3600
	case 4:
		return sec > 3600
	}
	return true
}
func bplanSortArcs(arcs []store.Archive, order string) bool {
	var key func(store.Archive) int64
	switch order {
	case "click":
		key = func(a store.Archive) int64 { return a.Stat.View }
	case "pubdate":
		key = func(a store.Archive) int64 { return a.Pubdate }
	case "dm":
		key = func(a store.Archive) int64 { return a.Stat.Danmaku }
	case "stow":
		key = func(a store.Archive) int64 { return a.Stat.Favorite }
	case "scores":
		key = func(a store.Archive) int64 { return a.Stat.Reply }
	default:
		return false
	}
	sort.SliceStable(arcs, func(i, j int) bool { return key(arcs[i]) > key(arcs[j]) })
	return true
}

var bplanAreas = []struct {
	code  string
	names []string
}{{"1", []string{"中国大陆"}}, {"2", []string{"日本"}}, {"3", []string{"美国"}}, {"4", []string{"英国"}}, {"6", []string{"中国香港", "中国台湾"}}, {"8", []string{"韩国"}}, {"9", []string{"法国"}}, {"10", []string{"泰国"}}, {"13", []string{"西班牙"}}, {"15", []string{"德国"}}}

func bplanStyles(seasons []store.Season, kind int) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range seasons {
		if s.State == 0 && s.SeasonType == kind {
			for _, v := range s.Styles {
				if !seen[v] {
					out = append(out, v)
					seen[v] = true
				}
			}
		}
	}
	collator := collate.New(language.Chinese)
	sort.SliceStable(out, func(i, j int) bool { return collator.CompareString(out[i], out[j]) < 0 })
	return out
}
func bplanMulti(v string) []string {
	out := []string{}
	for _, x := range strings.Split(v, ",") {
		x = Trim(x)
		if x != "" && x != "-1" {
			out = append(out, x)
		}
	}
	return out
}

var bplanYearRange = regexp.MustCompile(`^\[(\d{4}),(\d{4})\)$`)

func bplanIndexFilter(p map[string]string, s store.Season, styles []string) bool {
	areas := bplanMulti(p["area"])
	if len(areas) > 0 {
		found := false
		for _, code := range areas {
			names := []string{"—"}
			for _, a := range bplanAreas {
				if Number(code, math.NaN()) == Number(a.code, math.NaN()) {
					names = a.names
					break
				}
			}
			for _, v := range s.Areas {
				if slices.Contains(names, v) {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	wanted := bplanMulti(p["style_id"])
	if len(wanted) > 0 {
		found := false
		for _, id := range wanted {
			n := Number(id, math.NaN())
			for i, v := range styles {
				if n == float64(i+1) && slices.Contains(s.Styles, v) {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	year := p["year"]
	if year == "" || year == "-1" {
		year = p["release_date"]
	}
	if year != "" && year != "-1" {
		y := time.Unix(s.Ctime, 0).In(time.Local).Year()
		if m := bplanYearRange.FindStringSubmatch(year); m != nil {
			lo, _ := strconv.Atoi(m[1])
			hi, _ := strconv.Atoi(m[2])
			if y < lo || y >= hi {
				return false
			}
		} else if Number(year, math.NaN()) != float64(y) {
			return false
		}
	}
	finish := "0"
	if s.IsFinish {
		finish = "1"
	}
	copyright := "1"
	if s.Copyright == 3 {
		copyright = "3"
	}
	for _, filter := range []struct{ key, value string }{{"is_finish", finish}, {"copyright", copyright}, {"season_status", "1"}} {
		values := bplanMulti(p[filter.key])
		if len(values) > 0 && !slices.Contains(values, filter.value) {
			return false
		}
	}
	return true
}
func (s *Server) bplanCondition(c *Ctx) (any, error) {
	seasons, err := s.Store.ListSeason(c.Context(), "ORDER BY season_id")
	if err != nil {
		return nil, err
	}
	kind := Number(c.P["season_type"], 1)
	subset := []store.Season{}
	years := []int{}
	for _, v := range seasons {
		if float64(v.SeasonType) == kind {
			subset = append(subset, v)
			year := time.Unix(v.Ctime, 0).In(time.Local).Year()
			if !slices.Contains(years, year) {
				years = append(years, year)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	opt := func(k, n string) render.M { return render.M{"keyword": k, "name": n} }
	all := func() []render.M { return []render.M{opt("-1", "全部")} }
	areas, styles, yearValues := all(), all(), all()
	for _, a := range bplanAreas {
		found := false
		for _, v := range subset {
			for _, name := range v.Areas {
				if slices.Contains(a.names, name) {
					found = true
				}
			}
		}
		if found {
			areas = append(areas, opt(a.code, strings.Join(a.names, "/")))
		}
	}
	// The subset is already restricted without truncating fractional season types.
	for i, name := range bplanStyles(subset, int(kind)) {
		styles = append(styles, opt(strconv.Itoa(i+1), name))
	}
	for _, y := range years {
		yearValues = append(yearValues, opt(strconv.Itoa(y), strconv.Itoa(y)))
	}
	filters := []render.M{{"field": "area", "name": "地区", "values": areas}, {"field": "style_id", "name": "类型", "values": styles}, {"field": "year", "name": "年份", "values": yearValues}}
	if kind != 2 {
		filters = append(filters, render.M{"field": "is_finish", "name": "状态", "values": []render.M{opt("-1", "全部"), opt("1", "完结"), opt("0", "连载")}})
	}
	filters = append(filters, render.M{"field": "copyright", "name": "版权", "values": []render.M{opt("-1", "全部"), opt("3", "独家"), opt("1", "其他")}}, render.M{"field": "season_status", "name": "付费", "values": []render.M{opt("-1", "全部"), opt("1", "免费"), opt("2,6", "付费"), opt("4,6", "大会员")}})
	follow := "追剧人数"
	if kind == 1 || kind == 4 {
		follow = "追番人数"
	}
	order := []render.M{}
	for _, o := range [][2]string{{"0", "更新时间"}, {"2", "播放数量"}, {"4", "最高评分"}, {"3", follow}, {"1", "弹幕数量"}} {
		order = append(order, render.M{"field": o[0], "name": o[1], "sort": "0"})
	}
	return render.M{"filter": filters, "order": order}, nil
}
