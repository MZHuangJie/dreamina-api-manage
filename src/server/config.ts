import fs from "node:fs";
import path from "node:path";

/** 项目根目录（src/server/config.ts -> ../../） */
export const ROOT_DIR = path.resolve(import.meta.dirname, "..", "..");

function envInt(name: string, fallback: number): number {
  const raw = process.env[name];
  if (!raw) return fallback;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function envBool(name: string, fallback: boolean): boolean {
  const raw = process.env[name];
  if (!raw) return fallback;
  return ["1", "true", "yes", "on"].includes(raw.trim().toLowerCase());
}

export const DATA_DIR = process.env.MANAGER_DATA_DIR
  ? path.resolve(process.env.MANAGER_DATA_DIR)
  : path.join(ROOT_DIR, "data");

export const config = {
  rootDir: ROOT_DIR,
  dataDir: DATA_DIR,
  dbPath: path.join(DATA_DIR, "manager.db"),
  keyPath: path.join(DATA_DIR, "secret.key"),
  webDistDir: path.join(ROOT_DIR, "dist", "web"),

  host: process.env.MANAGER_HOST ?? "127.0.0.1",
  port: envInt("MANAGER_PORT", 8787),

  /**
   * 服务端只监听本机。若显式开放到局域网，需要设置访问令牌，
   * 否则任何能连上端口的人都能读取全部账号凭据。
   */
  accessToken: process.env.MANAGER_ACCESS_TOKEN ?? "",

  /** 每账号 HTTP 请求超时 */
  requestTimeoutMs: envInt("MANAGER_REQUEST_TIMEOUT_MS", 30_000),

  /** 保活调度器 */
  health: {
    enabled: envBool("MANAGER_HEALTH_ENABLED", true),
    /** 全量探活间隔 */
    intervalMs: envInt("MANAGER_HEALTH_INTERVAL_MS", 30 * 60_000),
    /** 启动后多久开始第一轮 */
    startupDelayMs: envInt("MANAGER_HEALTH_STARTUP_DELAY_MS", 5_000),
    /** 单轮并发数，避免同时打爆即梦风控 */
    concurrency: envInt("MANAGER_HEALTH_CONCURRENCY", 3),
    /** 探活失败后的冷却时间 */
    failureCooldownMs: envInt("MANAGER_FAILURE_COOLDOWN_MS", 60_000)
  },

  /** 即梦客户端常量 */
  jimeng: {
    origin: "https://jimeng.jianying.com",
    appid: "513695",
    /** Appvr，即梦网页端版本号；接口会校验签名中的这一项 */
    versionCode: "5.8.0",
    platformCode: "7",
    userAgent:
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"
  }
} as const;

export function ensureDataDir(): void {
  fs.mkdirSync(config.dataDir, { recursive: true });
}

export const isLoopbackHost = (host: string): boolean =>
  ["127.0.0.1", "localhost", "::1", "0.0.0.0"].includes(host);
