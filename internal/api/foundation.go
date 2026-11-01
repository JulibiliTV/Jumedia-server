package api

import (
	"jumedia-server/internal/render"
	"jumedia-server/internal/store"
)

func (s *Server) registerFoundation() {
	s.handle("/x/web-interface/nav", func(c *Ctx) (any, error) {
		origin := s.Origin(c.R)
		wbi := map[string]any{"img_url": origin + "/bfs/wbi/7cd084941338484aae1ad9425b84077c.png", "sub_url": origin + "/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png"}
		if c.Me == nil {
			return nil, FailData(-101, "账号未登录", map[string]any{"isLogin": false, "wbi_img": wbi})
		}
		u := c.Me
		label := ""
		if u.VIP != 0 {
			label = "年度大会员"
		}
		return map[string]any{"isLogin": true, "mid": u.MID, "uname": u.Uname, "face": u.Face, "level_info": render.LevelInfo(*u), "money": u.Coins, "moral": 70, "email_verified": 1, "mobile_verified": 0, "vipStatus": u.VIP, "vipType": store.BoolInt(u.VIP != 0) * 2, "vip_label": map[string]any{"text": label}, "official": map[string]any{"type": -1}, "wbi_img": wbi, "role": u.Role, "sign": u.Sign}, nil
	})
	s.handle("/x/jumedia/site", func(c *Ctx) (any, error) {
		settings, err := s.Store.Settings(c.Context())
		if err != nil {
			return nil, err
		}
		banners, err := s.Store.ListBanner(c.Context(), "WHERE position=? ORDER BY sort,id", "home")
		if err != nil {
			return nil, err
		}
		v := map[string]any{"banners": banners}
		for _, k := range []string{"siteName", "allowRegister", "announcement"} {
			if x, ok := settings[k]; ok {
				v[k] = x
			}
		}
		return v, nil
	})
	s.handle("/x/web-interface/region/list", func(c *Ctx) (any, error) {
		regions, err := s.Store.ListRegion(c.Context(), "WHERE visible=1 AND COALESCE(parent,0)=0 ORDER BY sort,tid")
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, r := range regions {
			children, err := s.Store.ListRegion(c.Context(), "WHERE parent=? AND visible=1 ORDER BY sort,tid", r.TID)
			if err != nil {
				return nil, err
			}
			childRows := []map[string]any{}
			for _, child := range children {
				childRows = append(childRows, map[string]any{"tid": child.TID, "name": child.Name, "id": child.TID})
			}
			out = append(out, map[string]any{"tid": r.TID, "name": r.Name, "id": r.TID, "children": childRows})
		}
		return out, nil
	})
}
