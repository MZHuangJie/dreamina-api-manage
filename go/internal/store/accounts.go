package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dreamina-manager/internal/crypto"
)

// Provider 标识账号所属平台。
//
// 目前只支持海外版 Dreamina；接口与即梦同源，但主机、aid、账号体系不同，
// 保留这个字段是为了将来接入其它平台时不必改表结构。
type Provider string

// ProviderDreamina 是海外版 dreamina.capcut.com。
const ProviderDreamina Provider = "dreamina"

// Health 是账号的探活状态。
type Health string

const (
	HealthUnknown Health = "unknown"
	HealthHealthy Health = "healthy"
	HealthExpired Health = "expired" // 登录态失效
	HealthError   Health = "error"   // 网络/代理等问题
)

// CredentialKind 是凭据类型。
type CredentialKind string

const (
	KindSessionID CredentialKind = "sessionid"
	KindCookie    CredentialKind = "cookie"
)

// Account 是账号的完整视图（对外暴露时不含明文凭据）。
type Account struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Remark   string   `json:"remark"`
	Tags     []string `json:"tags"`
	Provider Provider `json:"provider"`

	CredentialKind CredentialKind `json:"credentialKind"`
	// Fingerprint 是凭据哈希前缀，用于人工区分账号，不可逆。
	Fingerprint string `json:"credentialFingerprint"`

	Enabled  bool `json:"enabled"`
	IsActive bool `json:"isActive"`

	ProxyEnabled bool   `json:"proxyEnabled"`
	ProxyURLMask string `json:"proxyUrlMasked"`

	// StoreIDC / StoreCountry 决定 Dreamina 打哪个集群，是地区解析的依据。
	StoreIDC     string `json:"storeIdc"`
	StoreCountry string `json:"storeCountry"`

	Health    Health `json:"health"`
	UserID    string `json:"userId"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatarUrl"`

	FreeCredit     *int64 `json:"freeCredit"`
	PurchaseCredit *int64 `json:"purchaseCredit"`
	VipCredit      *int64 `json:"vipCredit"`
	TotalCredit    *int64 `json:"totalCredit"`
	// Membership 目前未填充，字段保留以对齐前端契约。
	Membership      Membership `json:"membership"`
	StatusCheckedAt string     `json:"statusCheckedAt"`
	StatusError     string     `json:"statusError"`

	SuccessCount  int64  `json:"successCount"`
	FailureCount  int64  `json:"failureCount"`
	CooldownUntil string `json:"cooldownUntil"`
	InCooldown    bool   `json:"inCooldown"`
	LastUsedAt    string `json:"lastUsedAt"`
	LastSuccessAt string `json:"lastSuccessAt"`
	LastFailureAt string `json:"lastFailureAt"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Membership 是会员信息。字段可空，前端能正确处理 null。
type Membership struct {
	IsVip          *bool   `json:"isVip"`
	VipExpireAt    *string `json:"vipExpireAt"`
	MembershipType *string `json:"membershipType"`
}

// Secret 是账号的敏感信息，只在发起请求时短暂持有。
type Secret struct {
	Account Account
	// Credential 是原始凭据（sessionid 或整段 cookie）。
	Credential string
	ProxyURL   string
}

// ErrNotFound 表示账号不存在。
var ErrNotFound = errors.New("账号不存在")

// ErrDuplicate 表示凭据重复。
var ErrDuplicate = errors.New("该账号凭据已存在，请勿重复添加")

type rowScanner interface{ Scan(dest ...any) error }

const accountColumns = `
	id, name, remark, tags, provider,
	credential_kind, credential_hash, credential_ciphertext,
	enabled, is_active, proxy_url, proxy_enabled,
	store_idc, store_country,
	health, user_id, nickname, avatar_url,
	free_credit, purchase_credit, vip_credit, total_credit,
	status_checked_at, status_error,
	success_count, failure_count, cooldown_until,
	last_used_at, last_success_at, last_failure_at,
	created_at, updated_at`

func scanAccount(s rowScanner) (*Account, string, string, error) {
	var (
		a            Account
		tagsRaw      string
		hash         string
		cipherText   string
		enabled      int
		isActive     int
		proxyEnabled int
		provider     string
		free, purch  sql.NullInt64
		vip, total   sql.NullInt64
		userID       sql.NullString
		nickname     sql.NullString
		avatar       sql.NullString
		statusAt     sql.NullString
		statusErr    sql.NullString
		cooldown     sql.NullString
		lastUsed     sql.NullString
		lastSuccess  sql.NullString
		lastFailure  sql.NullString
	)
	err := s.Scan(
		&a.ID, &a.Name, &a.Remark, &tagsRaw, &provider,
		&a.CredentialKind, &hash, &cipherText,
		&enabled, &isActive, &a.ProxyURLMask, &proxyEnabled,
		&a.StoreIDC, &a.StoreCountry,
		&a.Health, &userID, &nickname, &avatar,
		&free, &purch, &vip, &total,
		&statusAt, &statusErr,
		&a.SuccessCount, &a.FailureCount, &cooldown,
		&lastUsed, &lastSuccess, &lastFailure,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, "", "", err
	}

	a.Provider = Provider(provider)
	a.Enabled = enabled == 1
	a.IsActive = isActive == 1
	a.ProxyEnabled = proxyEnabled == 1
	a.ProxyURLMask = maskProxy(a.ProxyURLMask)

	_ = json.Unmarshal([]byte(tagsRaw), &a.Tags)
	if a.Tags == nil {
		a.Tags = []string{}
	}

	a.Fingerprint = hash
	if len(hash) > 8 {
		a.Fingerprint = hash[:8]
	}
	if free.Valid {
		a.FreeCredit = &free.Int64
	}
	if purch.Valid {
		a.PurchaseCredit = &purch.Int64
	}
	if vip.Valid {
		a.VipCredit = &vip.Int64
	}
	if total.Valid {
		a.TotalCredit = &total.Int64
	}
	a.UserID = userID.String
	a.Nickname = nickname.String
	a.AvatarURL = avatar.String
	a.StatusCheckedAt = statusAt.String
	a.StatusError = statusErr.String
	a.CooldownUntil = cooldown.String
	a.LastUsedAt = lastUsed.String
	a.LastSuccessAt = lastSuccess.String
	a.LastFailureAt = lastFailure.String
	a.InCooldown = a.CooldownUntil != "" && a.CooldownUntil > NowISO()

	return &a, hash, cipherText, nil
}

// maskProxy 隐去代理里的密码。
func maskProxy(raw string) string {
	if raw == "" {
		return ""
	}
	at := strings.LastIndex(raw, "@")
	scheme := strings.Index(raw, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return raw
	}
	userinfo := raw[scheme+3 : at]
	if !strings.Contains(userinfo, ":") {
		return raw
	}
	user := userinfo[:strings.Index(userinfo, ":")]
	return raw[:scheme+3] + user + ":***" + raw[at:]
}

// ListAccounts 返回全部账号，当前账号排在最前。
func (d *DB) ListAccounts() ([]Account, error) {
	rows, err := d.sql.Query("SELECT " + accountColumns + " FROM accounts ORDER BY is_active DESC, created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Account{}
	for rows.Next() {
		a, _, _, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetAccount 按 id 取账号。
func (d *DB) GetAccount(id string) (*Account, error) {
	row := d.sql.QueryRow("SELECT "+accountColumns+" FROM accounts WHERE id = ?", id)
	a, _, _, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// GetSecret 取出可用于发起请求的完整凭据。调用方用完应立即丢弃。
func (d *DB) GetSecret(id string) (*Secret, error) {
	if d.key == nil {
		return nil, errors.New("数据库未配置加密密钥，无法读取凭据")
	}
	row := d.sql.QueryRow("SELECT "+accountColumns+" FROM accounts WHERE id = ?", id)
	a, _, cipherText, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// scanAccount 会把 proxy_url 塞进 ProxyURLMask 再打码，这里重新取原始值
	var proxyURL string
	if err := d.sql.QueryRow("SELECT proxy_url FROM accounts WHERE id = ?", id).Scan(&proxyURL); err != nil {
		return nil, err
	}

	credential, err := d.key.Decrypt(cipherText)
	if err != nil {
		return nil, fmt.Errorf("账号 %s 凭据解密失败: %w", a.Name, err)
	}
	proxy := ""
	if a.ProxyEnabled {
		proxy = proxyURL
	}
	return &Secret{Account: *a, Credential: credential, ProxyURL: proxy}, nil
}

// CreateAccountInput 是新建账号的入参。
type CreateAccountInput struct {
	Name         string
	Remark       string
	Tags         []string
	Provider     Provider
	Credential   string // sessionid 或整段 cookie
	Kind         CredentialKind
	ProxyURL     string
	ProxyEnabled bool
	Enabled      bool
	StoreIDC     string
	StoreCountry string
}

// CreateAccount 新建账号。首个账号自动成为当前账号。
func (d *DB) CreateAccount(in CreateAccountInput) (*Account, error) {
	if d.key == nil {
		return nil, errors.New("数据库未配置加密密钥，无法保存凭据")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, errors.New("账号名称不能为空")
	}
	if in.Provider == "" {
		in.Provider = ProviderDreamina
	}
	if in.Kind == "" {
		in.Kind = KindSessionID
	}

	hash := crypto.Hash(in.Credential)
	cipherText, err := d.key.Encrypt(in.Credential)
	if err != nil {
		return nil, fmt.Errorf("加密凭据失败: %w", err)
	}

	var count int
	if err := d.sql.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
		return nil, err
	}
	isActive := 0
	if count == 0 {
		isActive = 1
	}

	tagsRaw, _ := json.Marshal(in.Tags)
	id := randomID()
	now := NowISO()

	_, err = d.sql.Exec(`
		INSERT INTO accounts (
			id, name, remark, tags, provider,
			credential_kind, credential_hash, credential_ciphertext,
			enabled, is_active, proxy_url, proxy_enabled,
			store_idc, store_country, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, name, in.Remark, string(tagsRaw), string(in.Provider),
		string(in.Kind), hash, cipherText,
		boolInt(in.Enabled), isActive, in.ProxyURL, boolInt(in.ProxyEnabled),
		in.StoreIDC, in.StoreCountry, now, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return d.GetAccount(id)
}

// DeleteAccount 删除账号。若删的是当前账号，顺位把最早的启用账号顶上。
func (d *DB) DeleteAccount(id string) error {
	account, err := d.GetAccount(id)
	if err != nil {
		return err
	}
	if _, err := d.sql.Exec("DELETE FROM accounts WHERE id = ?", id); err != nil {
		return err
	}
	if account.IsActive {
		var next string
		err := d.sql.QueryRow("SELECT id FROM accounts WHERE enabled = 1 ORDER BY created_at ASC LIMIT 1").Scan(&next)
		if err == nil && next != "" {
			return d.SetActiveAccount(next)
		}
	}
	return nil
}

// SetActiveAccount 一键切换当前账号。
func (d *DB) SetActiveAccount(id string) error {
	a, err := d.GetAccount(id)
	if err != nil {
		return err
	}
	if !a.Enabled {
		return errors.New("该账号已停用，请先启用后再切换")
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE accounts SET is_active = 0 WHERE is_active = 1"); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec("UPDATE accounts SET is_active = 1, updated_at = ? WHERE id = ?", NowISO(), id); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// SetEnabled 启用/停用账号。
func (d *DB) SetEnabled(id string, enabled bool) error {
	res, err := d.sql.Exec("UPDATE accounts SET enabled = ?, updated_at = ? WHERE id = ?",
		boolInt(enabled), NowISO(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if !enabled {
		_, err = d.sql.Exec("UPDATE accounts SET is_active = 0 WHERE id = ?", id)
	}
	return err
}

// ProbeResult 是一次成功探活拿到的数据。
type ProbeResult struct {
	UserID         string
	Nickname       string
	AvatarURL      string
	FreeCredit     *int64
	PurchaseCredit *int64
	VipCredit      *int64
	TotalCredit    *int64
}

// ApplyProbeResult 写入一次成功探活的结果。
func (d *DB) ApplyProbeResult(id string, r ProbeResult) error {
	now := NowISO()
	_, err := d.sql.Exec(`
		UPDATE accounts SET
			health = 'healthy', user_id = ?, nickname = ?, avatar_url = ?,
			free_credit = ?, purchase_credit = ?, vip_credit = ?, total_credit = ?,
			status_checked_at = ?, status_error = NULL,
			failure_count = 0, cooldown_until = NULL, last_success_at = ?, updated_at = ?
		WHERE id = ?`,
		NullString(r.UserID), NullString(r.Nickname), NullString(r.AvatarURL),
		r.FreeCredit, r.PurchaseCredit, r.VipCredit, r.TotalCredit,
		now, now, now, id,
	)
	return err
}

// ApplyProbeError 记录一次探活失败并进入冷却。
func (d *DB) ApplyProbeError(id string, health Health, message string, cooldownISO string) error {
	now := NowISO()
	_, err := d.sql.Exec(`
		UPDATE accounts SET
			health = ?, status_checked_at = ?, status_error = ?,
			failure_count = failure_count + 1, last_failure_at = ?,
			cooldown_until = ?, updated_at = ?
		WHERE id = ?`,
		string(health), now, truncate(message, 500), now, cooldownISO, now, id,
	)
	return err
}

// ResetHealth 清除失效标记。
func (d *DB) ResetHealth(id string) error {
	_, err := d.sql.Exec(`
		UPDATE accounts SET health = 'unknown', status_error = NULL,
			failure_count = 0, cooldown_until = NULL, updated_at = ?
		WHERE id = ?`, NowISO(), id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
