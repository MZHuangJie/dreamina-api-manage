#!/bin/bash
# 容器入口：先拉起浏览器通道，再启动核心。
#
# 用 bash 而不是 sh：下面的 wait -n 是 bash 专有语法，
# Debian 的 /bin/sh 是 dash，跑到那里会直接报错退出。
#
# 为什么不用 supervisord：只有两个进程，而且它们的生命周期本来就是绑死的——
# 通道挂了核心也用不了，不如让它一起死，交给 Docker 的 restart 策略统一重启。
set -euo pipefail

echo "[entrypoint] 启动浏览器通道 sidecar (端口 ${SIDECAR_PORT}) ..."
node /app/sidecar/server.mjs &
SIDECAR_PID=$!

cleanup() {
  echo "[entrypoint] 收到信号，关闭中…"
  kill "$SIDECAR_PID" "${CORE_PID:-}" 2>/dev/null || true
  exit 0
}
trap cleanup TERM INT

# 等通道把 HTTP 端口监听起来。
# 注意只等它监听，不等它建会话——会话要等有账号发起请求才会建立。
ready=0
for _ in $(seq 1 40); do
  if curl -fsS "http://127.0.0.1:${SIDECAR_PORT}/health" >/dev/null 2>&1; then
    echo "[entrypoint] 浏览器通道已就绪"
    ready=1
    break
  fi
  if ! kill -0 "$SIDECAR_PID" 2>/dev/null; then
    echo "[entrypoint] 浏览器通道启动失败" >&2
    exit 1
  fi
  sleep 0.5
done

if [ "$ready" -ne 1 ]; then
  echo "[entrypoint] 等待浏览器通道就绪超时，仍继续启动核心（核心会自行重试）" >&2
fi

echo "[entrypoint] 启动核心服务 ..."
/app/manager serve &
CORE_PID=$!

# 任一进程退出就整体退出，让 Docker 重启容器，
# 而不是留一个半死的服务在跑
wait -n "$SIDECAR_PID" "$CORE_PID" || true
echo "[entrypoint] 有进程退出，容器即将结束"
kill "$SIDECAR_PID" "$CORE_PID" 2>/dev/null || true
exit 1
