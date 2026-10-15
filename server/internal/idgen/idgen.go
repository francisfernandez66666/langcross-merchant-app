// Package idgen 生成无外部依赖的随机 ID 与券码。
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
)

// New 返回 32 位十六进制随机 ID（128 bit 熵）。
func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("idgen: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Slug 生成 8 位短码，用作门店页 /s/{slug}。
func Slug() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("idgen: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Code 生成 n 位纯数字券码（便于人工念读 / 输入核销）。
func Code(n int) string {
	const digits = "0123456789"
	out := make([]byte, n)
	for i := range out {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			panic("idgen: crypto/rand unavailable: " + err.Error())
		}
		out[i] = digits[idx.Int64()]
	}
	return string(out)
}
