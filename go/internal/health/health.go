// Package health 负责账号保活：定时探活、失效标记、积分刷新。
package health

import (
	"context"
	"sync"
	"time"

	"dreamina-manager/internal/provider"
	"dreamina-manager/internal/store"
)

// RunState 是一次批量巡检的进度。
type RunState struct {
	Running    bool   `json:"running"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Healthy    int    `json:"healthy"`
	Failed     int    `json:"failed"`
	LastError  string `json:"lastError,omitempty"`
}

// Prober 执行探活。
type Prober struct {
	db          *store.DB
	providers   *provider.Registry
	concurrency int
	cooldown    time.Duration
	// snapshotInterval 是「余额没变也记一条」的最小间隔。
	snapshotInterval time.Duration

	mu    sync.Mutex
	state RunState
}

// Options 是构造 Prober 的参数。
type Options struct {
	Concurrency int
	Cooldown    time.Duration
	// SnapshotInterval 是余额未变化时也记录快照的最小间隔，默认 6 小时。
	//
	// 为什么不是每次都记：探活默认 30 分钟一次，每次都记会得到一堆
	// 数值相同的点，把真正有意义的变化淹没掉。
	SnapshotInterval time.Duration
}

// New 创建 Prober。
func New(db *store.DB, providers *provider.Registry, opts Options) *Prober {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 3
	}
	if opts.Cooldown <= 0 {
		opts.Cooldown = time.Minute
	}
	if opts.SnapshotInterval <= 0 {
		opts.SnapshotInterval = 6 * time.Hour
	}
	return &Prober{
		db: db, providers: providers,
		concurrency:      opts.Concurrency,
		cooldown:         opts.Cooldown,
		snapshotInterval: opts.SnapshotInterval,
	}
}

// State 返回当前巡检进度。
func (p *Prober) State() RunState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// ProbeOne 探活单个账号，结果写回数据库。
func (p *Prober) ProbeOne(ctx context.Context, accountID string) error {
	secret, err := p.db.GetSecret(accountID)
	if err != nil {
		return err
	}
	adapter, err := p.providers.For(string(secret.Account.Provider))
	if err != nil {
		return err
	}

	acct := provider.Account{
		ID:           "acct-" + shortID(secret.Account.ID),
		Provider:     string(secret.Account.Provider),
		Credential:   secret.Credential,
		Proxy:        secret.ProxyURL,
		StoreIDC:     secret.Account.StoreIDC,
		StoreCountry: secret.Account.StoreCountry,
	}

	result, err := adapter.Probe(ctx, acct)
	if err != nil {
		health := store.HealthError
		if provider.IsAuthError(err) {
			health = store.HealthExpired
		}
		cooldown := time.Now().Add(p.cooldown).UTC().Format("2006-01-02T15:04:05.000Z07:00")
		_ = p.db.ApplyProbeError(accountID, health, err.Error(), cooldown)
		level := "error"
		kind := "account.probe_error"
		if health == store.HealthExpired {
			level = "warn"
			kind = "account.expired"
		}
		p.db.LogEvent(store.Event{AccountID: accountID, Level: level, Kind: kind, Message: err.Error()})
		return err
	}

	probe := store.ProbeResult{
		UserID:         result.UserID,
		Nickname:       result.Nickname,
		AvatarURL:      result.Avatar,
		FreeCredit:     result.Credit.Free,
		PurchaseCredit: result.Credit.Purchase,
		VipCredit:      result.Credit.Vip,
		TotalCredit:    result.Credit.Total,
	}
	if err := p.db.ApplyProbeResult(accountID, probe); err != nil {
		return err
	}

	// 记一条积分快照。
	//
	// 这是回答「每日赠送到底有没有到账」的关键——只存当前余额看不出这件事，
	// 必须能看到「什么时候、变了多少」。探活本身就是天然的采样点。
	//
	// 只在余额变化或超过采样间隔时才写，免得每 30 分钟探活一次就把表撑满。
	_ = p.db.SnapshotCredit(accountID, probe, store.HealthHealthy, p.snapshotInterval)
	p.db.LogEvent(store.Event{AccountID: accountID, Kind: "account.probe_ok", Message: "探活成功"})
	return nil
}

// ProbeAll 并发探活一批账号。
func (p *Prober) ProbeAll(ctx context.Context, ids []string) (RunState, error) {
	p.mu.Lock()
	if p.state.Running {
		current := p.state
		p.mu.Unlock()
		return current, nil
	}
	targets, err := p.db.EnabledIDs(ids)
	if err != nil {
		p.mu.Unlock()
		return RunState{}, err
	}
	p.state = RunState{Running: true, StartedAt: store.NowISO(), Total: len(targets)}
	p.mu.Unlock()

	sem := make(chan struct{}, p.concurrency)
	var wg sync.WaitGroup

	for _, id := range targets {
		wg.Add(1)
		go func(accountID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			err := p.ProbeOne(ctx, accountID)

			p.mu.Lock()
			p.state.Completed++
			if err != nil {
				p.state.Failed++
				p.state.LastError = err.Error()
			} else {
				p.state.Healthy++
			}
			p.mu.Unlock()
		}(id)
	}
	wg.Wait()

	p.mu.Lock()
	p.state.Running = false
	p.state.FinishedAt = store.NowISO()
	final := p.state
	p.mu.Unlock()

	p.db.LogEvent(store.Event{
		Kind: "health.run",
		Message: "保活巡检完成：" + itoa(final.Healthy) + " 正常 / " +
			itoa(final.Failed) + " 异常（共 " + itoa(final.Total) + "）",
	})
	return final, nil
}

// Scheduler 按固定间隔跑巡检。
type Scheduler struct {
	prober     *Prober
	interval   time.Duration
	startDelay time.Duration
	stop       chan struct{}
	done       chan struct{}
}

// NewScheduler 创建调度器。
func NewScheduler(prober *Prober, interval, startDelay time.Duration) *Scheduler {
	return &Scheduler{
		prober:     prober,
		interval:   interval,
		startDelay: startDelay,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Start 启动后台巡检。
func (s *Scheduler) Start() {
	go func() {
		defer close(s.done)
		select {
		case <-time.After(s.startDelay):
		case <-s.stop:
			return
		}
		for {
			// 每次都用独立的超时上下文，避免一次卡死影响后续
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			_, _ = s.prober.ProbeAll(ctx, nil)
			cancel()

			select {
			case <-time.After(s.interval):
			case <-s.stop:
				return
			}
		}
	}()
}

// Stop 停止调度器并等待退出。
func (s *Scheduler) Stop() {
	close(s.stop)
	<-s.done
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
