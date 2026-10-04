// Package provider 抽象出「一个平台」需要具备的能力。
//
// 即梦（国内版）与 Dreamina（海外版）接口协议同源，但主机、aid、账号体系、
// 签名里的 appvr 都不同。这里把差异收敛到 Provider 接口后面，
// 上层（调度、保活、任务队列、HTTP API）完全不需要知道在跟哪个平台打交道。
package provider

import (
	"context"
	"time"
)

// Account 是发起请求所需的账号上下文。
//
// Credential 是**原始凭据**（sessionid 或整段 cookie）。如何把它变成平台需要的
// cookie 属于适配器自己的知识——即梦用 .jianying.com，Dreamina 用 .capcut.com，
// 两者完全不同，所以不放在这里。
type Account struct {
	ID           string
	Provider     string
	Credential   string
	Proxy        string
	StoreIDC     string
	StoreCountry string
	// Cluster 是平台自己的集群标识（如 US / SG / CN），用于日志与展示。
	Cluster string
}

// Credit 是积分余额。
type Credit struct {
	Free     *int64
	Purchase *int64
	Vip      *int64
	Total    *int64
}

// ProbeResult 是一次探活的完整结果。
type ProbeResult struct {
	UserID   string
	Nickname string
	Avatar   string
	Credit   Credit
}

// ImageModel 是可用的文生图模型。
type ImageModel struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Resolution     string `json:"resolution"`
	CreditEstimate int64  `json:"creditEstimate"`
}

// ImageRequest 是一次文生图的入参。
type ImageRequest struct {
	Prompt      string
	ModelID     string
	WorkspaceID int64
	ImageRatio  int
	Width       int
	Height      int
}

// SubmitResult 是提交后的受理结果。
type SubmitResult struct {
	HistoryID string
}

// ImageResult 是轮询到的成品。
type ImageResult struct {
	Status int
	URLs   []string
}

// TickFunc 用于上报轮询进度。
type TickFunc func(attempt, status int)

// Provider 是一个平台适配器。
type Provider interface {
	// Name 返回平台标识（jimeng / dreamina）。
	Name() string

	// Probe 探活：验证登录态并刷新账号信息与积分。
	Probe(ctx context.Context, acct Account) (*ProbeResult, error)

	// GetCredit 只查积分（比 Probe 轻）。
	GetCredit(ctx context.Context, acct Account) (*Credit, error)

	// ListWorkspaces 取可用于提交生成的 workspace。
	ListWorkspaces(ctx context.Context, acct Account) ([]int64, error)

	// Models 返回该账号当前可用的文生图模型。
	Models(ctx context.Context, acct Account) ([]ImageModel, error)

	// SubmitImage 提交一次文生图。
	SubmitImage(ctx context.Context, acct Account, req ImageRequest) (*SubmitResult, error)

	// PollImage 轮询直到出图或超时。
	PollImage(ctx context.Context, acct Account, historyID string, timeout time.Duration, onTick TickFunc) (*ImageResult, error)
}

// IsAuthError 判断错误是否为登录态失效。各适配器把平台的错误码翻译成它。
type AuthError struct{ Message string }

func (e *AuthError) Error() string { return e.Message }

// IsAuthError 供 errors.As 使用。
func IsAuthError(err error) bool {
	var target *AuthError
	return asError(err, &target)
}

// InsufficientCreditError 表示积分/权益不足。
type InsufficientCreditError struct{ Message string }

func (e *InsufficientCreditError) Error() string { return e.Message }
