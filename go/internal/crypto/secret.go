// Package crypto 负责账号凭据的加密存储。
//
// 密文格式与密钥派生**必须**与 TypeScript 实现保持一致，
// 否则已有的 data/manager.db 里存着的账号将无法解密：
//
//	密钥：data/secret.key，内容是 32 随机字节的 base64
//	密文：v1.<iv b64url>.<authTag b64url>.<ciphertext b64url>
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const keyBytes = 32

// nodeBase64URL 对应 Node 的 Buffer.toString("base64url")，即无填充的 URL-safe base64。
var nodeBase64URL = base64.RawURLEncoding

// Key 是账号凭据的加密密钥。
type Key struct {
	raw [keyBytes]byte
}

// LoadOrCreateKey 读取密钥文件；不存在则生成一个。
//
// 优先级与 TS 版一致：
//  1. 环境变量 MANAGER_SECRET（内容做 sha256 派生 32 字节）
//  2. keyPath 指向的密钥文件
func LoadOrCreateKey(keyPath, envSecret string) (*Key, error) {
	if strings.TrimSpace(envSecret) != "" {
		sum := sha256.Sum256([]byte(envSecret))
		k := &Key{}
		copy(k.raw[:], sum[:])
		return k, nil
	}

	data, err := os.ReadFile(keyPath)
	switch {
	case err == nil:
		trimmed := strings.TrimSpace(string(data))
		decoded, decErr := base64.StdEncoding.DecodeString(trimmed)
		if decErr != nil {
			decoded, decErr = nodeBase64URL.DecodeString(trimmed)
		}
		if decErr != nil {
			return nil, fmt.Errorf("密钥文件 %s 不是合法 base64: %w", keyPath, decErr)
		}
		if len(decoded) != keyBytes {
			return nil, fmt.Errorf(
				"密钥文件 %s 内容无效（期望 %d 字节，实际 %d 字节）；请从备份恢复，或删除该文件后重新添加账号",
				keyPath, keyBytes, len(decoded))
		}
		k := &Key{}
		copy(k.raw[:], decoded)
		return k, nil

	case errors.Is(err, os.ErrNotExist):
		if mkErr := os.MkdirAll(filepath.Dir(keyPath), 0o700); mkErr != nil {
			return nil, fmt.Errorf("创建数据目录失败: %w", mkErr)
		}
		generated := make([]byte, keyBytes)
		if _, rErr := rand.Read(generated); rErr != nil {
			return nil, fmt.Errorf("生成密钥失败: %w", rErr)
		}
		if wErr := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(generated)), 0o600); wErr != nil {
			return nil, fmt.Errorf("写入密钥文件失败: %w", wErr)
		}
		k := &Key{}
		copy(k.raw[:], generated)
		return k, nil

	default:
		return nil, fmt.Errorf("读取密钥文件失败: %w", err)
	}
}

// Encrypt 用 AES-256-GCM 加密，输出与 TS 版完全一致的 v1.<iv>.<tag>.<ct> 格式。
func (k *Key) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(k.raw[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, iv, []byte(plaintext), nil)
	// Go 把 tag 追加在密文尾部；Node 是分开存的，这里按 GCM 标准长度拆开
	tagStart := len(sealed) - gcm.Overhead()
	ciphertext, tag := sealed[:tagStart], sealed[tagStart:]

	return strings.Join([]string{
		"v1",
		nodeBase64URL.EncodeToString(iv),
		nodeBase64URL.EncodeToString(tag),
		nodeBase64URL.EncodeToString(ciphertext),
	}, "."), nil
}

// Decrypt 解密 TS 版或 Go 版写出的密文。
func (k *Key) Decrypt(payload string) (string, error) {
	parts := strings.Split(payload, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", errors.New("凭据密文格式无效")
	}
	iv, err := nodeBase64URL.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("密文 IV 解析失败: %w", err)
	}
	tag, err := nodeBase64URL.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("密文认证标签解析失败: %w", err)
	}
	ciphertext, err := nodeBase64URL.DecodeString(parts[3])
	if err != nil {
		return "", fmt.Errorf("密文主体解析失败: %w", err)
	}

	block, err := aes.NewCipher(k.raw[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)

	plaintext, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return "", errors.New(
			"凭据解密失败：当前密钥与写入时不一致。请确认 data/secret.key（或 MANAGER_SECRET）未被更换")
	}
	return string(plaintext), nil
}

// Hash 用于凭据去重，与 TS 版一致（sha256 hex）。
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}
