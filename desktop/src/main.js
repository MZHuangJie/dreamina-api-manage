/**
 * Dreamina 账号管理器 —— Electron 主进程
 * ====================================
 *
 * 进程拓扑：
 *
 *   Electron 主进程
 *     ├─ 浏览器通道（本文件内的 BrowserChannel）
 *     │    用 Electron 自带的 Chromium 开隐藏窗口，在已登录页面上下文里代发写请求
 *     ├─ Go 核心（子进程 manager.exe serve）
 *     │    HTTP API + 聚合网关 + 前端静态资源
 *     └─ 主窗口 → 加载 http://127.0.0.1:<核心端口>
 *
 * 相比原来的「Node sidecar + 系统 Chrome」方案，这里没有额外外部依赖：
 * Electron 本身就是 Node，也自带 Chromium。
 *
 * Go 核心完全不知道外面的壳换了——它仍然按 SIDECAR_URL 调本机 HTTP 接口。
 */
const { app, BrowserWindow, Tray, Menu, shell, dialog, nativeImage } = require("electron");
const { spawn } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");

const { BrowserChannel } = require("./browser-channel.js");
const { reservePort, waitForHTTP } = require("./ports.js");

const PREFERRED_CORE_PORT = 8787;
const BRIDGE_TOKEN = require("node:crypto").randomBytes(16).toString("hex");

let mainWindow = null;
let tray = null;
let channel = null;
let coreProcess = null;
let corePort = 0;
let quitting = false;

/* ------------------------------ 路径 ------------------------------ */

const isDev = !app.isPackaged;

/**
 * 开发模式下可以从前端开发服务器加载，这样改前端有热更新。
 *
 * 不设的话就走核心自带的静态产物——那种方式改一行前端就得重新 build，迭代很慢。
 */
const devServerURL = isDev ? (process.env.MANAGER_DEV_URL ?? "") : "";

function resourcePath(...parts) {
  const base = isDev ? path.join(__dirname, "..", "..") : process.resourcesPath;
  return path.join(base, ...parts);
}

function coreBinary() {
  const name = process.platform === "win32" ? "manager.exe" : "manager";
  return isDev ? path.join(__dirname, "..", "..", "go", name) : path.join(process.resourcesPath, name);
}

function webDist() {
  return isDev ? path.join(__dirname, "..", "..", "dist", "web") : path.join(process.resourcesPath, "web");
}

/* ------------------------------ 数据目录 ------------------------------ */

/**
 * 数据放 userData，不放安装目录——安装目录在 Program Files 下不可写。
 *
 * 首次启动时会尝试从旁边迁移一份已有的 data/（开发目录或便携放置），
 * 这样从命令行版本升级过来的用户不用手动搬账号。
 */
function dataDir() {
  if (process.env.MANAGER_DATA_DIR) return process.env.MANAGER_DATA_DIR;
  return path.join(app.getPath("userData"), "data");
}

function migrateLegacyData(target) {
  if (fs.existsSync(path.join(target, "manager.db"))) return null;

  const candidates = isDev
    ? [path.join(__dirname, "..", "..", "data")]
    : [path.join(path.dirname(app.getPath("exe")), "data"), resourcePath("data")];

  for (const source of candidates) {
    if (!fs.existsSync(path.join(source, "manager.db"))) continue;
    try {
      fs.mkdirSync(target, { recursive: true });
      fs.cpSync(source, target, { recursive: true });
      return source;
    } catch (error) {
      log(`迁移旧数据失败: ${error.message}`, "ERROR");
    }
  }
  return null;
}

/* ------------------------------ 日志 ------------------------------ */

const logFile = () => path.join(app.getPath("userData"), "desktop.log");

function log(message, level = "INFO") {
  const line = `[${new Date().toISOString()}] ${level} ${message}`;
  if (level === "ERROR") console.error(line);
  else console.log(line);
  try {
    fs.appendFileSync(logFile(), line + "\n");
  } catch {
    /* 日志写不了就算了，不能因此崩溃 */
  }
}

/* ------------------------------ 启动流程 ------------------------------ */

async function boot() {
  const dir = dataDir();
  fs.mkdirSync(dir, { recursive: true });
  const migrated = migrateLegacyData(dir);
  if (migrated) log(`已从 ${migrated} 迁移历史数据`);

  // 开发模式下允许把核心端口固定下来。
  //
  // Vite 的代理目标是写死的（默认 8787），而 reservePort 每次挑一个随机端口。
  // 端口对不上的话，界面里的 /api 请求会被代理到别的核心，或者干脆没人接。
  const pinned = isDev ? Number(process.env.MANAGER_CORE_PORT || 0) : 0;
  corePort = pinned > 0 ? pinned : await reservePort(PREFERRED_CORE_PORT);
  const bridgePort = await reservePort(0);
  log(`核心端口 ${corePort}，浏览器通道端口 ${bridgePort}`);

  // —— 浏览器通道 ——
  channel = new BrowserChannel({
    log: (msg, extra, level) => log(extra ? `${msg} ${JSON.stringify(extra)}` : msg, level ?? "INFO"),
    secret: BRIDGE_TOKEN,
    createWindow: createChannelWindow
  });
  await channel.start(bridgePort);

  // —— Go 核心 ——
  const binary = coreBinary();
  if (!fs.existsSync(binary)) {
    throw new Error(`找不到核心程序：${binary}`);
  }
  coreProcess = spawn(binary, ["serve"], {
    cwd: path.dirname(binary),
    env: {
      ...process.env,
      MANAGER_HOST: "127.0.0.1",
      MANAGER_PORT: String(corePort),
      MANAGER_DATA_DIR: dir,
      MANAGER_WEB_DIST: webDist(),
      SIDECAR_URL: `http://127.0.0.1:${bridgePort}`,
      SIDECAR_SECRET: BRIDGE_TOKEN,
      // 桌面端自己管生命周期，不需要额外令牌
      MANAGER_ACCESS_TOKEN: ""
    },
    windowsHide: true,
    stdio: ["ignore", "pipe", "pipe"]
  });

  coreProcess.stdout.on("data", (buf) => process.stdout.write(`[core] ${buf}`));
  coreProcess.stderr.on("data", (buf) => process.stderr.write(`[core] ${buf}`));
  coreProcess.on("exit", (code, signal) => {
    log(`核心进程退出 code=${code} signal=${signal}`, code === 0 || quitting ? "INFO" : "ERROR");
    if (!quitting) {
      dialog.showErrorBox(
        "核心服务已停止",
        `后台服务意外退出（code=${code}）。\n\n日志：${logFile()}`
      );
      app.quit();
    }
  });

  // 打包后没有控制台，核心启动失败必须让用户看见
  await waitForHTTP(`http://127.0.0.1:${corePort}/api/ping`, { timeoutMs: 40_000 }).catch((error) => {
    throw new Error(`核心服务启动超时：${error.message}\n\n日志：${logFile()}`);
  });
  log("核心服务已就绪");
}

/** 给浏览器通道用的隐藏窗口。 */
/**
 * 给浏览器通道用的窗口工厂。
 *
 * 默认隐藏——业务会话不需要用户看见。但人工登录时必须可见，
 * 所以这里要接受 opts.visible：早先这个工厂写死了 show: false 且只收一个参数，
 * 调用方传的 { visible: true } 被静默忽略，登录窗口创建了却从不显示，
 * 表现出来就是「点了登录没反应」。
 */
function createChannelWindow(ses, opts = {}) {
  const visible = opts.visible === true;
  return new BrowserWindow({
    show: visible,
    width: visible ? 1180 : 1440,
    height: visible ? 820 : 900,
    // 登录窗口不要出现在托盘/任务栏之外的地方，给个正常标题
    title: visible ? "登录 Dreamina" : undefined,
    autoHideMenuBar: true,
    webPreferences: {
      session: ses,
      // 隐藏窗口默认会被 Chromium 降频，secsdk 的定时任务可能因此不跑
      backgroundThrottling: false,
      nodeIntegration: false,
      contextIsolation: true,
      sandbox: true,
      webSecurity: true,
      images: true
    }
  });
}

/* ------------------------------ 界面 ------------------------------ */

function loadingPage() {
  const html = `<!doctype html><html><head><meta charset="utf-8"><style>
    html,body{height:100%;margin:0;background:#0d0f14;color:#e6e8ee;
      font-family:"Segoe UI","Microsoft YaHei",system-ui,sans-serif;
      display:flex;align-items:center;justify-content:center}
    .box{text-align:center}
    .ring{width:34px;height:34px;border:3px solid #2a2f3a;border-top-color:#5b8cff;
      border-radius:50%;margin:0 auto 18px;animation:s .9s linear infinite}
    @keyframes s{to{transform:rotate(360deg)}}
    h1{font-size:15px;font-weight:500;margin:0 0 6px}
    p{font-size:12px;color:#8b93a5;margin:0}
  </style></head><body><div class="box">
    <div class="ring"></div>
    <h1>Dreamina 账号管理器</h1>
    <p>正在启动本地服务…</p>
  </div></body></html>`;
  return "data:text/html;charset=utf-8," + encodeURIComponent(html);
}

/**
 * 加载页面，把「被新导航取代」当成正常结果，而不是失败。
 *
 * Electron 的 loadURL() 返回的 Promise 会在导航被中止（-3 / ERR_ABORTED）时拒绝。
 * 窗口创建时先显示启动画面，boot() 完成后又导航到真实地址——第二次导航必然中止第一次，
 * 而那个中止的错误会落到第二次的 Promise 上，被 catch 当成「启动失败」，
 * 于是应用刚跑起来就被 app.quit() 杀掉（日志里能看到前端已经连上、React 已挂载）。
 *
 * 真正加载失败时（比如核心没起来）会拿到别的错误码，照常抛出。
 */
async function loadToleratingAbort(target, url) {
  try {
    await target.loadURL(url);
  } catch (error) {
    if (error && Number(error.code ?? error.errno) === -3) return;
    throw error;
  }
}

function createMainWindow() {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 940,
    minWidth: 1080,
    minHeight: 680,
    show: false,
    backgroundColor: "#0d0f14",
    title: "Dreamina 账号管理器",
    icon: appIcon(),
    autoHideMenuBar: true,
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      sandbox: true
    }
  });

  // 启动画面随后会被真实页面取代，那次导航中止是预期行为。
  mainWindow.loadURL(loadingPage()).catch(() => {});
  mainWindow.once("ready-to-show", () => mainWindow.show());

  // 把渲染进程的 console 转发到主进程日志。
  //
  // 没有这个的话，前端报错时窗口就只是一片空白，
  // 既没有提示也看不到原因——只能靠开 DevTools 手动翻。
  mainWindow.webContents.on("console-message", (_e, level, message, line, sourceId) => {
    const tag = ["VERBOSE", "INFO", "WARNING", "ERROR"][level] ?? "LOG";
    const where = sourceId ? ` (${sourceId.split("/").pop()}:${line})` : "";
    log(`${tag} [界面] ${message}${where}`);
  });

  // 页面加载失败也要说清楚，否则用户只会看到一个空窗口
  mainWindow.webContents.on("did-fail-load", (_e, code, desc, url) => {
    log(`ERROR 界面加载失败 ${code} ${desc} — ${url}`);
  });

  mainWindow.on("closed", () => {
    mainWindow = null;
  });

  // 外链一律用系统浏览器打开，不要在应用窗口里导航走
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:/i.test(url)) void shell.openExternal(url);
    return { action: "deny" };
  });
  mainWindow.webContents.on("will-navigate", (event, url) => {
    if (!url.startsWith(`http://127.0.0.1:${corePort}`)) {
      event.preventDefault();
      if (/^https?:/i.test(url)) void shell.openExternal(url);
    }
  });

  return mainWindow;
}

function appIcon() {
  const candidates = [
    path.join(__dirname, "..", "build", "icon.ico"),
    path.join(__dirname, "..", "build", "icon.png")
  ];
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) {
      const image = nativeImage.createFromPath(candidate);
      if (!image.isEmpty()) return image;
    }
  }
  return undefined;
}

function createTray() {
  const icon = appIcon();
  if (!icon) return;
  try {
    tray = new Tray(icon.resize({ width: 16, height: 16 }));
  } catch (error) {
    log(`托盘创建失败: ${error.message}`, "ERROR");
    return;
  }
  tray.setToolTip("Dreamina 账号管理器");
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: "显示主界面", click: () => showMainWindow() },
      { label: "打开数据目录", click: () => void shell.openPath(dataDir()) },
      { label: "打开日志", click: () => void shell.openPath(logFile()) },
      { type: "separator" },
      {
        label: "退出",
        click: () => {
          quitting = true;
          app.quit();
        }
      }
    ])
  );
  tray.on("double-click", () => showMainWindow());
}

function showMainWindow() {
  if (!mainWindow) {
    createMainWindow();
    mainWindow.loadURL(`http://127.0.0.1:${corePort}`).catch(() => {});
    return;
  }
  if (mainWindow.isMinimized()) mainWindow.restore();
  mainWindow.show();
  mainWindow.focus();
}

/* ------------------------------ 单实例 ------------------------------ */

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", () => showMainWindow());

  // Chromium 的时区跟着 TZ 走。账号在美区、代理在美国，
  // 浏览器却报 Asia/Shanghai 会是不必要的指纹差异。
  if (!process.env.TZ) process.env.TZ = "America/Los_Angeles";

  app.whenReady().then(async () => {
    createMainWindow();
    createTray();

    try {
      await boot();
      // 开发模式优先加载 Vite 拿热更新，否则用核心自带的产物
      await loadToleratingAbort(mainWindow, devServerURL || `http://127.0.0.1:${corePort}`);
      mainWindow.setTitle("Dreamina 账号管理器");
    } catch (error) {
      log(`启动失败: ${error.message}`, "ERROR");
      dialog.showErrorBox("启动失败", String(error.message ?? error));
      quitting = true;
      app.quit();
    }
  });

  app.on("window-all-closed", () => {
    // 有托盘就留在后台，没有就退出——否则用户会找不到进程
    if (!tray) app.quit();
  });

  app.on("activate", () => showMainWindow());

  app.on("before-quit", async (event) => {
    if (quitting && !coreProcess && !channel) return;
    quitting = true;
    event.preventDefault();
    try {
      if (channel) await channel.stop();
      if (coreProcess && !coreProcess.killed) {
        coreProcess.kill();
        // 给核心一点时间落盘，再强杀
        await new Promise((resolve) => setTimeout(resolve, 800));
        if (!coreProcess.killed) coreProcess.kill("SIGKILL");
      }
    } catch (error) {
      log(`退出清理出错: ${error.message}`, "ERROR");
    } finally {
      app.exit(0);
    }
  });
}
