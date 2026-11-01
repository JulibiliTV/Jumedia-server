package api

import (
	"fmt"
	"jumedia-server/internal/render"
	"math"
	"sort"
	"time"
)

var weeklySubjects = []string{"秋天的第一场追剧马拉松", "名场面放映室", "这周的快乐来自综艺", "老番新看", "下饭视频大赏", "幕后与彩蛋", "值得二刷的剧集", "周末宅家片单"}
var weeklyReasons = []string{"本周最多人看完", "编辑推荐", "百万播放", "老番新看", "综艺幕后", "下饭必备", "名场面", "幕后揭秘", "二刷必看", "高分好评"}

func weeklyEpoch() int64 { return time.Date(2026, 1, 2, 18, 0, 0, 0, time.Local).Unix() }
func weeklyCurrent(at time.Time) float64 {
	return math.Max(1, math.Floor(float64(at.Unix()-weeklyEpoch())/(7*86400))+1)
}
func weeklyConfig(n float64) render.M {
	etime := float64(weeklyEpoch()) + (n-1)*7*86400
	label := fmt.Sprintf("第%v期", n)
	v := render.M{"id": n, "type": "weekly_selected", "number": n, "label": label, "name": fmt.Sprintf("%d%s", time.Unix(int64(etime), 0).In(time.Local).Year(), label), "stime": etime - 7*86400, "etime": etime, "status": 2, "share_title": "每周必看 · " + label, "share_subtitle": ""}
	if math.Trunc(n) == n {
		v["subject"] = weeklySubjects[int(n)%len(weeklySubjects)]
	}
	return v
}
func (s *Server) discoverWeeklyList(c *Ctx) (any, error) {
	cur := weeklyCurrent(time.Now())
	list := []render.M{}
	for i := 0; i < min(int(cur), 30); i++ {
		n := cur - float64(i)
		v := weeklyConfig(n)
		list = append(list, render.M{"number": n, "subject": v["subject"], "status": 2, "name": v["name"]})
	}
	return render.M{"list": list}, nil
}
func (s *Server) discoverWeeklyOne(c *Ctx) (any, error) {
	cur := weeklyCurrent(time.Now())
	n := NumberWide(c.P["number"], cur)
	if n < 1 || n > cur {
		return nil, Fail(-404, "啥都木有")
	}
	pool, err := s.Store.PublishedArchives(c.Context())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(pool, func(i, j int) bool {
		return pool[i].Stat.View+pool[i].Stat.Like*10 > pool[j].Stat.View+pool[j].Stat.Like*10
	})
	offset := int(math.Mod(n*3, float64(max(1, len(pool)))))
	picks := append(pool[offset:], pool[:offset]...)
	list := []render.M{}
	for i, a := range picks[:min(12, len(picks))] {
		v, e := s.Render.ArcView(c.Context(), a, render.M{"rcmd_reason": weeklyReasons[(a.AID+int64(i))%int64(len(weeklyReasons))]})
		if e != nil {
			return nil, e
		}
		list = append(list, v)
	}
	return render.M{"config": weeklyConfig(n), "reminder": "每周五 18:00 更新", "list": list}, nil
}
