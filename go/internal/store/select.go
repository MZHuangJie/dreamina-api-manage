package store

import (
	"fmt"
	"strings"
	"sync"
)

// SelectionStrategy 决定自动派活时怎么挑账号。
type SelectionStrategy string

const (
	StrategyActive        SelectionStrategy = "active"         // 只用当前账号
	StrategyLeastFailures SelectionStrategy = "least_failures" // 失败最少优先（默认）
	StrategyRoundRobin    SelectionStrategy = "round_robin"    // 轮询
	StrategyMostCredit    SelectionStrategy = "most_credit"    // 积分最多优先
)

// ParseStrategy 解析策略字符串，非法值回退到默认。
func ParseStrategy(raw string) SelectionStrategy {
	switch SelectionStrategy(raw) {
	case StrategyActive, StrategyLeastFailures, StrategyRoundRobin, StrategyMostCredit:
		return SelectionStrategy(raw)
	default:
		return StrategyLeastFailures
	}
}

var (
	rrMu     sync.Mutex
	rrCursor int
)

// SelectAccount 按策略挑一个可用账号。
//
// 会被跳过的账号：已停用、登录态失效（expired）、仍在冷却期内的。
func (d *DB) SelectAccount(strategy SelectionStrategy, providerFilter string) (*Account, error) {
	return d.SelectAccountExcluding(strategy, providerFilter, nil)
}

// SelectAccountExcluding 是 SelectAccount 的加强版，额外排除一批 id。
//
// 重试换号时用它：已经在这个请求里失败过的账号不该再被挑中，
// 否则重试只是在同一个坏号上打转。
func (d *DB) SelectAccountExcluding(strategy SelectionStrategy, providerFilter string, exclude map[string]bool) (*Account, error) {
	accounts, err := d.ListAccounts()
	if err != nil {
		return nil, err
	}

	eligible := make([]Account, 0, len(accounts))
	for _, a := range accounts {
		if providerFilter != "" && string(a.Provider) != providerFilter {
			continue
		}
		if exclude[a.ID] {
			continue
		}
		if !a.Enabled || a.Health == HealthExpired || a.InCooldown {
			continue
		}
		eligible = append(eligible, a)
	}
	if len(eligible) == 0 {
		if len(exclude) > 0 {
			return nil, fmt.Errorf("已试过 %d 个账号且都不可用，没有其它可用账号", len(exclude))
		}
		return nil, fmt.Errorf("没有可用的账号（均已停用、冷却中或登录态失效）")
	}

	switch strategy {
	case StrategyActive:
		for i := range eligible {
			if eligible[i].IsActive {
				return &eligible[i], nil
			}
		}
		return nil, fmt.Errorf("尚未设置当前账号，或当前账号不可用")
	case StrategyRoundRobin:
		rrMu.Lock()
		pick := eligible[rrCursor%len(eligible)]
		rrCursor++
		rrMu.Unlock()
		return &pick, nil
	case StrategyMostCredit:
		best := eligible[0]
		for _, a := range eligible[1:] {
			if creditOf(a) > creditOf(best) {
				best = a
			}
		}
		return &best, nil
	default: // least_failures：列表已按 failure_count, last_used_at 排序
		best := eligible[0]
		for _, a := range eligible[1:] {
			if a.FailureCount < best.FailureCount ||
				(a.FailureCount == best.FailureCount && a.LastUsedAt < best.LastUsedAt) {
				best = a
			}
		}
		return &best, nil
	}
}

func creditOf(a Account) int64 {
	if a.TotalCredit == nil {
		return -1
	}
	return *a.TotalCredit
}

// MarkUsed 记录账号被使用的时间。
func (d *DB) MarkUsed(id string) error {
	now := NowISO()
	_, err := d.sql.Exec("UPDATE accounts SET last_used_at = ?, updated_at = ? WHERE id = ?", now, now, id)
	return err
}

// MarkSuccess 记一次成功。
func (d *DB) MarkSuccess(id string) error {
	now := NowISO()
	_, err := d.sql.Exec(`
		UPDATE accounts SET success_count = success_count + 1, failure_count = 0,
			cooldown_until = NULL, last_success_at = ?, updated_at = ?
		WHERE id = ?`, now, now, id)
	return err
}

// MarkFailure 记一次失败并进入冷却。
func (d *DB) MarkFailure(id, message, cooldownISO string) error {
	now := NowISO()
	_, err := d.sql.Exec(`
		UPDATE accounts SET failure_count = failure_count + 1, last_failure_at = ?,
			cooldown_until = ?, status_error = ?, updated_at = ?
		WHERE id = ?`,
		now, cooldownISO, truncate(message, 500), now, id)
	return err
}

// MarkExpired 把账号标记为登录态失效。
func (d *DB) MarkExpired(id, message string) error {
	now := NowISO()
	_, err := d.sql.Exec(`
		UPDATE accounts SET health = 'expired', status_error = ?, status_checked_at = ?, updated_at = ?
		WHERE id = ?`, truncate(message, 500), now, now, id)
	return err
}

// Stats 是账号池总览。
type Stats struct {
	Total           int    `json:"total"`
	Enabled         int    `json:"enabled"`
	Healthy         int    `json:"healthy"`
	Expired         int    `json:"expired"`
	ErrorCount      int    `json:"errorCount"`
	Unknown         int    `json:"unknown"`
	InCooldown      int    `json:"inCooldown"`
	WithProxy       int    `json:"withProxy"`
	TotalCredit     int64  `json:"totalCredit"`
	ActiveAccountID string `json:"activeAccountId,omitempty"`
}

// GetStats 汇总账号池状态。
func (d *DB) GetStats() (*Stats, error) {
	accounts, err := d.ListAccounts()
	if err != nil {
		return nil, err
	}
	s := &Stats{Total: len(accounts)}
	for _, a := range accounts {
		if a.Enabled {
			s.Enabled++
		}
		switch a.Health {
		case HealthHealthy:
			s.Healthy++
		case HealthExpired:
			s.Expired++
		case HealthError:
			s.ErrorCount++
		default:
			s.Unknown++
		}
		if a.InCooldown {
			s.InCooldown++
		}
		if a.ProxyEnabled {
			s.WithProxy++
		}
		if a.Enabled && a.TotalCredit != nil {
			s.TotalCredit += *a.TotalCredit
		}
		if a.IsActive {
			s.ActiveAccountID = a.ID
		}
	}
	return s, nil
}

// EnabledIDs 返回所有启用账号的 id；ids 非空时只返回其中启用的那些。
func (d *DB) EnabledIDs(ids []string) ([]string, error) {
	accounts, err := d.ListAccounts()
	if err != nil {
		return nil, err
	}
	filter := map[string]bool{}
	for _, id := range ids {
		filter[id] = true
	}
	out := []string{}
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		if len(filter) > 0 && !filter[a.ID] {
			continue
		}
		out = append(out, a.ID)
	}
	return out, nil
}

// NormalizeProxy 校验并规范化代理地址。
func NormalizeProxy(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	return value, nil
}
