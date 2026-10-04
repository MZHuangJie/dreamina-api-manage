const net = require("node:net");

/** 向系统要一个当前空闲的端口。 */
function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.on("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

/** 优先用 preferred；被占用就退回随机空闲端口。 */
async function reservePort(preferred) {
  if (preferred) {
    const available = await isFree(preferred);
    if (available) return preferred;
  }
  return freePort();
}

function isFree(port) {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.unref();
    server.once("error", () => resolve(false));
    server.listen(port, "127.0.0.1", () => {
      server.close(() => resolve(true));
    });
  });
}

/** 轮询等待某个 HTTP 端点可用（用于等 Go 核心起来）。 */
async function waitForHTTP(url, { timeoutMs = 30_000, intervalMs = 300 } = {}) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    try {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), 2000);
      const response = await fetch(url, { signal: controller.signal });
      clearTimeout(timer);
      if (response.ok) return true;
      lastError = new Error(`HTTP ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((r) => setTimeout(r, intervalMs));
  }
  throw lastError ?? new Error("等待超时");
}

module.exports = { freePort, reservePort, isFree, waitForHTTP };
