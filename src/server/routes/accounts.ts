import { Hono } from "hono";
import { z } from "zod";

import {
  AccountError,
  bulkImport,
  createAccount,
  deleteAccount,
  getAccountCredential,
  getOverview,
  getPublicAccount,
  listAccounts,
  resetHealth,
  selectAccount,
  setAccountEnabled,
  setActiveAccount,
  updateAccount
} from "../accounts/pool.ts";
import { getProbeRunState, probeAccountById, probeAllAccounts } from "../accounts/health.ts";
import { getSetting, db, queryAll, logEvent } from "../db.ts";
import { receiveDailyCredit } from "../jimeng/account.ts";
import { fail, ok, readJson } from "../http.ts";

const proxySchema = z.string().trim().max(500).optional();
const tagsSchema = z.array(z.string().trim().min(1).max(32)).max(20).optional();

const createSchema = z
  .object({
    name: z.string().trim().max(80).optional(),
    remark: z.string().trim().max(500).optional(),
    tags: tagsSchema,
    cookie: z.string().trim().min(1).max(8000).optional(),
    sessionId: z.string().trim().min(1).max(8000).optional(),
    enabled: z.boolean().optional(),
    proxyUrl: proxySchema,
    proxyEnabled: z.boolean().optional()
  })
  .refine((value) => Boolean(value.cookie || value.sessionId), {
    message: "必须提供 sessionId 或 cookie"
  });

const updateSchema = z.object({
  name: z.string().trim().max(80).optional(),
  remark: z.string().trim().max(500).optional(),
  tags: tagsSchema,
  cookie: z.string().trim().min(1).max(8000).optional(),
  sessionId: z.string().trim().min(1).max(8000).optional(),
  enabled: z.boolean().optional(),
  proxyUrl: proxySchema,
  proxyEnabled: z.boolean().optional()
});

const bulkSchema = z.object({ text: z.string().min(1).max(500_000) });

const listQuerySchema = z.object({
  keyword: z.string().trim().optional(),
  health: z.enum(["unknown", "healthy", "expired", "error"]).optional(),
  enabled: z.enum(["true", "false"]).optional(),
  tag: z.string().trim().optional()
});

export const accountRoutes = new Hono();

/** 账号列表 */
accountRoutes.get("/", (c) => {
  const parsed = listQuerySchema.safeParse(c.req.query());
  const query = parsed.success ? parsed.data : {};
  return ok(
    c,
    listAccounts({
      keyword: query.keyword,
      health: query.health,
      tag: query.tag,
      enabled: query.enabled === undefined ? undefined : query.enabled === "true"
    })
  );
});

/** 批量导入（放在 /:id 之前，避免被当成 id 匹配） */
accountRoutes.post("/bulk", async (c) => {
  try {
    const { text } = await readJson(c, bulkSchema);
    const result = bulkImport(text);
    if (result.created.length) {
      logEvent({
        kind: "account.bulk_import",
        message: `批量导入成功 ${result.created.length} 个账号，失败 ${result.failed.length} 个`
      });
    }
    return ok(c, {
      created: result.created.length,
      failed: result.failed.length,
      accounts: result.created,
      errors: result.failed
    });
  } catch (error) {
    return fail(c, error);
  }
});

/** 导出账号（含明文凭据，仅用于本地备份） */
accountRoutes.get("/export", (c) => {
  const includeCredentials = c.req.query("credentials") === "1";
  const accounts = listAccounts();
  if (!includeCredentials) {
    return ok(c, { accounts });
  }
  const payload = accounts.map((account) => {
    const cred = getAccountCredential(account.id);
    return {
      name: account.name,
      remark: account.remark,
      tags: account.tags,
      credentialKind: cred.credentialKind,
      credential: cred.credential,
      proxyUrl: cred.proxyUrl ?? ""
    };
  });
  logEvent({ level: "warn", kind: "account.export", message: "导出含明文凭据的账号备份" });
  return c.json({ ok: true, data: { exportedAt: new Date().toISOString(), accounts: payload } });
});

/** 新建账号 */
accountRoutes.post("/", async (c) => {
  try {
    const input = await readJson(c, createSchema);
    const account = createAccount(input);
    // 新建后立刻探活一次，用户马上就能看到登录态与积分
    const probed = await probeAccountById(account.id);
    return ok(c, probed, 201);
  } catch (error) {
    return fail(c, error);
  }
});

/** 账号详情 */
accountRoutes.get("/:id", (c) => {
  try {
    return ok(c, getPublicAccount(c.req.param("id")));
  } catch (error) {
    return fail(c, error);
  }
});

accountRoutes.patch("/:id", async (c) => {
  try {
    const input = await readJson(c, updateSchema);
    return ok(c, updateAccount(c.req.param("id"), input));
  } catch (error) {
    return fail(c, error);
  }
});

accountRoutes.delete("/:id", (c) => {
  try {
    deleteAccount(c.req.param("id"));
    return ok(c, { deleted: true });
  } catch (error) {
    return fail(c, error);
  }
});

/** 启用 / 停用 */
accountRoutes.post("/:id/enabled", async (c) => {
  try {
    const { enabled } = await readJson(c, z.object({ enabled: z.boolean() }));
    return ok(c, setAccountEnabled(c.req.param("id"), enabled));
  } catch (error) {
    return fail(c, error);
  }
});

/** 一键切换当前账号 */
accountRoutes.post("/:id/activate", (c) => {
  try {
    return ok(c, setActiveAccount(c.req.param("id")));
  } catch (error) {
    return fail(c, error);
  }
});

/** 单个账号探活 */
accountRoutes.post("/:id/probe", async (c) => {
  try {
    return ok(c, await probeAccountById(c.req.param("id")));
  } catch (error) {
    return fail(c, error);
  }
});

/** 领取每日赠送积分 */
accountRoutes.post("/:id/receive-credit", async (c) => {
  try {
    const cred = getAccountCredential(c.req.param("id"));
    const result = await receiveDailyCredit({
      accountId: cred.id,
      credential: cred.credential,
      credentialKind: cred.credentialKind,
      proxyUrl: cred.proxyUrl
    });
    logEvent({
      accountId: cred.id,
      kind: "account.receive_credit",
      message: `领取每日积分 ${result.received ?? "?"}，当前总额 ${result.totalCredit ?? "?"}`
    });
    const updated = await probeAccountById(cred.id);
    return ok(c, { ...result, account: updated });
  } catch (error) {
    return fail(c, error);
  }
});

/** 清除失效标记，重新参与调度 */
accountRoutes.post("/:id/reset-health", (c) => {
  try {
    return ok(c, resetHealth(c.req.param("id")));
  } catch (error) {
    return fail(c, error);
  }
});

/** 某账号的历史事件 */
accountRoutes.get("/:id/events", (c) => {
  const limit = Math.min(Number(c.req.query("limit") ?? 50) || 50, 500);
  const rows = queryAll<{
    id: number;
    level: string;
    kind: string;
    message: string;
    detail: string | null;
    created_at: string;
  }>(
    "SELECT id, level, kind, message, detail, created_at FROM events WHERE account_id = ? ORDER BY id DESC LIMIT ?",
    c.req.param("id"),
    limit
  );
  return ok(c, rows);
});

export const poolRoutes = new Hono();

/** 账号池总览 */
poolRoutes.get("/overview", (c) =>
  ok(c, {
    ...getOverview(),
    probe: getProbeRunState(),
    strategy: getSetting("selection.strategy") ?? "least_failures",
    activeAccount: (() => {
      const active = listAccounts().find((a) => a.isActive);
      return active ?? null;
    })()
  })
);

/** 全量探活 */
poolRoutes.post("/probe-all", async (c) => {
  try {
    const body = await c.req.json().catch(() => ({}));
    const parsed = z
      .object({ ids: z.array(z.string()).optional(), concurrency: z.number().int().min(1).max(10).optional() })
      .safeParse(body ?? {});
    const options = parsed.success ? parsed.data : {};
    const state = await probeAllAccounts(options);
    return ok(c, state);
  } catch (error) {
    return fail(c, error);
  }
});

poolRoutes.get("/probe-status", (c) => ok(c, getProbeRunState()));

/** 试算：按当前策略会选中哪个账号 */
poolRoutes.get("/pick", (c) => {
  try {
    const strategy = (c.req.query("strategy") ?? getSetting("selection.strategy") ?? "least_failures") as
      | "active"
      | "least_failures"
      | "round_robin"
      | "most_credit";
    const cred = selectAccount({ strategy });
    return ok(c, cred.publicAccount);
  } catch (error) {
    return fail(c, error);
  }
});

/** 设置选取策略 */
poolRoutes.put("/strategy", async (c) => {
  try {
    const { strategy } = await readJson(
      c,
      z.object({ strategy: z.enum(["active", "least_failures", "round_robin", "most_credit"]) })
    );
    db.prepare(
      "INSERT INTO settings (key, value) VALUES ('selection.strategy', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value"
    ).run(strategy);
    return ok(c, { strategy });
  } catch (error) {
    return fail(c, error);
  }
});

/** 全局事件流 */
poolRoutes.get("/events", (c) => {
  const limit = Math.min(Number(c.req.query("limit") ?? 80) || 80, 500);
  const accountId = c.req.query("accountId");
  const rows = accountId
    ? queryAll(
        "SELECT * FROM events WHERE account_id = ? ORDER BY id DESC LIMIT ?",
        accountId,
        limit
      )
    : queryAll("SELECT * FROM events ORDER BY id DESC LIMIT ?", limit);
  return ok(c, rows);
});

export { AccountError };
