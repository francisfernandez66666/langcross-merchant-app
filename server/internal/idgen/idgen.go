// Package idgen 生成无外部依赖的随机 ID 与券码。
//
// 统一使用 crypto/rand（密码学安全随机源）而非 math/rand：
// 门店 slug 出现在公开 URL 里、券码可被用来核销，二者都不能被预测或枚举。
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
)

// New 返回 32 位十六进制随机 ID（128 bit 熵）。
// 用于数据库主键：熵足够大，碰撞概率可忽略，无需先查库再插入。
//
// crypto/rand 不可用属于「进程已无法安全工作」的严重故障（系统熵源异常），
// 此时 panic 好过返回可预测的 ID。
func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("idgen: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Slug 生成 12 位十六进制短码（6 字节），用作门店页 /s/{slug}。
//
// 长度取舍：比主键（32 位）短，方便二维码编码（码面更疏、更易扫）；
// 6 字节 = 48 bit 熵，对门店规模而言碰撞概率足够低。
// ★ 不变量：slug 一旦生成即永久稳定，已印刷的门口码 / 桌码依赖它。
func Slug() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("idgen: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Code 生成 n 位纯数字券码（便于人工念读 / 输入核销）。
//
// 之所以不用十六进制：券码需要店员肉眼核对、口头报给客人，
// 纯数字更不易念错。逐位取随机数（而非取整段随机数的模）以避免取模偏置。
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
