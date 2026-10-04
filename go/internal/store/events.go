package store

import "database/sql"

// Event 是一条操作记录。
//
// 注意字段名是 **snake_case**：TS 版直接返回数据库原始行
// （SELECT * FROM events），前端读的就是 event.created_at。
// 写成 createdAt 会让前端 new Date(undefined) → "Invalid Date"。
type Event struct {
	ID        int64  `json:"id"`
	AccountID string `json:"account_id,omitempty"`
	Level     string `json:"level"`
	Kind      string `json:"kind"`
	Message   string `json:"message"`
	Detail    string `json:"detail,omitempty"`
	CreatedAt string `json:"created_at"`
}

// LogEvent 写一条审计记录。失败不影响主流程。
func (d *DB) LogEvent(e Event) {
	if e.Level == "" {
		e.Level = "info"
	}
	if e.CreatedAt == "" {
		e.CreatedAt = NowISO()
	}
	_, _ = d.sql.Exec(
		"INSERT INTO events (account_id, level, kind, message, detail, created_at) VALUES (?,?,?,?,?,?)",
		NullString(e.AccountID), e.Level, e.Kind, e.Message, NullString(e.Detail), e.CreatedAt,
	)
}

// RecentEvents 返回最近的记录；accountID 非空时只返回该账号的。
func (d *DB) RecentEvents(accountID string, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 80
	}

	var (
		rows *sql.Rows
		err  error
	)
	if accountID == "" {
		rows, err = d.sql.Query(
			"SELECT id, account_id, level, kind, message, detail, created_at FROM events ORDER BY id DESC LIMIT ?", limit)
	} else {
		rows, err = d.sql.Query(
			"SELECT id, account_id, level, kind, message, detail, created_at FROM events WHERE account_id = ? ORDER BY id DESC LIMIT ?",
			accountID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var (
			e         Event
			accountID sql.NullString
			detail    sql.NullString
		)
		if err := rows.Scan(&e.ID, &accountID, &e.Level, &e.Kind, &e.Message, &detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.AccountID = accountID.String
		e.Detail = detail.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetSetting 读键值配置（不存在时返回空串）。
func (d *DB) GetSetting(key string) (string, error) {
	var value string
	err := d.sql.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSetting 写键值配置。
func (d *DB) SetSetting(key, value string) error {
	_, err := d.sql.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	return err
}
