import { config } from "../config.ts";
import { logEvent } from "../db.ts";
import { probeAccount } from "../jimeng/account.ts";
import {
  applyProbeError,
  applyProbeResult,
  getAccountCredential,
  listAccounts
} from "./pool.ts";
import type { PublicAccount } from "./pool.ts";

export interface ProbeRunState {
  running: boolean;
  startedAt: string | null;
  finishedAt: string | null;
  total: number;
  completed: number;
  healthy: number;
  failed: number;
  lastError: string | null;
}

const runState: ProbeRunState = {
  running: false,
  startedAt: null,
  finishedAt: null,
  total: 0,
  completed: 0,
  healthy: 0,
  failed: 0,
  lastError: null
};

export function getProbeRunState(): ProbeRunState {
  return { ...runState };
}

/** 探活单个账号：成功/失败都会把结果写回数据库 */
export async function probeAccountById(id: string): Promise<PublicAccount> {
  const cred = getAccountCredential(id);
  try {
    const result = await probeAccount({
      accountId: cred.id,
      credential: cred.credential,
      credentialKind: cred.credentialKind,
      proxyUrl: cred.proxyUrl
    });
    if (!result.userId) {
      // /passport/account/info/v2 没返回 user_id 基本等同于未登录
      throw new Error("即梦未返回用户信息，登录态可能已失效");
    }
    return applyProbeResult(id, result);
  } catch (error) {
    return applyProbeError(id, error);
  }
}

/** 有并发上限的批量探活，避免同一时间打太多请求触发风控 */
async function mapWithConcurrency<T, R>(
  items: T[],
  limit: number,
  worker: (item: T) => Promise<R>
): Promise<R[]> {
  const results = new Array<R>(items.length);
  let cursor = 0;

  const runners = Array.from({ length: Math.min(Math.max(1, limit), items.length) }, async () => {
    while (true) {
      const index = cursor++;
      if (index >= items.length) return;
      results[index] = await worker(items[index] as T);
    }
  });

  await Promise.all(runners);
  return results;
}

export interface ProbeAllOptions {
  /** 只探活这些账号；缺省则探活全部启用的账号 */
  ids?: string[];
  concurrency?: number;
}

export async function probeAllAccounts(options: ProbeAllOptions = {}): Promise<ProbeRunState> {
  if (runState.running) return getProbeRunState();

  const targets = options.ids?.length
    ? listAccounts().filter((account) => (options.ids as string[]).includes(account.id))
    : listAccounts().filter((account) => account.enabled);

  runState.running = true;
  runState.startedAt = new Date().toISOString();
  runState.finishedAt = null;
  runState.total = targets.length;
  runState.completed = 0;
  runState.healthy = 0;
  runState.failed = 0;
  runState.lastError = null;

  try {
    await mapWithConcurrency(targets, options.concurrency ?? config.health.concurrency, async (account) => {
      const updated = await probeAccountById(account.id);
      runState.completed += 1;
      if (updated.health === "healthy") runState.healthy += 1;
      else runState.failed += 1;
      return updated;
    });
  } catch (error) {
    runState.lastError = error instanceof Error ? error.message : String(error);
  } finally {
    runState.running = false;
    runState.finishedAt = new Date().toISOString();
    logEvent({
      kind: "health.run",
      message: `保活巡检完成：${runState.healthy} 正常 / ${runState.failed} 异常（共 ${runState.total}）`
    });
  }

  return getProbeRunState();
}

/* ------------------------------------------------------------------ */
/* 定时调度                                                            */
/* ------------------------------------------------------------------ */

let timer: NodeJS.Timeout | null = null;

export function startHealthScheduler(): void {
  if (!config.health.enabled || timer) return;

  timer = setTimeout(function tick() {
    void probeAllAccounts()
      .catch(() => {})
      .finally(() => {
        timer = setTimeout(tick, config.health.intervalMs);
      });
  }, config.health.startupDelayMs);

  // 调度器不应该阻止进程退出
  timer.unref?.();
  logEvent({
    kind: "health.scheduler",
    message: `保活调度器已启动，间隔 ${Math.round(config.health.intervalMs / 60000)} 分钟`
  });
}

export function stopHealthScheduler(): void {
  if (timer) clearTimeout(timer);
  timer = null;
}
