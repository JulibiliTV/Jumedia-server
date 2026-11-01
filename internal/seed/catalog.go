package seed

import "jumedia-server/internal/store"

var people = [][3]string{{"jumedia", "聚媒官方", "欢迎来到 Jumedia，一起记录、分享与发现。"},
	{"lumen", "流明实验室", "用影像拆解光与色。"},
	{"oolong", "乌龙茶不加糖", "做饭 / 旅行 / 胡思乱想"},
	{"pixel", "像素旅人", "独立游戏发烧友，每周一款宝藏游戏。"},
	{"wave", "声波工作室", "原创编曲与混音教学。"},
	{"maple", "枫糖动画社", "手绘动画短片，慢慢画。"},
	{"circuit", "电路板日记", "拆机、焊接、做点没用的小东西。"},
	{"deer", "小鹿爱跳舞", "街舞 / 爵士 / 编舞分享"},
	{"atlas", "地图上的猫", "用纪录片的方式看世界。"},
	{"nova", "星野Nova", "天文摄影与科普"},
	{"bamboo", "竹林喵喵", "三只猫和一个不靠谱的铲屎官"},
	{"drift", "漂移老王", "赛道日与改装车"}}
var comments = []string{"讲得太清楚了，收藏了！", "这期质量好高，催更下一期", "前排围观～", "背景音乐叫什么名字？", "看完立刻去试了一下，真的有用", "剪辑节奏好舒服", "已三连，期待更多作品", "这个角度从来没想过，学到了", "弹幕护体！", "UP 主辛苦了", "第一次刷到，已关注", "04:12 那里笑死我了"}
var LiveChatter = []string{"来了来了", "前方高能", "哈哈哈哈哈", "好家伙", "这里好评", "泪目", "学到了", "画质好清晰", "打卡", "太强了", "名场面", "妙啊", "经典", "感谢分享", "awsl", "下次一定"}
var colors = []int{16777215, 16777215, 16777215, 16646914, 16765698, 52480, 41194, 13369971}
var regions = []store.Region{{TID: 1005, Name: "动画", Sort: 0, Visible: true},
	{TID: 1008, Name: "游戏", Sort: 1, Visible: true},
	{TID: 1007, Name: "鬼畜", Sort: 2, Visible: true},
	{TID: 1003, Name: "音乐", Sort: 3, Visible: true},
	{TID: 1004, Name: "舞蹈", Sort: 4, Visible: true},
	{TID: 1001, Name: "影视", Sort: 5, Visible: true},
	{TID: 1002, Name: "娱乐", Sort: 6, Visible: true},
	{TID: 1010, Name: "知识", Sort: 7, Visible: true},
	{TID: 1012, Name: "科技数码", Sort: 8, Visible: true},
	{TID: 1020, Name: "美食", Sort: 9, Visible: true},
	{TID: 1013, Name: "汽车", Sort: 10, Visible: true},
	{TID: 1014, Name: "时尚美妆", Sort: 11, Visible: true},
	{TID: 1018, Name: "体育运动", Sort: 12, Visible: true},
	{TID: 1024, Name: "动物", Sort: 13, Visible: true},
	{TID: 1030, Name: "兴趣", Sort: 14, Visible: true},
	{TID: 1031, Name: "经验", Sort: 15, Visible: true}}
var liveAreas = []store.LiveArea{{ID: 2, Name: "网游", Position: 0, Children: []store.LiveAreaChild{{ID: 86, Name: "英雄联盟", ParentID: 2, Position: 0},
	{ID: 252, Name: "逃离塔科夫", ParentID: 2, Position: 1}}},
	{ID: 3, Name: "手游", Position: 1, Children: []store.LiveAreaChild{{ID: 35, Name: "王者荣耀", ParentID: 3, Position: 0},
		{ID: 255, Name: "明日方舟", ParentID: 3, Position: 1}}},
	{ID: 6, Name: "单机游戏", Position: 2, Children: []store.LiveAreaChild{{ID: 236, Name: "主机游戏", ParentID: 6, Position: 0},
		{ID: 216, Name: "独立游戏", ParentID: 6, Position: 1}}},
	{ID: 1, Name: "娱乐", Position: 3, Children: []store.LiveAreaChild{{ID: 21, Name: "视频唱见", ParentID: 1, Position: 0},
		{ID: 207, Name: "舞见", ParentID: 1, Position: 1}}},
	{ID: 9, Name: "虚拟主播", Position: 4, Children: []store.LiveAreaChild{{ID: 371, Name: "虚拟日常", ParentID: 9, Position: 0}}},
	{ID: 10, Name: "生活", Position: 5, Children: []store.LiveAreaChild{{ID: 646, Name: "生活分享", ParentID: 10, Position: 0},
		{ID: 215, Name: "影音馆", ParentID: 10, Position: 1}}},
	{ID: 11, Name: "知识", Position: 6, Children: []store.LiveAreaChild{{ID: 376, Name: "学习·陪伴", ParentID: 11, Position: 0},
		{ID: 377, Name: "科技·科学", ParentID: 11, Position: 1}}}}
