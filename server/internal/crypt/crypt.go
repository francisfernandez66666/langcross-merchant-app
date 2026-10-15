// Package crypt 提供「秘密的加密存储」能力：目前唯一的用途是保护租户的翻译 API Key。
//
// 为什么不用「配置项传 Key」而要单独建表 + 加密：
//
//	用户要自己在后台维护 Key（增删改、轮换、启停），配置项做不到这些；
//	而 Key 属于高价值凭据，一次数据库泄露不该把它们全部送出去，
//	因此落库的是密文，密钥由 LC_SECRET 派生，只在进程内存里存在。
//
// 算法选型：AES-256-GCM。
//   - GCM 是 AEAD：同时保证机密性与完整性，密文被改动能被发现（而不是解密出垃圾）；
//   - 标准库原生支持，不引入第三方依赖；
//   - 每条密文带 12 字节随机 nonce，同一明文两次加密结果不同。
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
)

// 派生用的固定上下文串。改了它等于更换整套密钥，旧密文全部作废——
// 因此除非主动轮换，这个常量不应变动（变动需要同时重存所有 Key）。
var keyInfo = []byte("langcross/translation-api-key/v1")

// ErrShortSecret / ErrBadCiphertext 是调用方可用于区分的哨兵错误。
var (
	ErrShortSecret   = errors.New("密钥长度不足，至少需要 16 字符")
	ErrBadCiphertext = errors.New("密文已损坏或不是本系统加密的")
)

// Cipher 持有派生出的 AES-256 密钥，可并发使用（gcm.AEAD 本身是并发安全的）。
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher 从应用密钥（cfg.Secret）派生 AES-256 密钥。
//
// 用 HKDF-SHA256 而不是直接 SHA256(secret)：派生函数能把「一把 secret」
// 扩展成彼此独立的多个用途密钥（本期只用一个，但签名密钥将来要另起一路）。
// secret 至少 16 字符：过短的 secret 意味着暴力枚举成本过低。
func NewCipher(secret string) (*Cipher, error) {
	if len(strings.TrimSpace(secret)) < 16 {
		return nil, ErrShortSecret
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, string(keyInfo), 32)
	if err != nil {
		return nil, fmt.Errorf("派生密钥失败: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("创建 AES 失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建 GCM 失败: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密明文，返回 nonce‖密文‖tag 的拼接字节（调用方按 BYTEA 存库）。
func (c *Cipher) Encrypt(plain string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("读取随机数失败: %w", err)
	}
	// Seal 的第一个参数是 dst：直接复用 nonce 的底层数组做前缀，少一次拷贝
	return c.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

// Decrypt 解密 Encrypt 产出的字节；任何损坏 / 篡改 / 长度不足都返回 ErrBadCiphertext。
func (c *Cipher) Decrypt(blob []byte) (string, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return "", ErrBadCiphertext
	}
	plain, err := c.aead.Open(nil, blob[:ns], blob[ns:], nil)
	if err != nil {
		// 明确归一成哨兵错误：调用方不该看到"AES 解密失败"这种内部细节
		return "", ErrBadCiphertext
	}
	return string(plain), nil
}

// Mask 把 Key 脱敏成可安全展示的形式：保留前 4 与后 4 位，中间用 … 代替。
//
// 列表接口、日志、审计 detail 一律用 Mask 的结果，绝不放明文：
// 前后各留几位足够让人认出「是哪把 Key」，又不构成凭据泄露。
func Mask(key string) string {
	k := strings.TrimSpace(key)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		// 太短的 Key 给它留头留尾也没意义，整串打星
		return strings.Repeat("*", len(k))
	}
	return k[:4] + "…" + k[len(k)-4:]
}
