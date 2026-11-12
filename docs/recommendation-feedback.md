# 首页推荐反馈

## 提交和撤销

| 操作 | 请求 |
| --- | --- |
| 不感兴趣 | `POST /x/web-interface/feedback/dislike` |
| 撤销反馈 | `POST /x/web-interface/feedback/dislike/cancel` |

请求使用表单编码，必须登录并携带当前请求所使用会话的 `csrf`。`csrf` 为空时读取 `biliCSRF`，两者都有值时以 `csrf` 为准。禁止用同账号另一会话的 CSRF 替代。

| 参数 | 含义 |
| --- | --- |
| `goto` | 普通视频固定为 `av` |
| `id` | 正整数视频 ID，须对应已有稿件 |
| `reason_id` | `1` 表示不推荐此视频；`4` 表示不推荐此 UP 主 |
| `mid` | 可选，表示稿件作者，不是反馈提交人。缺失、空或 `0` 时从稿件读取；正数必须与稿件作者一致 |
| `track_id` | 可选推荐追踪信息，不参与偏好记录的身份识别 |

`app_id`、`platform`、`from_spmid`、`spmid`、`feedback_page` 及签名查询参数可携带，不改变上述行为。反馈提交人始终由登录会话决定。

成功返回 `{"code":0,"message":"0","ttl":1,"data":null}`。重复提交不会增加重复记录，撤销不存在的记录也成功。撤销使用与提交相同的参数：原因 `1` 只删除该用户对此视频的反馈，原因 `4` 删除该用户对稿件作者的反馈，同一作者的其他稿件也可用来撤销该作者反馈。

视频与作者反馈彼此独立。若两者同时存在，撤销视频反馈后，视频仍会因作者反馈被过滤；撤销作者反馈后，单独标记过的视频仍被过滤。撤销表示重新进入推荐候选池，不保证马上出现在某一页。

| 错误码 | 条件 |
| --- | --- |
| `-101` | 未登录 |
| `-403` | 账号被封禁 |
| `-111` | 当前会话 CSRF 缺失或不匹配 |
| `-405` | 请求方法不支持 |
| `-400` | 视频 ID、作者 ID、目标类型或原因无效，或作者与稿件不匹配 |
| `-404` | 稿件不存在，提交与撤销均要求稿件存在 |
| `-500` | 存储或服务内部错误，不返回虚假的提交成功 |

推广目标 `goto=ad` 保留原来的登录、CSRF 校验后确认成功行为；Jumedia 不投放推广卡片，此类请求不保存视频或作者偏好。

## 推荐列表生效范围

反馈持久化保存在 `recommendation_feedback`，按用户、原因和目标唯一存储，重启服务后保留。不会修改视频踩记录、关注关系、用户黑名单或视频统计。

两个首页推荐接口都会在排序和分页前排除当前用户反馈的视频与作者：

- `GET /x/web-interface/wbi/index/top/feed/rcmd`：保留 `ps`（默认 12）与 `fresh_idx`（默认 1）的现有分页行为，返回 `data.item`，发布时间为 `pubdate`。卡片新增布尔值 `dislike_switch`，登录时为 `true`，访客为 `false`。
- `GET /x/v2/feed/index`：保留 `idx` 偏移及每次 20 条的循环取样行为，同时继续遵守稿件的 `no_tv` 设置。

反馈不改变现有推荐排序、循环取样规则或其他发布限制；候选数量不足时可能在不同页重复。过滤后无候选时分别返回 `item: []` 或 `items: []`，不会报错。访客和其他账号不受影响。已加载到客户端的卡片由客户端决定是否立即移除，后续刷新和分页请求使用最新反馈。

热门、排行、搜索、关注动态、作者空间及视频详情不受推荐反馈影响。

## 旧入口和网页 Mock

`/x/feed/dislike` 与新入口共用同一份偏好，支持相同的 `goto`、`id`、`reason_id` 和可选 `mid`。使用当前会话匹配的 `access_key` 可通过 GET 或 POST 调用；仅用 Cookie 时必须 POST 并提交当前会话 CSRF。旧入口提交的反馈可通过新撤销接口撤销。原因 `3` 不受支持，卡片不再展示该原因。

网页反馈调用使用新的 Web POST 接口；`feedDislike` 与 `cancelFeedDislike` 共用参数结构。浏览器 Mock 实现相同的提交、撤销、过滤、鉴权和错误码，按用户将反馈保存到浏览器数据库。旧数据库缺少反馈数组时视为没有反馈，保留原数据。

后端契约测试：`go test ./internal/api -run 'TestRecommendationFeedback|TestDiscoverFeedAndLists|TestRegionRankingAndFeedback'`。可用 PostgreSQL 环境通过 `JUMEDIA_TEST_PG_DSN` 启用隔离 schema 测试；网页契约测试在网页仓库运行 `node scripts/check-recommendation-feedback.mjs`。
