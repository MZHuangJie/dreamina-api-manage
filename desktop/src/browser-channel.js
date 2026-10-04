const http = require("node:http");
const { applySessionProxy } = require("./proxy-config.js");
const SITE = "https://dreamina.capcut.com";
const LANDING = `${SITE}/ai-tool/home`;

const DEFAULT_UA =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

class BrowserChannel {
  /**
   * @param {object} options
   * @param {import('electron').Session} options.rootSession Electron 默认 session（用于兜底）
   * @param {(opts: object) => import('electron').BrowserWindow} options.createWindow 创建隐藏窗口的工厂
   * @param {(msg: string, extra?: object) => void} options.log
   */
  constructor({ createWindow, log, secret = "", idleMs = 15 * 60_000, fetchTimeoutMs = 60_000, secsdkWaitMs = 30_000 }) {
    this.createWindow = createWindow;
    this.log = log;
    this.secret = secret;
    this.idleMs = idleMs;
    this.fetchTimeoutMs = fetchTimeoutMs;
    this.secsdkWaitMs = secsdkWaitMs;

    /** accountId -> 登录窗口（人工登录用，可见） */
    this.loginWindows = new Map();

    /**
     * accountId -> 正在进行的打开操作。
     *
     * 用来串行化同一个账号的打开请求。没有它的话，两次并发调用会**双双**
     * 在「关闭旧窗口」那步扑空（此时旧窗口还没登记进 Map），于是两个窗口
     * 都留下来，而 Map 只记住后一个——用户在第一个窗口登录，管理器却去
     * 读第二个窗口的 cookie，永远读不到。
     */
    this.loginOpening = new Map();

    /**
     * accountId -> 窗口关闭后保留下来的 cookie。
     *
     * 用户登录完顺手关窗口是最自然的动作。如果关掉就把记录删了，
     * 之后就再也读不到登录态，表现为「登了但没反应」。
     */
    this.loginCaptures = new Map();

    /** accountId -> session 记录 */
    this.sessions = new Map();
    this.server = null;
    this.startedAt = Date.now();
    this.sweeper = null;
  }

  /* ------------------------------ 生命周期 ------------------------------ */

  async start(port, host = "127.0.0.1") {
    this.server = http.createServer((req, res) => {
      void this.handle(req, res);
    });
    await new Promise((resolve, reject) => {
      this.server.once("error", reject);
      this.server.listen(port, host, resolve);
    });
    this.sweeper = setInterval(() => void this.sweep(), 60_000);
    this.sweeper.unref?.();
    this.log(`浏览器通道已启动 http://${host}:${port}`);
  }

  async stop() {
    if (this.sweeper) clearInterval(this.sweeper);
    for (const accountId of [...this.sessions.keys()]) {
      await this.destroySession(accountId);
    }
    if (this.server) {
      await new Promise((resolve) => this.server.close(resolve));
      this.server = null;
    }
  }

  /* ------------------------------ 会话 ------------------------------ */

  async ensureSession({ accountId, cookies, proxy, timezone }) {
    const existing = this.sessions.get(accountId);
    if (existing) {
      // 代理或时区变了必须重建，否则出口 IP / 时区和账号归属地对不上
      if ((existing.proxy ?? "") === (proxy ?? "") && (existing.timezone ?? "") === (timezone ?? "")) {
        existing.lastUsed = Date.now();
        return existing;
      }
      this.log(`代理或时区变更，重建会话 ${accountId}`);
      await this.destroySession(accountId);
    }

    const { session: electronSession } = require("electron");
    // 不带 persist: 前缀 = 纯内存分区，进程退出即清空
    const partition = `acct-${accountId.replace(/[^a-zA-Z0-9]/g, "")}`;
    const ses = electronSession.fromPartition(partition);

    // 每次打开前把分区清干净，让它等价于「无痕窗口」。
    //
    // 分区是持久的：上一次尝试留下的 cookie / localStorage 会带进来。
    // 典型症状是「输入邮箱点继续没反应」—— 因为站点还认为你在之前那个
    // 被判定为不可用的地区，或者拿着一份过期的风控指纹。
    // 这也是为什么同一套操作在无痕窗口里反而正常：无痕每次都是全新状态。
    //
    // 这里刻意清得比较彻底：存储、HTTP 缓存、DNS 缓存都清，
    // 少清一样都可能留下上一次的痕迹。
    try {
      await clearSessionStorage(ses);
      this.log(`已清空会话分区 ${partition} 的缓存与存储`);
    } catch (error) {
      this.log(`清理登录分区失败: ${error?.message}`, null, "ERROR");
    }

    await applySessionProxy(ses, proxy, `会话 ${accountId}`, (m) => this.log(m));
    ses.setUserAgent(DEFAULT_UA, "en-US,en;q=0.9");
    this.log(`[步骤] UA 已设置 ${accountId}`);

    if (cookies?.length) {
      await applyCookies(ses, cookies);
      this.log(`[步骤] cookie 已注入 ${accountId}`);
    }

    const win = this.createWindow(ses);
    this.log(`[步骤] 窗口已创建 ${accountId}`);
    if (timezone) await applyTimezone(win, timezone, this.log);
    this.log(`[步骤] 时区已应用 ${accountId}`);

    const record = {
      accountId,
      partition,
      session: ses,
      win,
      proxy: proxy ?? "",
      timezone: timezone ?? "",
      lastUsed: Date.now(),
      ready: false,
      queue: Promise.resolve()
    };
    this.sessions.set(accountId, record);

    // 刻意**不** await 页面加载。
    //
    // Dreamina 首页资源极多（埋点、广告、字体），loadURL 可能要几十秒，
    // 甚至一直不 resolve。等它会把 /session 这个 HTTP 请求整个卡住，调用方
    // 直接超时——会话其实已经建好，接口却不返回。
    //
    // 我们真正要等的是 secsdk 就绪（下一步，它自带超时），不是页面加载完成。
    win.loadURL(LANDING).catch((error) => {
      this.log(`会话 ${accountId} 页面加载失败: ${error?.message}`, null, "ERROR");
    });
    record.ready = await this.waitForSecsdk(win);
    this.log(`会话 ${accountId} 已建立`, {
      secsdkReady: record.ready,
      proxy: proxy || "(直连)",
      timezone: timezone || "(默认)"
    });
    return record;
  }

  async destroySession(accountId) {
    const record = this.sessions.get(accountId);
    if (!record) return false;
    this.sessions.delete(accountId);
    try {
      if (!record.win.isDestroyed()) record.win.destroy();
    } catch {
      /* 窗口可能已经没了 */
    }
    try {
      await clearSessionStorage(record.session);
    } catch {
      /* 尽力而为 */
    }
    return true;
  }

  /** 同一账号的请求必须串行——页面上下文不是线程安全的。 */
  withSession(accountId, fn) {
    const record = this.sessions.get(accountId);
    if (!record) throw new Error(`会话不存在: ${accountId}`);
    const run = record.queue.then(() => fn(record), () => fn(record));
    // 吞掉拒绝，避免队列被卡死
    record.queue = run.then(
      () => undefined,
      () => undefined
    );
    return run;
  }

  /* ------------------------------ 人工登录 ------------------------------ */

  async openLoginWindow(accountId, target, proxy) {
    // 幂等：已经有活着的窗口就直接用，别重开。
    //
    // 重复调用是常态（前端 StrictMode 会跑两次 effect、用户也可能连点两下）。
    // 关掉再开会让用户正在登录的窗口凭空消失，看起来就像「点了没反应」。
    const alive = this.loginWindows.get(accountId);
    if (alive && !alive.win.isDestroyed()) {
      try { alive.win.focus(); } catch {}
      return;
    }
    // 串行化：同一账号若已有打开操作在途，复用它而不是再开一个。
    if (this.loginOpening.has(accountId)) {
      return this.loginOpening.get(accountId);
    }
    const task = this.doOpenLoginWindow(accountId, target, proxy).finally(() => {
      this.loginOpening.delete(accountId);
    });
    this.loginOpening.set(accountId, task);
    return task;
  }

  async doOpenLoginWindow(accountId, target, proxy) {
    // 用本机真实 Chrome，不用 Electron 内嵌的浏览器。
    //
    // 内嵌那个是被改造过的 Chromium：同一个登录页，表单能填、点提交却没有任何
    // 网络请求。而用户自己开无痕窗口走同一套流程完全正常。与其去猜是哪个指纹
    // 或 CSP 差异，不如直接借真 Chrome 用——环境完全一致。
    const { ChromeLoginSession } = require("./chrome-login.js");

    const session = new ChromeLoginSession(accountId, {
      url: target,
      proxy: proxy ? normalizeProxy(proxy) : "",
      log: (m) => this.log(m)
    });

    this.loginWindows.set(accountId, { chrome: session, accountId });
    try {
      await session.start();
    } catch (error) {
      this.loginWindows.delete(accountId);
      throw error;
    }
    this.log(`已打开登录窗口 ${accountId}，请在其中完成登录`);
  }

  async inspectLogin(accountId) {
    const record = this.loginWindows.get(accountId);
    let cookies;
    if (record) {
      try {
        cookies = await record.chrome.cookies();
      } catch (error) {
        return { loggedIn: false, error: String(error?.message ?? error) };
      }
    } else {
      // 窗口已经关了，用留存下来的那一份
      const saved = this.loginCaptures.get(accountId);
      if (!saved) return { loggedIn: false, error: "登录窗口不存在" };
      cookies = saved.cookies;
    }
    const sid = cookies.find((c) => c.name === "sessionid" && c.value);
    if (!sid) return { loggedIn: false };
    return { loggedIn: true, cookies };
  }


  async closeLoginWindow(accountId) {
    const record = this.loginWindows.get(accountId);
    if (!record) return false;
    try {
      const cookies = await record.chrome.cookies();
      if (cookies.some((k) => k.name === "sessionid" && k.value)) {
        this.loginCaptures.set(accountId, { cookies, at: Date.now() });
      }
    } catch {
      /* 读不到就算了，可能已经退了 */
    }
    this.loginWindows.delete(accountId);
    await record.chrome.close().catch(() => {});
    return true;
  }

  async sweep() {
    const now = Date.now();
    for (const [accountId, record] of this.sessions) {
      if (now - record.lastUsed > this.idleMs) {
        this.log(`回收闲置会话 ${accountId}`, { idleMs: now - record.lastUsed });
        await this.destroySession(accountId);
      }
    }
  }

  /* ------------------------------ 页面内执行 ------------------------------ */

  /** secsdk 就绪的标志：byted_acrawler 出现，且 fetch 已被它接管。 */
  async waitForSecsdk(win) {
    const deadline = Date.now() + this.secsdkWaitMs;
    while (Date.now() < deadline) {
      if (win.isDestroyed()) return false;
      const state = await win.webContents
        .executeJavaScript(
          `({
            acrawler: typeof window.byted_acrawler,
            fetchPatched: !/native code/.test(String(window.fetch)),
            xhrPatched: !/native code/.test(String(XMLHttpRequest.prototype.open))
          })`,
          true
        )
        .catch(() => null);
      if (state && state.acrawler === "object" && state.fetchPatched) return true;
      await sleep(500);
    }
    return false;
  }

  /* ------------------------------ HTTP ------------------------------ */

  async handle(req, res) {
    const url = new URL(req.url, `http://${req.headers.host}`);

    if (this.secret && req.headers["x-sidecar-token"] !== this.secret) {
      return send(res, 401, { ok: false, error: "unauthorized" });
    }

    try {
      if (req.method === "GET" && url.pathname === "/health") {
        return send(res, 200, {
          ok: true,
          uptimeMs: Date.now() - this.startedAt,
          browser: "electron",
          sessions: [...this.sessions.entries()].map(([id, s]) => ({
            accountId: id,
            ready: s.ready,
            proxy: s.proxy || null,
            timezone: s.timezone || null,
            idleMs: Date.now() - s.lastUsed
          }))
        });
      }

      if (req.method === "GET" && url.pathname === "/sessions") {
        return send(res, 200, { ok: true, sessions: [...this.sessions.keys()] });
      }

      if (req.method === "POST" && url.pathname === "/session") {
        const { accountId, cookies, proxy, timezone } = await readJSON(req);
        if (!accountId) return send(res, 400, { ok: false, error: "accountId 必填" });
        const record = await this.ensureSession({ accountId, cookies, proxy, timezone });
        return send(res, 200, { ok: true, accountId, secsdkReady: record.ready });
      }

      if (req.method === "DELETE" && url.pathname.startsWith("/session/")) {
        const accountId = decodeURIComponent(url.pathname.slice("/session/".length));
        const removed = await this.destroySession(accountId);
        return send(res, 200, { ok: true, removed });
      }

      // —— 人工登录 ——
      // 不逆向登录协议，而是把真实浏览器窗口交给用户操作。
      // 图形验证码、邮箱验证码、Google 登录全都天然支持。
      if (req.method === "POST" && url.pathname === "/login") {
        const { accountId, url: target, proxy } = await readJSON(req);
        if (!accountId) return send(res, 400, { ok: false, error: "accountId 必填" });
        await this.openLoginWindow(accountId, target || SITE, proxy);
        return send(res, 200, { ok: true, accountId });
      }

      if (req.method === "GET" && url.pathname.startsWith("/login/")) {
        const accountId = decodeURIComponent(url.pathname.slice("/login/".length));
        const result = await this.inspectLogin(accountId);
        return send(res, 200, { ok: true, ...result });
      }

      // 调试用：往登录窗口注入一段 JS 并取回结果。
      // 只监听回环地址，且要带 sidecar 密钥。
      if (req.method === "POST" && url.pathname === "/login-debug") {
        const { accountId, script } = await readJSON(req);
        const rec = this.loginWindows.get(accountId);
        if (!rec) return send(res, 404, { ok: false, error: "登录窗口未打开" });
        try {
          const value = await rec.win.webContents.executeJavaScript(script, true);
          return send(res, 200, { ok: true, value });
        } catch (error) {
          return send(res, 200, { ok: false, error: String(error?.message ?? error) });
        }
      }

      if (req.method === "DELETE" && url.pathname.startsWith("/login/")) {
        const accountId = decodeURIComponent(url.pathname.slice("/login/".length));
        const removed = await this.closeLoginWindow(accountId);
        return send(res, 200, { ok: true, removed });
      }

      if (req.method === "POST" && url.pathname === "/fetch") {
        const { accountId, url: target, method = "POST", headers = {}, body } = await readJSON(req);
        if (!accountId || !target) {
          return send(res, 400, { ok: false, error: "accountId 与 url 必填" });
        }
        if (!this.sessions.has(accountId)) {
          return send(res, 409, { ok: false, error: `会话不存在，请先 POST /session: ${accountId}` });
        }

        const result = await this.withSession(accountId, async (record) => {
          record.lastUsed = Date.now();
          const script = buildFetchScript({ target, method, headers, body, timeoutMs: this.fetchTimeoutMs });
          return record.win.webContents.executeJavaScript(script, true);
        });

        return send(res, 200, { ok: true, ...result });
      }

      return send(res, 404, { ok: false, error: "not found" });
    } catch (error) {
      this.log(`${req.method} ${url.pathname} 失败: ${error?.message}`, null, "ERROR");
      return send(res, 500, { ok: false, error: String(error?.message ?? error) });
    }
  }
}

/* ------------------------------ 辅助 ------------------------------ */

/**
 * 生成在页面主世界里执行的 fetch 脚本。
 *
 * 必须在主世界执行——secsdk 补丁打的就是主世界的 window.fetch，
 * 隔离世界里拿到的是原始 fetch，签名参数不会被附加。
 */
function buildFetchScript({ target, method, headers, body, timeoutMs }) {
  const bodyExpr = body === undefined || body === null ? "undefined" : JSON.stringify(body);
  return `(async () => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), ${Number(timeoutMs)});
    try {
      const response = await fetch(${JSON.stringify(target)}, {
        method: ${JSON.stringify(method)},
        headers: ${JSON.stringify(headers)},
        body: ${bodyExpr},
        credentials: "include",
        signal: controller.signal
      });
      const text = await response.text();
      const responseHeaders = {};
      response.headers.forEach((v, k) => { responseHeaders[k] = v; });
      return { status: response.status, body: text, headers: responseHeaders };
    } finally {
      clearTimeout(timer);
    }
  })()`;
}

/**
 * 给隐藏窗口设置页面时区。
 *
 * Electron 没有 per-session 的时区 API，只能通过 CDP 的
 * Emulation.setTimezoneOverride。附加调试器有失败的可能（比如已被占用），
 * 所以这里失败只记日志、不中断——时区不对是「不够好」，不是「不能用」。
 */
// 给窗口设置页面时区。
//
// 目的是让浏览器报出的时区和账号归属地一致——账号在美区、代理在美国，
// 页面却报北京时间，是很显眼的指纹差异。
//
// 但这是锦上添花，**绝不能挡住会话创建**：窗口刚建好时页面还没开始加载，
// debugger.sendCommand 可能一直等不到响应（实测会永久挂起）。
// 一旦挂住，整个 /session 请求就被拖死，调用方 5 分钟超时后才报错，
// 而日志上看起来只是「什么都没发生」。所以这里必须带超时。
async function applyTimezone(win, timezoneId, log) {
  const timeoutMs = 5000;
  let timer;
  try {
    win.webContents.debugger.attach("1.3");
    await Promise.race([
      win.webContents.debugger.sendCommand("Emulation.setTimezoneOverride", { timezoneId }),
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(`设置时区超过 ${timeoutMs}ms`)), timeoutMs);
      })
    ]);
  } catch (error) {
    if (log) log(`设置时区 ${timezoneId} 失败（不影响使用）: ${error?.message}`, null, "WARN");
  } finally {
    if (timer) clearTimeout(timer);
  }
}

function normalizeProxy(raw) {
  const value = String(raw ?? "").trim();
  if (!value) return "direct://";
  const withScheme = /^[a-z0-9+.-]+:\/\//i.test(value) ? value : `http://${value}`;
  const url = new URL(withScheme);
  // Electron 的 proxyRules 直接吃 scheme://host:port
  return `${url.protocol}//${url.host}`;
}

async function applyCookies(ses, cookies) {
  for (const cookie of cookies) {
    if (!cookie?.name) continue;
    const domain = String(cookie.domain ?? ".capcut.com").replace(/^\./, "");
    const path = cookie.path || "/";
    try {
      await ses.cookies.set({
        url: `https://${domain}${path}`,
        name: cookie.name,
        value: String(cookie.value ?? ""),
        domain: cookie.domain ?? undefined,
        path,
        secure: true,
        httpOnly: false,
        sameSite: "no_restriction"
      });
    } catch (error) {
      // 单个 cookie 失败不该拖垮整个会话
      console.warn(`[cookies] 写入 ${cookie.name} 失败: ${error?.message}`);
    }
  }
}

function send(res, status, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(status, { "content-type": "application/json; charset=utf-8" });
  res.end(body);
}

async function readJSON(req, limitBytes = 8 * 1024 * 1024) {
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

/**
 * 把一个会话的站点数据彻底清干净。
 *
 * 为什么必须清：站点把地区判定、风控指纹、甚至旧版前端资源都塞在本地存储里。
 * 这些状态一旦陈旧，页面会「看起来正常、但表单提交毫无反应」——
 * 而且在真 Chrome 里同样会犯（用户实测：清掉站点存储再刷新就好了）。
 *
 * 必须显式列出 storages：不传参数时的默认集合**不包含 service worker**，
 * 而 service worker 恰恰可能缓存着那份坏掉的资源，刷新多少次都没用。
 */
async function clearSessionStorage(ses) {
  await ses.clearStorageData({
    storages: [
      "cookies", "localstorage", "indexdb", "websql", "filesystem",
      "cachestorage", "serviceworkers", "shadercache", "appcache"
    ]
  });
  await ses.clearCache();
  await ses.clearHostResolverCache();
  await ses.clearAuthCache();
}

module.exports = { BrowserChannel, SITE, LANDING };
