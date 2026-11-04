package api

import (
	"context"
	"testing"

	"jumedia-server/internal/emby"
	"jumedia-server/internal/store"
)

func TestDandanplayQueryName(t *testing.T) {
	for _, tt := range []struct {
		title, part, previous string
		kind, position        int
		want                  string
	}{
		{"待审核：测试剧", "第1集 开始", "", 0, 0, "测试剧.S01E01"},
		{"【待审核】测试剧", "S02E03 分集标题", "", 0, 2, "测试剧.S02E03"},
		{"审核中：测试剧", "第3集 标题", "release.S02E03.1080p.mkv", 0, 2, "测试剧.S02E03"},
		{"测试剧", "12", "", 1, 11, "测试剧.S01E12"},
		{"测试剧", "特别篇", "", 1, 3, "测试剧.S01E04"},
		{"待审核：电影名称", "正片", "release.1080p.mkv", 2, 0, "电影名称"},
		{"[待审核] 电影名称", "待审核：电影名称", "release.S01E01.mkv", 0, 0, "电影名称"},
		{"待审：普通视频", "片段介绍", "", 0, 0, "普通视频"},
		{"电影名称", "1", "", 2, 0, "电影名称"},
		{"电影名称", "正片", "release.S01E01.mkv", 0, 0, "电影名称"},
		{"等待审核的人", "正片", "", 2, 0, "等待审核的人"},
		{"待审核：审核中：测试剧", "S01E100 标题", "", 0, 99, "测试剧.S01E100"},
	} {
		t.Run(tt.want+tt.part, func(t *testing.T) {
			if got := dandanplayQueryName(tt.title, tt.part, tt.previous, tt.kind, tt.position); got != tt.want {
				t.Fatalf("query name = %q, want %q", got, tt.want)
			}
		})
	}
}

type dandanplayNameEpisodes struct{ episodes []emby.Item }

func (f dandanplayNameEpisodes) Episodes(context.Context, string) ([]emby.Item, error) {
	return f.episodes, nil
}

func TestDandanplaySourceNames(t *testing.T) {
	ep := sourceEpisode("episode", "分集标题", 2, 3)
	ep.Path = "/private/release.S09E99.1080p.mkv"
	f := dandanplayNameEpisodes{[]emby.Item{ep}}
	mirror, err := mirrorSource(t.Context(), f, &store.DataSource{ID: 1}, nil, store.DataSourceLibrary{}, emby.Item{ID: "show", Type: "Series", Name: "待审核：测试剧"})
	if err != nil || mirror.filenames["episode"] != "测试剧.S02E03" {
		t.Fatalf("series match metadata: %+v, %v", mirror, err)
	}
	movie := sourceMovie("movie", "【待审核】测试电影", "mp4")
	movie.Path = "/private/release.1080p.mkv"
	mirror, err = mirrorSource(t.Context(), f, &store.DataSource{ID: 1}, nil, store.DataSourceLibrary{}, movie)
	if err != nil || mirror.filenames["movie"] != "测试电影" {
		t.Fatalf("movie match metadata: %+v, %v", mirror, err)
	}
}
