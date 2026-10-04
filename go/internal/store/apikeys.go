package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// APIKey 是发给外部调用方的接入密钥。
//
// 明文只在创建时返回一次，库里只存 SHA-256。密钥本身是 32 字节随机数
// （不是用户选的密码），熵足够，不需要 bcrypt 这类慢哈希。
type APIKey struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Prefix 是密钥前 12 位，用于在界面上辨认是哪一把，不可用于调用。
	Prefix     string `json:"keyPrefix"`
	Enabled    bool   `json:"enabled"`
	Remark     string `json:"remark"`
	UsageCount int64  `json:"usageCount"`
	LastUsedAt string `json:"lastUsedAt"`
	CreatedAt  string `json:"createdAt"`

	// —— 配额。0 一律表示「不限」 ——
	//
	// RatePerMinute 限制每分钟调用次数。
	RatePerMinute int `json:"ratePerMinute"`
	// MaxConcurrent 限制同时进行中的生成数。
	MaxConcurrent int `json:"maxConcurrent"`
	// DailyQuota 限制每天累计成功提交的生成数。
	DailyQuota int64 `json:"dailyQuota"`

	// TodayUsed 是今日已用配额（仅列表时填充，不落库）。
	TodayUsed int64 `json:"todayUsed"`
}

// ErrKeyInvalid 表示密钥不存在、已吊销或格式不对。
var ErrKeyInvalid = errors.New("API Key 无效或已吊销")

// keyPrefix 是密钥的固定前缀，方便在日志和界面上识别。
const keyPrefix = "sk-dm-"

// HashAPIKey 计算密钥的存储哈希。
func HashAPIKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// GenerateAPIKey 生成一把新密钥，返回明文与可展示的前缀。
func GenerateAPIKey() (plain, prefix string, err error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("生成密钥失败: %w", err)
	}
	plain = keyPrefix + hex.EncodeToString(buf)
	prefix = plain[:12] + "…" + plain[len(plain)-4:]
	return plain, prefix, nil
}

// CreateAPIKeyInput 是创建密钥的入参。配额字段 0 表示不限。
type CreateAPIKeyInput struct {
	Name          string
	Remark        string
	RatePerMinute int
	MaxConcurrent int
	DailyQuota    int64
}

// CreateAPIKey 创建并保存一把密钥，返回的明文仅此一次。
func (d *DB) CreateAPIKey(in CreateAPIKeyInput) (*APIKey, string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, "", errors.New("密钥名称不能为空")
	}
	plain, prefix, err := GenerateAPIKey()
	if err != nil {
		return nil, "", err
	}
	id := randomID()
	now := NowISO()
	_, err = d.sql.Exec(`
		INSERT INTO api_keys (id, name, remark, key_hash, key_prefix, enabled, usage_count,
			rate_per_minute, max_concurrent, daily_quota, created_at)
		VALUES (?,?,?,?,?,1,0,?,?,?,?)`,
		id, name, in.Remark, HashAPIKey(plain), prefix,
		in.RatePerMinute, in.MaxConcurrent, in.DailyQuota, now)
	if err != nil {
		return nil, "", err
	}
	key, err := d.GetAPIKey(id)
	if err != nil {
		return nil, "", err
	}
	return key, plain, nil
}

// ListAPIKeys 返回全部密钥（不含明文）。
func (d *DB) ListAPIKeys() ([]APIKey, error) {
	rows, err := d.sql.Query(`
		SELECT k.id, k.name, k.remark, k.key_prefix, k.enabled, k.usage_count,
		       COALESCE(k.last_used_at, ''), k.created_at,
		       k.rate_per_minute, k.max_concurrent, k.daily_quota,
		       COALESCE(u.count, 0)
		FROM api_keys k
		LEFT JOIN api_key_usage u ON u.key_id = k.id AND u.day = ?
		ORDER BY k.created_at DESC`, TodayKey())
	// 注意：这里比 apiKeyColumns 多一列（今日用量），所以不复用 scanAPIKey
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		var enabled int
		if err := rows.Scan(&k.ID, &k.Name, &k.Remark, &k.Prefix, &enabled,
			&k.UsageCount, &k.LastUsedAt, &k.CreatedAt,
			&k.RatePerMinute, &k.MaxConcurrent, &k.DailyQuota, &k.TodayUsed); err != nil {
			return nil, err
		}
		k.Enabled = enabled == 1
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetAPIKey 按 id 取密钥。
func (d *DB) GetAPIKey(id string) (*APIKey, error) {
	row := d.sql.QueryRow("SELECT "+apiKeyColumns+" FROM api_keys WHERE id = ?", id)
	k, err := scanAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// 回读时顺带带上今日用量，方便界面展示
	k.TodayUsed = d.TodayUsage(id)
	return k, nil
}

// apiKeyColumns 是所有读取密钥的查询必须共用的列清单。
//
// 抽出来是有原因的：之前 GetAPIKey 和 VerifyAPIKey 各自手写列名，
// 加配额字段时只改了 INSERT 和 ListAPIKeys，漏了这两个——
// 结果**配额功能整体静默失效**：创建后回读是 0，限流器拿到的永远是「不限」。
// 共用一个常量，以后加字段就不会再漏。
const apiKeyColumns = `id, name, remark, key_prefix, enabled, usage_count,
	COALESCE(last_used_at, ''), created_at,
	rate_per_minute, max_concurrent, daily_quota`

func scanAPIKey(s rowScanner) (*APIKey, error) {
	var k APIKey
	var enabled int
	if err := s.Scan(&k.ID, &k.Name, &k.Remark, &k.Prefix, &enabled,
		&k.UsageCount, &k.LastUsedAt, &k.CreatedAt,
		&k.RatePerMinute, &k.MaxConcurrent, &k.DailyQuota); err != nil {
		return nil, err
	}
	k.Enabled = enabled == 1
	return &k, nil
}

// VerifyAPIKey 校验一个明文密钥。
//
// 返回的对象带着配额字段——限流器就是靠它决定放不放行的。
func (d *DB) VerifyAPIKey(plain string) (*APIKey, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return nil, ErrKeyInvalid
	}
	row := d.sql.QueryRow("SELECT "+apiKeyColumns+" FROM api_keys WHERE key_hash = ?", HashAPIKey(plain))
	k, err := scanAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKeyInvalid
	}
	if err != nil {
		return nil, err
	}
	if !k.Enabled {
		return nil, ErrKeyInvalid
	}
	return k, nil
}

// TouchAPIKey 记录一次成功调用。
func (d *DB) TouchAPIKey(id string) {
	_, _ = d.sql.Exec(
		"UPDATE api_keys SET usage_count = usage_count + 1, last_used_at = ? WHERE id = ?",
		NowISO(), id)
}

/* ------------------------------ 配额 ------------------------------ */

// TodayKey 返回用于按天计量的日期键（UTC）。
//
// 用 UTC 而非本地时区，是为了让配额在服务器迁移时不会因为时区变化而重置。
func TodayKey() string {
	return time.Now().UTC().Format("2006-01-02")
}

// ConsumeQuota 尝试消耗一次每日配额。
//
// 返回 consumed=false 表示今日配额已用完。quota<=0 表示不限量。
func (d *DB) ConsumeQuota(keyID string, quota int64) (used int64, consumed bool, err error) {
	day := TodayKey()
	if quota <= 0 {
		err = d.bumpUsage(keyID, day)
		return 0, true, err
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var current int64
	row := tx.QueryRow("SELECT count FROM api_key_usage WHERE key_id = ? AND day = ?", keyID, day)
	if scanErr := row.Scan(&current); scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return 0, false, scanErr
	}

	if current >= quota {
		return current, false, nil
	}

	if _, err := tx.Exec(`
		INSERT INTO api_key_usage (key_id, day, count) VALUES (?, ?, 1)
		ON CONFLICT(key_id, day) DO UPDATE SET count = count + 1`, keyID, day); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return current + 1, true, nil
}

func (d *DB) bumpUsage(keyID, day string) error {
	_, err := d.sql.Exec(`
		INSERT INTO api_key_usage (key_id, day, count) VALUES (?, ?, 1)
		ON CONFLICT(key_id, day) DO UPDATE SET count = count + 1`, keyID, day)
	return err
}

// TodayUsage 查询某把密钥今天的用量。
func (d *DB) TodayUsage(keyID string) int64 {
	var n int64
	_ = d.sql.QueryRow("SELECT count FROM api_key_usage WHERE key_id = ? AND day = ?",
		keyID, TodayKey()).Scan(&n)
	return n
}

// CountAPIKeys 统计密钥数量（含已吊销）。
func (d *DB) CountAPIKeys() (int64, error) {
	var n int64
	err := d.sql.QueryRow("SELECT COUNT(*) FROM api_keys").Scan(&n)
	return n, err
}

// SetAPIKeyEnabled 启用/吊销密钥。
func (d *DB) SetAPIKeyEnabled(id string, enabled bool) error {
	res, err := d.sql.Exec("UPDATE api_keys SET enabled = ? WHERE id = ?", boolInt(enabled), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAPIKey 删除密钥。
func (d *DB) DeleteAPIKey(id string) error {
	res, err := d.sql.Exec("DELETE FROM api_keys WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
