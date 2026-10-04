// Command cryptocheck 用于验证 Go 的凭据加解密实现。
//
// 用法：
//
//	cryptocheck encrypt <明文>   输出密文
//	cryptocheck decrypt <密文>   输出明文
//
// 密钥来源与主程序一致（MANAGER_SECRET 或 data/secret.key）。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"dreamina-manager/internal/crypto"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: cryptocheck encrypt|decrypt <值>")
		os.Exit(1)
	}
	dataDir := os.Getenv("MANAGER_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	keyPath := filepath.Join(dataDir, "secret.key")

	key, err := crypto.LoadOrCreateKey(keyPath, os.Getenv("MANAGER_SECRET"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载密钥失败:", err)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "encrypt":
		out, err := key.Encrypt(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "加密失败:", err)
			os.Exit(1)
		}
		fmt.Println(out)
	case "decrypt":
		out, err := key.Decrypt(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "解密失败:", err)
			os.Exit(1)
		}
		fmt.Println(out)
	default:
		fmt.Fprintln(os.Stderr, "未知子命令:", os.Args[1])
		os.Exit(1)
	}
}
