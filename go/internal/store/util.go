package store

import (
	"fmt"
	"os"
	"time"
)

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
	}
	return nil
}

// NowISO 返回与 TS 版一致的 ISO8601 时间串，便于字符串比较排序。
func NowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// NullString 把空串转成 SQL NULL。
func NullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
