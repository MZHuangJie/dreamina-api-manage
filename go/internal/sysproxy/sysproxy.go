// Package sysproxy 读取操作系统的代理设置。
//
// 为什么需要它：用户开着 VPN（通常是系统代理模式，没有 TUN 网卡），
// 浏览器会认系统代理，而 Go 的 net/http **不会**。
// 结果是同一个程序里两条路径走出两个国家——
// 浏览器通道从美国出去，Go 的直连请求从本地出去。
//
// 这个不对称既可能导致平台判定异常，也让「我明明开了 VPN」变成一句空话。
package sysproxy

import (
	"os"
	"strings"
	"sync"
	"time"
)

// Info 描述一次探测到的系统代理。
type Info struct {
	// URL 是可直接使用的代理地址（http:// 或 socks5://），为空表示没有代理。
	URL string
	// Source 说明它是从哪读到的，便于排查。
	Source string
}

var (
	mu       sync.Mutex
	cached   Info
	cachedAt time.Time
)

// cacheTTL 是缓存时长。VPN 开关或换节点时系统代理会变，
// 但没必要每个请求都读一次注册表（Windows 上那是跨进程调用，不便宜）。
const cacheTTL = 30 * time.Second

// Detect 返回当前系统代理。结果缓存 30 秒。
//
// 环境变量优先级高于系统设置——容器和 CI 里通常只有环境变量。
func Detect() Info {
	if fromEnv := detectFromEnv(); fromEnv.URL != "" {
		return fromEnv
	}

	mu.Lock()
	defer mu.Unlock()
	if time.Since(cachedAt) < cacheTTL {
		return cached
	}
	cached = detectPlatform()
	cachedAt = time.Now()
	return cached
}

// DetectFresh 跳过缓存重新探测。
func DetectFresh() Info {
	if fromEnv := detectFromEnv(); fromEnv.URL != "" {
		return fromEnv
	}
	mu.Lock()
	defer mu.Unlock()
	cached = detectPlatform()
	cachedAt = time.Now()
	return cached
}

// Invalidate 让下次 Detect 重新读取。
func Invalidate() {
	mu.Lock()
	cachedAt = time.Time{}
	mu.Unlock()
}

func detectFromEnv() Info {
	// 顺序有讲究：HTTPS 优先，因为平台接口都是 https
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return Info{URL: normalize(v), Source: "环境变量 " + key}
		}
	}
	return Info{}
}

// normalize 补上协议前缀，并把 Windows 的 socks= 写法转成标准 URL。
func normalize(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)

	// Windows 支持 "socks=127.0.0.1:1080" 这种每协议单独的写法
	if idx := strings.Index(lower, "socks="); idx >= 0 {
		rest := value[idx+len("socks="):]
		if end := strings.IndexAny(rest, ";,"); end >= 0 {
			rest = rest[:end]
		}
		return withScheme(rest, "socks5://")
	}
	if idx := strings.Index(lower, "http="); idx >= 0 {
		rest := value[idx+len("http="):]
		if end := strings.IndexAny(rest, ";,"); end >= 0 {
			rest = rest[:end]
		}
		return withScheme(rest, "http://")
	}

	// "host:port" 形式（没有协议）时按 http 处理——
	// 绝大多数 VPN 客户端暴露的是 http 代理
	return withScheme(value, "http://")
}

func withScheme(value, scheme string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		return value
	}
	return scheme + value
}

// IsUsable 判断一个代理地址是否值得用。
//
// 系统里经常残留着已失效的代理配置（VPN 卸载了但注册表没清），
// 直接拿去用会让所有请求都连不上，比不用更糟。
// 所以这里只做格式校验，连通性交给调用方按需验证。
func IsUsable(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	// 至少要有 host:port 的样子
	trimmed := value
	if idx := strings.Index(trimmed, "://"); idx >= 0 {
		trimmed = trimmed[idx+3:]
	}
	return strings.Contains(trimmed, ":")
}
