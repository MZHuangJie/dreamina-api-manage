# 即梦账号管理器

即梦（jimeng.jianying.com）多账号管理器：把多个即梦账号收进一个本地服务里统一管理，
并直接调用即梦网页端的接口完成文生图 / 图生图 / 文生视频 / 图生视频。

- **本地优先**：数据全部落在本机 SQLite，凭据用 AES-256-GCM 加密，默认只监听 `127.0.0.1`。
- **账号池调度**：失败最少 / 轮询 / 积分最多 / 只用当前账号，四种策略自动挑号。
- **登录态保活**：定时巡检每个账号，失效自动标记并停止派活，避免把任务浪费在掉线的号上。
- **独立代理**：每个账号可以挂自己的 HTTP/SOCKS5 代理，出口 IP 互不影响。
- **零原生依赖**：用 Node 24 内置的 `node:sqlite` 与原生 TypeScript 执行，不需要 node-gyp 编译。

---

## 快速开始

要求 **Node.js ≥ 24**（用到了内置 `node:sqlite` 与原生 TS 执行）。

```bash
pnpm install      # 或 npm install
pnpm build        # 构建前端
pnpm start        # 启动，默认 http://127.0.0.1:8787
```

开发模式（前后端分离，前端热更新）：

```bash
pnpm dev          # 终端 1：后端，改动自动重启
pnpm dev:web      # 终端 2：Vite 开发服务器 http://127.0.0.1:5173
```

首次启动会在 `data/` 下自动生成：

| 文件 | 说明 |
| --- | --- |
| `data/manager.db` | SQLite 数据库，存放账号、状态、生成记录 |
| `data/secret.key` | 凭据加密密钥（首次自动生成，**请务必备份**） |

> `secret.key` 丢失后，库里已保存的 sessionid 将无法解密，等同于账号全部丢失。

---

## 添加账号

在浏览器打开 [jimeng.jianying.com](https://jimeng.jianying.com) 并登录，然后任选一种方式拿到凭据：

1. **F12 → Application → Cookies → `jimeng.jianying.com` → 复制 `sessionid` 的值**
2. 或者直接复制整段 Cookie（管理器会自动识别其中的 `sessionid` / `sessionid_ss` / `sid_tt` / `sid_guard`）

回到管理器点「+ 添加账号」粘贴即可，保存后会立即探活一次，验证凭据并读取积分。

**批量导入**：每行一个账号，支持 `备注名<Tab>凭据` 或 `备注名,凭据` 的写法：

```
主力号	sessionid_abc123...
备用号,sessionid_def456...
sessionid_ghi789...
```

---

## 功能说明

### 账号管理
增删改查、启用/停用、标签、备注、搜索筛选。**一键切换**（「切换为当前」）会把该账号设为当前账号，
其它账号自动取消，适合「只用当前账号」策略。

### 登录态保活
服务启动后按 `MANAGER_HEALTH_INTERVAL_MS`（默认 30 分钟）巡检所有启用账号：

- 调用 `/passport/account/info/v2` 判断登录态，`/commerce/v1/benefits/user_credit` 刷新积分
- 登录失效 → 标记为 `expired` 并移出调度队列，界面红色告警
- 网络/代理异常 → 标记为 `error` 并进入冷却，稍后自动重试
- 巡检有并发上限（默认 3），避免同一时间打太多请求触发风控

失效的账号重新登录后，在详情页点「编辑」替换 sessionid，再点「恢复状态」即可重新参与调度。

### 独立代理
在账号的添加 / 编辑弹窗里填写代理地址，支持：

```
http://127.0.0.1:7890
http://user:pass@host:1080
socks5://127.0.0.1:1080
```

省略协议时按 `http://` 处理。每个账号的请求会独立走自己的代理，互不串线。

### 生成
切到「生成」标签页：

| 模式 | 说明 |
| --- | --- |
| 文生图 | 提示词直接出图，可选模型 / 比例 / 分辨率 / 张数 |
| 图生图 | 上传最多 10 张参考图，按提示词改造 |
| 文生视频 | 提示词生成视频，可选模型 / 时长 |
| 图生视频 | 上传首帧图让它动起来 |

生成任务在后台异步执行（视频可能跑几分钟），界面实时显示轮询进度，可随时取消。
完成后直接展示图片 / 播放视频，并保留历史记录。

---

## 配置

全部通过环境变量，无需配置文件：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MANAGER_HOST` | `127.0.0.1` | 监听地址 |
| `MANAGER_PORT` | `8787` | 监听端口 |
| `MANAGER_DATA_DIR` | `./data` | 数据目录 |
| `MANAGER_SECRET` | 空 | 指定加密密钥（优先于 `secret.key` 文件） |
| `MANAGER_ACCESS_TOKEN` | 空 | 访问令牌，**开放到局域网时必须设置** |
| `MANAGER_HEALTH_ENABLED` | `true` | 是否开启保活巡检 |
| `MANAGER_HEALTH_INTERVAL_MS` | `1800000` | 巡检间隔（30 分钟） |
| `MANAGER_HEALTH_CONCURRENCY` | `3` | 巡检并发数 |
| `MANAGER_REQUEST_TIMEOUT_MS` | `30000` | 单次请求超时 |

设置令牌后，需要在 URL 上带一次 `?token=xxx`（会存进 sessionStorage），或用请求头 `X-Access-Token`。

---

## HTTP API

所有接口以 `/api` 为前缀，统一返回 `{ ok: true, data }` 或 `{ ok: false, error: { code, message } }`。

**账号**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/accounts` | 列表，支持 `keyword` / `health` / `enabled` / `tag` 筛选 |
| POST | `/accounts` | 新建并立即探活 |
| POST | `/accounts/bulk` | 批量导入 |
| GET | `/accounts/:id` | 详情 |
| PATCH | `/accounts/:id` | 修改（含更换凭据） |
| DELETE | `/accounts/:id` | 删除 |
| POST | `/accounts/:id/enabled` | 启用 / 停用 |
| POST | `/accounts/:id/activate` | 一键切换为当前账号 |
| POST | `/accounts/:id/probe` | 立即探活 |
| POST | `/accounts/:id/receive-credit` | 领取每日赠送积分 |
| POST | `/accounts/:id/reset-health` | 清除失效标记 |
| GET | `/accounts/:id/events` | 该账号操作记录 |
| GET | `/accounts/export?credentials=1` | 导出备份（**含明文凭据**） |

**账号池**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/pool/overview` | 总览统计 |
| POST | `/pool/probe-all` | 全量探活 |
| GET | `/pool/probe-status` | 探活进度 |
| PUT | `/pool/strategy` | 设置调度策略 |
| GET | `/pool/events` | 全局操作记录 |

**生成**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/models` | 模型目录与可选参数 |
| POST | `/generate` | 提交生成任务（返回 202，后台执行） |
| GET | `/generations` | 生成记录 |
| GET | `/generations/:id` | 单条进度 |
| POST | `/generations/:id/cancel` | 取消 |
| GET | `/generations/stats` | 统计 |

---

## 目录结构

```
src/
  server/
    index.ts              Hono 服务入口、静态资源、优雅退出
    config.ts             环境变量与常量
    crypto.ts             AES-256-GCM 凭据加解密、sessionid 解析
    db.ts                 node:sqlite 连接、迁移、事件日志
    http.ts               统一响应与错误映射
    accounts/
      pool.ts             账号池 CRUD、选取策略、状态回写
      health.ts           探活与保活调度器
    jimeng/
      client.ts           签名算法、请求信封、按账号代理
      errors.ts           错误分类（登录失效 / 积分不足 / 网络 / 代理）
      account.ts          登录态、积分、会员
      models.ts           模型目录、比例枚举、像素尺寸表
      generate.ts         文生图 / 图生图 / 文生视频 / 图生视频 请求体与轮询
      upload.ts           ImageX 参考图上传（AWS SigV4）
    generate/
      tasks.ts            后台生成任务与进度持久化
    routes/               HTTP 路由
  web/                    React 前端
docs/
  jimeng-protocol.md      即梦网页端接口协议逆向参考
```

---

## 安全提醒

- 数据库里的 sessionid **等同于账号本身**，拿到就能以你的身份使用即梦。请勿把 `data/` 目录提交到 git 或分享出去。
- 默认只监听 `127.0.0.1`。如果确实要开放到局域网，**必须**设置 `MANAGER_ACCESS_TOKEN`。
- 本工具直接复用即梦网页端接口，属于非官方用法。接口随时可能变更，账号也存在被风控的可能，建议控制请求频率、避免高频批量操作。
- 仅供个人管理自己的账号使用。

---

## 已知限制

- 即梦接口未公开且有版本校验（当前对齐网页端 `Appvr 5.8.0`）。若某天开始大量报签名错误，需要更新 `src/server/config.ts` 里的 `versionCode`。
- 会员（VIP）字段依赖即梦返回，部分账号可能识别为未知。
- 生成任务的进度保存在进程内存 + 数据库；服务重启时正在进行的任务会被标记为「已中断」，需要重新提交。
