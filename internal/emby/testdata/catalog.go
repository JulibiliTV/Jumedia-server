// Package testdata supplies an offline, deliberately small Emby catalog.
package testdata

import (
	"fmt"
	"jumedia-server/internal/emby"
)

func Catalog() emby.Catalog {
	c := emby.Catalog{Server: "https://emby.example.invalid/emby", Username: "fixture"}
	names := []string{"星海旅团", "放学后的咖啡馆", "剑与月光的协奏曲", "电波少女侦探社", "山海异闻录", "机甲纪元：觉醒", "小城四季", "周末音乐会"}
	styles := [][]string{{"科幻", "冒险"}, {"日常", "治愈"}, {"奇幻", "战斗"}, {"悬疑", "校园"}, {"奇幻", "国风"}, {"科幻", "机战"}, {"家庭", "生活"}, {"音乐", "真人秀"}}
	images := func(id string) emby.Images {
		primary := "https://images.example.test/" + id + "-primary.jpg"
		backdrop := "https://images.example.test/" + id + "-backdrop.jpg"
		return emby.Images{Primary: &primary, Backdrop: &backdrop}
	}
	for i, name := range names {
		library, country := "动漫", "Japan"
		if i >= 4 {
			country = "China"
		}
		if i == 6 {
			library = "电视剧"
		}
		if i == 7 {
			library = "综艺"
		}
		s := emby.Series{ID: fmt.Sprintf("series%d", i), Library: library, Name: name, Overview: "测试媒体库：" + name, Countries: []string{country}, Genres: styles[i], Images: images(fmt.Sprint(i)), Ended: i%2 == 1}
		for j := 0; j < 6+i%6; j++ {
			id := fmt.Sprintf("item%d_%d", i, j)
			image := "https://images.example.test/" + id + ".jpg"
			s.Episodes = append(s.Episodes, emby.Episode{ID: id, SourceID: "source_" + id, Name: fmt.Sprintf("第%d集", j+1), Index: j + 1, Runtime: 240 + j*60, Container: "mp4", Width: 1920, Height: 1080, Image: &image})
		}
		c.Series = append(c.Series, s)
	}
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("movie%d", i)
		c.Movies = append(c.Movies, emby.Movie{ID: id, SourceID: "source_" + id, Name: fmt.Sprintf("电影%d", i), Library: "电影", Genres: []string{"剧情", "都市"}, Countries: []string{"China"}, Runtime: 3600, Images: images(id), Container: "mp4", Width: 1920, Height: 1080})
	}
	return c
}
