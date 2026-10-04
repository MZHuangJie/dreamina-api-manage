# Dreamina 账号管理器

Dreamina（`dreamina.capcut.com`，即梦海外版）的多账号管理器：把多个账号收进一个本地服务统一管理，
提供**账号池调度**、**登录态保活**、**文生图 / 图生图**，并把整个账号池包装成一个
**OpenAI 兼容的图片生成网关**，供外部程序用 `baseURL + API Key` 直接接入。

- **本地优先** —— 数据落在本机 SQLite，凭据用 AES-256-GCM 加密，管理端默认只监听 `127.0.0.1`。
- **账号池调度** —— 失败最少 / 轮询 / 积分最多 / 只用当前账号，四种策略自动挑号，失败自动换号重试。
- **登录态保活** —— 定时巡检，失效账号自动移出调度队列并告警。
- **多区域路由** —— 按账号归属地自动选对 API 集群，避免「打错地方」导致的假失效。
- **独立代理** —— 每个账号可挂自己的 HTTP/SOCKS5 代理，出口 IP 互不影响。
- **聚合网关** —— `POST /v1/images/generations`，OpenAI 形状的请求与响应。
- **两种外壳** —— 桌面端（Electron，自带浏览器）或服务端（Go 核心 + Node 浏览器通道）。

生成产物示例见 [docs/img/go-core-verified-output.jpg](docs/img/go-core-verified-output.jpg)。

---

## 为什么「必须有个浏览器」

这是整个架构的核心约束，不是技术选型偏好。

Dreamina 的**写操作**（提交生成）必须带 `msToken` / `X-Bogus` / `X-Gnarly`，
这三个参数由页面里的 **secsdk** 实时计算并附加到 URL 上，服务端无法复现。实测：

| 发起方式 | 结果 |
| --- | --- |
| Node 直发（自算签名头） | ❌ `ret=-6 shark not pass reject` |
| 浏览器页面内 `fetch`（只带 content-type） | ❌ `ret=3018 permission denied` |
| **浏览器页面内 `fetch` + 完整签名头** | ✅ `ret=0` |

**所以架构上把「必须有浏览器」隔离成一个只干一件事的小进程**，其余全部用 Go 写。
详细推导见 [docs/architecture-b.md](docs/architecture-b.md)。

---

## 架构

```
┌───────────────────────────────────────────────────────────┐
│  Go 核心 (manager)                                         │
│                                                           │
│  · HTTP API（给前端）   · 账号池 / 凭据加密                 │
│  · SQLite 存储          · 调度策略 / 失败冷却               │
│  · 保活巡检             · 生成任务队列 / 轮询               │
│  · 签名计算 / 地区解析   · 聚合网关（OpenAI 兼容）          │
│                                                           │
│  读操作 ──────────► 直连 Dreamina（net/http + 代理）        │
│  写操作 ──────────► 浏览器通道                              │
└──────────────────────────┬────────────────────────────────┘
                           │ HTTP 127.0.0.1:8790
                           │ X-Sidecar-Token
┌──────────────────────────▼────────────────────────────────┐
│  浏览器通道（二选一）                                        │
│                                                           │
│  A. sidecar/server.mjs   Node + Playwright + Chromium     │
│     服务端 / Docker 用                                      │
│                                                           │
│  B. desktop/             Electron 内置 Chromium            │
│     桌面端用，无需外部依赖                                   │
│                                                           │
│  职责被刻意压到最小：                                        │
│    「在指定账号的已登录页面上下文里，代发一次 HTTP 请求」      │
│                                                           │
│  · 每账号一个 BrowserContext（独立 Cookie + 独立代理）       │
│  · 每上下文一个常驻页面，等 secsdk 就绪                       │
│  · 闲置自动回收；同账号请求串行化                             │
│                                                           │
│  ✗ 不懂业务语义   ✗ 不存账号   ✗ 不做调度                    │
└───────────────────────────────────────────────────────────┘
```

**关键取舍**：浏览器通道是个**通用代发器**，不理解任何业务。
所有协议逻辑（签名、payload、地区、轮询）都在 Go 里，所以它几乎不需要随 Dreamina 改版而改动。

---

## 运行方式

### 一、桌面端（推荐，开箱即用）

```bash
cd desktop
npm install
npm run dist        # 产出安装包到 desktop/release/
```

产物：

| 文件 | 说明 |
| --- | --- |
| `release/Dreamina 账号管理器-安装包-x.y.z.exe` | NSIS 安装包 |
| `release/win-unpacked/Dreamina 账号管理器.exe` | 免安装版 |

桌面端**自带 Chromium**（Electron 内置），不需要装 Chrome，也不需要单独跑 sidecar。
首次启动会在 `%APPDATA%\Dreamina 账号管理器\data` 建库。

开发模式：`npm start`（依赖 `desktop/node_modules`，需要先 `npm install`）。

### 二、源码 / 命令行

需要 **Go 1.26+**、**Node 22+**、**pnpm**。

```bash
# 依赖（首次）
pnpm install                        # 前端构建依赖
npm --prefix sidecar install        # 浏览器通道依赖

# 构建
pnpm build                          # 前端 → dist/web
(cd go && go build -o manager.exe ./cmd/manager)
```

然后在**两个终端**里分别启动：

```bash
node sidecar/server.mjs             # 终端 1：浏览器通道

go/manager.exe serve                # 终端 2：核心
```

打开 `http://127.0.0.1:8787`。

> 两条命令都在**仓库根目录**执行：这样 `manager.exe` 会自动找到 `dist/web`，
> 数据也落在仓库根的 `data/`，不会像从 `go/` 里启动那样另建一份。

**Windows 上可以直接双击根目录的批处理**，它们就是把上面的步骤串了起来：

| 文件 | 作用 |
| --- | --- |
| `启动.bat` | 起浏览器通道 + 核心，并自动打开界面 |
| `停止.bat` | 停掉核心与通道 |
| `开发.bat` | 开发模式：Vite 热更新 + Electron（Electron 自己会拉起核心与通道） |
| `打开界面.bat` | 单独打开界面（`启动.bat` 会调用它） |

前端热更新：`pnpm dev:web`（Vite 跑在 5173，`/api` 与 `/v1` 代理到 8787）。

### 三、Docker

```bash
cp .env.example .env      # 填 DOMAIN 和 MANAGER_ACCESS_TOKEN
docker compose up -d --build
```

镜像内含 Go 核心 + Node 浏览器通道，由 `docker/entrypoint.sh` 拉起并绑死生命周期
（任一进程退出就整体退出，交给 Docker 重启）。

**部署要点**：只有 Caddy 暴露公网。核心的管理端口用 `expose` 而不是 `ports`，
不发布到宿主机，外面扫不到。见 [docs/deployment.md](docs/deployment.md)。

| 端口 | 用途 |
| --- | --- |
| 8787 | 管理端（界面 + `/api`） |
| 8791 | 聚合网关（`/v1/*`） |
| 8790 | 浏览器通道（仅本机） |

---

## 添加账号

在浏览器登录 [dreamina.capcut.com](https://dreamina.capcut.com)，然后任选一种方式拿凭据：

1. **F12 → Application → Cookies → `dreamina.capcut.com` → 复制 `sessionid`**
2. 或者复制**整段 Cookie** —— 推荐这种，管理器会从中自动抽取
   `sessionid` / `sid_tt` / `sid_guard` / `uid_tt`，以及**归属地**所需的
   `store-idc` 和 `store-country-code`

界面点「+ 添加账号」粘贴即可，保存后立即探活，验证凭据并读取积分。

**更省事的方式**：用 `manager login` 或界面上的「登录」按钮，
会开一个真浏览器窗口让你登录，**自动抓取完整凭据并保存**。

---

## 功能说明

### 账号管理

增删改查、启用/停用、标签、备注、搜索筛选。「切换为当前」会把该账号设为当前账号，
配合「只用当前账号」策略使用。

### 多区域

Dreamina 的 API 主机**按账号归属地划分**。把美区账号打到新加坡集群会返回
`ret=1015 login error` —— **这个报错极具误导性**，看起来像 sessionid 失效，
其实只是打错了地方。

归属地由两个 cookie 决定：

| Cookie | 含义 | 示例 |
| --- | --- | --- |
| `store-idc` | 数据中心 | `useast5` / `alisg` / `no1a` |
| `store-country-code` | 国家 | `us` / `vn` / `gb` |

解析按三级走：**缓存 → `store-idc` 前缀 → 国家代码**，都没命中则退回美区并继续自动探测。

| 国家 | 集群 |
| --- | --- |
| `us` `ca` `mx` `br` | US |
| `sg` `my` `id` `th` `ph` `vn` | SG |
| `jp` `kr` `tw` `hk` | JP |
| `gb` `de` `fr` | EU |

详见 [docs/regions.md](docs/regions.md)。

### 登录态保活

按 `MANAGER_HEALTH_INTERVAL_MS`（默认 30 分钟）巡检所有启用账号：

- 登录失效 → 标记 `expired` 并移出调度队列
- 网络 / 代理异常 → 标记 `error` 并进入冷却，稍后自动重试
- 巡检有并发上限，避免同时打太多请求触发风控

失效账号重新登录后，替换凭据再点「恢复状态」即可重新参与调度。

### 每日积分

`manager claim` 或界面上的「领取」按钮调用 `/commerce/v1/benefits/credit_receive`。

> **注意**：能否领到完全由服务端决定。响应里的 `receive_quota` 就是可领额度，
> 为 `0` 时无论客户端怎么调都拿不到。客户端侧的签名、参数都是齐全的——
> 这一点已用页面自身的请求做过对照验证。

`manager timeline` 按本机采样画积分时间线，用来观察某个账号的每日积分是否真的到账。

### 生成

| 模式 | 说明 |
| --- | --- |
| 文生图 | 提示词直接出图，可选模型 / 比例 |
| 图生图 | 参考图 + 提示词改造 |

任务后台异步执行，界面实时显示轮询进度，可随时取消。完成后直接展示并保留历史。

### 聚合网关

外部程序用 **baseURL + API Key** 接入，把管理器的账号池当成一个图片生成服务。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/images/generations` | 生成图片；`?async=1` 立即返回任务 id |
| GET | `/v1/images/generations/{id}` | 查询异步任务 |
| GET | `/v1/models` | 可用模型 |
| GET | `/v1/models/{id}` | 单个模型信息 |

鉴权：`Authorization: Bearer sk-dm-…`（也接受 `x-api-key`）。

```bash
curl http://127.0.0.1:8787/v1/images/generations \
  -H "Authorization: Bearer sk-dm-你的密钥" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"a ripe strawberry","size":"1024x1024"}'
```

支持 `model` / `size`（只决定宽高比）/ `ratio` / `account_id` / `strategy` 等参数。
详见 [docs/gateway.md](docs/gateway.md)。

---

## 命令行

```
manager serve                                  启动 HTTP 服务
manager sidecar                                检查浏览器通道健康状态
manager account add <名称> <凭据> [--proxy url]  添加账号
manager account list                           列出账号
manager account rm <id|名称>                   删除账号
manager account use <id|名称>                  切换当前账号
manager login [id|名称] [--new 名称]           开浏览器登录并自动保存凭据
manager credit [id|名称]                       查询积分
manager claim [id|名称]                        尝试领取每日赠送积分
manager history [id|名称]                      积分流水（平台侧收支明细）
manager timeline [id|名称]                     积分时间线（本机采样）
manager netcheck [id|名称]                     体检出口 IP 与账号归属地是否一致
manager gen <提示词> [id|名称]                 端到端生成一张图
```

`manager netcheck` 值得单独说：它同时报告**账号归属地**和**当前出口 IP 的地区**，
两者不一致时是个明确的异常信号 —— 平台会看到你的请求从一个和账号无关的国家发出。

---

## 配置

全部通过环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MANAGER_HOST` | `127.0.0.1` | 管理端监听地址 |
| `MANAGER_PORT` | `8787` | 管理端端口 |
| `MANAGER_DATA_DIR` | `./data` | 数据目录 |
| `MANAGER_SECRET` | 空 | 指定加密密钥（优先于 `secret.key` 文件） |
| `MANAGER_ACCESS_TOKEN` | 空 | 管理端访问令牌，**开放到局域网时必须设置** |
| `GATEWAY_HOST` / `GATEWAY_PORT` | `127.0.0.1` / `8791` | 聚合网关监听 |
| `SIDECAR_URL` | `http://127.0.0.1:8790` | 浏览器通道地址 |
| `SIDECAR_SECRET` | 空 | 浏览器通道共享密钥 |
| `MANAGER_IGNORE_SYSTEM_PROXY` | `false` | 设为 `true` 强制直连，忽略系统代理 |
| `MANAGER_HEALTH_ENABLED` | `true` | 是否开启保活巡检 |
| `MANAGER_HEALTH_INTERVAL_MS` | `1800000` | 巡检间隔（30 分钟） |
| `MANAGER_MAX_ATTEMPTS` | `3` | 一次生成最多尝试几个账号 |
| `MANAGER_MAX_CONCURRENT` | `4` | 同时进行中的生成上限 |
| `MANAGER_FAILURE_COOLDOWN_MS` | `60000` | 失败账号冷却时长 |
| `GATEWAY_WAIT_TIMEOUT_MS` | `180000` | 网关同步等出图的上限 |

**关于系统代理**：核心会读 Windows 的 Internet 设置（`ProxyEnable` / `ProxyServer`）并跟随，
目的是让 Go 的请求和浏览器通道走同一个出口 —— **两个通道出口不一致本身就是个异常信号**。
如果你的代理同时开了 TUN 和「系统代理」，而后者有问题，就设 `MANAGER_IGNORE_SYSTEM_PROXY=true`。

---

## HTTP API

管理端以 `/api` 为前缀，统一返回 `{ ok: true, data }` 或 `{ ok: false, error: { code, message } }`。

**账号**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/accounts` | 列表，支持 `keyword` / `health` / `enabled` / `tag` 筛选 |
| POST | `/accounts` | 新建并立即探活 |
| POST | `/accounts/bulk` | 批量导入 |
| GET | `/accounts/export?credentials=1` | 导出备份（**含明文凭据**） |
| GET / PATCH / DELETE | `/accounts/{id}` | 详情 / 修改 / 删除 |
| POST | `/accounts/{id}/enabled` | 启用 / 停用 |
| POST | `/accounts/{id}/activate` | 切换为当前账号 |
| POST | `/accounts/{id}/probe` | 立即探活 |
| POST | `/accounts/{id}/receive-credit` | 领取每日赠送积分 |
| POST | `/accounts/{id}/reset-health` | 清除失效标记 |
| POST | `/accounts/{id}/netcheck` | 出口 IP 与归属地体检 |
| GET | `/accounts/{id}/events` | 该账号操作记录 |
| POST / GET / DELETE | `/accounts/{id}/login` | 开浏览器登录（GET 查状态，DELETE 取消） |
| POST | `/accounts/{id}/login/capture` | 登录完成后抓取凭据 |

**账号池**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/pool/overview` | 总览统计 |
| POST | `/pool/probe-all` | 全量探活 |
| GET | `/pool/probe-status` | 探活进度 |
| GET | `/pool/pick` | 按策略试选一个账号（不消耗） |
| PUT | `/pool/strategy` | 设置调度策略 |
| GET | `/pool/events` | 全局操作记录 |

**生成**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/models` | 模型目录与可选参数 |
| POST | `/generate` | 提交生成任务（202，后台执行） |
| GET | `/generations` | 生成记录 |
| GET | `/generations/stats` | 统计 |
| GET / DELETE | `/generations/{id}` | 单条进度 / 删除 |
| POST | `/generations/{id}/cancel` | 取消 |

**网关密钥**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET / POST | `/keys` | 列出 / 创建 API Key |
| PATCH / DELETE | `/keys/{id}` | 修改（启用、改名）/ 吊销 |

**其他**：`GET /api/ping`、`GET /healthz`（无需令牌，供健康检查用）。

网关接口见上文「聚合网关」，它**不经过**管理端令牌校验 —— 是两套身份。

---

## 目录结构

```
go/                              Go 核心
  cmd/manager/                   CLI 入口、serve、密钥引导
  cmd/cryptocheck/               加解密自检工具
  internal/
    api/                         HTTP 路由（账号 / 账号池 / 生成 / 网关密钥 / 登录 / 体检）
    dreamina/                    Dreamina 协议：签名、地区、commerce、生成、轮询
    gateway/                     聚合网关（OpenAI 兼容）+ 限流
    provider/                    Provider 抽象与重试
    store/                       SQLite：账号、API Key、事件、调度、快照
    crypto/                      AES-256-GCM 凭据加解密
    browser/                     浏览器通道客户端
    health/                      保活巡检
    netcheck/                    出口 IP 与归属地体检
    sysproxy/                    系统代理探测
    tasks/                       生成任务队列

src/web/                         React 前端

desktop/                         Electron 桌面端
  src/main.js                    主进程：拉起核心、托盘、窗口
  src/browser-channel.js         内置 Chromium 浏览器通道
  src/chrome-login.js            登录窗口
  electron-builder.yml           打包配置

sidecar/server.mjs               Node + Playwright 浏览器通道（服务端用）

docs/                            设计与协议文档
docker/entrypoint.sh             容器入口（拉起通道 + 核心）

dist/web/                        前端构建产物（gitignore）
data/                            数据库与密钥（gitignore，**务必备份**）
```

---

## 文档

| 文档 | 内容 |
| --- | --- |
| [architecture-b.md](docs/architecture-b.md) | 为什么拆成 Go 核心 + 浏览器通道 |
| [dreamina-protocol.md](docs/dreamina-protocol.md) | Dreamina 网页端接口逆向参考 |
| [jimeng-protocol.md](docs/jimeng-protocol.md) | 即梦（国内版）接口参考 |
| [regions.md](docs/regions.md) | 多区域解析与集群映射 |
| [scheduling.md](docs/scheduling.md) | 账号调度与失败处理 |
| [gateway.md](docs/gateway.md) | 聚合网关协议 |
| [proxy-and-vpn.md](docs/proxy-and-vpn.md) | 代理与 VPN 配置 |
| [deployment.md](docs/deployment.md) | 部署 |

---

## 安全提醒

- 数据库里的 `sessionid` **等同于账号本身**，拿到就能以你的身份使用 Dreamina。
  `data/` 目录已在 `.gitignore` 中，**不要提交、不要分享**。
- `data/secret.key` 是解密密钥，**丢了库里所有凭据都解不开**，等同于账号全部丢失。请单独备份。
- 默认只监听 `127.0.0.1`。要开放到局域网，**必须**设置 `MANAGER_ACCESS_TOKEN`。
- 网关的 API Key 泄露等同于账号被免费用，`GET /api/keys` 里可以随时吊销。
- 本工具直接复用 Dreamina 网页端接口，属于**非官方用法**。接口随时可能变更，
  账号也存在被风控的可能 —— 建议控制请求频率、避免高频批量操作。
- **仅供个人管理自己的账号使用。**

---

## 已知限制

- 接口未公开且有版本校验。若某天开始大量报签名错误，需要更新 `go/internal/dreamina/` 里的版本常量。
- **写操作强依赖真实浏览器**。浏览器通道不通时，所有生成都会失败（读操作不受影响）。
- **每日积分能否领取由服务端决定**，客户端的 `receive_quota` 只反映结果，改不了它。
- 生成进度保存在进程内存 + 数据库；服务重启时进行中的任务会被标记为「已中断」，需要重新提交。
- 桌面端与命令行模式**各自使用独立的 data 目录**（桌面端默认在 `%APPDATA%`），
  需要共用同一份账号时，给桌面端设 `MANAGER_DATA_DIR`。
