// Package browser 是 Node 浏览器通道 sidecar 的客户端。
//
// Dreamina 的写操作强制校验 secsdk 在页面里生成的 msToken / X-Bogus / X-Gnarly，
// 这些参数无法在服务端复现，因此凡是需要过风控的请求，都要经由 sidecar 在
// 真实浏览器上下文里代发。读操作则直接走 net/http 即可。
package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Cookie 是注入浏览器上下文的一条 cookie。
type Cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// Client 与 sidecar 通信。
type Client struct {
	baseURL string
	secret  string
	http    *http.Client
}

// New 创建 sidecar 客户端。baseURL 形如 http://127.0.0.1:8790。
func New(baseURL, secret string) *Client {
	return &Client{
		baseURL: baseURL,
		secret:  secret,
		// sidecar 内部已经有超时控制，这里留宽一些
		http: &http.Client{Timeout: 5 * time.Minute},
	}
}

// FetchRequest 让 sidecar 在指定账号的页面上下文里代发一次请求。
type FetchRequest struct {
	AccountID string            `json:"accountId"`
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
}

// FetchResponse 是 sidecar 代发请求的结果。
type FetchResponse struct {
	Status  int               `json:"status"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
}

// SessionOptions 是建立会话时的可选参数。
type SessionOptions struct {
	// Proxy 是该账号的独立代理，留空走本机网络。
	Proxy string
	// Timezone 是页面时区（如 Asia/Tokyo）。
	//
	// 不设的话浏览器会报宿主机的时区。账号在日区、代理在日本、
	// 浏览器却报美国时间，是一个很显眼的指纹差异。
	Timezone string
}

// EnsureSession 确保该账号的浏览器上下文已建立（幂等，可重复调用）。
func (c *Client) EnsureSession(ctx context.Context, accountID string, cookies []Cookie, opts SessionOptions) error {
	payload := map[string]any{"accountId": accountID, "cookies": cookies}
	if opts.Proxy != "" {
		payload["proxy"] = opts.Proxy
	}
	if opts.Timezone != "" {
		payload["timezone"] = opts.Timezone
	}
	_, err := c.do(ctx, http.MethodPost, "/session", payload, nil)
	return err
}

// DropSession 销毁该账号的浏览器上下文。
func (c *Client) DropSession(ctx context.Context, accountID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/session/"+accountID, nil, nil)
	return err
}

// Fetch 在页面上下文里代发请求。这是本包唯一的业务入口。
func (c *Client) Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error) {
	if req.Method == "" {
		req.Method = http.MethodPost
	}
	var out FetchResponse
	if _, err := c.do(ctx, http.MethodPost, "/fetch", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Health 返回 sidecar 的健康信息。
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if _, err := c.do(ctx, http.MethodGet, "/health", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// sidecarError 是 sidecar 返回的错误信封。
type sidecarError struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func (c *Client) do(ctx context.Context, method, path string, in any, out any) (int, error) {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return 0, fmt.Errorf("序列化请求失败: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	if c.secret != "" {
		req.Header.Set("x-sidecar-token", c.secret)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("连接 sidecar 失败（它启动了吗？）: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("读取 sidecar 响应失败: %w", err)
	}

	if resp.StatusCode >= 400 {
		var se sidecarError
		if json.Unmarshal(raw, &se) == nil && se.Error != "" {
			return resp.StatusCode, fmt.Errorf("sidecar 返回 %d: %s", resp.StatusCode, se.Error)
		}
		return resp.StatusCode, fmt.Errorf("sidecar 返回 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("解析 sidecar 响应失败: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
