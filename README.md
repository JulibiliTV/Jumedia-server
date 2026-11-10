# Jumedia Server

Jumedia 是一个聚合 Emby、Jellyfin、飞牛影视等媒体源和弹弹 Play 弹幕源的媒体平台，既可用于多人之间的媒体分享，也可用于个人多个媒体库的统一聚合。

本仓库是 Jumedia 的 Go 后端，为 [Jumedia Web](../Jumedia-web) 网页端和 JulibiliTV 电视客户端提供同一套 HTTP 接口。

- **视频**：投稿、审核、合集、分区与子分区、推荐与排行、搜索、字幕、protobuf 弹幕。
- **番剧 / 影视**：剧集与分集、索引筛选、时间表、榜单、追番与观看进度。
- **直播**：直播间、分区、m3u8 拉流转播、把点播视频转码后循环转播，以及基于 WebSocket 的直播聊天。
- **社区**：评论、点赞、投币、收藏夹、稍后再看、历史、关注、空间、动态、粉丝勋章。
- **创作中心与管理后台**：稿件与数据、直播设置、用户、审核、轮播、站点与服务器设置、操作日志。
- **媒体数据源**：接入 Emby 或 Jellyfin 媒体库，自动同步为稿件与合集，图片经服务端代理与缓存，点播可按数据源选择代理或直连。
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

管理员也可在后台「用户管理 → 修改密码」直接设置用户的新密码，无需原密码，至少 6 位。保存后撤销该用户已有登录会话及绑定的扫码登录凭据，用户需重新登录；修改管理员自己的密码也会退出当前登录。接口为 `POST /x/admin/user/password`，参数为 `mid`、`new_password` 和当前会话的 `csrf`，密码不会在响应或操作日志中回显。

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
| `JUMEDIA_FFMPEG_VIDEO_ENCODER` | `auto` | 直播转码的视频编码器：`auto`、`libx264`、`h264_vaapi`、`h264_videotoolbox`、`h264_nvenc`、`h264_qsv` |

相对路径以服务的运行目录为基准。

公开站点接口 `/x/jumedia/site` 的 `version` 返回当前服务端构建版本，供网页版权说明下方显示。原生构建默认使用 `0.1.0` 与 Go 记录的 Git 短提交号；未提交修改带 `.dirty`，缺少提交信息时标记 `dev`。从源码归档构建时可用 `-ldflags "-X jumedia-server/internal/buildinfo.Version=<版本号> -X jumedia-server/internal/buildinfo.Revision=<提交号>"` 注入；Docker 对应参数为 `BUILD_VERSION`、`BUILD_REVISION`，发布工作流自动传入。

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

网页托管响应包含 `Cross-Origin-Opener-Policy: same-origin` 和 `Cross-Origin-Embedder-Policy: credentialless`，让网页播放器在安全上下文中使用共享内存和多线程处理高负载媒体。反向代理应保留这两个响应头；独立托管网页时需为页面和 Worker 脚本设置相同的响应头。

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

创作中心支持 Emby 和 Jellyfin，两种类型合计最多可添加 5 个个人数据源。管理员可在后台添加系统数据源，其内容归属官方账号。

添加 Jellyfin 时选择「Jellyfin」，填写服务器地址和有权读取媒体库的用户名、密码。如果服务器配置了基础路径，地址中要包含该路径。已保存的数据源类型固定。

管理员可在后台「数据源 → 用户数据源」编辑用户添加的数据源，包括名称、地址、账号、媒体库选择、自动同步和代理播放设置，保存后仍归原用户所有。数据源列表显示代理是否开启。已保存的数据源密码不回显，编辑时密码留空保留原值；填写新密码或勾选清除才会修改凭据。

创作中心「投稿 → 从数据源获取视频」或个人数据源卡片的「获取视频」支持按媒体库搜索、分页和勾选视频，每次最多 20 条。选中的电影各生成一个草稿，剧集生成多 P 草稿；单条导入直接打开编辑页，多条导入进入草稿列表。可修改标题、简介、分区、标签、封面并保存草稿，点击「立即投稿」后按站点规则审核。已导入的条目显示已有稿件，重复请求不会复制或重置稿件，已删除条目不会重新导入。整库同步保留草稿，不会自动发布。

只需手动选片时，在数据源表单关闭「自动同步」后保存，连接会保留且不立即导入整库；「立即同步」仍会同步已接入的整个媒体库。只允许访问本人已接入的个人媒体库，沿用站点个人源开关和网络策略。

- 整库同步时，每个启用的视频媒体库对应一个合集；电影导入为单 P 稿件，剧集导入为多 P 稿件。
- 同步在后台进行，默认每 6 小时一次，也可手动触发；每个数据源独立限速，网络错误会自动重试。
- Jumedia 以只读方式访问源媒体库。在 Jumedia 中编辑过的标题、简介、封面等字段会被锁定，之后的同步不会覆盖。
- 源中已删除的条目会被隐藏，重新出现时恢复。
- 个人数据源默认不能访问内网地址（按 DNS 解析后的 IP 判断）；系统数据源不受此限制。
- 数据源密码使用 AES-256-GCM 加密保存，数据源管理接口只返回是否已设置密码；源站访问令牌缓存在服务端内存中，启用直连时也会随播放链接下发。

### 弹弹play 弹幕

管理员可在「站点设置 → 弹弹play 弹幕」填写接口地址并启用同步。支持自部署的兼容服务，地址可包含 token 路径或以 `/api/v2` 结尾；无需签名的服务只填地址即可，App ID 和 App Secret 均可留空。需要签名时同时填写两项凭据。默认接口地址为 `https://api.dandanplay.net`，凭据申请与接口说明见[官方文档](https://doc.dandanplay.com/open/)。App Secret 使用数据源密钥加密保存，读取设置只返回是否已设置；留空保留。切换到无需签名的服务时，清空 App ID 并勾选清除已保存的 App Secret，无需关闭同步。

启用后，请求数据源视频弹幕会按需触发后台获取，先返回本地弹幕及已有缓存；完成后，网页和 JulibiliTV 自动刷新当前分段并合并显示，不重启视频。支持同步稿件、已有导入稿件和影视分集，无需先重新同步媒体库。媒体库同步仍可预取各分集弹幕，正在播放的任务优先处理。剧集使用 `名称.S01E01` 格式匹配，电影和其他视频只使用名称；查询前移除「待审核」「待审」「审核中」等展示前缀，不附带分集标题、原始文件名、源站地址、目录路径或登录凭据。已知季集编号优先使用，未记录季号时按第 1 季处理。只有唯一候选才自动获取弹幕，多个候选显示「未匹配」。

任务在重启后恢复；仅正常结果更新缓存，失败不保存为加载结果，也不覆盖上次正常数据、匹配信息或更新时间。超过间隔后的重试如果失败，继续使用之前的正常结果（包括正常空结果）；没有正常结果时保持未加载。尝试时间单独保留用于 30 分钟请求间隔，同一媒体重新同步也不会绕过该间隔。无弹幕时，播放或续播会检查是否需要重新获取；成功且已有弹幕时直接使用缓存。重新获取成功后替换该分集的外部弹幕，不修改用户发表的弹幕。关闭同步停止后续获取，已有缓存继续可用。数据库迁移 017 保留旧缓存，并支持影视分集缓存随所属内容删除。

稿件审核支持选择分集，查看同步状态、匹配作品、弹幕数量、更新时间和分页弹幕列表，并在播放器中预览；点击弹幕时间可跳转到对应位置。管理员可通过 `GET /x/admin/archive/danmaku?aid=<稿件ID>&cid=<分集ID>&pn=1&ps=20` 读取相同信息。

### 播放与图片

- 数据源新增和编辑表单默认勾选「通过 Jumedia 代理播放」，已有数据源升级后也保持代理。取消后，该数据源的普通视频和番剧点播直接返回源站原文件 URL，视频流量不经过 Jumedia；图片代理和直播转播不受此开关影响。
- 两种播放方式均不请求源站转码，支持 Range 请求。MP4 / M4V / MOV（H.264 + AAC/MP3）在浏览器和电视上都能直接播放；其他容器（如 MKV）由网页端在浏览器内解码播放，电视端无法播放。
- 代理模式隐藏源服务器地址和令牌，并校验媒体引用。直连链接包含数据源账号的访问令牌，其权限范围由该账号决定，并不限于这一条视频；请使用仅具所需媒体播放权限的账号。切回代理不会撤销已经发出的令牌。
- 直连时，数据源地址必须能被观众的设备访问，不能只在 Jumedia 容器内可达；HTTPS 网页应连接 HTTPS 源站，浏览器内解码需要源站允许跨域读取。直连播放地址响应不缓存，每次获取时校验源站会话并在令牌失效时重新登录。
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
- 缓存大小取决于编码器和内容；VAAPI 的 1080p 视频目标码率为 8 Mbps，含音频约 3.7 GB / 小时。转码期间还需要额外的临时空间。超过 24 小时没有直播间引用的缓存会被自动删除。
- 源为 HDR 时，如果 ffmpeg 带有 `zscale` 与 `tonemap` 滤镜，会转换为 SDR。
- 转码进程只访问本机的临时地址，源站地址和凭据不会出现在 ffmpeg 命令行中。

默认使用 `auto`：每个任务先检查 `/dev/dri/renderD128` 的访问权限，并通过 FFmpeg 实际试编码一帧。可用时用 VAAPI 加速 H.264 编码；设备不存在、权限不足、驱动或编码器不可用时使用 `libx264`。正式硬件编码失败时会清理临时缓存、重置进度，并用软件编码重试一次。解码、缩放及 HDR 色调映射仍由 CPU 完成。

后台「服务器配置 → 视频编码器」可选择 `auto`，或选择 `libx264` 强制软件编码。已有的非空后台设置会继续覆盖环境变量；手动指定编码器时不会自动切换或回退。原生部署需要安装支持 VAAPI 的 FFmpeg 与对应显卡驱动，并让服务用户拥有设备读写权限。Docker 镜像包含 Mesa 驱动，x86_64 镜像还包含 Intel 驱动；宿主机仍需安装并加载显卡内核驱动。

Docker 在具有该设备的 Linux 主机上可叠加 `deploy/compose.gpu.yaml`，按设备实际组 ID 授权给容器用户，无需特权容器：

```sh
export JUMEDIA_RENDER_GID=$(stat -c '%g' /dev/dri/renderD128)
docker compose -f deploy/compose.yaml -f deploy/compose.gpu.yaml up -d
```

如果已把部署文件复制到独立目录，请同时复制两个 Compose 文件并调整命令中的路径。无 GPU 的主机继续只使用基础 `compose.yaml`。仅切换编码器不会重新生成已完成的缓存；需要重新编码时，先让所有引用该缓存的直播间更换直播源，再在后台删除对应缓存并重新选择视频。

### 直播聊天

`GET /sub` 提供二进制 WebSocket 弹幕协议：

1. 客户端先调用 `/room/v1/Room/room_init` 取得真实房间号，再调用 `/xlive/web-room/v1/index/getDanmuInfo?id=<房间号>` 取得令牌与 `host_list`。
2. 连接后第一包必须是认证包，令牌有效期 1 小时，绑定房间号与用户。
3. 心跳返回房间人气；`/msg/send` 发送的弹幕会广播给房间内的所有连接；直播间被封禁或下播时，服务端推送 `PREPARING` 并断开连接。

订阅中心在进程内实现，多实例部署时不会跨实例转发消息。网页端通过轮询 `/xlive/web-room/v1/dM/gethistory` 获取聊天记录。

## 客户端

### 发表评论

`POST /x/v2/reply/add` 使用统一的 HTTP 200 JSON 信封（`code`、`message`、`ttl`、`data`）。支持 `type=1` 视频评论，必填 `oid`、`message`、登录会话与 `csrf`；楼中楼同时填写 `root` 和 `parent`，两者必须属于同一稿件及同一根评论。其他评论区类型返回 `12009`。

成功时 `code=0`，`data` 包含 `success_action=0`、`success_toast="发送成功"`、`need_captcha=false`、`url=""`、`rpid/rpid_str`、`dialog/dialog_str`、`root/root_str`、`parent/parent_str`、`emote={}` 和 `reply`。暂不启用验证码；不返回需要验证码的挑战，也不提供付费评论功能。文字中的表情转义符按普通文本保留，`emote` 为空对象。

| code | 条件 |
| --- | --- |
| -101 / -102 / -111 | 未登录 / 账号被封停 / CSRF 校验失败 |
| -400 / -404 | 请求错误 / 稿件不存在或未发布 |
| 12009 / 12025 / 12016 | 评论区类型错误 / 内容为空或超过 1000 个 UTF-16 单元 / 包含敏感词 |
| 12006 / 12035 | 回复目标不存在或不属于指定楼层 / 被 UP 主拉黑 |
| 12002 / 12003 | 站点关闭评论 / 站点禁止楼中楼回复 |
| -509 / 12051 | 发言间隔过短 / 在同一位置重复发送相同内容 |

站点设置中的 `commentEnabled`、`commentReplyEnabled` 默认开启。`commentMinInterval` 按账号限制发言间隔，`commentDuplicateWindow` 限制同一账号在同一稿件和楼层重复提交；单位为秒，范围 0–86400，默认 0（不限制），可在后台「站点设置 → 评论」修改。检查和评论写入使用同一事务；失败请求不增加评论计数。其他评论读取、删除和举报接口保留各自原有返回码。

### 评论举报与拉黑

使用 Jumedia 接口协议，均通过当前登录账号的会话认证；写入需要 `csrf`。

- `POST /x/v2/reply/report`：`type=1`、`oid`、`rpid`、数字 `reason`，可选 `content`、`add_blacklist`。例如 `reason=7` 为人身攻击、`15` 为侵犯隐私、`1` 为垃圾广告；`0`（其他）和 `22`（虚假不实信息）要求说明。`add_blacklist=false` 只举报，不同时拉黑。举报按账号和评论去重。
- `POST /x/relation/modify`：`fid`、`act=5` 拉黑 / `act=6` 解除拉黑；兼容 `re_src`。拉黑取消该账号对目标的关注，解除拉黑不自动重新关注。关系查询使用 `attribute=128` 表示黑名单。
- `GET /x/relation/blacks`：`pn`、`ps` 分页，返回 `list`、`total`、`re_version`。

举报和黑名单保存在数据库，账号间隔离。评论列表、楼中楼和预览在分页前过滤被当前用户举报的评论及被拉黑作者；管理员仍能审阅原评论。后台「评论与弹幕」的「待处理举报」可查看数量与原因、驳回举报、删除评论或封禁用户。删除评论同时将关联待处理举报标为已移除；驳回不恢复举报者已隐藏的评论。发表评论时阻止站点配置的敏感词，普通链接仍沿用原有后台疑似广告标记。

升级启动会自动应用 `015_comment_moderation.sql`。客户端菜单需要部署此版本服务端；仅更新客户端无法使旧服务端具备这些处理能力。

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
- 设置 `JUMEDIA_PUBLIC_ORIGIN` 为电视能访问的地址，代理播放、图片等本站资源以它为前缀返回；直连播放使用数据源的服务器地址。
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

账号弹幕设置、弹幕分段和媒体数据源的请求与响应详见[接口参考](docs/api.md)。

## 开发

```text
cmd/jumedia/        程序入口：启动、admin、demo import、reset、优雅退出
internal/api/       请求管线与各业务路由
internal/config/    环境变量
internal/db/        数据库连接、占位符改写、事务
internal/store/     数据模型、查询与迁移
internal/seed/      基础初始化、管理员引导与演示包生成
internal/render/    响应渲染
internal/emby/      Emby、Jellyfin 客户端与媒体代理
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
- 修改接口时，先更新网页端的 Mock 契约，再在服务端实现；客户端已使用的接口只能新增字段，不能修改或删除已有字段。

## 许可证

Copyright 2026 Jumedia contributors.

除另有声明的第三方组件与资源外，本项目采用 [Apache License 2.0](LICENSE) 开源。第三方代码、依赖与资源保留各自的许可证和版权声明。
