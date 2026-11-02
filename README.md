# Jumedia Server

Jumedia 是一个自托管的在线视频与直播平台。本仓库是它的 Go 后端，为 [Jumedia Web](../Jumedia-web) 网页端和 JulibiliTV 电视客户端提供同一套 HTTP 接口。

- **视频**：投稿、审核、合集、分区与子分区、推荐与排行、搜索、字幕、protobuf 弹幕。
- **番剧 / 影视**：剧集与分集、索引筛选、时间表、榜单、追番与观看进度。
- **直播**：直播间、分区、m3u8 拉流转播、把点播视频转码后循环转播，兼容 JulibiliTV 的直播聊天 WebSocket。
- **社区**：评论、点赞、投币、收藏夹、稍后再看、历史、关注、空间、动态、粉丝勋章。
- **创作中心与管理后台**：稿件与数据、直播设置、用户、审核、轮播、站点与服务器设置、操作日志。
- **媒体数据源**：接入 Emby 媒体库，自动同步为稿件与合集，图片和视频经服务端代理与缓存。
- **数据库**：SQLite（默认，零配置）或 PostgreSQL，启动时自动迁移。

## 快速开始

需要 Go 1.24 或更新版本。

```sh
git clone <repo-url> Jumedia-server
cd Jumedia-server
go run ./cmd/jumedia
```

服务默认监听 `:8080`，数据保存在 `./data`。每次启动幂等补齐分区、直播分区、默认设置和运行序列；不自动生成演示用户或内容。

首次启动可通过环境变量创建管理员（密码至少 6 位，最多 72 字节）：

```sh
JUMEDIA_ADMIN_USERNAME=owner JUMEDIA_ADMIN_PASSWORD='<管理员密码>' go run ./cmd/jumedia
```

已有管理员时忽略这两个引导变量，不改账号或密码。没有管理员且两者都未设置时只记录 WARN，服务继续启动；只设置其中一个或新建密码不合要求时启动报错。创建、提升同名用户或重设管理员密码：

```sh
go run ./cmd/jumedia admin owner '<新密码>'
```

演示内容通过网页端 `scripts/import-emby.mjs` 生成的 `jumedia-demo` v1 JSON 数据包导入。仅初始状态（基础数据和管理员账号）允许导入：

```sh
go run ./cmd/jumedia demo import ./jumedia-demo.json
```

也可在后台「站点设置」上传，接口为 `POST /x/admin/demo/import`，multipart 字段 `file`，需要管理员会话和 `csrf`。无效格式/版本/空目录返回 `-400`；已有业务内容返回 `-400 请先重置为初始状态再导入演示数据`。登录会话、扫码状态及操作日志不阻挡首次导入。内容、演示连接和直播缓存记录在同一事务写入。演示用户均为普通账号、密码 `demo123`，用户名为 `demo_<mid>_<原名>`（遇到同名追加下划线），不创建演示管理员，不覆盖站点设置。

数据包格式：

```json
{"format":"jumedia-demo","version":1,"generatedAt":1793280407,"emby":{"url":"https://emby.example.test","username":"demo","password":""},"catalog":{"series":[],"movies":[]}}
```

上例仅展示结构，导入时 catalog 必须有可用条目。密码可省略；服务端以 `datasource.key`（或 `JUMEDIA_DATASOURCE_KEY`）的 AES-GCM 方式加密保存在设置中，不从接口回显。演示媒体代理、旧媒体迁移和演示直播转码均使用这份持久化连接，未导入时没有演示媒体。只有演示 `emby:` 媒体生成演示弹幕，上传与数据源导入视频不会生成。

重置为初始状态（单个事务，保留所有管理员账号和直播聊天签名密钥）：

```sh
go run ./cmd/jumedia reset
```

后台 `/x/admin/settings/save` 的 `reset=true` 行为相同：删除业务内容、数据源、演示连接和缓存记录，清理缓存文件，重新补齐基础数据，不写演示内容。管理员需重新登录；上传目录保留。命令行重置后若没有管理员，则按引导环境变量创建。

进程收到 SIGINT / SIGTERM 时停止接受新请求，最多等待 10 秒处理在途请求后退出。

## 配置

### 环境变量

修改后需重启服务。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `JUMEDIA_ADDR` | `:8080` | HTTP 监听地址 |
| `JUMEDIA_DB_DRIVER` | `sqlite` | `sqlite` 或 `postgres` |
| `JUMEDIA_DB_DSN` | `data/jumedia.db` | SQLite 文件路径或 PostgreSQL 连接串 |
| `JUMEDIA_DATA_DIR` | `data` | 数据目录：上传文件、图片缓存、直播转码缓存、数据源密钥 |
| `JUMEDIA_WEB_DIR` | 空 | 已构建的网页端目录（`Jumedia-web/dist`）；设置后由本服务直接提供网页，未命中的路径回退到 `index.html` |
| `JUMEDIA_STATIC_DIR` | `../Jumedia-web/public` | 演示资源目录，`/mock/*` 从其中的 `mock/` 子目录读取 |
| `JUMEDIA_PUBLIC_ORIGIN` | 空 | 对外访问地址，例如 `https://media.example.test`；为空时按 `X-Forwarded-Proto` / `X-Forwarded-Host` 或请求 Host 推导 |
| `JUMEDIA_ADMIN_USERNAME` | 空 | 无管理员时的引导用户名 |
| `JUMEDIA_ADMIN_PASSWORD` | 空 | 无管理员时的引导密码，至少 6 位 |
| `JUMEDIA_DATASOURCE_KEY` | 空 | base64 编码的 32 字节密钥，用于加密数据源密码；为空时首次启动生成 `${JUMEDIA_DATA_DIR}/datasource.key` |
| `JUMEDIA_FFMPEG` | `ffmpeg` | ffmpeg 路径或命令名 |
| `JUMEDIA_FFPROBE` | `ffprobe` | ffprobe 路径或命令名 |
| `JUMEDIA_FFMPEG_VIDEO_ENCODER` | `libx264` | 直播转码的视频编码器：`libx264`、`h264_videotoolbox`、`h264_nvenc`、`h264_qsv` |

相对路径以服务的运行目录为基准。

### 管理后台设置

以下设置在管理后台「站点设置」中修改，保存后立即生效，无需重启：

- **服务器配置**：公开地址、ffmpeg / ffprobe 路径、视频编码器。非空时覆盖同名环境变量，留空则使用环境变量。启动时才读取的参数（监听地址、数据库、目录等）在这里只读展示，数据库密码会打码，数据源密钥只显示是否已设置。
- **服务分布**：主站、直播、客户端、登录四个接口地址和 CDN 线路列表，通过公开接口 `GET /x/jumedia/service` 下发给客户端。地址留空时使用本站公开地址；CDN 列表只下发，不改变服务端返回的播放地址。
- **站点**：站点名称、公告、注册开关、稿件是否需要审核、官方账号。
- **数据源**：个人数据源开关、是否允许访问内网、同步间隔、限速与并发。
- **缓存**：图片缓存开关、目录与容量上限；直播转码缓存目录与转码分辨率。
- **重置为初始状态**：与命令行 `reset` 相同。

## 部署

### Docker

服务端镜像 `ghcr.io/julibilitv/jumedia-server` 包含服务端和 ffmpeg，数据保存在容器内的 `/data`（以 uid 10001 运行）。网页端单独发布为 `ghcr.io/julibilitv/jumedia-web`（见 Jumedia-web），运行时把网页文件装进与服务端共享的 `/web` 卷，由服务端提供（`JUMEDIA_WEB_DIR` 与 `JUMEDIA_STATIC_DIR` 都指向 `/web`）。两个仓库推送到 `main` 后，各自的 GitHub Actions 会构建并发布对应镜像。

本地构建：

```sh
docker buildx build -t jumedia-server .
```

服务器上使用 `deploy/compose.yaml`，在同目录的 `.env` 中设置 `JUMEDIA_PUBLIC_ORIGIN` 等变量，容器只监听 `127.0.0.1:8098`，由宿主机的反向代理对外提供 HTTPS：

```sh
mkdir -p /data/jumedia && chown 10001 /data/jumedia
docker compose pull && docker compose up -d
```

只更新网页端时不需要重启服务端：

```sh
docker compose pull web && docker compose up web
```

### 由服务端直接提供网页

```sh
# Jumedia-web 目录
VITE_MOCK=0 npm run build

# Jumedia-server 目录
JUMEDIA_WEB_DIR=../Jumedia-web/dist go run ./cmd/jumedia
```

### 反向代理与 HTTPS

服务本身只监听 HTTP。对外提供服务时，请在前面部署 HTTPS 反向代理，并设置 `JUMEDIA_PUBLIC_ORIGIN`（或后台的公开地址），非标准端口需要写明，例如 `https://media.example.test:8443`。

**电视客户端必须通过 HTTPS 访问**：它固定用 `wss://` 连接直播聊天，不会退回 `ws://`。代理需要为 `/sub` 转发 WebSocket 升级，例如 Nginx：

```nginx
location = /sub {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Host $http_host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 90s;
}

location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Host $http_host;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 4g;
}
```

### 备份

需要一起备份的内容：

- 数据库（SQLite 文件或 PostgreSQL 库）。直播聊天的签名密钥也保存在数据库中。
- `${JUMEDIA_DATA_DIR}/uploads`：用户上传的视频和图片。
- 数据源密钥（`JUMEDIA_DATASOURCE_KEY` 或 `${JUMEDIA_DATA_DIR}/datasource.key`）。更换密钥后，已保存的数据源密码将无法解密。多实例部署必须使用同一密钥。

图片缓存和直播转码缓存可以不备份，丢失后会重新生成。

## 数据库

默认使用 SQLite（WAL 模式、外键约束、单连接）。使用 PostgreSQL：

```sh
JUMEDIA_DB_DRIVER=postgres \
JUMEDIA_DB_DSN='postgres://jumedia:<password>@127.0.0.1:5432/jumedia?sslmode=disable' \
go run ./cmd/jumedia
```

迁移文件位于 `internal/store/migrations`，启动时自动执行且可重复执行，无需手工导入 SQL。

## 媒体数据源

创作中心的用户最多可添加 5 个个人 Emby 数据源；管理员可在后台添加系统数据源，其内容归属站点的官方账号。

- 每个启用的视频媒体库对应一个合集；电影导入为单 P 稿件，剧集导入为多 P 稿件。
- 同步在后台进行，默认每 6 小时一次，也可手动触发；每个数据源独立限速，网络错误会自动重试。
- 数据源只读，不会向 Emby 写入任何内容。在 Jumedia 中编辑过的标题、简介、封面等字段会被锁定，之后的同步不会覆盖。
- 源中已删除的条目会被隐藏，重新出现时恢复。
- 个人数据源默认不能访问内网地址（按 DNS 解析后的 IP 判断）；系统数据源不受此限制。
- Emby 密码使用 AES-256-GCM 加密保存，接口只返回是否已设置密码；Emby 访问令牌只保存在内存中。

### 播放与图片

- 视频不经过 Emby 转码，服务端原样转发原始文件，支持 Range 请求。MP4 / M4V / MOV（H.264 + AAC/MP3）在浏览器和电视上都能直接播放；其他容器（如 MKV）由网页端在浏览器内解码播放，电视端无法播放。
- 图片和视频都经本站代理，观众看不到源服务器地址和令牌；只有已导入稿件引用的条目可以访问。
- 图片默认缓存在 `${JUMEDIA_DATA_DIR}/image-cache`，超出容量后按最久未访问淘汰；管理员可在后台查看占用或清空。

## 直播

直播间的视频源有三种：

| 来源 | 说明 |
| --- | --- |
| 推流 | 生成推流地址与推流码。服务端尚未内置 RTMP 接收，目前不能用推流开播。 |
| m3u8 拉流 | 转播一个 HLS 地址。源是直播流时原样转发；源是点播列表时，按开播时间输出滑动窗口，所有观众看到同一进度，可选择循环播放。 |
| 转播视频 | 选择数据源中的一个视频，转码为 H.264 / AAC 的 HLS 缓存后循环转播。 |

### 转播视频

需要在服务器上安装 ffmpeg 和 ffprobe（macOS 可用 `brew install ffmpeg`），并确保它们在服务进程的 `PATH` 中，或通过环境变量 / 后台设置指定路径。

- 视频首次被选用时会排队转码，缓存完成前不能开播；勾选自动开播后，转码完成即开播。转码失败可在创作中心或管理后台重试。
- 后台每次只转码一个任务。同一视频、同一分辨率的缓存会被共享。
- 1080p 缓存约占 1.4 GB / 小时，转码期间还需要额外的临时空间。超过 24 小时没有直播间引用的缓存会被自动删除。
- 源为 HDR 时，如果 ffmpeg 带有 `zscale` 与 `tonemap` 滤镜，会转换为 SDR。
- 转码进程只访问本机的临时地址，Emby 地址和凭据不会出现在 ffmpeg 命令行中。

### 直播聊天

`GET /sub` 提供二进制 WebSocket 弹幕协议：

1. 客户端先调用 `/room/v1/Room/room_init` 取得真实房间号，再调用 `/xlive/web-room/v1/index/getDanmuInfo?id=<房间号>` 取得令牌与 `host_list`。
2. 连接后第一包必须是认证包，令牌有效期 1 小时，绑定房间号与用户。
3. 心跳返回房间人气；`/msg/send` 发送的弹幕会广播给房间内的所有连接；直播间被封禁或下播时，服务端推送 `PREPARING` 并断开连接。

订阅中心在进程内实现，多实例部署时不会跨实例转发消息。网页端通过轮询 `/xlive/web-room/v1/dM/gethistory` 获取聊天记录。

## 客户端

### 网页端

参见 [Jumedia Web](../Jumedia-web)。开发时把接口代理到本服务：

```sh
cd ../Jumedia-web
npm install
API_PROXY=http://127.0.0.1:8080 VITE_MOCK=0 npm run dev
```

### 电视端（JulibiliTV）

电视端可直接将 Jumedia 作为数据源使用。

- 电视必须能访问服务器的局域网或公网地址（不能是 `127.0.0.1`），并通过 HTTPS 访问（见[反向代理与 HTTPS](#反向代理与-https)）。
- 设置 `JUMEDIA_PUBLIC_ORIGIN` 为电视能访问的地址，播放、图片等地址都会以它为前缀返回。
- 登录方式为扫码：电视调用 `/x/passport-tv-login/qrcode/auth_code` 显示二维码，用户在网页上登录后扫码确认，电视轮询 `/x/passport-tv-login/qrcode/poll` 取得登录信息。

## 接口概览

普通响应为 `{ "code": 0, "message": "0", "ttl": 1, "data": … }`，番剧类接口使用 `result` 字段。业务错误同样返回 HTTP 200，通过 `code` 区分：未登录 `-101`、CSRF 校验失败 `-111`、无权限 `-403`、参数错误 `-400`、不存在 `-404`。字幕、protobuf 弹幕、播放列表和静态文件使用各自的格式。

登录态通过 Cookie `SESSDATA`（HttpOnly）、`bili_jct`、`DedeUserID` 保存，有效期 180 天；电视端也可使用 `access_key`。写操作需要携带 `csrf`（或 `biliCSRF`）参数，其值为 `bili_jct`。

| 分组 | 主要路径 |
| --- | --- |
| 基础 | `/x/web-interface/nav`、`/x/jumedia/site`、`/x/jumedia/service`、`/x/web-interface/region/list` |
| 账号 | `/x/passport-login/web/*`、`/x/passport-tv-login/*`、`/api/v2/oauth2/refresh_token`、`/x/member/web/update`、`/login/exit/v2` |
| 发现与视频 | `/x/v2/feed/index`、`/x/web-interface/{popular,ranking/v2,search/*,suggest,view,view/detail}`、`/x/player/*`、`/x/polymer/web-dynamic/*` |
| 空间与互动 | `/x/space/*`、`/x/relation/*`、`/x/polymer/web-space/*`、点赞 / 投币 / 收藏 / 分享 |
| 评论与弹幕 | `/x/v2/reply*`、`/x/v2/dm/*` |
| 历史与收藏夹 | `/x/click-interface/web/heartbeat`、`/x/web-interface/history/cursor`、`/x/v2/history/*`、`/x/v3/fav/*` |
| 番剧 | `/pgc/*`、`/x/space/bangumi/follow/list` |
| 直播 | `/xlive/*`、`/room/v1/*`、`/live_user/v1/*`、`/msg/send`、`/live-relay/*`、`/live-cache/*`、`/sub` |
| 创作中心 | `/x/upload/web/{video,image}`、`/x/vu/web/{add/v3,edit}`、`/x/web/*` |
| 管理后台 | `/x/admin/*` |
| 媒体 | `/upload/*`、`/media/*`、`/mock/*` |

普通接口同时接受 GET 与 POST。完整的参数与响应结构以网页端 `src/mock/server.ts` 中的实现为准，它是前后端共用的接口契约。

## 开发

```text
cmd/jumedia/        程序入口：启动、admin、demo import、reset、优雅退出
internal/api/       请求管线与各业务路由
internal/config/    环境变量
internal/db/        数据库连接、占位符改写、事务
internal/store/     数据模型、查询与迁移
internal/seed/      基础初始化、管理员引导与演示包生成
internal/render/    响应渲染
internal/emby/      Emby 客户端与媒体代理
internal/dm/        protobuf 弹幕
internal/hls/       HLS 播放列表拉取与转播
internal/testutil/  测试工具
```

### 测试

```sh
gofmt -l .
go vet ./...
go test ./...
```

设置 `JUMEDIA_TEST_PG_DSN` 后，同一套测试会额外在 PostgreSQL 上运行，每个测试使用独立的 schema 并自动清理（测试账号需要创建 / 删除 schema 的权限）：

```sh
docker run -d --name jumedia-pg-test \
  -e POSTGRES_USER=jumedia -e POSTGRES_PASSWORD=jumedia-test -e POSTGRES_DB=jumedia_test \
  -p 127.0.0.1:55432:5432 postgres:16

JUMEDIA_TEST_PG_DSN='postgres://jumedia:jumedia-test@127.0.0.1:55432/jumedia_test?sslmode=disable' \
  go test -p 1 ./...
```

常规媒体测试使用本地 `httptest` Emby，无需外部网络（需要允许监听本地端口）。真实 Emby 冒烟测试要求显式设置 `JUMEDIA_TEST_EMBY_URL`、`JUMEDIA_TEST_EMBY_USERNAME`、`JUMEDIA_TEST_EMBY_PASSWORD`（允许显式为空）和 `JUMEDIA_TEST_EMBY_PACKAGE`；缺少必需项就跳过，无默认地址：

```sh
JUMEDIA_TEST_EMBY_URL=https://emby.example.test JUMEDIA_TEST_EMBY_USERNAME=demo \
JUMEDIA_TEST_EMBY_PASSWORD='<密码>' JUMEDIA_TEST_EMBY_PACKAGE=./jumedia-demo.json \
  go test ./internal/emby -run '^TestRealEmbySmoke$' -v
```

### 约定

- SQL 统一使用 `?` 占位，由 `internal/db` 按驱动改写；只使用 SQLite 与 PostgreSQL 都支持的语法。
- 来自请求的值一律作为参数传入，不拼接进 SQL；排序字段使用白名单映射。
- 多步写操作放在同一事务中，事务内只使用传入的 `tx`。
- 新增数据库变更以新的迁移文件提供，且必须可重复执行。
- 修改接口时，先更新网页端的 Mock 契约，再在服务端实现；JulibiliTV 已使用的接口只能新增字段，不能修改或删除已有字段。
