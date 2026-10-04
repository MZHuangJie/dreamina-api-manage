package dreamina

import (
	"context"
	"dreamina-manager/internal/browser"
	"encoding/json"
	"fmt"
)

// CallCommerce 向 commerce 域下的接口发一次请求，返回解包后的 data。
//
// 积分相关的能力都走这个域。它们属于「读 + 幂等写」，
// 直连即可，不需要过浏览器风控。
func (c *Client) CallCommerce(ctx context.Context, account Account, pathname string, body any) (json.RawMessage, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	proxy, _ := effectiveProxy(account.Proxy)

	return c.tryClusters(ctx, account, pathname, func(endpoints Endpoints) (json.RawMessage, error) {
		headers := account.headersFor(endpoints, pathname)
		headers["cookie"] = account.CookieHeader()
		resp, err := doJSON(ctx, endpoints.CommerceURL(pathname), headers, string(encoded), proxy)
		if err != nil {
			return nil, &fatalClusterError{err}
		}
		return unwrap(resp, pathname)
	})
}

// CallCommerceEnvelope 返回整个响应信封，不做 data 解包。
//
// 平台在这点上不一致：/benefits/user_credit 把载荷放在 data 里，
// 而 /benefits/user_credit_history 放在顶层的 response 里。
// 需要后者时必须拿到原始信封，否则会静默解析出空结果——
// 不报错，只是「查不到记录」，很难发现是解析错了。
func (c *Client) CallCommerceEnvelope(ctx context.Context, account Account, pathname string, body any) (json.RawMessage, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	proxy, _ := effectiveProxy(account.Proxy)

	return c.tryClusters(ctx, account, pathname, func(endpoints Endpoints) (json.RawMessage, error) {
		headers := account.headersFor(endpoints, pathname)
		headers["cookie"] = account.CookieHeader()
		resp, err := doJSON(ctx, endpoints.CommerceURL(pathname), headers, string(encoded), proxy)
		if err != nil {
			return nil, &fatalClusterError{err}
		}
		var env struct {
			Ret    string `json:"ret"`
			ErrMsg string `json:"errmsg"`
		}
		if err := json.Unmarshal([]byte(resp), &env); err != nil {
			return nil, fmt.Errorf("%s 返回了非 JSON 响应: %s", pathname, truncate(resp, 200))
		}
		if env.Ret != "" && env.Ret != "0" {
			return nil, &APIError{Code: env.Ret, ErrMsg: env.ErrMsg, Path: pathname}
		}
		return json.RawMessage(resp), nil
	})
}

// CallCommerceBrowser 经由浏览器上下文调用 commerce 域接口。
//
// 写操作必须走这里，不能直连。直连时请求里没有页面上下文注入的
// msToken / X-Bogus / X-Gnarly 签名，风控会直接拒绝：
//
//	34070104 shark action check reject
//
// 这个报错看着像「接口不存在」或者「没资格」，其实只是缺少页面签名。
func (c *Client) CallCommerceBrowser(ctx context.Context, account Account, pathname string, body any) (json.RawMessage, error) {
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
			browserFetch(account.ID, endpoints.CommerceURL(pathname), headers, string(encoded)))
		if err != nil {
			return nil, &fatalClusterError{err}
		}
		return unwrap(resp.Body, pathname)
	})
}
