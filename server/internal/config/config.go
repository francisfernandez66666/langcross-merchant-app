// Package config 汇总运行期配置，全部来自环境变量，带安全默认值。
//
// 设计原则：
//   - 配置项全部通过环境变量注入（12-factor），不读配置文件；
//   - 每一项都有可用默认值，保证本地「零配置」也能起服务（连上本地 PostgreSQL 即可）；
//   - 空字符串是合法默认值，表示「该外部能力未启用」，由上层走降级实现。
package config

import (
	"os"
	"strings"
)

// Config 为进程级配置。所有字段都有可用默认值，便于本地零配置启动。
type Config struct {
	Addr          string // HTTP 监听地址
	DatabaseURL   string // PostgreSQL DSN
	PublicBaseURL string // 对外基址，用于生成门店页 URL 与二维码内容
	Secret        string // token 签名密钥（生产必须改）
	DevMode       bool   // 开发模式：验证码固定、错误更详细
	OCRServiceURL string // Python OCR 微服务；为空则走内置 stub（手动保底）
	TranslateURL  string // 既有翻译链 /api/translation；为空则走 MyMemory（本地开发）
	MTEmail       string // MyMemory 联系邮箱（可选，提升免费配额）
}

// env 读取环境变量，去空白后为空则回落到默认值。
// 统一走这里是为了让「空字符串 = 用默认值」，避免误把空值当显式配置。
func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Load 读取环境变量并返回配置。
//
// 各环境变量与默认值：
//   - LC_ADDR              → :8080
//   - LC_DATABASE_URL      → 本地 postgres，sslmode=disable（本地无 TLS）
//   - LC_PUBLIC_BASE_URL   → http://localhost:8080（决定二维码内容，生产务必改）
//   - LC_SECRET            → 开发用占位密钥，生产必须替换，否则 token 可被伪造
//   - LC_DEV               → "1" 为开发模式；其余任何值都视为生产模式
//   - LC_OCR_URL           → 空（走 StubOCR 手动保底）
//   - LC_TRANSLATE_URL     → 空（走 MyMemory）
//   - LC_MT_EMAIL          → 空（MyMemory 匿名配额，较低）
func Load() Config {
	return Config{
		Addr:          env("LC_ADDR", ":8080"),
		DatabaseURL:   env("LC_DATABASE_URL", "postgres://localhost:5432/langcross?sslmode=disable"),
		PublicBaseURL: env("LC_PUBLIC_BASE_URL", "http://localhost:8080"),
		Secret:        env("LC_SECRET", "dev-insecure-secret-change-me"),
		DevMode:       env("LC_DEV", "1") == "1",
		OCRServiceURL: env("LC_OCR_URL", ""),
		TranslateURL:  env("LC_TRANSLATE_URL", ""),
		MTEmail:       env("LC_MT_EMAIL", ""),
	}
}
