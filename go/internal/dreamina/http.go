package dreamina

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// transportCache 按代理地址复用 Transport，避免每次请求都重建连接池。
var (
	transportMu    sync.Mutex
	transportCache = map[string]*http.Transport{}
)

func transportFor(proxy string) (*http.Transport, error) {
	transportMu.Lock()
	defer transportMu.Unlock()

	if t, ok := transportCache[proxy]; ok {
		return t, nil
	}

	t := &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("代理地址无效 %q: %w", proxy, err)
		}
		t.Proxy = http.ProxyURL(u)
	}
	transportCache[proxy] = t
	return t, nil
}

// CloseTransports 关闭所有缓存的连接池。
func CloseTransports() {
	transportMu.Lock()
	defer transportMu.Unlock()
	for _, t := range transportCache {
		t.CloseIdleConnections()
	}
	transportCache = map[string]*http.Transport{}
}

// doJSON 直连发一次请求并返回原始响应体。
//
// 只用于**读操作**。写操作会被 Dreamina 的 shark 风控拦截（ret=-6），
// 必须改走 CallBrowser。
func doJSON(ctx context.Context, url string, headers map[string]string, body string, proxy string) (string, error) {
	transport, err := transportFor(proxy)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Transport: transport, Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if proxy != "" {
			return "", fmt.Errorf("经代理 %s 请求 Dreamina 失败: %w", proxy, err)
		}
		return "", fmt.Errorf("请求 Dreamina 失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("Dreamina 服务端错误 HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return "", fmt.Errorf("Dreamina 拒绝请求 HTTP %d，登录态可能已失效", resp.StatusCode)
	}
	return string(raw), nil
}
