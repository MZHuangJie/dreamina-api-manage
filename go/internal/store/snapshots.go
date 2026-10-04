package store

import (
	"time"
)

// CreditSnapshot 是某个时刻的积分余额。
type CreditSnapshot struct {
	ID        int64  `json:"id"`
	AccountID string `json:"accountId"`
	Total     int64  `json:"total"`
	Gift      int64  `json:"gift"`
	Purchase  int64  `json:"purchase"`
	Vip       int64  `json:"vip"`
	// Health 记录这次探活时的登录态，便于区分「没赠送」和「没登录上」。
	Health    string `json:"health"`
	CreatedAt string `json:"createdAt"`
}

// CreditDelta 描述两次快照之间的变化。
type CreditDelta struct {
	From  CreditSnapshot `json:"from"`
	To    CreditSnapshot `json:"to"`
	Delta int64          `json:"delta"`
}

// SnapshotCredit 记录一次积分快照。
//
// 只在余额发生变化、或者距上次超过 minInterval 时才写。
// 探活默认 30 分钟一次，每次都记会得到一堆数值相同的点，
// 把真正有意义的变化淹没掉。
func (d *DB) SnapshotCredit(accountID string, credit ProbeResult, health Health, minInterval time.Duration) error {
	var lastTotal int64
	var lastAt string
	row := d.sql.QueryRow(
		"SELECT total, created_at FROM credit_snapshots WHERE account_id = ? ORDER BY id DESC LIMIT 1",
		accountID)
	hasLast := true
	if err := row.Scan(&lastTotal, &lastAt); err != nil {
		hasLast = false
	}

	newTotal := int64Or(credit.TotalCredit)

	if hasLast {
		changed := newTotal != lastTotal
		stale := true
		if t, err := time.Parse(time.RFC3339, lastAt); err == nil {
			stale = time.Since(t) >= minInterval
		}
		if !changed && !stale {
			return nil
		}
	}

	return d.insertSnapshot(accountID, credit, health)
}

// SnapshotCreditAlways 无条件记录一次快照（手动触发时用）。
func (d *DB) SnapshotCreditAlways(accountID string, credit ProbeResult, health Health) error {
	return d.insertSnapshot(accountID, credit, health)
}

func (d *DB) insertSnapshot(accountID string, credit ProbeResult, health Health) error {
	_, err := d.sql.Exec(`
		INSERT INTO credit_snapshots (account_id, total, gift, purchase, vip, health, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		accountID, int64Or(credit.TotalCredit), int64Or(credit.FreeCredit),
		int64Or(credit.PurchaseCredit), int64Or(credit.VipCredit),
		string(health), NowISO())
	return err
}

// CreditTimeline 返回某个账号的积分时间线（最新的在前）。
func (d *DB) CreditTimeline(accountID string, limit int) ([]CreditSnapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.sql.Query(`
		SELECT id, account_id, total, gift, purchase, vip, health, created_at
		FROM credit_snapshots WHERE account_id = ?
		ORDER BY id DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CreditSnapshot{}
	for rows.Next() {
		var s CreditSnapshot
		if err := rows.Scan(&s.ID, &s.AccountID, &s.Total, &s.Gift, &s.Purchase,
			&s.Vip, &s.Health, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CreditDeltas 把时间线转换成「变化点」，只保留余额变过的那些。
//
// 看原始快照列表很累；看「什么时候多了/少了多少」才是重点。
func (d *DB) CreditDeltas(accountID string, limit int) ([]CreditDelta, error) {
	snapshots, err := d.CreditTimeline(accountID, limit)
	if err != nil {
		return nil, err
	}
	// 时间线是倒序的，反过来算增量
	out := []CreditDelta{}
	for i := len(snapshots) - 1; i > 0; i-- {
		older, newer := snapshots[i], snapshots[i-1]
		if older.Total == newer.Total {
			continue
		}
		out = append(out, CreditDelta{From: older, To: newer, Delta: newer.Total - older.Total})
	}
	// 最新的在前
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// PruneSnapshots 清理旧快照，每个账号只保留最近 keep 条。
func (d *DB) PruneSnapshots(keep int) error {
	if keep <= 0 {
		keep = 500
	}
	_, err := d.sql.Exec(`
		DELETE FROM credit_snapshots
		WHERE id NOT IN (
			SELECT id FROM credit_snapshots c2
			WHERE c2.account_id = credit_snapshots.account_id
			ORDER BY id DESC LIMIT ?
		)`, keep)
	return err
}

func int64Or(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
