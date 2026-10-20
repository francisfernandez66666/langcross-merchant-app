// Package config 汇总运行期配置，全部来自环境变量，带安全默认值。
//
// 设计原则：
//   - 配置项全部通过环境变量注入（12-factor），不读配置文件；
//   - 每一项都有可用默认值，保证本地「零配置」也能起服务（连上本地 PostgreSQL 即可）；
//   - 空字符串是合法默认值，表示「该外部能力未启用」，由上层走降级实现；
//   - ★ 安全相关项「fail-safe」：默认值必须是安全侧的值——开发模式默认关闭、
//     占位密钥在生产模式下直接拒绝启动（见 Load 与 main.go 的校验）。
package config

import (
	"os"
	"strings"
)

// DevSecret 是开发期占位密钥。
//
// 导出成常量而不是藏在 Load 里：main.go 的生产校验要拿它做等值比较
// （「没设 LC_SECRET」与「设了但等于占位值」是同一类事故，必须同罪）。
// 任何生产部署都不该看到这个值——看到了就该拒绝启动。
const DevSecret = "dev-insecure-secret-change-me"

// Config 为进程级配置。所有字段都有可用默认值，便于本地零配置启动。
type Config struct {
	Addr          string // HTTP 监听地址
	DatabaseURL   string // PostgreSQL DSN
	PublicBaseURL string // 对外基址，用于生成门店页 URL 与二维码内容
	Secret        string // token 签名密钥（生产必须改）
	DevMode       bool   // 开发模式：验证码固定、错误更详细；★ 默认关闭
	OCRServiceURL string // Python OCR 微服务；为空则走内置 stub（手动保底）
	TranslateURL  string // 既有翻译链 /api/translation；为空则走 MyMemory（本地开发）
	MTEmail       string // MyMemory 联系邮箱（可选，提升免费配额）
	UploadDir     string // 菜品图片落盘目录；为空则用 ./data/uploads（见 service.LocalImageStore）
	Currency      string // 价格展示符号（AI 助手与价目用）；为空则用 ¥
}

// DefaultCurrency 是默认价格符号。
//
// 为什么可配：本产品服务的是「来华外国客人 + 中国商家」，绝大多数场景是人民币；
// 但渠道客户可能在境外（华人聚集区的中餐馆），届时换成 $ / € 更自然。
const DefaultCurrency = "¥"

// env 读取环境变量，去空白后为空则回落到默认值。
//
// 「显式设为空串」与「完全没设」在这里故意同等对待（都用默认值）：
// 空值几乎总是部署脚本的笔误（变量名写错、值没传进去），静默回落
// 比把空串当合法配置更安全。真正需要区分两者的场景是 LC_DEV——
// 它有专属的读取逻辑（见 Load），不走本函数。
func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// Load 读取环境变量并返回配置。
//
// 各环境变量与默认值：
//   - LC_ADDR              → :8080
//   - LC_DATABASE_URL      → 本地 postgres，sslmode=disable（本地无 TLS）
//   - LC_PUBLIC_BASE_URL   → http://localhost:8080（决定二维码内容，生产务必改）
//   - LC_SECRET            → 开发用占位密钥；生产模式（LC_DEV 未设 1）下
//     仍是占位值或不足 32 字符则 main.go 拒绝启动
//   - LC_DEV               → ★ 默认不设即生产模式；只有显式 "1" 开开发模式
//     （验证码固定 000000 并回显）。忘了设的生产部署不会因此敞开登录——
//     这是 2026-10-19 字节级审计定级的 P0-4，默认值必须站在安全侧。
//   - LC_OCR_URL           → 空（走 StubOCR 手动保底）
//   - LC_TRANSLATE_URL     → 空（走 MyMemory）
//   - LC_MT_EMAIL          → 空（MyMemory 匿名配额，较低）
//   - LC_UPLOAD_DIR        → ./data/uploads（菜品图片落盘目录）
//   - LC_CURRENCY          → ¥（价格展示符号，AI 助手价目用）
func Load() Config {
	return Config{
		Addr:          env("LC_ADDR", ":8080"),
		DatabaseURL:   env("LC_DATABASE_URL", "postgres://localhost:5432/langcross?sslmode=disable"),
		PublicBaseURL: env("LC_PUBLIC_BASE_URL", "http://localhost:8080"),
		Secret:        env("LC_SECRET", DevSecret),
		// 显式 "1" 才开开发模式：未设置 / 空串 / 任何其它值都按生产模式走。
		// 本地开发请用 LC_DEV=1 启动（Makefile 的 api / api-offline 已带上）。
		DevMode:       strings.TrimSpace(os.Getenv("LC_DEV")) == "1",
		OCRServiceURL: env("LC_OCR_URL", ""),
		TranslateURL:  env("LC_TRANSLATE_URL", ""),
		MTEmail:       env("LC_MT_EMAIL", ""),
		UploadDir:     env("LC_UPLOAD_DIR", "./data/uploads"),
		Currency:      env("LC_CURRENCY", DefaultCurrency),
	}
}
