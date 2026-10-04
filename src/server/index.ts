import fs from "node:fs";
import path from "node:path";

import { serve } from "@hono/node-server";
import { serveStatic } from "@hono/node-server/serve-static";
import { Hono } from "hono";
import { cors } from "hono/cors";

import { config, ensureDataDir } from "./config.ts";
import { getKey } from "./crypto.ts";
import { logEvent, migrate } from "./db.ts";
import { startHealthScheduler, stopHealthScheduler } from "./accounts/health.ts";
import { closeAllProxyAgents } from "./jimeng/client.ts";
import { accountRoutes, poolRoutes } from "./routes/accounts.ts";
import { generateRoutes } from "./routes/generate.ts";
import { reconcileInterruptedGenerations } from "./generate/tasks.ts";

ensureDataDir();
migrate();

// 启动时就生成加密密钥，这样用户第一次打开界面就能备份它，
// 而不是等到添加第一个账号后才悄悄出现。
getKey();

// 上次进程被杀掉时残留的 pending/running 任务不可能再恢复，标记为中断
const interrupted = reconcileInterruptedGenerations();
if (interrupted > 0) {
  console.log(`已清理 ${interrupted} 条上次中断的生成任务`);
}

const app = new Hono();

app.use(
  "*",
  cors({
    origin: (origin) =>
      !origin || /^http:\/\/(127\.0\.0\.1|localhost)(:\d+)?$/.test(origin) ? origin || "*" : null,
    allowHeaders: ["Content-Type", "X-Access-Token"],
    allowMethods: ["GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"]
  })
);

/**
 * 默认只监听 127.0.0.1。一旦通过 MANAGER_HOST 暴露到局域网，
 * 必须设置 MANAGER_ACCESS_TOKEN，否则等同于把全部即梦账号交出去。
 */
if (config.accessToken) {
  app.use("/api/*", async (c, next) => {
    const provided =
      c.req.header("X-Access-Token") ?? c.req.query("token") ?? "";
    if (provided !== config.accessToken) {
      return c.json({ ok: false, error: { code: "UNAUTHORIZED", message: "访问令牌无效" } }, 401);
    }
    await next();
  });
}

const api = new Hono();
api.route("/accounts", accountRoutes);
api.route("/pool", poolRoutes);
api.route("/", generateRoutes);

api.get("/ping", (c) => c.json({ ok: true, data: { pong: true, at: new Date().toISOString() } }));

app.route("/api", api);

/* ------------------------------------------------------------------ */
/* 静态资源（前端构建产物）                                            */
/* ------------------------------------------------------------------ */

const indexHtmlPath = path.join(config.webDistDir, "index.html");
const hasWebBuild = fs.existsSync(indexHtmlPath);

if (hasWebBuild) {
  app.use("/*", serveStatic({ root: config.webDistDir }));
  app.get("*", async (c) => {
    if (c.req.path.startsWith("/api")) return c.notFound();
    const html = await fs.promises.readFile(indexHtmlPath, "utf8");
    return c.html(html);
  });
} else {
  app.get("*", (c) =>
    c.html(
      `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
       <title>即梦账号管理器</title>
       <style>
         body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#0b0d12;color:#e6e8ee;
              display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
         .card{max-width:560px;padding:32px 36px;border:1px solid #232733;border-radius:16px;background:#12151d}
         code{background:#1c2030;padding:2px 8px;border-radius:6px;color:#8ab4ff}
         a{color:#8ab4ff}
       </style></head><body><div class="card">
       <h2>即梦账号管理器 · API 已就绪</h2>
       <p>前端尚未构建。开发模式请另开一个终端运行 <code>pnpm dev:web</code>，
       然后访问 <a href="http://127.0.0.1:5173">http://127.0.0.1:5173</a>。</p>
       <p>生产方式请先运行 <code>pnpm build</code>，再刷新本页。</p>
       <p>API 健康检查：<a href="/api/ping">/api/ping</a></p>
       </div></body></html>`
    )
  );
}

/* ------------------------------------------------------------------ */
/* 启动                                                                */
/* ------------------------------------------------------------------ */

const server = serve({ fetch: app.fetch, hostname: config.host, port: config.port }, (info) => {
  const url = `http://${config.host === "0.0.0.0" ? "127.0.0.1" : config.host}:${info.port}`;
  console.log("");
  console.log("  即梦账号管理器");
  console.log(`  ├─ 服务地址   ${url}`);
  console.log(`  ├─ 数据目录   ${config.dataDir}`);
  console.log(`  ├─ 密钥文件   ${process.env.MANAGER_SECRET ? "由 MANAGER_SECRET 提供" : config.keyPath}`);
  console.log(`  ├─ 前端产物   ${hasWebBuild ? config.webDistDir : "未构建（开发模式请运行 pnpm dev:web）"}`);
  console.log(`  ├─ 保活调度   ${config.health.enabled ? `启用，每 ${Math.round(config.health.intervalMs / 60000)} 分钟` : "已关闭"}`);
  console.log(`  └─ 访问令牌   ${config.accessToken ? "已启用" : "未设置（仅限本机访问）"}`);
  console.log("");
});

startHealthScheduler();
logEvent({ kind: "server.start", message: `服务启动于 ${config.host}:${config.port}` });

function shutdown(signal: string): void {
  console.log(`\n收到 ${signal}，正在关闭...`);
  stopHealthScheduler();
  closeAllProxyAgents();
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(0), 3000).unref();
}

process.on("SIGINT", () => shutdown("SIGINT"));
process.on("SIGTERM", () => shutdown("SIGTERM"));
