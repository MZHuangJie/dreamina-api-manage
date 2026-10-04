package dreamina

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dreamina-manager/internal/browser"
)

// Account 是发起请求所需的账号上下文。
type Account struct {
	// ID 用于在 sidecar 里标识浏览器上下文。
	ID string
	// Cookies 是注入浏览器上下文的完整 cookie 列表。
	Cookies []browser.Cookie
	// Proxy 是该账号的独立代理，留空表示直连。
	Proxy string
	// Endpoints 是该账号首选集群。遇到 1015 时会自动尝试候选集群。
	Endpoints Endpoints
	// Country 是**账号的**归属国家（vn / jp / us…），不是集群 region。
	//
	// 这两个必须分开：越南账号走新加坡集群时，URL 上的 region 是 SG，
	// 但请求头里的 loc / store-country-code 仍然是 vn。
	// 混用会让平台看到自相矛盾的归属信号。
	Country string
	// StoreIDC 用于在探测成功后缓存「这个归属地该用哪个集群」。
	StoreIDC string
}

// headersFor 构造请求头，并保证签名是最新的。
func (a Account) headersFor(endpoints Endpoints, pathname string) map[string]string {
	headers := endpoints.CommonHeaders(HeadersOptions{Pathname: pathname, Country: a.Country})
	sign, deviceTime := SignNow(pathname)
	headers["sign"] = sign
	headers["device-time"] = fmt.Sprintf("%d", deviceTime)
	return headers
}

// candidates 返回要依次尝试的集群列表。
//
// 首位是账号当前配的集群；如果它不在候选表里（比如用户在配置里写了个自定义的），
// 就把它放到最前面，其余按归属地推断排序。
func (a Account) candidates() []Endpoints {
	inferred := Candidates(a.StoreIDC, a.Country)
	if a.Endpoints.API == "" {
		return inferred
	}
	out := []Endpoints{a.Endpoints}
	for _, e := range inferred {
		if e.API != a.Endpoints.API {
			out = append(out, e)
		}
	}
	return out
}

// rememberWorking 记下探测成功的集群，让后续请求直接用。
func (a Account) rememberWorking(endpoints Endpoints) {
	if a.StoreIDC == "" && a.Country == "" {
		return
	}
	if endpoints.API == a.Endpoints.API {
		return // 首选就对了，不用缓存
	}
	RememberCluster(a.StoreIDC, a.Country, endpoints)
}

// CookieHeader 把账号 cookie 拼成 Cookie 请求头。
//
// 直连（读操作）时必须手工带上——sidecar 那条路径由浏览器自己管理 cookie，
// 不需要这个头。
func (a Account) CookieHeader() string {
	parts := make([]string, 0, len(a.Cookies))
	for _, c := range a.Cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// Client 用「读走直连、写走浏览器」的混合方式访问 Dreamina。
type Client struct {
	sidecar *browser.Client
}

// NewClient 创建 Dreamina 客户端。
func NewClient(sidecar *browser.Client) *Client {
	return &Client{sidecar: sidecar}
}

// envelope 是 Dreamina 的统一响应信封。
type envelope struct {
	Ret    string          `json:"ret"`
	ErrMsg string          `json:"errmsg"`
	Data   json.RawMessage `json:"data"`
}

// ErrPermissionDenied 表示平台以权限为由拒绝了请求。
//
// 实测触发条件：提交生成时若缺少风控签名（msToken/X-Bogus/X-Gnarly）或
// 签名头，会返回 ret=3018 / fail_code=1017。这是「请求本身没问题但环境不被信任」，
// 与登录态失效是两回事，单独区分便于排查。
var ErrPermissionDenied = errors.New("平台拒绝了请求（权限不足）")

// APIError 表示 Dreamina 返回了非 0 的 ret。
type APIError struct {
	Code   string
	ErrMsg string
	Path   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Dreamina 接口错误 %s: %s（%s）", e.Code, e.ErrMsg, e.Path)
}

// IsAuthError 判断是否为登录态失效。
func (e *APIError) IsAuthError() bool {
	switch e.Code {
	case "1015", "1000", "1001", "1014", "8":
		return true
	}
	return false
}

// CallBrowser 经由 sidecar 在浏览器上下文里发请求——所有写操作都必须走这里。
//
// 会依次尝试候选集群：账号归属地和集群的对应关系没法穷举（新区域随时可能出现），
// 所以遇到 1015 就换下一个候选再试，成功了就把结果缓存下来。
// 这样即使某个区域的主机名我们没猜中，系统也能自己找到能用的那个。
func (c *Client) CallBrowser(ctx context.Context, account Account, pathname string, body any) (json.RawMessage, error) {
	if err := c.sidecar.EnsureSession(ctx, account.ID, account.Cookies, browser.SessionOptions{
		Proxy:    account.Proxy,
		Timezone: TimezoneFor(account.Country),
	}); err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	return c.tryClusters(ctx, account, pathname, func(endpoints Endpoints) (json.RawMessage, error) {
		headers := account.headersFor(endpoints, pathname)
		resp, err := c.sidecar.Fetch(ctx,
			browserFetch(account.ID, endpoints.APIURL(pathname), headers, string(encoded)))
		if err != nil {
			// 网络层错误（sidecar 挂了之类）换集群也没用，直接返回
			return nil, &fatalClusterError{err}
		}
		return unwrap(resp.Body, pathname)
	})
}

// CallDirect 直连 Dreamina——只适用于读操作（写操作会被 shark 风控拦截）。
func (c *Client) CallDirect(ctx context.Context, account Account, pathname string, body any) (json.RawMessage, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	proxy, _ := effectiveProxy(account.Proxy)

	return c.tryClusters(ctx, account, pathname, func(endpoints Endpoints) (json.RawMessage, error) {
		headers := account.headersFor(endpoints, pathname)
		headers["cookie"] = account.CookieHeader()
		resp, err := doJSON(ctx, endpoints.APIURL(pathname), headers, string(encoded), proxy)
		if err != nil {
			return nil, &fatalClusterError{err}
		}
		return unwrap(resp, pathname)
	})
}

// fatalClusterError 包装「换集群也解决不了」的错误。
type fatalClusterError struct{ err error }

func (e *fatalClusterError) Error() string { return e.err.Error() }
func (e *fatalClusterError) Unwrap() error { return e.err }

// tryClusters 依次尝试候选集群，只在「像是打错了集群」时才换下一个。
func (c *Client) tryClusters(ctx context.Context, account Account, pathname string,
	attempt func(Endpoints) (json.RawMessage, error)) (json.RawMessage, error) {

	candidates := account.candidates()
	if len(candidates) == 0 {
		return nil, fmt.Errorf("账号 %s 没有可用的集群配置", account.ID)
	}

	var lastErr error
	for i, endpoints := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := attempt(endpoints)
		if err == nil {
			account.rememberWorking(endpoints)
			return data, nil
		}

		var fatal *fatalClusterError
		if errors.As(err, &fatal) {
			return nil, fatal.err
		}
		lastErr = err

		// 只有「登录态类」错误才值得换集群。
		// 1015 的典型成因就是打错了区域，换一个往往就好了；
		// 而 3018（权限不足）、参数错误之类，换集群纯属浪费。
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "1015" {
			return nil, err
		}
		if i < len(candidates)-1 {
			ForgetCluster(account.StoreIDC, account.Country)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("所有候选集群都失败了")
	}
	return nil, fmt.Errorf("在 %d 个候选集群上都被拒绝（账号归属地 store-idc=%q country=%q）：%w",
		len(candidates), account.StoreIDC, account.Country, lastErr)
}

func unwrap(raw, pathname string) (json.RawMessage, error) {
	var env envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("%s 返回了非 JSON 响应: %s", pathname, truncate(raw, 200))
	}
	if env.Ret != "" && env.Ret != "0" {
		apiErr := &APIError{Code: env.Ret, ErrMsg: env.ErrMsg, Path: pathname}
		if env.Ret == "3018" {
			return nil, fmt.Errorf("%w: %w", ErrPermissionDenied, apiErr)
		}
		return nil, apiErr
	}
	return env.Data, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// browserFetch 是构造 sidecar 代发请求的便捷包装。
func browserFetch(accountID, target string, headers map[string]string, body string) browser.FetchRequest {
	return browser.FetchRequest{
		AccountID: accountID,
		URL:       target,
		Method:    "POST",
		Headers:   headers,
		Body:      body,
	}
}

// PollInterval 是轮询生成结果的间隔。
var PollInterval = 4 * time.Second
