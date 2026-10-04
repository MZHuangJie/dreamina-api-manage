package browser

import (
	"context"
	"net/http"
)

// LoginOptions 是打开人工登录窗口的参数。
type LoginOptions struct {
	// URL 是登录页地址。
	URL string `json:"url"`
	// Proxy 是这个账号的独立代理，留空走系统代理。
	Proxy string `json:"proxy,omitempty"`
}

// LoginResult 是登录状态查询的结果。
type LoginResult struct {
	// LoggedIn 表示已经检测到有效登录态。
	LoggedIn bool `json:"loggedIn"`
	// Cookies 是登录态就绪时返回的完整 cookie 列表。
	Cookies []Cookie `json:"cookies,omitempty"`
}

// CookieValue 按名字取一个 cookie 的值。
func (r LoginResult) CookieValue(name string) string {
	for _, c := range r.Cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// StartLogin 让浏览器通道打开一个**可见的**登录窗口。
//
// 这是「在管理器里登录」的关键：不逆向登录协议，而是把真实浏览器
// 交给用户操作，所有登录方式（账密、验证码、Google）天然支持。
func (c *Client) StartLogin(ctx context.Context, accountID string, opts LoginOptions) error {
	payload := map[string]any{"accountId": accountID, "url": opts.URL}
	if opts.Proxy != "" {
		payload["proxy"] = opts.Proxy
	}
	_, err := c.do(ctx, http.MethodPost, "/login", payload, nil)
	return err
}

// PollLogin 查询登录是否完成。
func (c *Client) PollLogin(ctx context.Context, accountID string) (*LoginResult, error) {
	var out LoginResult
	if _, err := c.do(ctx, http.MethodGet, "/login/"+accountID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseLogin 关闭登录窗口。
func (c *Client) CloseLogin(ctx context.Context, accountID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/login/"+accountID, nil, nil)
	return err
}
