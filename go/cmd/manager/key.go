package main

import (
	"os"

	"dreamina-manager/internal/crypto"
)

// loadKey 加载或创建凭据加密密钥。
func loadKey(keyPath string) (*crypto.Key, error) {
	return crypto.LoadOrCreateKey(keyPath, os.Getenv("MANAGER_SECRET"))
}
