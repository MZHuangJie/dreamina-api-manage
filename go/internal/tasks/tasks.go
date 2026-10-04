// Package tasks 管理后台生成任务。
//
// 生成（尤其是视频）可能跑几分钟，所以提交后立即返回，
// 由后台 goroutine 轮询并把进度写回数据库，前端轮询查询即可。
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"dreamina-manager/internal/provider"
	"dreamina-manager/internal/store"
)

// Status 是任务状态。
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

// Result 是生成产物。图片走 URLs，视频走 URL。
//
// 形状必须与 TS 版一致：前端读的是 record.result.urls / record.result.url，
// 不是扁平的 record.urls。
type Result struct {
	URLs []string `json:"urls,omitempty"`
	URL  string   `json:"url,omitempty"`
}

// Record 是一条生成记录。
//
// 可空字段一律用指针，序列化成 JSON null——TS 版就是这么输出的，
// 前端用 ?? 判断，用空串会导致 UI 显示空白而不是默认文案。
type Record struct {
	ID          string  `json:"id"`
	AccountID   string  `json:"accountId"`
	AccountName string  `json:"accountName"`
	Mode        string  `json:"mode"`
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	Status      Status  `json:"status"`
	Progress    *string `json:"progress"`
	HistoryID   *string `json:"historyId"`
	Result      *Result `json:"result"`
	Error       *string `json:"error"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

// CreateInput 是提交一次生成的入参。
type CreateInput struct {
	Mode       string // text2image / image2image
	Prompt     string
	AccountID  string // 留空则按策略自动挑
	Strategy   store.SelectionStrategy
	Provider   string // 限定平台，留空不限
	ModelID    string
	ImageRatio int
	Width      int
	Height     int
	// APIKeyID 非空表示这次生成来自聚合网关的外部调用。
	APIKeyID string
}

// Manager 管理后台任务。
type Manager struct {
	db        *store.DB
	providers *provider.Registry

	mu      sync.Mutex
	running map[string]context.CancelFunc

	// maxAttempts 是一次生成最多尝试几个账号（含首次）。
	maxAttempts int
	// cooldown 是账号失败后的冷却时长。
	cooldown time.Duration
	// slots 限制同时进行中的生成数。
	//
	// 生成任务会占用浏览器通道里的页面上下文，无节制地放进来只会
	// 让所有请求一起变慢、一起超时，还不如排队或明确拒绝。
	slots chan struct{}
	// waitTimeout 是排队等待空闲槽位的上限。
	waitTimeout time.Duration
}

// ErrTooBusy 表示并发已满且排队超时。
var ErrTooBusy = errors.New("服务器繁忙，同时进行的生成过多，请稍后重试")

// Options 是任务管理器的可选配置。
type Options struct {
	// MaxAttempts 为 0 时用 DefaultMaxAttempts。
	MaxAttempts int
	// Cooldown 为 0 时用 1 分钟。
	Cooldown time.Duration
	// MaxConcurrent 是同时进行中的生成上限，<=0 时用 4。
	MaxConcurrent int
	// WaitTimeout 是并发满时的排队上限，<=0 时用 30 秒。
	WaitTimeout time.Duration
}

// DefaultMaxConcurrent 是默认的并发生成上限。
//
// 取 4 是因为浏览器通道每个账号一个页面上下文，而一次生成要占用
// 几十秒到几分钟；太多并发只会互相拖慢。
const DefaultMaxConcurrent = 4

// NewManager 创建任务管理器。
func NewManager(db *store.DB, providers *provider.Registry) *Manager {
	return NewManagerWithOptions(db, providers, Options{})
}

// NewManagerWithOptions 创建带配置的任务管理器。
func NewManagerWithOptions(db *store.DB, providers *provider.Registry, opts Options) *Manager {
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	cooldown := opts.Cooldown
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrent
	}
	waitTimeout := opts.WaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = 30 * time.Second
	}
	return &Manager{
		db: db, providers: providers,
		running:     map[string]context.CancelFunc{},
		maxAttempts: maxAttempts,
		cooldown:    cooldown,
		slots:       make(chan struct{}, maxConcurrent),
		waitTimeout: waitTimeout,
	}
}

// acquireSlot 申请一个并发槽位，排不上队就明确拒绝。
func (m *Manager) acquireSlot(ctx context.Context) (func(), error) {
	if m.slots == nil {
		return func() {}, nil
	}
	timer := time.NewTimer(m.waitTimeout)
	defer timer.Stop()

	select {
	case m.slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-m.slots }) }, nil
	case <-timer.C:
		return nil, ErrTooBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ReconcileInterrupted 把进程重启前残留在 pending/running 的任务标记为中断。
func (m *Manager) ReconcileInterrupted() (int, error) {
	res, err := m.db.SQL().Exec(
		"UPDATE generations SET status = 'failed', error = '服务重启，任务已中断', updated_at = ? "+
			"WHERE status IN ('pending','running')", store.NowISO())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Create 提交一次生成并立即返回；真正的执行在后台进行。
func (m *Manager) Create(ctx context.Context, in CreateInput) (*Record, error) {
	if in.Prompt == "" {
		return nil, errors.New("提示词不能为空")
	}
	if in.Mode == "" {
		in.Mode = "text2image"
	}

	var account store.Account
	if in.AccountID != "" {
		a, err := m.db.GetAccount(in.AccountID)
		if err != nil {
			return nil, err
		}
		if !a.Enabled {
			return nil, errors.New("指定账号已停用")
		}
		account = *a
	} else {
		a, err := m.db.SelectAccount(in.Strategy, in.Provider)
		if err != nil {
			return nil, err
		}
		account = *a
	}

	id := newID()
	now := store.NowISO()
	params, _ := json.Marshal(map[string]any{
		"modelId":     in.ModelID,
		"imageRatio":  in.ImageRatio,
		"width":       in.Width,
		"height":      in.Height,
		"accountName": account.Name,
	})
	if _, err := m.db.SQL().Exec(`
		INSERT INTO generations (id, account_id, mode, model, prompt, params, status, api_key_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?, 'pending', ?, ?, ?)`,
		id, account.ID, in.Mode, in.ModelID, in.Prompt, string(params),
		sqlNullString(in.APIKeyID), now, now,
	); err != nil {
		return nil, err
	}

	m.db.LogEvent(store.Event{
		AccountID: account.ID,
		Kind:      "generate.submit",
		Message:   "提交生成，使用账号「" + account.Name + "」",
		Detail:    truncate(in.Prompt, 200),
	})
	_ = m.db.MarkUsed(account.ID)

	// 占一个并发槽位；占不到就明确拒绝，而不是让它进来一起变慢
	release, err := m.acquireSlot(ctx)
	if err != nil {
		m.patch(id, patchFields{status: StatusFailed, errMsg: err.Error(), clearProgress: true})
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.running[id] = cancel
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.running, id)
			m.mu.Unlock()
			cancel()
			release()
		}()
		m.run(runCtx, id, account, in)
	}()

	return m.Get(id)
}

// DefaultMaxAttempts 是一次生成默认最多尝试几个账号。
const DefaultMaxAttempts = 3

// preSubmitError 标记「失败发生在提交成功之前」。
//
// 只有这类错误才值得换号重试——一旦平台已受理，换号会**重复生成**，
// 既浪费额度又在平台侧留下多余记录，所以提交之后的失败一律不重试。
type preSubmitError struct{ err error }

func (e *preSubmitError) Error() string { return e.err.Error() }
func (e *preSubmitError) Unwrap() error { return e.err }

func isPreSubmit(err error) bool {
	var target *preSubmitError
	return errors.As(err, &target)
}

// run 执行一次生成，失败时按需自动换号重试。
func (m *Manager) run(ctx context.Context, id string, first store.Account, in CreateInput) {
	limit := m.maxAttempts
	if limit <= 0 {
		limit = DefaultMaxAttempts
	}

	tried := map[string]bool{}
	account := first
	var lastErr error

	for attempt := 1; attempt <= limit; attempt++ {
		if attempt > 1 {
			next, err := m.db.SelectAccountExcluding(in.Strategy, in.Provider, tried)
			if err != nil {
				// 没号可换了。保留最后一次的真实错误——它比「没有可用账号」更有诊断价值
				break
			}
			account = *next
			m.db.LogEvent(store.Event{
				AccountID: account.ID, Level: "warn", Kind: "generate.switch",
				Message: fmt.Sprintf("第 %d 次尝试失败，自动切换到账号「%s」", attempt-1, account.Name),
			})
		}
		tried[account.ID] = true
		_ = m.db.MarkUsed(account.ID)

		// 每次尝试都刷新归属账号。
		//
		// 不这么做的话，换号成功后记录里仍然写着第一个账号的名字——
		// 多账号场景下这等于把「谁干的活」记错了，排查问题时会被带偏。
		m.patchAccount(id, account.ID, account.Name)

		label := "提交中…"
		if limit > 1 {
			label = fmt.Sprintf("第 %d/%d 次尝试 · 账号「%s」", attempt, limit, account.Name)
		}
		m.patch(id, patchFields{status: StatusRunning, progress: label})

		err := m.attempt(ctx, id, account, in)
		if err == nil {
			return
		}
		lastErr = err

		// 用户主动取消：不标记账号、不重试
		if errors.Is(err, context.Canceled) {
			m.fail(id, account.ID, err)
			return
		}

		m.penalize(account.ID, err)

		if !isPreSubmit(err) {
			m.db.LogEvent(store.Event{
				AccountID: account.ID, Level: "warn", Kind: "generate.no_retry",
				Message: "平台已受理，换号会重复生成，不再重试：" + err.Error(),
			})
			break
		}
		if !provider.AccountFault(err) {
			m.db.LogEvent(store.Event{
				AccountID: account.ID, Level: "warn", Kind: "generate.no_retry",
				Message: "该错误换号无法解决，不再重试：" + err.Error(),
			})
			break
		}
	}

	m.fail(id, account.ID, lastErr)
}

// attempt 用指定账号跑一次「取 workspace → 提交 → 轮询」。
//
// 返回 nil 表示成功，结果已写库；返回错误表示这个账号没跑成。
// 提交成功之前的错误会包成 preSubmitError，供上层判断是否值得换号。
func (m *Manager) attempt(ctx context.Context, id string, account store.Account, in CreateInput) error {
	secret, err := m.db.GetSecret(account.ID)
	if err != nil {
		return &preSubmitError{err}
	}
	adapter, err := m.providers.For(string(account.Provider))
	if err != nil {
		return &preSubmitError{err}
	}

	acct := provider.Account{
		ID:           "acct-" + shortID(account.ID),
		Provider:     string(account.Provider),
		Credential:   secret.Credential,
		Proxy:        secret.ProxyURL,
		StoreIDC:     account.StoreIDC,
		StoreCountry: account.StoreCountry,
	}

	// workspace 是提交生成的必填项，拿不到说明这号还没启用过
	workspaces, err := adapter.ListWorkspaces(ctx, acct)
	if err != nil {
		return &preSubmitError{fmt.Errorf("取 workspace 失败: %w", err)}
	}
	if len(workspaces) == 0 {
		return &preSubmitError{provider.ErrNoWorkspace}
	}

	submit, err := adapter.SubmitImage(ctx, acct, provider.ImageRequest{
		Prompt:      in.Prompt,
		ModelID:     in.ModelID,
		WorkspaceID: workspaces[0],
		ImageRatio:  in.ImageRatio,
		Width:       in.Width,
		Height:      in.Height,
	})
	if err != nil {
		return &preSubmitError{err}
	}

	// —— 从这里开始平台已经受理了，之后的失败一律不重试 ——
	m.patch(id, patchFields{historyID: submit.HistoryID, progress: "已受理，等待出图…"})

	result, err := adapter.PollImage(ctx, acct, submit.HistoryID, 5*time.Minute,
		func(attempt, status int) {
			m.patch(id, patchFields{progress: fmt.Sprintf("第 %d 次查询，状态码 %d", attempt, status)})
		})
	if err != nil {
		return err
	}

	urls, _ := json.Marshal(result.URLs)
	m.patch(id, patchFields{
		status:   StatusSucceeded,
		urls:     string(urls),
		progress: fmt.Sprintf("完成，共 %d 张", len(result.URLs)),
	})
	_ = m.db.MarkSuccess(account.ID)
	m.db.LogEvent(store.Event{
		AccountID: account.ID, Kind: "generate.success",
		Message: "生成成功", Detail: truncate(in.Prompt, 200),
	})
	return nil
}

// patchAccount 更新这次生成归属的账号。
func (m *Manager) patchAccount(id, accountID, accountName string) {
	_, _ = m.db.SQL().Exec(
		"UPDATE generations SET account_id = ?, params = json_set(params, '$.accountName', ?), updated_at = ? WHERE id = ?",
		accountID, accountName, store.NowISO(), id)
}

// penalize 给失败的账号记账：累加失败次数、进冷却，登录态问题额外标记失效。
func (m *Manager) penalize(accountID string, err error) {
	cooldown := m.cooldown
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	until := time.Now().Add(cooldown).UTC().Format("2006-01-02T15:04:05.000Z07:00")
	_ = m.db.MarkFailure(accountID, err.Error(), until)
	if provider.IsAuthError(err) {
		_ = m.db.MarkExpired(accountID, err.Error())
	}
}

// fail 把任务置为终态失败。账号记账已经在 penalize 里做过了。
func (m *Manager) fail(id, accountID string, err error) {
	if err == nil {
		err = errors.New("生成失败")
	}
	canceled := errors.Is(err, context.Canceled)
	status := StatusFailed
	message := err.Error()
	if canceled {
		status = StatusCanceled
		message = "已取消"
	}
	m.patch(id, patchFields{status: status, errMsg: message, clearProgress: true})

	if canceled {
		return
	}
	m.db.LogEvent(store.Event{
		AccountID: accountID, Level: "error", Kind: "generate.failed",
		Message: "生成失败：" + message,
	})
}

type patchFields struct {
	status        Status
	historyID     string
	urls          string
	errMsg        string
	progress      string
	clearProgress bool
}

func (m *Manager) patch(id string, f patchFields) {
	sets := []string{"updated_at = ?"}
	args := []any{store.NowISO()}

	if f.status != "" {
		sets = append(sets, "status = ?")
		args = append(args, string(f.status))
	}
	if f.historyID != "" {
		sets = append(sets, "history_id = ?")
		args = append(args, f.historyID)
	}
	if f.urls != "" {
		sets = append(sets, "result = ?")
		args = append(args, f.urls)
	}
	if f.errMsg != "" {
		sets = append(sets, "error = ?")
		args = append(args, f.errMsg)
	}
	if f.clearProgress {
		sets = append(sets, "params = json_remove(params, '$.progress')")
	} else if f.progress != "" {
		sets = append(sets, "params = json_set(params, '$.progress', ?)")
		args = append(args, f.progress)
	}

	args = append(args, id)
	query := "UPDATE generations SET " + joinComma(sets) + " WHERE id = ?"
	_, _ = m.db.SQL().Exec(query, args...)
}

// Get 读一条记录。
func (m *Manager) Get(id string) (*Record, error) {
	row := m.db.SQL().QueryRow(
		"SELECT id, account_id, mode, model, prompt, params, status, history_id, result, error, created_at, updated_at "+
			"FROM generations WHERE id = ?", id)
	return scanRecord(row)
}

// List 读最近若干条记录。
func (m *Manager) List(limit int) ([]Record, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := m.db.SQL().Query(
		"SELECT id, account_id, mode, model, prompt, params, status, history_id, result, error, created_at, updated_at "+
			"FROM generations ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// Cancel 取消一个正在跑的任务。
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	cancel, ok := m.running[id]
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

// Delete 删除一条记录（先取消）。
func (m *Manager) Delete(id string) error {
	_ = m.Cancel(id)
	_, err := m.db.SQL().Exec("DELETE FROM generations WHERE id = ?", id)
	return err
}

type scanner interface{ Scan(...any) error }

func scanRecord(s scanner) (*Record, error) {
	var (
		r          Record
		accountID  *string
		params     string
		historyID  *string
		result     *string
		errMsg     *string
		statusText string
	)
	err := s.Scan(&r.ID, &accountID, &r.Mode, &r.Model, &r.Prompt, &params,
		&statusText, &historyID, &result, &errMsg, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	r.Status = Status(statusText)
	if accountID != nil {
		r.AccountID = *accountID
	}
	r.HistoryID = historyID
	r.Error = errMsg

	var meta struct {
		Progress    string `json:"progress"`
		AccountName string `json:"accountName"`
	}
	_ = json.Unmarshal([]byte(params), &meta)
	if meta.Progress != "" {
		r.Progress = &meta.Progress
	}
	r.AccountName = meta.AccountName
	if result != nil && *result != "" {
		var urls []string
		if err := json.Unmarshal([]byte(*result), &urls); err == nil && len(urls) > 0 {
			r.Result = &Result{URLs: urls}
		}
	}
	return &r, nil
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// sqlNullString 把空串写成 NULL，避免外键/查询把「无来源」和「来源为空」混为一谈。
func sqlNullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
