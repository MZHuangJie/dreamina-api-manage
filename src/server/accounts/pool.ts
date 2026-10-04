import { config } from "../config.ts";
import {
  boolToSql,
  db,
  logEvent,
  nowIso,
  nz,
  sqlToBool
} from "../db.ts";
import {
  decryptSecret,
  encryptSecret,
  extractSessionId,
  hashSecret,
  newId,
  previewSecret
} from "../crypto.ts";
import { JimengAuthError, JimengError } from "../jimeng/errors.ts";
import type { AccountProbeResult, MembershipInfo } from "../jimeng/account.ts";

export type CredentialKind = "sessionid" | "cookie";
export type HealthStatus = "unknown" | "healthy" | "expired" | "error";
export type SelectionStrategy = "active" | "least_failures" | "round_robin" | "most_credit";

export interface PublicAccount {
  id: string;
  name: string;
  remark: string;
  tags: string[];

  credentialKind: CredentialKind;
  credentialFingerprint: string;

  enabled: boolean;
  isActive: boolean;

  proxyEnabled: boolean;
  proxyUrlMasked: string;

  health: HealthStatus;
  userId: string | null;
  nickname: string | null;
  avatarUrl: string | null;

  freeCredit: number | null;
  purchaseCredit: number | null;
  vipCredit: number | null;
  totalCredit: number | null;
  membership: MembershipInfo;
  statusCheckedAt: string | null;
  statusError: string | null;

  successCount: number;
  failureCount: number;
  cooldownUntil: string | null;
  inCooldown: boolean;
  lastUsedAt: string | null;
  lastSuccessAt: string | null;
  lastFailureAt: string | null;

  createdAt: string;
  updatedAt: string;
}

/** 仅供服务端内部使用，绝不可直接返回给 HTTP 层 */
export interface AccountCredential {
  id: string;
  name: string;
  enabled: boolean;
  credential: string;
  credentialKind: CredentialKind;
  proxyUrl: string | null;
  publicAccount: PublicAccount;
}

export interface CreateAccountInput {
  name?: string;
  remark?: string;
  tags?: string[];
  cookie?: string;
  sessionId?: string;
  enabled?: boolean;
  proxyUrl?: string;
  proxyEnabled?: boolean;
}

export interface UpdateAccountInput {
  name?: string;
  remark?: string;
  tags?: string[];
  cookie?: string;
  sessionId?: string;
  enabled?: boolean;
  proxyUrl?: string;
  proxyEnabled?: boolean;
}

interface AccountRow {
  id: string;
  name: string;
  remark: string;
  tags: string;
  credential_kind: CredentialKind;
  credential_hash: string;
  credential_ciphertext: string;
  enabled: number;
  is_active: number;
  proxy_url: string;
  proxy_enabled: number;
  health: HealthStatus;
  user_id: string | null;
  nickname: string | null;
  avatar_url: string | null;
  free_credit: number | null;
  purchase_credit: number | null;
  vip_credit: number | null;
  total_credit: number | null;
  membership: string | null;
  status_checked_at: string | null;
  status_error: string | null;
  success_count: number;
  failure_count: number;
  cooldown_until: string | null;
  last_used_at: string | null;
  last_success_at: string | null;
  last_failure_at: string | null;
  created_at: string;
  updated_at: string;
}

export class AccountError extends Error {
  readonly status: number;
  constructor(message: string, status = 400) {
    super(message);
    this.name = "AccountError";
    this.status = status;
  }
}

/* ------------------------------------------------------------------ */
/* 工具                                                                */
/* ------------------------------------------------------------------ */

export function maskProxyUrl(url: string): string {
  if (!url) return "";
  try {
    const parsed = new URL(url);
    if (parsed.password) parsed.password = "***";
    if (parsed.username) parsed.username = parsed.username;
    return parsed.toString();
  } catch {
    return url.replace(/\/\/[^@/]*@/, "//***@");
  }
}

export function normalizeProxyUrl(raw: string | undefined | null): string {
  const value = (raw ?? "").trim();
  if (!value) return "";
  const withScheme = /^[a-z0-9+.-]+:\/\//i.test(value) ? value : `http://${value}`;
  let parsed: URL;
  try {
    parsed = new URL(withScheme);
  } catch {
    throw new AccountError(`代理地址格式不正确：${raw}`);
  }
  if (!["http:", "https:", "socks:", "socks4:", "socks5:"].includes(parsed.protocol)) {
    throw new AccountError(`不支持的代理协议：${parsed.protocol}，请使用 http/https/socks5`);
  }
  if (!parsed.hostname || !parsed.port) {
    throw new AccountError("代理地址必须包含主机与端口，例如 http://127.0.0.1:7890");
  }
  return parsed.toString();
}

function parseTags(raw: string): string[] {
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((t): t is string => typeof t === "string") : [];
  } catch {
    return [];
  }
}

function parseMembership(raw: string | null): MembershipInfo {
  if (!raw) return { isVip: null, vipExpireAt: null, membershipType: null };
  try {
    const parsed = JSON.parse(raw) as MembershipInfo;
    return {
      isVip: parsed.isVip ?? null,
      vipExpireAt: parsed.vipExpireAt ?? null,
      membershipType: parsed.membershipType ?? null
    };
  } catch {
    return { isVip: null, vipExpireAt: null, membershipType: null };
  }
}

function toPublic(row: AccountRow): PublicAccount {
  const cooldownUntil = row.cooldown_until;
  return {
    id: row.id,
    name: row.name,
    remark: row.remark,
    tags: parseTags(row.tags),
    credentialKind: row.credential_kind,
    credentialFingerprint: row.credential_hash.slice(0, 8),
    enabled: sqlToBool(row.enabled),
    isActive: sqlToBool(row.is_active),
    proxyEnabled: sqlToBool(row.proxy_enabled) && Boolean(row.proxy_url),
    proxyUrlMasked: maskProxyUrl(row.proxy_url),
    health: row.health,
    userId: row.user_id,
    nickname: row.nickname,
    avatarUrl: row.avatar_url,
    freeCredit: row.free_credit,
    purchaseCredit: row.purchase_credit,
    vipCredit: row.vip_credit,
    totalCredit: row.total_credit,
    membership: parseMembership(row.membership),
    statusCheckedAt: row.status_checked_at,
    statusError: row.status_error,
    successCount: row.success_count,
    failureCount: row.failure_count,
    cooldownUntil,
    inCooldown: Boolean(cooldownUntil && cooldownUntil > nowIso()),
    lastUsedAt: row.last_used_at,
    lastSuccessAt: row.last_success_at,
    lastFailureAt: row.last_failure_at,
    createdAt: row.created_at,
    updatedAt: row.updated_at
  };
}

function getRow(id: string): AccountRow {
  const row = db.prepare("SELECT * FROM accounts WHERE id = ?").get(id) as AccountRow | undefined;
  if (!row) throw new AccountError(`账号不存在：${id}`, 404);
  return row;
}

function resolveCredentialInput(
  input: CreateAccountInput | UpdateAccountInput
): { value: string; kind: CredentialKind } | null {
  const cookie = input.cookie?.trim();
  const sessionId = input.sessionId?.trim();
  if (cookie && sessionId) throw new AccountError("cookie 与 sessionId 只能提供一个");
  if (cookie) {
    extractSessionId(cookie); // 提前校验，避免存入无法解析的凭据
    return { value: cookie, kind: "cookie" };
  }
  if (sessionId) {
    extractSessionId(sessionId);
    return { value: sessionId, kind: "sessionid" };
  }
  return null;
}

/* ------------------------------------------------------------------ */
/* 查询                                                                */
/* ------------------------------------------------------------------ */

export interface ListAccountsOptions {
  keyword?: string;
  health?: HealthStatus;
  enabled?: boolean;
  tag?: string;
}

export function listAccounts(options: ListAccountsOptions = {}): PublicAccount[] {
  const clauses: string[] = [];
  const params: (string | number)[] = [];

  if (options.keyword?.trim()) {
    clauses.push("(name LIKE ? OR remark LIKE ? OR nickname LIKE ? OR user_id LIKE ?)");
    const like = `%${options.keyword.trim()}%`;
    params.push(like, like, like, like);
  }
  if (options.health) {
    clauses.push("health = ?");
    params.push(options.health);
  }
  if (options.enabled !== undefined) {
    clauses.push("enabled = ?");
    params.push(boolToSql(options.enabled));
  }

  const where = clauses.length ? `WHERE ${clauses.join(" AND ")}` : "";
  const rows = db
    .prepare(`SELECT * FROM accounts ${where} ORDER BY is_active DESC, created_at ASC`)
    .all(...params) as unknown as AccountRow[];

  const accounts = rows.map(toPublic);
  return options.tag ? accounts.filter((a) => a.tags.includes(options.tag as string)) : accounts;
}

export function getPublicAccount(id: string): PublicAccount {
  return toPublic(getRow(id));
}

/** 内部使用：取出可直接发起请求的凭据 */
export function getAccountCredential(id: string): AccountCredential {
  const row = getRow(id);
  const credential = decryptSecret(row.credential_ciphertext);
  return {
    id: row.id,
    name: row.name,
    enabled: sqlToBool(row.enabled),
    credential,
    credentialKind: row.credential_kind,
    proxyUrl: sqlToBool(row.proxy_enabled) && row.proxy_url ? row.proxy_url : null,
    publicAccount: toPublic(row)
  };
}

export function getActiveAccount(): PublicAccount | null {
  const row = db.prepare("SELECT * FROM accounts WHERE is_active = 1 LIMIT 1").get() as
    | AccountRow
    | undefined;
  return row ? toPublic(row) : null;
}

/* ------------------------------------------------------------------ */
/* 写入                                                                */
/* ------------------------------------------------------------------ */

export function createAccount(input: CreateAccountInput): PublicAccount {
  const credential = resolveCredentialInput(input);
  if (!credential) throw new AccountError("必须提供即梦的 sessionId 或完整 Cookie");

  const sessionId = extractSessionId(credential.value);
  const name = input.name?.trim() || `即梦账号 ${sessionId.slice(0, 6)}`;
  const proxyUrl = normalizeProxyUrl(input.proxyUrl);
  const timestamp = nowIso();
  const id = newId();

  const total = db.prepare("SELECT COUNT(*) AS count FROM accounts").get() as { count: number };
  const shouldActivate = total.count === 0;

  try {
    db.prepare(
      `INSERT INTO accounts (
         id, name, remark, tags, credential_kind, credential_hash, credential_ciphertext,
         enabled, is_active, proxy_url, proxy_enabled, created_at, updated_at
       ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
    ).run(
      id,
      name,
      input.remark?.trim() ?? "",
      JSON.stringify(input.tags ?? []),
      credential.kind,
      hashSecret(credential.value),
      encryptSecret(credential.value),
      boolToSql(input.enabled !== false),
      boolToSql(shouldActivate),
      proxyUrl,
      boolToSql(Boolean(proxyUrl) && input.proxyEnabled !== false),
      timestamp,
      timestamp
    );
  } catch (error) {
    if (String((error as Error).message).includes("UNIQUE")) {
      throw new AccountError("该账号凭据已存在，请勿重复添加", 409);
    }
    throw error;
  }

  logEvent({ accountId: id, kind: "account.create", message: `新增账号「${name}」` });
  return getPublicAccount(id);
}

export function updateAccount(id: string, input: UpdateAccountInput): PublicAccount {
  const row = getRow(id);
  const credential = resolveCredentialInput(input);
  const proxyUrl = input.proxyUrl === undefined ? row.proxy_url : normalizeProxyUrl(input.proxyUrl);
  const name = input.name === undefined ? row.name : input.name.trim();
  if (!name) throw new AccountError("账号名称不能为空");

  try {
    db.prepare(
      `UPDATE accounts SET
         name = ?, remark = ?, tags = ?, credential_kind = ?, credential_hash = ?,
         credential_ciphertext = ?, enabled = ?, proxy_url = ?, proxy_enabled = ?, updated_at = ?
       WHERE id = ?`
    ).run(
      name,
      input.remark === undefined ? row.remark : input.remark.trim(),
      input.tags === undefined ? row.tags : JSON.stringify(input.tags),
      credential?.kind ?? row.credential_kind,
      credential ? hashSecret(credential.value) : row.credential_hash,
      credential ? encryptSecret(credential.value) : row.credential_ciphertext,
      input.enabled === undefined ? row.enabled : boolToSql(input.enabled),
      proxyUrl,
      input.proxyEnabled === undefined
        ? row.proxy_enabled
        : boolToSql(input.proxyEnabled && Boolean(proxyUrl)),
      nowIso(),
      id
    );
  } catch (error) {
    if (String((error as Error).message).includes("UNIQUE")) {
      throw new AccountError("该账号凭据与其它账号重复", 409);
    }
    throw error;
  }

  // 凭据变了，旧的状态快照立即失效
  if (credential) {
    db.prepare(
      "UPDATE accounts SET health = 'unknown', status_error = NULL, status_checked_at = NULL, failure_count = 0, cooldown_until = NULL WHERE id = ?"
    ).run(id);
  }

  logEvent({ accountId: id, kind: "account.update", message: `更新账号「${name}」` });
  return getPublicAccount(id);
}

export function deleteAccount(id: string): void {
  const row = getRow(id);
  db.prepare("DELETE FROM accounts WHERE id = ?").run(id);
  logEvent({ kind: "account.delete", message: `删除账号「${row.name}」` });

  // 删掉的恰好是当前账号时，顺位把最早的启用账号顶上
  if (sqlToBool(row.is_active)) {
    const next = db
      .prepare("SELECT id FROM accounts WHERE enabled = 1 ORDER BY created_at ASC LIMIT 1")
      .get() as { id: string } | undefined;
    if (next) setActiveAccount(next.id);
  }
}

export function setAccountEnabled(id: string, enabled: boolean): PublicAccount {
  getRow(id);
  db.prepare("UPDATE accounts SET enabled = ?, updated_at = ? WHERE id = ?").run(
    boolToSql(enabled),
    nowIso(),
    id
  );
  if (!enabled) {
    db.prepare("UPDATE accounts SET is_active = 0 WHERE id = ?").run(id);
  }
  logEvent({
    accountId: id,
    kind: "account.toggle",
    message: enabled ? "启用账号" : "停用账号"
  });
  return getPublicAccount(id);
}

/** 一键切换：把指定账号设为当前账号，同时清掉其它账号的 active 标记 */
export function setActiveAccount(id: string): PublicAccount {
  const row = getRow(id);
  if (!sqlToBool(row.enabled)) throw new AccountError("该账号已停用，请先启用后再切换");

  db.exec("BEGIN");
  try {
    db.prepare("UPDATE accounts SET is_active = 0 WHERE is_active = 1").run();
    db.prepare("UPDATE accounts SET is_active = 1, updated_at = ? WHERE id = ?").run(nowIso(), id);
    db.exec("COMMIT");
  } catch (error) {
    db.exec("ROLLBACK");
    throw error;
  }

  logEvent({ accountId: id, kind: "account.switch", message: `切换当前账号为「${row.name}」` });
  return getPublicAccount(id);
}

/* ------------------------------------------------------------------ */
/* 选取                                                                */
/* ------------------------------------------------------------------ */

let roundRobinCursor = 0;

export interface PickOptions {
  strategy?: SelectionStrategy;
  /** 指定账号 id，优先级最高 */
  accountId?: string;
  /** 最少需要多少积分 */
  minCredit?: number;
}

/**
 * 选出一个可用于发起请求的账号。
 * 会跳过：已停用、冷却中、登录态失效、积分不足（当 minCredit 给出时）的账号。
 */
export function selectAccount(options: PickOptions = {}): AccountCredential {
  if (options.accountId) {
    const credential = getAccountCredential(options.accountId);
    if (!credential.enabled) throw new AccountError("指定账号已停用", 409);
    return credential;
  }

  const strategy = options.strategy ?? "least_failures";

  if (strategy === "active") {
    const active = getActiveAccount();
    if (!active) throw new AccountError("尚未设置当前账号", 409);
    return getAccountCredential(active.id);
  }

  const now = nowIso();
  let rows = db
    .prepare(
      `SELECT * FROM accounts
        WHERE enabled = 1
          AND health != 'expired'
          AND (cooldown_until IS NULL OR cooldown_until <= ?)
        ORDER BY failure_count ASC, last_used_at ASC`
    )
    .all(now) as unknown as AccountRow[];

  if (options.minCredit !== undefined) {
    rows = rows.filter((row) => (row.total_credit ?? Number.POSITIVE_INFINITY) >= (options.minCredit as number));
  }

  if (!rows.length) throw new AccountError("没有可用的即梦账号（均已停用、冷却中或登录态失效）", 409);

  let chosen: AccountRow;
  if (strategy === "round_robin") {
    chosen = rows[roundRobinCursor++ % rows.length] as AccountRow;
  } else if (strategy === "most_credit") {
    chosen = rows.reduce((best, row) =>
      (row.total_credit ?? -1) > (best.total_credit ?? -1) ? row : best
    );
  } else {
    chosen = rows[0] as AccountRow;
  }

  db.prepare("UPDATE accounts SET last_used_at = ?, updated_at = ? WHERE id = ?").run(
    now,
    now,
    chosen.id
  );
  return getAccountCredential(chosen.id);
}

/* ------------------------------------------------------------------ */
/* 状态回写                                                            */
/* ------------------------------------------------------------------ */

export function applyProbeResult(id: string, result: AccountProbeResult): PublicAccount {
  const timestamp = nowIso();
  db.prepare(
    `UPDATE accounts SET
       health = 'healthy', user_id = ?, nickname = ?, avatar_url = ?,
       free_credit = ?, purchase_credit = ?, vip_credit = ?, total_credit = ?,
       membership = ?, status_checked_at = ?, status_error = NULL,
       failure_count = 0, cooldown_until = NULL, last_success_at = ?, updated_at = ?
     WHERE id = ?`
  ).run(
    nz(result.userId),
    nz(result.nickname),
    nz(result.avatarUrl),
    nz(result.freeCredit),
    nz(result.purchaseCredit),
    nz(result.vipCredit),
    nz(result.totalCredit),
    JSON.stringify(result.membership),
    timestamp,
    timestamp,
    timestamp,
    id
  );
  return getPublicAccount(id);
}

export function applyProbeError(id: string, error: unknown): PublicAccount {
  const timestamp = nowIso();
  const isAuth = error instanceof JimengAuthError;
  const message = error instanceof Error ? error.message : String(error);
  const health: HealthStatus = isAuth ? "expired" : "error";
  const cooldownUntil = new Date(Date.now() + config.health.failureCooldownMs).toISOString();

  db.prepare(
    `UPDATE accounts SET
       health = ?, status_checked_at = ?, status_error = ?,
       failure_count = failure_count + 1, last_failure_at = ?,
       cooldown_until = ?, updated_at = ?
     WHERE id = ?`
  ).run(health, timestamp, message.slice(0, 500), timestamp, cooldownUntil, timestamp, id);

  logEvent({
    accountId: id,
    level: isAuth ? "warn" : "error",
    kind: isAuth ? "account.expired" : "account.probe_error",
    message: isAuth ? "登录态已失效" : "探活失败",
    detail: message
  });

  return getPublicAccount(id);
}

export function markSuccess(id: string): void {
  const timestamp = nowIso();
  db.prepare(
    `UPDATE accounts SET
       success_count = success_count + 1, failure_count = 0,
       cooldown_until = NULL, last_success_at = ?, updated_at = ?
     WHERE id = ?`
  ).run(timestamp, timestamp, id);
}

export function markFailure(id: string, error: unknown, cooldownMs?: number): void {
  const timestamp = nowIso();
  const cooldownUntil = new Date(
    Date.now() + (cooldownMs ?? config.health.failureCooldownMs)
  ).toISOString();
  const message = error instanceof Error ? error.message : String(error);

  db.prepare(
    `UPDATE accounts SET
       failure_count = failure_count + 1, last_failure_at = ?,
       cooldown_until = ?, updated_at = ?
     WHERE id = ?`
  ).run(timestamp, cooldownUntil, timestamp, id);

  if (error instanceof JimengAuthError) {
    db.prepare("UPDATE accounts SET health = 'expired', status_error = ? WHERE id = ?").run(
      message.slice(0, 500),
      id
    );
    logEvent({ accountId: id, level: "warn", kind: "account.expired", message, detail: message });
  }
}

/** 登录态失效的账号在重新登录后需要手工恢复 */
export function resetHealth(id: string): PublicAccount {
  db.prepare(
    "UPDATE accounts SET health = 'unknown', status_error = NULL, failure_count = 0, cooldown_until = NULL, updated_at = ? WHERE id = ?"
  ).run(nowIso(), id);
  return getPublicAccount(id);
}

/* ------------------------------------------------------------------ */
/* 批量导入 / 概览                                                     */
/* ------------------------------------------------------------------ */

export interface BulkImportResult {
  created: PublicAccount[];
  failed: { line: number; value: string; reason: string }[];
}

/**
 * 批量导入：每行一个 sessionid 或 Cookie。
 * 支持 `备注名<TAB>凭据` 或 `备注名,凭据` 的形式。
 */
export function bulkImport(text: string): BulkImportResult {
  const lines = text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);

  const created: PublicAccount[] = [];
  const failed: BulkImportResult["failed"] = [];

  lines.forEach((line, index) => {
    let name = "";
    let payload = line;

    const tabIndex = line.indexOf("\t");
    const commaIndex = line.indexOf(",");
    const splitIndex = tabIndex >= 0 ? tabIndex : commaIndex;
    if (splitIndex > 0) {
      const head = line.slice(0, splitIndex).trim();
      const rest = line.slice(splitIndex + 1).trim();
      // 只有当头部不像凭据时才当作备注名
      if (head && !/[=;]/.test(head) && rest) {
        name = head;
        payload = rest;
      }
    }

    try {
      created.push(createAccount({ name, sessionId: payload }));
    } catch (error) {
      failed.push({
        line: index + 1,
        value: payload.slice(0, 24),
        reason: error instanceof Error ? error.message : String(error)
      });
    }
  });

  return { created, failed };
}

export interface PoolOverview {
  total: number;
  enabled: number;
  healthy: number;
  expired: number;
  errorCount: number;
  unknown: number;
  inCooldown: number;
  withProxy: number;
  totalCredit: number;
  vipCount: number;
  activeAccountId: string | null;
}

export function getOverview(): PoolOverview {
  const rows = db.prepare("SELECT * FROM accounts").all() as unknown as AccountRow[];
  const now = nowIso();
  return {
    total: rows.length,
    enabled: rows.filter((r) => sqlToBool(r.enabled)).length,
    healthy: rows.filter((r) => r.health === "healthy").length,
    expired: rows.filter((r) => r.health === "expired").length,
    errorCount: rows.filter((r) => r.health === "error").length,
    unknown: rows.filter((r) => r.health === "unknown").length,
    inCooldown: rows.filter((r) => r.cooldown_until && r.cooldown_until > now).length,
    withProxy: rows.filter((r) => sqlToBool(r.proxy_enabled) && r.proxy_url).length,
    totalCredit: rows.reduce(
      (sum, r) => sum + (sqlToBool(r.enabled) ? (r.total_credit ?? 0) : 0),
      0
    ),
    vipCount: rows.filter((r) => {
      const membership = parseMembership(r.membership);
      return membership.isVip === true;
    }).length,
    activeAccountId: rows.find((r) => sqlToBool(r.is_active))?.id ?? null
  };
}

/** 给前端识别用的脱敏预览 */
export function fingerprintOf(id: string): string {
  return getRow(id).credential_hash.slice(0, 8);
}

export { previewSecret, JimengError };
