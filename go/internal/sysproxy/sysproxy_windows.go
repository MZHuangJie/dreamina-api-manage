//go:build windows

package sysproxy

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// registryPath 是 Windows 保存代理设置的位置。
//
// 注意：这个字符串**必须**用反斜杠分隔。曾经因为构建脚本把反斜杠
// 当转义符吃掉，路径变成了 SoftwareMicrosoftWindows...，
// 导致 OpenKey 一直失败、系统代理永远检测不到——而且不报错，只是静默返回空。
const registryPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// detectPlatform 读取 Windows 的 Internet 设置。
//
// 位置和一个普通程序在「Internet 选项」里看到的完全一致。
func detectPlatform() Info {
	key, err := registry.OpenKey(registry.CURRENT_USER, registryPath, registry.QUERY_VALUE)
	if err != nil {
		return Info{}
	}
	defer key.Close()

	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return Info{}
	}

	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return Info{}
	}

	url := normalize(server)
	if !IsUsable(url) {
		return Info{}
	}
	return Info{URL: url, Source: "Windows Internet 设置"}
}
