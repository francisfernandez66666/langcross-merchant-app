// Package config 汇总运行期配置，全部来自环境变量，带安全默认值。
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
	TranslateURL  string // 既有翻译链 /api/translation；为空则走内置词典
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Load 读取环境变量并返回配置。
func Load() Config {
	return Config{
		Addr:          env("LC_ADDR", ":8080"),
		DatabaseURL:   env("LC_DATABASE_URL", "postgres://localhost:5432/langcross?sslmode=disable"),
		PublicBaseURL: env("LC_PUBLIC_BASE_URL", "http://localhost:8080"),
		Secret:        env("LC_SECRET", "dev-insecure-secret-change-me"),
		DevMode:       env("LC_DEV", "1") == "1",
		OCRServiceURL: env("LC_OCR_URL", ""),
		TranslateURL:  env("LC_TRANSLATE_URL", ""),
	}
}
