package dreamina

import (
	"os"
	"strings"

	"dreamina-manager/internal/sysproxy"
)

// effectiveProxy 决定这次请求实际走哪个出口。
//
// 优先级：账号自己的代理 > 系统代理 > 直连。
//
// 为什么要管系统代理：用户的 VPN 多数是「系统代理」模式（没有 TUN 网卡）。
// 浏览器认系统代理，而 Go 的 net/http 不认——不管的话，
// 同一个程序里浏览器通道从 VPN 出去、直连请求从本地出去，
// 平台会看到两个国家的出口，这本身就是个异常信号。
//
// 想强制直连（比如排查代理问题时）可以设 MANAGER_IGNORE_SYSTEM_PROXY=true。
func effectiveProxy(accountProxy string) (proxy string, fromSystem bool) {
	if trimmed := strings.TrimSpace(accountProxy); trimmed != "" {
		return trimmed, false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MANAGER_IGNORE_SYSTEM_PROXY")), "true") {
		return "", false
	}
	detected := sysproxy.Detect()
	if detected.URL == "" || !sysproxy.IsUsable(detected.URL) {
		return "", false
	}
	return detected.URL, true
}
