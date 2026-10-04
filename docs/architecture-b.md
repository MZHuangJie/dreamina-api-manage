# 方案 B 架构设计：Go 核心 + Node 浏览器通道

> 状态：**垂直切片已跑通**（2026-10-01）。Go 核心成功驱动 Node sidecar 完成了一次真实的
> Dreamina 文生图，产物见 [go-core-verified-output.jpg](img/go-core-verified-output.jpg)。

## 1. 为什么要拆成两个进程

不是「为了用 Go 而用 Go」，而是被一条硬约束逼出来的：

**Dreamina 的写操作必须带 `msToken` / `X-Bogus` / `X-Gnarly`，这三个参数由 secsdk 在
浏览器页面里计算并附加到 URL 上。** 实测结论：

| 发起方式 | 结果 |
| --- | --- |
| Node 直发（签名头齐全） | `ret=-6 shark not pass reject` |
| 浏览器页面内 `fetch`（只带 content-type） | `ret=3018 permission denied` |
| **浏览器页面内 `fetch` + 完整签名头** | ✅ `ret=0` |

而社区没有可用的 Go 实现（`ylcangel/douyin_sign` 只有 JS/Python，且是抖音的算法）。
`playwright-go` 本身也会**下载 Node.js 24 + playwright-core** 来跑驱动——
所以「全 Go」既去不掉 Node，还要额外维护一个浏览器驱动，收益为负。

**结论**：把「必须有浏览器」这件事隔离成一个小进程，其余全部用 Go 写。

## 2. 组件划分

```
┌──────────────────────────────────────────────────────────────┐
│  Go 核心 (manager)                                            │
│                                                              │
│  · HTTP API（给前端）        · 账号池 / 凭据加密               │
│  · SQLite 存储               · 调度策略 / 失败冷却             │
│  · 保活巡检                  · 生成任务队列 / 轮询             │
│  · 签名计算 / 地区解析        · 直连接口（所有读操作）          │
│                                                              │
│  读操作 ────────────────► 直连 Dreamina（net/http + 代理）     │
│  写操作 ────────────────► sidecar                             │
└──────────────────────────────────┬───────────────────────────┘
                                   │ HTTP 127.0.0.1:8790
                                   │ X-Sidecar-Token
┌──────────────────────────────────▼───────────────────────────┐
│  Node sidecar (sidecar/server.mjs)                           │
│                                                              │
│  职责被刻意压到最小：                                          │
│    「在指定账号的已登录页面上下文里，代发一次 HTTP 请求」        │
│                                                              │
│  · 一个 Chromium 进程，每账号一个 BrowserContext               │
│    （独立 Cookie 容器 + 独立代理出口）                         │
│  · 每上下文一个页面，常驻 dreamina.capcut.com，等 secsdk 就绪   │
│  · 闲置 15 分钟自动回收上下文                                  │
│  · 同一账号的请求串行化（页面上下文非线程安全）                  │
│                                                              │
│  ✗ 不懂 Dreamina 业务语义   ✗ 不存账号   ✗ 不做调度            │
└──────────────────────────────────────────────────────────────┘
```

**关键设计取舍**：sidecar 是个**通用的浏览器代发器**，不理解任何业务。
所有协议逻辑（签名、payload、地区、轮询）都在 Go 里。
这样 sidecar 几乎不需要随 Dreamina 改版而改动，风险被关在最小面里。

## 3. 边界契约

sidecar 只暴露 4 个端点，全部要求 `X-Sidecar-Token`：

### `GET /health`
```json
{ "ok": true, "uptimeMs": 120308, "browser": "running",
  "sessions": [{ "accountId": "acct-0342e2cd", "ready": true, "proxy": "http://...", "idleMs": 92633 }] }
```

### `POST /session` — 幂等，确保账号的浏览器上下文已建立
```json
{ "accountId": "acct-0342e2cd",
  "cookies": [{ "name": "sessionid", "value": "...", "domain": ".capcut.com", "path": "/" }],
  "proxy": "http://127.0.0.1:7897" }
→ { "ok": true, "accountId": "...", "secsdkReady": true }
```
代理变更会自动重建上下文。

### `POST /fetch` — 核心能力
```json
{ "accountId": "acct-0342e2cd",
  "url": "https://dreamina-api.us.capcut.com/mweb/v1/aigc_draft/generate?...",
  "method": "POST",
  "headers": { "sign": "...", "device-time": "...", "appid": "513641", "...": "..." },
  "body": "{...}" }
→ { "ok": true, "status": 200, "body": "{\"ret\":\"0\",...}", "headers": {...} }
```
在页面上下文里用 `fetch(url, { credentials: "include" })` 发出——
secsdk 会从这个调用里自动补上风控参数。

### `DELETE /session/:accountId` — 销毁上下文

## 4. 目录结构

```
go/                                   ← Go 核心（新）
  cmd/manager/main.go                 入口
  internal/browser/client.go          sidecar 客户端
  internal/dreamina/
    sign.go                           签名（两次抓包逐字节验证）
    region.go                         地区 → 主机映射 + 公共请求头
    http.go                           直连 HTTP + 代理连接池
    client.go                         读走直连 / 写走浏览器
    image.go                          payload 构造、提交、轮询、积分

sidecar/                              ← Node 浏览器通道（新）
  server.mjs
  package.json                       仅依赖 playwright-core

src/                                  ← 现有 TypeScript 实现（保留作参考与对照）
docs/dreamina-protocol.md             协议逆向记录
docs/architecture-b.md                本文件
```

## 5. 已验证（垂直切片）

| 能力 | 结果 |
| --- | --- |
| Go 计算签名 | ✅ 与两次真实抓包逐字节一致 |
| 地区 → 主机解析 | ✅ `useast5`/`us` → `dreamina-api.us.capcut.com` |
| 直连读积分 | ✅ 122 分 |
| 直连取 workspace | ✅ `501633082374` |
| 经 sidecar 提交生成 | ✅ `ret=0`，`history_record_id=5130319614214` |
| 直连轮询 | ✅ `status 45 → 50`，5 次查询 |
| **端到端出图** | ✅ **2 张 2048×2048** |

## 5b. 阶段 2 验证结果

| 项 | 结果 |
| --- | --- |
| **跨语言加密互通** | Go 加密 → Node 解密 ✅；Node 加密 → Go 解密 ✅ |
| **Schema 兼容** | TS 建的库（user_version=1）被 Go 平滑升级到 v2，TS 仍可正常读取 ✅ |
| 账号加密入库 | ✅ 凭据落库后无法明文读取 |
| 从库里读凭据查积分 | ✅ 110 分（自动解密 + 解析 `store-idc` → 选对集群） |
| **从库里读凭据端到端出图** | ✅ `history_record_id=5278466860806`，2 张 2048×2048 |
| gofmt / go vet | ✅ 干净 |

Go 代码量：**2042 行**（15 个文件）。

关键取舍：`LoadOrCreateKey` 与 `Encrypt/Decrypt` 严格复刻 TS 版的
`v1.<iv>.<tag>.<ct>`（base64url 无填充）格式与密钥派生方式，
所以**现有的 data/manager.db 可以直接被 Go 接管，不需要导出再导入**。

## 6. 迁移阶段

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| **1** | 边界跑通：sidecar + Go 客户端 + 一次真实生成 | ✅ 完成 |
| **2** | 存储与账号池：SQLite（`modernc.org/sqlite` 纯 Go，无 cgo）、AES-256-GCM 凭据加密、CRUD、事件日志、凭据解析 | ✅ 完成 |
| **3** | 业务面：Provider 抽象、调度策略、保活巡检、生成任务队列、HTTP API | ✅ 完成 |
| **4** | 即梦适配器移植（协议同源，签名只差 `appvr`） | ✅ 完成 |
| **5** | 前端平台接线 + 打包分发（单二进制 11MB，无 cgo） | ✅ 完成 |

## 6b. 阶段 3-5 验证结果

| 项 | 结果 |
| --- | --- |
| **前端零改动切换** | ✅ 同一份前端构建产物直接连 Go 后端，UI 全部正常 |
| API 契约对齐 | ✅ 响应信封、字段名、状态码、错误码逐项核对；截图发现并修掉了事件时间戳的 `created_at` 命名偏差 |
| Provider 抽象 | ✅ `dreamina` / `jimeng` 双平台并存，`/api/models?platform=` 分流 |
| 生成任务队列 | ✅ 后台执行、进度写回、可取消、重启对账 |
| 保活巡检 | ✅ 并发探活 + 定时调度器 |
| 调度策略 | ✅ active / least_failures / round_robin / most_credit |
| 签名抽取回归 | ✅ 两次真实抓包向量在重构后依然命中 |
| **发布包** | ✅ `manager.exe` 11MB 单静态二进制（CGO_ENABLED=0） |
| **发布包端到端出图** | ✅ 提交 → 轮询 → 2 张 2048×2048 |

Go 代码量：**4879 行 / 38 个文件**。

### 两个平台的关键差异（已在代码中体现）

| | 即梦 jimeng | Dreamina |
| --- | --- | --- |
| 主机 | `jimeng.jianying.com`（单集群） | 按 `store-idc` 分集群 |
| aid | 513695 | 513641 |
| appvr | 5.8.0 | 8.4.0 |
| **浏览器通道** | **不需要**，签名头齐全即可直连 | **必需**，写操作要浏览器风控签名 |
| workspace_id | 无此概念 | 提交生成必填 |
| payload | `version=3.0.2`、有 `gen_type`/`history_option`/`sceneOptions` | `version=3.3.28`、无上述字段 |

## 7. 运维要点

这是「稳不稳」的真正答案——**跟语言无关，靠这几点**：

1. **进程守护**：Go 核心与 sidecar 都要有 supervisor（Windows 用 NSSM/计划任务，Linux 用 systemd），挂了自动拉起
2. **健康检查**：核心 `/api/health` 要**级联探测** sidecar，sidecar 挂了要有明确状态而不是静默失败
3. **浏览器泄漏**：sidecar 已内置闲置回收 + `disconnected` 自动重启；再加一个「上下文数上限」兜底
4. **SQLite 并发**：Go 侧用 `modernc.org/sqlite` + WAL + 单写连接，天然避开 TS 版同步阻塞事件循环的问题
5. **日志**：两边都要结构化日志 + 轮转

## 7b. 打包分发

`tools/build-release.ps1` 产出 `dist/release/`：

```
manager.exe        11 MB   Go 核心（CGO_ENABLED=0，纯静态）
web/              0.3 MB   前端产物
sidecar/         25.7 MB   Node 浏览器通道（含 playwright-core）
start.bat / stop.bat       启动停止
README.txt                 使用说明
```

> 脚本刻意写成**纯 ASCII**：Windows PowerShell 5.1 会把无 BOM 的 .ps1
> 当 ANSI 读，中文字符串会全部乱码并导致语法错误。所有中文内容放在
> `tools/release-templates/` 里由脚本复制。

## 8. 已知取舍

- **多一个运行时**：仍然要装 Node（用来跑 Playwright 驱动），这是硬约束
- **多一跳 IPC**：写操作多一次本地 HTTP 往返，量级 <1ms，可忽略
- **sidecar 是单点**：它挂了写操作全停。但读操作（积分/轮询/列表）不受影响，降级行为是清晰的
