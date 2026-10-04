package dreamina

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dreamina-manager/internal/browser"
)

// LoginURL 是 Dreamina 的登录页。
// SiteOrigin 是 Dreamina 网页端的源。
const SiteOrigin = "https://dreamina.capcut.com"

// LoginURL 是登录页；need_login=true 会让它直接弹登录框。
// LoginURL 用普通首页，不加 need_login 参数。
//
// 加 ?need_login=true 会让服务端渲染另一个变体的页面，那个变体的内联脚本
// 会被自己的 CSP 拦掉（Refused to execute inline script），表单提交逻辑
// 就在那段脚本里——表现是「输入邮箱点继续没反应」，而且网络面板里
// 一个请求都没有。用普通首页，让用户自己点 Sign in，走的是正常路径。
const LoginURL = SiteOrigin + "/ai-tool/home"

// LoginSession 描述一次「人工登录」会话。
type LoginSession struct {
	AccountID string `json:"accountId"`
	// LoggedIn 表示已经检测到有效的 sessionid。
	LoggedIn bool `json:"loggedIn"`
	// SessionID 只在登录成功后返回。
	SessionID string `json:"sessionId,omitempty"`
	// Credential 是可直接存库的完整凭据。
	Credential   string `json:"credential,omitempty"`
	StoreIDC     string `json:"storeIdc,omitempty"`
	StoreCountry string `json:"storeCountry,omitempty"`
}

// loginCookieNames 是登录后需要一并保存的 cookie。
//
// 只存 sessionid 是不够的——store-idc / store-country-code 决定打哪个集群，
// passport_csrf_token 后续请求可能用得上。
var loginCookieNames = []string{
	"sessionid", "sessionid_ss", "sid_tt",
	"store-idc", "store-country-code",
	"passport_csrf_token", "passport_csrf_token_default",
}

// StartLogin 打开可见的浏览器窗口让用户手动登录。
func (c *Client) StartLogin(ctx context.Context, accountID, proxy string) error {
	return c.sidecar.StartLogin(ctx, accountID, browser.LoginOptions{URL: LoginURL, Proxy: proxy})
}

// PollLogin 查询登录是否完成；完成时返回可存库的凭据。
func (c *Client) PollLogin(ctx context.Context, accountID string) (*LoginSession, error) {
	result, err := c.sidecar.PollLogin(ctx, accountID)
	if err != nil {
		return nil, err
	}

	session := &LoginSession{AccountID: accountID, LoggedIn: result.LoggedIn}
	if !result.LoggedIn {
		return session, nil
	}

	sessionID := result.CookieValue("sessionid")
	if sessionID == "" {
		session.LoggedIn = false
		return session, nil
	}
	session.SessionID = sessionID
	session.StoreIDC = result.CookieValue("store-idc")
	session.StoreCountry = result.CookieValue("store-country-code")

	parts := []string{}
	for _, name := range loginCookieNames {
		if v := result.CookieValue(name); v != "" {
			parts = append(parts, name+"="+v)
		}
	}
	session.Credential = strings.Join(parts, "; ")
	return session, nil
}

// CloseLogin 关闭登录窗口。
func (c *Client) CloseLogin(ctx context.Context, accountID string) error {
	return c.sidecar.CloseLogin(ctx, accountID)
}

// WaitForLogin 轮询等待用户完成登录。
func (c *Client) WaitForLogin(ctx context.Context, accountID string, timeout, interval time.Duration,
	onTick func(remaining time.Duration)) (*LoginSession, error) {

	if interval <= 0 {
		interval = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		session, err := c.PollLogin(ctx, accountID)
		if err != nil {
			return nil, err
		}
		if session.LoggedIn {
			return session, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待登录超时（%s）——窗口还开着的话可以继续登，然后重跑本命令", timeout)
		}
		if onTick != nil {
			onTick(time.Until(deadline))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}
