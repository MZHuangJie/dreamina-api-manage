/**
 * Dreamina 浏览器通道 sidecar
 * ============================
 *
 * 为什么需要它：Dreamina 的写操作强制校验 secsdk 在页面里生成的
 * msToken / X-Bogus / X-Gnarly，这些参数由浏览器 JS 计算并附加到 URL 上，
 * 纯服务端无论用什么语言都无法复现（社区也没有可用的 Go 实现）。
 *
 * 本进程的职责被刻意限制得非常窄：
 *   「在指定账号的已登录页面上下文里，代发一次 HTTP 请求」
 * 它不理解 Dreamina 的业务语义，所有协议逻辑都在 Go 侧。
 *
 * 每个账号一个独立的 BrowserContext（独立 Cookie 容器 + 独立代理出口），
 * 所有 Context 共享同一个 Chromium 进程以省内存。
 */
import http from "node:http";
import process from "node:process";

import { chromium } from "playwright-core";

/* ------------------------------ 配置 ------------------------------ */

const PORT = Number(process.env.SIDECAR_PORT ?? 8790);
const HOST = process.env.SIDECAR_HOST ?? "127.0.0.1";
/** 共享密钥；Go 侧每个请求都要带 X-Sidecar-Token */
const SECRET = process.env.SIDECAR_SECRET ?? "";
/** 系统 Chrome 路径；留空则由 playwright-core 自行查找 */
const CHROME_PATH = process.env.SIDECAR_CHROME ?? "";
const HEADLESS = process.env.SIDECAR_HEADLESS !== "false";
/** 空闲多久回收一个账号的浏览器上下文 */
const IDLE_MS = Number(process.env.SIDECAR_IDLE_MS ?? 15 * 60_000);
/** 单个账号最多并发多少个请求（同一页面串行执行） */
const FETCH_TIMEOUT_MS = Number(process.env.SIDECAR_FETCH_TIMEOUT_MS ?? 60_000);
/** 页面加载后等待 secsdk 就绪的上限 */
const SECSDK_WAIT_MS = Number(process.env.SIDECAR_SECSDK_WAIT_MS ?? 30_000);
/**
 * 追加的 Chromium 启动参数，逗号分隔。
 *
 * 容器里跑必须要 --no-sandbox（root 身份下 Chromium 会拒绝启动），
 * 以及 --disable-dev-shm-usage（容器默认 /dev/shm 只有 64MB，会崩标签页）。
 */
const EXTRA_ARGS = (process.env.SIDECAR_ARGS ?? "")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);

const SITE = "https://dreamina.capcut.com";
// 落地页可用 SIDECAR_LANDING 覆盖。
//
// 为什么必须能改：secsdk 是按**页面所属产品**生成签名和 token 的，而且请求受
// 同源策略约束。用 Dreamina 的页面去调 CapCut 的接口，会直接被浏览器判成跨域，
// 报 TypeError: Failed to fetch —— 看起来像网络故障，其实是页面加载错了。
const LANDING = process.env.SIDECAR_LANDING || `${SITE}/ai-tool/home`;

/* ------------------------------ 日志 ------------------------------ */

const started = Date.now();
function log(level, msg, extra) {
  const line = `[${new Date().toISOString()}] ${level} ${msg}${extra ? " " + JSON.stringify(extra) : ""}`;
  if (level === "ERROR") console.error(line);
  else console.log(line);
}

/* ------------------------------ 浏览器 ------------------------------ */

let browserPromise = null;

async function getBrowser() {
  if (!browserPromise) {
    browserPromise = chromium
      .launch({
        headless: HEADLESS,
        executablePath: CHROME_PATH || undefined,
        args: ["--disable-blink-features=AutomationControlled", "--lang=en-US", ...EXTRA_ARGS]
      })
      .then((browser) => {
        log("INFO", "chromium 已启动", { version: browser.version() });
        browser.on("disconnected", () => {
          log("ERROR", "chromium 断连，下次请求将重启");
          browserPromise = null;
        });
        return browser;
      })
      .catch((error) => {
        browserPromise = null;
        throw error;
      });
  }
  return browserPromise;
}

/* ------------------------------ 会话管理 ------------------------------ */

/**
 * accountId -> {
 *   context, page, proxy, lastUsed, ready,
 *   queue: Promise   // 串行化同一账号的请求，避免页面竞争
 * }
 */
const sessions = new Map();

/**
 * 人工登录会话。
 *
 * 与业务会话不同：这里必须启动一个**有头**的浏览器，因为用户要亲自操作。
 * accountId -> { browser, context, page }
 */
const loginSessions = new Map();

/** accountId -> 正在进行的打开操作，用来串行化同一账号的并发请求。 */
const loginOpening = new Map();

/** accountId -> 窗口关闭后留存下来的 cookie。 */
const loginCaptures = new Map();

function normalizeProxy(raw) {
  const value = (raw ?? "").trim();
  if (!value) return undefined;
  const withScheme = /^[a-z0-9+.-]+:\/\//i.test(value) ? value : `http://${value}`;
  const url = new URL(withScheme);
  return { server: `${url.protocol}//${url.host}` };
}

/** 等待页面里的 secsdk 就绪（byted_acrawler 是它的标志） */
async function waitForSecsdk(page, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const state = await page
      .evaluate(() => ({
        acrawler: typeof window.byted_acrawler,
        fetchPatched: !/native code/.test(String(window.fetch)),
        xhrPatched: !/native code/.test(String(XMLHttpRequest.prototype.open))
      }))
      .catch(() => null);
    // 只判 fetch 有没有被接管。
    //
    // 原先还要求 window.byted_acrawler 是对象 —— 那是 Dreamina 那套 secsdk
    // 的标志。CapCut 用的是另一个版本（webmssdk_cctbc），不暴露这个全局，
    // 于是检查永远不通过：会话建不起来，而页面本身完全正常。
    //
    // 真正决定签名能否工作的就是 fetch 有没有被接管，所以只判它。
    if (state?.fetchPatched) return true;
    await page.waitForTimeout(500);
  }
  return false;
}

async function ensureSession({ accountId, cookies, proxy, timezone }) {
  const existing = sessions.get(accountId);
  if (existing) {
    // 代理或时区变了就必须重建上下文
    const wanted = proxy ?? "";
    const wantedTz = timezone ?? "";
    if ((existing.proxy ?? "") === wanted && (existing.timezone ?? "") === wantedTz) {
      existing.lastUsed = Date.now();
      return existing;
    }
    log("INFO", "代理变更，重建上下文", { accountId });
    await destroySession(accountId);
  }

  const browser = await getBrowser();
  const context = await browser.newContext({
    userAgent:
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36",
    locale: "en-US",
    // 按账号归属地设时区。账号在日区、代理在日本，
    // 浏览器却报美国时间是很显眼的指纹差异。
    timezoneId: timezone || "America/Los_Angeles",
    viewport: { width: 1440, height: 900 },
    proxy: normalizeProxy(proxy)
  });

  if (cookies?.length) await context.addCookies(cookies);

  const page = await context.newPage();
  await page.goto(LANDING, { waitUntil: "domcontentloaded", timeout: 90_000 });
  const ready = await waitForSecsdk(page, SECSDK_WAIT_MS);

  const session = {
    context,
    page,
    proxy: proxy ?? "",
    timezone: timezone ?? "",
    lastUsed: Date.now(),
    ready,
    queue: Promise.resolve()
  };
  sessions.set(accountId, session);
  log("INFO", "会话已建立", {
    accountId,
    secsdkReady: ready,
    proxy: proxy ?? "(直连)",
    timezone: timezone || "(默认)"
  });
  return session;
}

async function destroySession(accountId) {
  const session = sessions.get(accountId);
  if (!session) return false;
  sessions.delete(accountId);
  await session.context.close().catch(() => {});
  return true;
}

/** 把同一账号的请求串行化——页面上下文不是线程安全的 */
function withSession(accountId, fn) {
  const session = sessions.get(accountId);
  if (!session) throw new Error(`会话不存在: ${accountId}`);
  const run = session.queue.then(() => fn(session), () => fn(session));
  // 吞掉错误，避免队列被拒绝状态卡死
  session.queue = run.then(
    () => undefined,
    () => undefined
  );
  return run;
}

/** 打开一个有头浏览器让用户登录。 */
async function openLoginSession(accountId, target, proxy) {
  // 幂等：已经有活着的会话就直接用，别重开。
  //
  // 重复调用是常态；关掉再开会把用户正在登录的窗口弄没，
  // 表现就是「点了登录，窗口闪一下就没了」。
  const existing = loginSessions.get(accountId);
  if (existing && !existing.page.isClosed()) {
    await existing.page.bringToFront().catch(() => {});
    return;
  }

  // 串行化：同一账号若已有打开操作在途，直接复用它。
  //
  // 否则两次并发调用会双双在「关闭旧会话」那步扑空（此时旧会话还没登记），
  // 结果开出两个窗口，而 Map 只记住后一个——用户在第一个里登录，
  // 管理器却去读第二个的 cookie，永远读不到。
  if (loginOpening.has(accountId)) return loginOpening.get(accountId);
  const task = doOpenLoginSession(accountId, target, proxy).finally(() => {
    loginOpening.delete(accountId);
  });
  loginOpening.set(accountId, task);
  return task;
}

async function doOpenLoginSession(accountId, target, proxy) {
  if (loginSessions.has(accountId)) {
    await closeLoginSession(accountId);
  }

  // 独立于业务浏览器：用户要能看到并操作它
  const browser = await chromium.launch({
    headless: false,
    executablePath: CHROME_PATH || undefined,
    args: ["--disable-blink-features=AutomationControlled", "--lang=en-US", ...EXTRA_ARGS]
  });

  const context = await browser.newContext({
    userAgent:
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36",
    locale: "en-US",
    viewport: { width: 1280, height: 860 },
    proxy: normalizeProxy(proxy)
  });

  const page = await context.newPage();
  loginSessions.set(accountId, { browser, context, page });

  // 窗口关掉时先把 cookie 留存下来。
  //
  // 用户登完顺手关窗是最自然的动作；不留存的话那一瞬间起就再也读不到
  // 登录态，界面只会一直显示「等待登录完成」。
  page.on("close", () => {
    void captureLoginCookies(accountId, context).finally(() => {
      loginSessions.delete(accountId);
    });
  });

  try {
    await page.goto(target, { waitUntil: "domcontentloaded", timeout: 90_000 });
  } catch (error) {
    log("ERROR", `登录窗口加载失败: ${error?.message}`);
  }
  log("INFO", `已打开登录窗口 ${accountId}，请在浏览器里完成登录`);
}

/** 关闭登录会话并清掉上下文，避免登录态串到业务会话。 */
/** 在销毁上下文前把 cookie 抄一份留底。 */
async function captureLoginCookies(accountId, context) {
  try {
    const cookies = await context.cookies();
    if (!cookies.some((k) => k.name === "sessionid" && k.value)) return;
    loginCaptures.set(accountId, { cookies, at: Date.now() });
    log("INFO", `已留存 ${accountId} 的登录 cookie（${cookies.length} 项）`);
  } catch (error) {
    log("ERROR", `留存登录 cookie 失败: ${error?.message}`);
  }
}

async function closeLoginSession(accountId) {
  const record = loginSessions.get(accountId);
  if (!record) return false;
  await captureLoginCookies(accountId, record.context);
  loginSessions.delete(accountId);
  await record.context.close().catch(() => {});
  await record.browser.close().catch(() => {});
  return true;
}

/* ------------------------------ 闲置回收 ------------------------------ */

setInterval(() => {
  const now = Date.now();
  for (const [accountId, session] of sessions) {
    if (now - session.lastUsed > IDLE_MS) {
      log("INFO", "回收闲置会话", { accountId, idleMs: now - session.lastUsed });
      void destroySession(accountId);
    }
  }
}, 60_000).unref();

/* ------------------------------ HTTP ------------------------------ */

function send(res, status, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(status, { "content-type": "application/json; charset=utf-8" });
  res.end(body);
}

async function readJson(req, limitBytes = 8 * 1024 * 1024) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    size += chunk.length;
    if (size > limitBytes) throw new Error("请求体过大");
    chunks.push(chunk);
  }
  if (!chunks.length) return {};
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

/* ------------------------------ 路由 ------------------------------ */

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);

  if (SECRET && req.headers["x-sidecar-token"] !== SECRET) {
    return send(res, 401, { ok: false, error: "unauthorized" });
  }

  try {
    /* --- 健康检查 --- */
    if (req.method === "GET" && url.pathname === "/health") {
      return send(res, 200, {
        ok: true,
        uptimeMs: Date.now() - started,
        browser: browserPromise ? "running" : "idle",
        loginWindows: loginSessions.size,
        sessions: [...sessions.entries()].map(([id, s]) => ({
          accountId: id,
          ready: s.ready,
          proxy: s.proxy || null,
          timezone: s.timezone || null,
          idleMs: Date.now() - s.lastUsed
        }))
      });
    }

    if (req.method === "GET" && url.pathname === "/sessions") {
      return send(res, 200, {
        ok: true,
        sessions: [...sessions.keys()]
      });
    }

    /* --- 建立/刷新会话 --- */
    if (req.method === "POST" && url.pathname === "/session") {
      const { accountId, cookies, proxy, timezone } = await readJson(req);
      if (!accountId) return send(res, 400, { ok: false, error: "accountId 必填" });
      const session = await ensureSession({ accountId, cookies, proxy, timezone });
      return send(res, 200, { ok: true, accountId, secsdkReady: session.ready });
    }

    /* --- 销毁会话 --- */
    if (req.method === "DELETE" && url.pathname.startsWith("/session/")) {
      const accountId = decodeURIComponent(url.pathname.slice("/session/".length));
      const removed = await destroySession(accountId);
      return send(res, 200, { ok: true, removed });
    }

    /* --- 人工登录 --- */
    //
    // 不逆向登录协议，而是开一个有头浏览器交给用户操作。
    // 图形验证码、邮箱验证码、Google 登录全都天然支持。
    //
    // 注意：容器里没有显示设备，这条路径在服务器上不可用——
    // 服务端部署需要在本机登录后把凭据拷过去。
    if (req.method === "POST" && url.pathname === "/login") {
      const { accountId, url: target, proxy } = await readJson(req);
      if (!accountId) return send(res, 400, { ok: false, error: "accountId 必填" });
      await openLoginSession(accountId, target || LANDING, proxy);
      return send(res, 200, { ok: true, accountId });
    }

    if (req.method === "GET" && url.pathname.startsWith("/login/")) {
      const accountId = decodeURIComponent(url.pathname.slice("/login/".length));
      const record = loginSessions.get(accountId);
      let cookies;
      if (record) {
        cookies = await record.context.cookies();
      } else {
        // 窗口已经关了（用户登完顺手关掉），用留存下来的那一份
        const saved = loginCaptures.get(accountId);
        if (!saved) return send(res, 200, { ok: true, loggedIn: false, error: "登录窗口不存在" });
        cookies = saved.cookies;
      }
      const has = cookies.some((k) => k.name === "sessionid" && k.value);
      return send(res, 200, {
        ok: true,
        loggedIn: has,
        cookies: has ? cookies.map((k) => ({ name: k.name, value: k.value, domain: k.domain, path: k.path })) : undefined,
      });
    }

    if (req.method === "DELETE" && url.pathname.startsWith("/login/")) {
      const accountId = decodeURIComponent(url.pathname.slice("/login/".length));
      const removed = await closeLoginSession(accountId);
      return send(res, 200, { ok: true, removed });
    }

    /* --- 核心：在页面上下文里代发请求 --- */
    // 调试用：在页面上下文里执行一段 JS。
    //
    // 用途很具体：把浏览器里验证过的代码**原样**搬进来跑一次。
    // 当我们的参数化 fetch 和页面真实请求行为不一致时，只有跑同一段代码
    // 才能判断差异出在「构造方式」还是「页面上下文本身」。
    if (req.method === "POST" && url.pathname === "/eval") {
      const { accountId, script } = await readJson(req);
      if (!accountId || !script) return send(res, 400, { ok: false, error: "accountId 与 script 必填" });
      if (!sessions.has(accountId)) return send(res, 409, { ok: false, error: "会话不存在" });
      const result = await withSession(accountId, async (session) => {
        session.lastUsed = Date.now();
        return session.page.evaluate(script);
      });
      return send(res, 200, { ok: true, result });
    }

    if (req.method === "POST" && url.pathname === "/fetch") {
      const { accountId, url: target, method = "POST", headers = {}, body } = await readJson(req);
      if (!accountId || !target) {
        return send(res, 400, { ok: false, error: "accountId 与 url 必填" });
      }
      if (!sessions.has(accountId)) {
        return send(res, 409, { ok: false, error: `会话不存在，请先 POST /session: ${accountId}` });
      }

      const result = await withSession(accountId, async (session) => {
        session.lastUsed = Date.now();
        return session.page.evaluate(
          async ({ target, method, headers, body, timeoutMs }) => {
            const controller = new AbortController();
            const timer = setTimeout(() => controller.abort(), timeoutMs);
            try {
              const response = await fetch(target, {
                method,
                headers,
                body: body ?? undefined,
                credentials: "include",
                signal: controller.signal
              });
              const text = await response.text();
              const responseHeaders = {};
              response.headers.forEach((v, k) => (responseHeaders[k] = v));
              return { status: response.status, body: text, headers: responseHeaders };
            } finally {
              clearTimeout(timer);
            }
          },
          { target, method, headers, body, timeoutMs: FETCH_TIMEOUT_MS }
        );
      });

      return send(res, 200, { ok: true, ...result });
    }

    return send(res, 404, { ok: false, error: "not found" });
  } catch (error) {
    log("ERROR", `${req.method} ${url.pathname} 失败: ${error?.message}`);
    return send(res, 500, { ok: false, error: String(error?.message ?? error) });
  }
});

server.listen(PORT, HOST, () => {
  log("INFO", `sidecar 已启动 http://${HOST}:${PORT}`, {
    headless: HEADLESS,
    chrome: CHROME_PATH || "(自动查找)",
    auth: SECRET ? "已启用" : "未设置"
  });
});

/* ------------------------------ 优雅退出 ------------------------------ */

async function shutdown(signal) {
  log("INFO", `收到 ${signal}，关闭中…`);
  server.close();
  for (const accountId of [...sessions.keys()]) await destroySession(accountId);
  if (browserPromise) {
    const browser = await browserPromise.catch(() => null);
    await browser?.close().catch(() => {});
  }
  process.exit(0);
}

process.on("SIGINT", () => void shutdown("SIGINT"));
process.on("SIGTERM", () => void shutdown("SIGTERM"));
