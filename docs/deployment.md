# 生产部署

## 0. 先理解一个硬约束

**Dreamina 的写操作必须由真实浏览器完成。**

提交生成时，平台强制校验 `msToken` / `X-Bogus` / `X-Gnarly` 三个参数，
它们由页面里的 secsdk 计算并附加到 URL 上。纯服务端无法复现（社区也没有可用的 Go 实现）。

所以部署包里**必须有一个 Chromium**。这就是镜像约 1.5GB 的原因，不是没优化。

### 这个约束带来的直接后果

- 不能部署到纯 Serverless（没有浏览器进程）
- 内存至少 2GB（Chromium 自己就要 500MB 起）
- `/dev/shm` 要给够，否则标签页会莫名其妙崩

---

## 1. 安全模型：两个端口，物理隔离

这是整套部署里最需要理解的一点。

```
公网 ──443──▶ Caddy ──▶ 容器:8791  (GATEWAY_PORT)
                          只有 /v1/*
                          Bearer 密钥鉴权

                       容器:8787  (MANAGER_PORT)
                          管理界面 + /api/*
                          绑 127.0.0.1，外面完全不可达
```

**为什么不用一个端口加令牌？**

因为管理接口里有一个 `GET /api/accounts/export?credentials=1`，
它会**导出账号的明文凭据**。这种东西不该和对外接口共享一个监听——
令牌可能忘记设、可能泄漏、可能被反代的路径规则绕过。

拆成两个端口之后，管理面是**物理上**不可达的：那个端口根本没有发布到宿主机。

### 兜底保护

即使有人不小心把管理端绑到 `0.0.0.0`：

- 启动时会**直接拒绝启动**，除非设了 `MANAGER_ACCESS_TOKEN`
- 运行中令牌为空时，只放行**来自回环地址**的请求

宁可起不来，也不能让明文凭据导出接口无声地挂在公网上。

---

## 2. 部署步骤

### 2.1 准备

一台 Linux 服务器，建议 2 核 4GB 起。装好 Docker 和 Docker Compose。

域名解析到服务器公网 IP（Caddy 需要它来签发证书）。

### 2.2 拉代码并配置

```bash
git clone <你的仓库> && cd manager
cp .env.example .env
vi .env
```

至少填 `DOMAIN`。`MANAGER_ACCESS_TOKEN` 建议也填上：

```bash
openssl rand -hex 24
```

### 2.3 起服务

```bash
docker compose up -d --build
docker compose logs -f manager
```

首次构建要下载 Chromium，约 5-10 分钟，取决于网络。

### 2.4 加账号

管理端在容器里绑的是回环，所以**不能直接从外面访问界面**。
用 SSH 隧道把它拉出来：

```bash
ssh -L 8787:127.0.0.1:8787 user@your-server
```

但注意：容器内的 127.0.0.1 和宿主机的 127.0.0.1 不是一回事。
需要临时把管理端也发布出来（用完就关）：

```bash
# docker-compose.override.yml
services:
  manager:
    ports:
      - "127.0.0.1:8787:8787"   # 只绑宿主机回环，公网仍然打不到
```

```bash
docker compose up -d
ssh -L 8787:127.0.0.1:8787 user@your-server
# 本地浏览器打开 http://127.0.0.1:8787
```

加完账号、创建好网关密钥之后，把 override 删掉即可。

---

## 3. 给别人接入

在管理界面「接入」页创建密钥（明文只显示一次），然后告诉对方：

```
Base URL: https://api.example.com/v1
API Key:  sk-dm-xxxxxxxx
```

```bash
curl https://api.example.com/v1/images/generations \
  -H "Authorization: Bearer sk-dm-xxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"a ripe strawberry","size":"1024x1024"}'
```

### 配额

创建密钥时可以设三个上限（0 表示不限）：

| 字段 | 含义 | 建议 |
| --- | --- | --- |
| `ratePerMinute` | 每分钟调用次数 | 10 —— 防止脚本狂刷 |
| `maxConcurrent` | 同时进行中的生成数 | 2 —— 防止一个人占满队列 |
| `dailyQuota` | 每天累计提交数 | 按积分算，比如 200 |

超额会返回 `429` 并带 `Retry-After`；服务器整体繁忙返回 `503`。

---

## 4. 容量与并发

并发由三层一起管：

| 层 | 配置 | 作用 |
| --- | --- | --- |
| 全局 | `MANAGER_MAX_CONCURRENT` | 整个进程同时跑几个生成 |
| 网关 | `GATEWAY_MAX_CONCURRENT` | 外部调用合计上限 |
| 单密钥 | `maxConcurrent` | 单个调用方的上限 |

超过上限不会立刻失败，而是**排队**最多 `MANAGER_QUEUE_WAIT_MS`；
排不上才返回 503。这比直接拒绝友好，也比无限堆积安全。

**怎么估算**：一次生成要占用浏览器通道里的一个页面上下文，
一个页面上下文约占 100-200MB。4 并发大约对应 1GB 内存余量。

账号数少的时候并发开大没有意义——真正卡住的是账号，不是进程。

---

## 5. 备份

只需要备份 `./data` 目录：

```bash
docker compose stop manager
tar czf backup-$(date +%F).tar.gz data/
docker compose start manager
```

里面两样东西：

- `manager.db` —— 账号、状态、生成记录、网关密钥
- `secret.key` —— **凭据加密密钥**

**`secret.key` 丢了，所有已保存的账号就都解不开了**，只能重新添加。
这个文件建议单独存一份到密码管理器。

---

## 6. 监控

```bash
# 容器健康状态
docker compose ps

# 实时日志
docker compose logs -f manager
```

容器自带的健康检查打的是网关的 `/v1/models`——它能通说明
核心、网关监听、鉴权链路都活着。

关键日志事件：

| 事件 | 含义 |
| --- | --- |
| `server.start` | 服务启动 |
| `gateway.request` | 外部调用进来了 |
| `gateway.key_create` | 新建密钥 |
| `generate.switch` | 失败换号 |
| `generate.no_retry` | 决定不重试，附带原因 |
| `account.expired` | 账号登录态失效 |

---

## 7. 常见问题

**Q: 调用返回 502**

Caddy 的 `response_header_timeout` 小于生成耗时。
默认配置里设的是 300s，如果你改过 `GATEWAY_WAIT_TIMEOUT_MS` 超过它，要同步改 Caddyfile。

**Q: 报 `shark not pass reject`**

请求没经过浏览器上下文。检查容器里 sidecar 是否活着：

```bash
docker compose exec manager curl -s http://127.0.0.1:8790/health
```

**Q: Chromium 起不来 / 标签页崩溃**

多半是 `/dev/shm` 太小。确认 compose 里 `shm_size: 1gb` 生效了。

**Q: 想用后面的 CDN / 负载均衡**

可以，但**不能横向扩多个容器共用一份 `data/`**。
SQLite 是单写者的，而且浏览器通道的会话是进程内的。要扩容得先换掉存储层。
