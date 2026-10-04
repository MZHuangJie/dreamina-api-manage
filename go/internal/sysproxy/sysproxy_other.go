//go:build !windows

package sysproxy

// detectPlatform 在非 Windows 上只看环境变量。
//
// Linux 桌面环境各有各的代理配置方式（GNOME / KDE / 环境变量），
// 没有统一的地方可读；容器里则一律用环境变量。
func detectPlatform() Info {
	return Info{}
}
