// Package store 是账号管理器的持久化层。
//
// 数据库 schema 与 TypeScript 版保持兼容——同一个 data/manager.db 两边都能读，
// 便于从 TS 实现平滑迁移。Go 版额外用 v2 迁移补了 provider 等列（TS 用 SELECT *
// 读命名列，多出来的列对它无害）。
package store

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite" // 纯 Go 驱动，无 cgo

	"dreamina-manager/internal/crypto"
)

// DB 包装一个 SQLite 连接与凭据密钥。
type DB struct {
	sql *sql.DB
	key *crypto.Key
}

// migrations 按顺序执行，索引即 user_version。
var migrations = []string{
	// v1 —— 与 TS 版完全一致
	`CREATE TABLE IF NOT EXISTS accounts (
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
	CREATE INDEX IF NOT EXISTS idx_generations_created ON generations(created_at DESC);`,

	// v2 —— Go 版新增：区分平台，以及 Dreamina 的地区归属
	`ALTER TABLE accounts ADD COLUMN provider TEXT NOT NULL DEFAULT 'dreamina';
	ALTER TABLE accounts ADD COLUMN store_idc TEXT NOT NULL DEFAULT '';
	ALTER TABLE accounts ADD COLUMN store_country TEXT NOT NULL DEFAULT '';
	CREATE INDEX IF NOT EXISTS idx_accounts_provider ON accounts(provider);`,

	// v3 —— 聚合网关：外部接入密钥，以及生成记录归属哪把密钥
	`CREATE TABLE IF NOT EXISTS api_keys (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		remark      TEXT NOT NULL DEFAULT '',
		key_hash    TEXT NOT NULL UNIQUE,
		key_prefix  TEXT NOT NULL,
		enabled     INTEGER NOT NULL DEFAULT 1,
		usage_count INTEGER NOT NULL DEFAULT 0,
		last_used_at TEXT,
		created_at  TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);
	ALTER TABLE generations ADD COLUMN api_key_id TEXT;`,

	// v4 —— 网关配额：按密钥限流，以及按天累计用量
	`ALTER TABLE api_keys ADD COLUMN rate_per_minute INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE api_keys ADD COLUMN max_concurrent INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE api_keys ADD COLUMN daily_quota INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE IF NOT EXISTS api_key_usage (
		key_id TEXT NOT NULL,
		day    TEXT NOT NULL,
		count  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (key_id, day)
	);
	CREATE INDEX IF NOT EXISTS idx_api_key_usage_day ON api_key_usage(day);`,

	// v5 —— 积分快照：回答「每日赠送到底有没有到账」
	//
	// 只存当前余额是看不出这件事的——你必须能看到「什么时候、变了多少」。
	// 每次探活记一条，就成了一条可以回看的时间线。
	`CREATE TABLE IF NOT EXISTS credit_snapshots (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id  TEXT NOT NULL,
		total       INTEGER NOT NULL DEFAULT 0,
		gift        INTEGER NOT NULL DEFAULT 0,
		purchase    INTEGER NOT NULL DEFAULT 0,
		vip         INTEGER NOT NULL DEFAULT 0,
		health      TEXT NOT NULL DEFAULT '',
		created_at  TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_credit_snap_account
		ON credit_snapshots(account_id, created_at DESC);`,
}

// Open 打开（或创建）数据库并执行迁移。
//
// key 用于加解密账号凭据，为 nil 时数据库仍可用，只是读不出明文凭据。
func Open(dbPath string, key *crypto.Key) (*DB, error) {
	if dir := filepath.Dir(dbPath); dir != "" {
		if err := ensureDir(dir); err != nil {
			return nil, err
		}
	}

	// modernc 驱动的 DSN 参数：WAL + busy timeout + 外键
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 写是串行的，限制连接数避免 database is locked
	conn.SetMaxOpenConns(1)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	db := &DB{sql: conn, key: key}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

// Close 关闭数据库。
func (d *DB) Close() error { return d.sql.Close() }

// SQL 暴露底层连接，供同包其它文件使用。
func (d *DB) SQL() *sql.DB { return d.sql }

func (d *DB) migrate() error {
	var current int
	if err := d.sql.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("读取 schema 版本失败: %w", err)
	}
	for version := current; version < len(migrations); version++ {
		tx, err := d.sql.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[version]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行迁移 v%d 失败: %w", version+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
