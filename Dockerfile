# Dreamina 账号管理器 —— 服务端镜像
#
# 刻意不写 "# syntax=docker/dockerfile:1"：那会让构建先拉一个前端镜像，
# 在配了镜像加速器的环境里经常因为鉴权失败而卡住。本文件只用标准指令，
# 不需要额外的构建前端。
# =================================
#
# 这个镜像装的是「Go 核心 + Node 浏览器通道」。为什么不装桌面端那套 Electron？
# 因为桌面的壳在服务器上没有意义，而 Playwright 驱动 Chromium 在无头环境里更省资源。
# 两者对 Go 核心而言是同一个 HTTP 契约，换掉没有任何影响。
#
# 镜像会比较大（约 1.5GB），主要是 Chromium。这是硬约束：
# Dreamina 的写操作必须由真实浏览器的 secsdk 生成风控签名，服务端无法复现。

# ---------------------------------------------------------------- 前端
FROM node:22-bookworm-slim AS web
WORKDIR /build
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY tsconfig.json vite.config.ts ./
COPY src ./src
RUN pnpm build

# ---------------------------------------------------------------- Go 核心
FROM golang:1.26-bookworm AS core
WORKDIR /build
COPY go/go.mod go/go.sum ./
RUN go mod download
COPY go ./
# modernc.org/sqlite 是纯 Go 实现，所以可以彻底关掉 cgo，产出静态二进制
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/manager ./cmd/manager

# ---------------------------------------------------------------- 浏览器通道
FROM node:22-bookworm-slim AS channel

# —— 层顺序是刻意安排的 ——
#
# 浏览器相关的东西（系统依赖 + 浏览器二进制）和源码无关，放在最前面：
# 只要这一段的指令不变，改业务代码时它们就一直命中缓存。
# 之前把 COPY 放在前面，导致改一行 server.mjs 就要重装 677 秒的系统依赖。

# 系统依赖用 root 装
RUN npx --yes playwright@1.63.0 install-deps chromium \
 && rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --uid 10001 app

USER app
ENV PLAYWRIGHT_BROWSERS_PATH=/home/app/.cache/ms-playwright
# 版本必须和 sidecar 依赖的 playwright-core 完全一致（都是 1.63.0），
# 否则运行时会报「找不到浏览器」——它们的版本号是绑死的
RUN npx --yes playwright@1.63.0 install chromium

# —— 以下是应用代码，改动频繁，放最后 ——
# WORKDIR 必须落在 /app/sidecar：运行时和本地开发都按这个路径找 server.mjs
WORKDIR /app/sidecar
COPY --chown=app:app sidecar/package.json ./
RUN npm install --omit=dev
COPY --chown=app:app sidecar/server.mjs ./

# ---------------------------------------------------------------- 运行时
FROM node:22-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive \
    NODE_ENV=production \
    TZ=UTC

# bash 是 entrypoint 需要的（wait -n）；curl 用于健康检查与等待通道就绪
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata curl bash \
 && rm -rf /var/lib/apt/lists/*

# 从 channel 阶段整体搬过来：node_modules + 浏览器 + 用户
COPY --from=channel /usr/lib/x86_64-linux-gnu /usr/lib/x86_64-linux-gnu
COPY --from=channel /etc/fonts /etc/fonts
COPY --from=channel /app /app
COPY --from=channel /home/app /home/app
COPY --from=channel /etc/passwd /etc/passwd
COPY --from=channel /etc/group /etc/group

COPY --from=core /out/manager /app/manager
COPY --from=web /build/dist/web /app/web

WORKDIR /app
RUN mkdir -p /data && chown -R app:app /app /data

USER app
ENV PLAYWRIGHT_BROWSERS_PATH=/home/app/.cache/ms-playwright \
    XDG_CACHE_HOME=/home/app/.cache

# 管理端只绑回环（容器内不可达外面），网关单独开一个端口对外
ENV MANAGER_HOST=127.0.0.1 \
    MANAGER_PORT=8787 \
    MANAGER_DATA_DIR=/data \
    MANAGER_WEB_DIST=/app/web \
    SIDECAR_URL=http://127.0.0.1:8790 \
    SIDECAR_PORT=8790 \
    SIDECAR_ARGS=--no-sandbox,--disable-dev-shm-usage \
    GATEWAY_HOST=0.0.0.0 \
    GATEWAY_PORT=8791

EXPOSE 8791

# 用 /healthz 而不是 /v1/models：后者无密钥返回 401，
# 而 curl -f 遇到 4xx 就失败，健康检查会永远是红的。
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8791/healthz >/dev/null 2>&1 || exit 1

COPY --chown=app:app docker/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

ENTRYPOINT ["/app/entrypoint.sh"]
