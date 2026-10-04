package gateway

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Limits 是一把密钥的配额。0 一律表示「不限」。
type Limits struct {
	// PerMinute 限制每分钟调用次数。
	PerMinute int
	// MaxConcurrent 限制同时进行中的生成数。0 表示用全局上限。
	MaxConcurrent int
	// DailyQuota 限制每天累计提交数（由 store 持久化，这里不处理）。
	DailyQuota int64
}

var (
	// ErrTooManyConcurrent 表示这把密钥同时在跑的生成太多了。
	ErrTooManyConcurrent = errors.New("该密钥并发请求过多")
	// ErrRateLimited 表示触发了每分钟频次限制。
	ErrRateLimited = errors.New("请求过于频繁")
	// ErrServerBusy 表示全局并发已满，与具体密钥无关。
	ErrServerBusy = errors.New("服务器繁忙，请稍后重试")
)

// Limiter 做两级限流：全局并发 + 按密钥的并发与频次。
//
// 这些计数刻意只放内存：重启后归零是可以接受的（甚至是我们想要的，
// 让服务重启能立刻恢复服务）。真正需要跨重启保持的是**每日配额**，
// 那个在 store 里，走数据库。
type Limiter struct {
	mu sync.Mutex

	globalLimit    int
	globalInflight int

	// keyID -> 进行中的请求数
	inflight map[string]int
	// keyID -> 当前分钟的计数窗口
	windows map[string]*window
}

type window struct {
	minute time.Time
	count  int
}

// NewLimiter 创建限流器。globalConcurrent <= 0 时取 4。
func NewLimiter(globalConcurrent int) *Limiter {
	if globalConcurrent <= 0 {
		globalConcurrent = 4
	}
	return &Limiter{
		globalLimit: globalConcurrent,
		inflight:    map[string]int{},
		windows:     map[string]*window{},
	}
}

// Acquire 尝试取得一次调用许可。
//
// 成功时返回的 release 必须被调用（建议 defer），否则并发名额会泄漏，
// 这把密钥就再也调不动了。
func (l *Limiter) Acquire(keyID string, limits Limits) (release func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	perKeyLimit := limits.MaxConcurrent
	if perKeyLimit <= 0 {
		perKeyLimit = l.globalLimit
	}

	if l.globalInflight >= l.globalLimit {
		return nil, ErrServerBusy
	}
	if l.inflight[keyID] >= perKeyLimit {
		return nil, fmt.Errorf("%w（上限 %d）", ErrTooManyConcurrent, perKeyLimit)
	}

	if limits.PerMinute > 0 {
		now := time.Now()
		w, ok := l.windows[keyID]
		if !ok || !sameMinute(w.minute, now) {
			w = &window{minute: now}
			l.windows[keyID] = w
		}
		if w.count >= limits.PerMinute {
			return nil, fmt.Errorf("%w（每分钟上限 %d）", ErrRateLimited, limits.PerMinute)
		}
		w.count++
	}

	l.globalInflight++
	l.inflight[keyID]++

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.globalInflight--
			if n := l.inflight[keyID] - 1; n <= 0 {
				delete(l.inflight, keyID)
			} else {
				l.inflight[keyID] = n
			}
		})
	}, nil
}

// Snapshot 返回当前水位，用于健康检查与运维观察。
func (l *Limiter) Snapshot() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()

	perKey := make(map[string]int, len(l.inflight))
	for k, v := range l.inflight {
		perKey[k] = v
	}
	return map[string]any{
		"globalInflight": l.globalInflight,
		"globalLimit":    l.globalLimit,
		"perKeyInflight": perKey,
		"activeWindows":  len(l.windows),
	}
}

// Sweep 清理过期的分钟窗口，避免长期运行后 map 无限增长。
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for id, w := range l.windows {
		if !sameMinute(w.minute, now) {
			delete(l.windows, id)
		}
	}
}

func sameMinute(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd && a.Hour() == b.Hour() && a.Minute() == b.Minute()
}
