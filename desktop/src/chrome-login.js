// 用本机真实 Chrome 完成登录。
//
// 为什么不复用 Electron 内嵌的浏览器：那是个被改造过的 Chromium，站点对它的
// 处理和真 Chrome 不一致——实测表现为表单能填、点提交却没有任何网络请求。
// 而同一个登录流程在用户自己的无痕窗口里完全正常，所以直接借真 Chrome 用。
//
// 与 Chrome 的通信走 --remote-debugging-pipe：一条管道，null 分隔的 JSON。
// 不用 --remote-debugging-port + WebSocket，是因为 Electron 33 内置的 Node 20
// 没有全局 WebSocket，走端口还得自己实现握手，反而更麻烦。

const { spawn } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const NUL = Buffer.from([0]);

/** 找一个可用的 Chrome。 */
function findChrome() {
  if (process.env.MANAGER_CHROME_PATH) return process.env.MANAGER_CHROME_PATH;
  const candidates = [
    path.join(process.env.ProgramFiles || "C:\\Program Files", "Google/Chrome/Application/chrome.exe"),
    path.join(process.env["ProgramFiles(x86)"] || "C:\\Program Files (x86)", "Google/Chrome/Application/chrome.exe"),
    path.join(process.env.LOCALAPPDATA || "", "Google/Chrome/Application/chrome.exe"),
    path.join(process.env.ProgramFiles || "", "Microsoft/Edge/Application/msedge.exe"),
    path.join(process.env["ProgramFiles(x86)"] || "", "Microsoft/Edge/Application/msedge.exe")
  ];
  return candidates.find((p) => p && fs.existsSync(p)) || "";
}

class ChromeLoginSession {
  constructor(accountId, { url, proxy, log }) {
    this.accountId = accountId;
    this.url = url;
    this.proxy = proxy;
    this.log = typeof log === "function" ? log : () => {};
    this.proc = null;
    this.nextId = 1;
    this.pending = new Map();
    this.buffer = Buffer.alloc(0);
    this.closed = false;
    this.profileDir = "";
  }

  /** 启动 Chrome 并连上调试管道。 */
  async start() {
    const chrome = findChrome();
    if (!chrome) throw new Error("找不到 Chrome 或 Edge，请设置 MANAGER_CHROME_PATH");

    // 独立配置目录，等价于无痕：不碰用户平时的浏览数据，
    // 也不会把上一次失败留下的状态带进来。
    this.profileDir = fs.mkdtempSync(path.join(os.tmpdir(), "dm-login-"));

    const args = [
      `--user-data-dir=${this.profileDir}`,
      "--remote-debugging-pipe",
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-features=Translate,OptimizationHints",
      "--lang=en-US"
    ];
    if (this.proxy) args.push(`--proxy-server=${this.proxy}`);
    args.push(this.url);

    this.proc = spawn(chrome, args, {
      // fd3 = 我们写、Chrome 读；fd4 = Chrome 写、我们读
      // stdout/stderr 必须是 pipe。
      //
      // 设成 "ignore" 时 Chrome 会**直接退出**（实测如此），原因不明；
      // 用 pipe 就正常。fd3/fd4 才是调试管道，和这两个无关。
      stdio: ["ignore", "pipe", "pipe", "pipe", "pipe"],
      windowsHide: false
    });

    this.proc.on("exit", () => {
      this.closed = true;
      for (const { reject } of this.pending.values()) reject(new Error("Chrome 已关闭"));
      this.pending.clear();
    });

    // 必须把 stdout/stderr 读掉：管道写满后 Chrome 会阻塞。
    this.proc.stdout.on("data", () => {});
    this.proc.stderr.on("data", (d) => {
      const text = d.toString().trim();
      // 只挑可能有用的行，否则噪音太多
      if (/error|fail|denied/i.test(text)) this.log(`[chrome] ${text.slice(0, 200)}`);
    });

    this.proc.stdio[4].on("data", (chunk) => this.onData(chunk));
    this.proc.stdio[4].on("error", () => {});

    // 等管道能用（Chrome 起来需要一点时间）
    // 探到能应答为止，比固定 sleep 可靠
    const deadline = Date.now() + 15_000;
    for (;;) {
      try {
        await this.send("Browser.getVersion");
        break;
      } catch (error) {
        if (Date.now() > deadline) throw new Error(`Chrome 调试管道未就绪: ${error.message}`);
        await new Promise((r) => setTimeout(r, 400));
      }
    }
    this.log(`已用 ${path.basename(chrome)} 打开登录窗口（独立配置，等价无痕）`);
  }

  /** 处理管道上的数据：null 分隔的 JSON。 */
  onData(chunk) {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    for (;;) {
      const idx = this.buffer.indexOf(0);
      if (idx < 0) break;
      const raw = this.buffer.subarray(0, idx).toString("utf8");
      this.buffer = this.buffer.subarray(idx + 1);
      if (!raw.trim()) continue;
      let msg;
      try {
        msg = JSON.parse(raw);
      } catch {
        continue;
      }
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        if (msg.error) reject(new Error(msg.error.message || "CDP 错误"));
        else resolve(msg.result);
      }
    }
  }

  /** 发一条 CDP 命令。 */
  send(method, params = {}) {
    if (this.closed || !this.proc) return Promise.reject(new Error("Chrome 未运行"));
    const id = this.nextId++;
    const payload = Buffer.from(JSON.stringify({ id, method, params }), "utf8");
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.proc.stdio[3].write(Buffer.concat([payload, NUL]));
      setTimeout(() => {
        if (this.pending.has(id)) {
          this.pending.delete(id);
          reject(new Error(`CDP 超时: ${method}`));
        }
      }, 10_000);
    });
  }

  /** 取当前所有 cookie。登录成功与否就看有没有 sessionid。 */
  async cookies() {
    const result = await this.send("Storage.getCookies");
    return (result?.cookies ?? []).map((c) => ({
      name: c.name,
      value: c.value,
      domain: c.domain,
      path: c.path,
      secure: c.secure,
      httpOnly: c.httpOnly,
      session: c.session,
      expires: c.expires
    }));
  }

  /** 关掉 Chrome 并清理临时配置目录。 */
  async close() {
    this.closed = true;
    try {
      await this.send("Browser.close");
    } catch {
      /* 可能已经退了 */
    }
    try {
      if (this.proc && !this.proc.killed) this.proc.kill();
    } catch {
      /* ignore */
    }
    if (this.profileDir) {
      setTimeout(() => {
        try {
          fs.rmSync(this.profileDir, { recursive: true, force: true });
        } catch {
          /* 文件可能还被占用，留着也无妨 */
        }
      }, 3000);
    }
  }
}

module.exports = { ChromeLoginSession, findChrome };
