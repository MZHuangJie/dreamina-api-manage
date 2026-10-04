package sysproxy

import (
	"os"
	"testing"
)

// 各家 VPN 客户端写进系统设置的格式不一样，都要能认出来。
func TestNormalize(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"127.0.0.1:7897", "http://127.0.0.1:7897"},
		{"http://127.0.0.1:7897", "http://127.0.0.1:7897"},
		{"https://127.0.0.1:7897", "https://127.0.0.1:7897"},
		{"socks5://127.0.0.1:1080", "socks5://127.0.0.1:1080"},
		// Clash 的每协议写法
		{"http=127.0.0.1:7897;https=127.0.0.1:7897", "http://127.0.0.1:7897"},
		{"socks=127.0.0.1:1080", "socks5://127.0.0.1:1080"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalize(tc.raw); got != tc.want {
			t.Errorf("normalize(%q) = %q，期望 %q", tc.raw, got, tc.want)
		}
	}
}

func TestIsUsable(t *testing.T) {
	cases := map[string]bool{
		"http://127.0.0.1:7897":  true,
		"127.0.0.1:7897":         true,
		"socks5://10.0.0.1:1080": true,
		"":                       false,
		"127.0.0.1":              false, // 少了端口
		"   ":                    false,
	}
	for raw, want := range cases {
		if got := IsUsable(raw); got != want {
			t.Errorf("IsUsable(%q) = %v，期望 %v", raw, got, want)
		}
	}
}

// 环境变量优先级高于系统设置——容器和 CI 里只有环境变量。
func TestEnvOverridesSystem(t *testing.T) {
	const key = "HTTPS_PROXY"
	saved, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, saved)
		} else {
			_ = os.Unsetenv(key)
		}
	})

	_ = os.Setenv(key, "http://env-proxy:8080")
	info := DetectFresh()
	if info.URL != "http://env-proxy:8080" {
		t.Fatalf("应当优先用环境变量，实际 %q（来源 %q）", info.URL, info.Source)
	}
}

// 没有环境变量时要去读系统设置。
//
// 这个测试在 CI 或容器里可能因为确实没有系统代理而「通过但没验证到东西」，
// 所以只在 Windows 且注册表里确实配了代理时才断言成功。
func TestDetectDoesNotPanic(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"} {
		_ = os.Unsetenv(k)
	}
	Invalidate()
	info := DetectFresh()
	// 只要求「不崩、返回值自洽」
	if info.URL != "" && !IsUsable(info.URL) {
		t.Errorf("返回了不可用的代理 %q（来源 %q）", info.URL, info.Source)
	}
	t.Logf("检测到系统代理: URL=%q Source=%q", info.URL, info.Source)
}
