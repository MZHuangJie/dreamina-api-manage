import { DatabaseSync } from "node:sqlite";

import { config, ensureDataDir } from "./config.ts";

/**
 * 使用 Node 24 内置的 node:sqlite，无需任何原生编译依赖。
 * 所有操作是同步的 —— 对本地单机管理工具而言，SQLite 的微秒级写入
 * 远小于一次即梦网络请求，阻塞可忽略。
 */

ensureDataDir();

export const db = new DatabaseSync(config.dbPath);

db.exec("PRAGMA journal_mode = WAL");
db.exec("PRAGMA foreign_keys = ON");
db.exec("PRAGMA busy_timeout = 5000");

/** node:sqlite 只接受 null / number / bigint / string / Uint8Array */
export function boolToSql(value: boolean): number {
  return value ? 1 : 0;
}

export function sqlToBool(value: unknown): boolean {
  return value === 1 || value === true;
}

/** 把 undefined 归一化成 null，避免 node:sqlite 抛 "Unsupported type" */
export function nz<T>(value: T | undefined | null): T | null {
  return value === undefined || value === null ? null : value;
}

const MIGRATIONS: string[] = [
  // v1 —— 初始结构
  `
  CREATE TABLE IF NOT EXISTS accounts (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    remark                TEXT NOT NULL DEFAULT '',
    tags                  TEXT NOT NULL DEFAULT '[]',

    credential_kind       TEXT NOT NULL CHECK (credential_kind IN ('sessionid','cookie')),
    credential_hash       TEXT NOT NULL UNIQUE,
    credential_ciphertext TEXT NOT NULL,

    enabled               INTEGER NOT NULL DEFAULT 1,
    is_active             INTEGER NOT NULL DEFAULT 0,

    proxy_url             TEXT NOT NULL DEFAULT '',
    proxy_enabled         INTEGER NOT NULL DEFAULT 0,

    health                TEXT NOT NULL DEFAULT 'unknown',
    user_id               TEXT,
    nickname              TEXT,
    avatar_url            TEXT,

    free_credit           INTEGER,
    purchase_credit       INTEGER,
    vip_credit            INTEGER,
    total_credit          INTEGER,
    membership            TEXT,
    status_checked_at     TEXT,
    status_error          TEXT,

    success_count         INTEGER NOT NULL DEFAULT 0,
    failure_count         INTEGER NOT NULL DEFAULT 0,
    cooldown_until        TEXT,
    last_used_at          TEXT,
    last_success_at       TEXT,
    last_failure_at       TEXT,

    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
  );

  CREATE INDEX IF NOT EXISTS idx_accounts_pick
    ON accounts(enabled, cooldown_until, failure_count, last_used_at);
  CREATE INDEX IF NOT EXISTS idx_accounts_active
    ON accounts(is_active) WHERE is_active = 1;

  CREATE TABLE IF NOT EXISTS events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT,
    level      TEXT NOT NULL,
    kind       TEXT NOT NULL,
    message    TEXT NOT NULL,
    detail     TEXT,
    created_at TEXT NOT NULL
  );
  CREATE INDEX IF NOT EXISTS idx_events_created ON events(created_at DESC);
  CREATE INDEX IF NOT EXISTS idx_events_account ON events(account_id, created_at DESC);

  CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS generations (
    id          TEXT PRIMARY KEY,
    account_id  TEXT,
    mode        TEXT NOT NULL,
    model       TEXT NOT NULL,
    prompt      TEXT NOT NULL DEFAULT '',
    params      TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'pending',
    history_id  TEXT,
    result      TEXT,
    error       TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
  );
  CREATE INDEX IF NOT EXISTS idx_generations_created ON generations(created_at DESC);
  `
];

export function migrate(): void {
  const row = db.prepare("PRAGMA user_version").get() as { user_version: number } | undefined;
  const current = row?.user_version ?? 0;

  for (let version = current; version < MIGRATIONS.length; version += 1) {
    db.exec("BEGIN");
    try {
      db.exec(MIGRATIONS[version] as string);
      db.exec(`PRAGMA user_version = ${version + 1}`);
      db.exec("COMMIT");
    } catch (error) {
      db.exec("ROLLBACK");
      throw error;
    }
  }
}

/** node:sqlite 的行类型是 Record<string, SQLOutputValue>，这里统一收敛断言 */
export function queryAll<T>(sql: string, ...params: SupportedValue[]): T[] {
  return db.prepare(sql).all(...params) as unknown as T[];
}

export function queryOne<T>(sql: string, ...params: SupportedValue[]): T | undefined {
  return db.prepare(sql).get(...params) as unknown as T | undefined;
}

export function execute(sql: string, ...params: SupportedValue[]): void {
  db.prepare(sql).run(...params);
}

export type SupportedValue = null | number | bigint | string | Uint8Array;

export function nowIso(): string {
  return new Date().toISOString();
}

export interface LogEventInput {
  accountId?: string | null;
  level?: "info" | "warn" | "error";
  kind: string;
  message: string;
  detail?: unknown;
}

export function logEvent(input: LogEventInput): void {
  try {
    db.prepare(
      "INSERT INTO events (account_id, level, kind, message, detail, created_at) VALUES (?, ?, ?, ?, ?, ?)"
    ).run(
      nz(input.accountId),
      input.level ?? "info",
      input.kind,
      input.message,
      input.detail === undefined ? null : JSON.stringify(input.detail),
      nowIso()
    );
  } catch {
    // 审计日志失败不应影响主流程
  }
}

export function getSetting(key: string): string | null {
  const row = db.prepare("SELECT value FROM settings WHERE key = ?").get(key) as
    | { value: string }
    | undefined;
  return row?.value ?? null;
}

export function setSetting(key: string, value: string): void {
  db.prepare(
    "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value"
  ).run(key, value);
}
