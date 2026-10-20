package config

import "testing"

/* ── Load 的安全默认值 ───────────────────────────────────────
   2026-10-19 字节级审计 P0-4：原实现 LC_DEV 默认 "1"，意味着忘了设
   环境变量的生产部署会以开发模式运行——固定验证码 000000 登录任意
   账号。现在默认值必须站在安全侧：只有显式 LC_DEV=1 才开开发模式。 */

// 验证零配置时的安全默认值：LC_DEV 未设必须落在生产模式（fail-safe），
// 否则忘了设环境变量的生产部署会以开发模式运行、固定验证码可登录任意账号。
func TestLoad_DefaultsToProductionMode(t *testing.T) {
	// 显式清空本包关心的变量。注意「设为空串」与「未设置」在实现层
	// 被刻意同等对待（见 env 的注释），因此这样就能测「零配置」路径。
	for _, k := range []string{"LC_DEV", "LC_SECRET", "LC_ADDR"} {
		t.Setenv(k, "")
	}

	cfg := Load()
	if cfg.DevMode {
		t.Error("未设置 LC_DEV 时应为生产模式（fail-safe），实际开了开发模式")
	}
	if cfg.Secret != DevSecret {
		t.Errorf("未设置 LC_SECRET 时应回落开发占位值（由 main.go 拦下），实际 %q", cfg.Secret)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr 默认值应为 :8080，实际 %q", cfg.Addr)
	}
}

// 验证开发模式只认显式 LC_DEV=1（容忍空白）：「true」「2」等一律不算，
// 杜绝真值歧义被误当成开启。
func TestLoad_DevModeOnlyOnExplicitOne(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"1", true},
		{" 1 ", true}, // 容忍意外空白
		{"0", false},
		{"true", false}, // 不接受真字面量：只认 "1"，杜绝歧义
		{"", false},
		{"2", false},
	}
	for _, c := range cases {
		t.Setenv("LC_DEV", c.val)
		if got := Load().DevMode; got != c.want {
			t.Errorf("LC_DEV=%q 应得 DevMode=%v，实际 %v", c.val, c.want, got)
		}
	}
}

// 验证 LC_DEV 未设置（含空串）时不是开发模式，兜底方向必须站在安全侧。
func TestLoad_DevModeUnsetIsProduction(t *testing.T) {
	t.Setenv("LC_DEV", "")
	if Load().DevMode {
		t.Error("LC_DEV 未设置应为生产模式")
	}
}

// 验证显式配置的 LC_SECRET 原样生效，不被默认值覆盖。
func TestLoad_ExplicitSecretWins(t *testing.T) {
	t.Setenv("LC_SECRET", "a-very-long-random-production-secret-value")
	if got := Load().Secret; got != "a-very-long-random-production-secret-value" {
		t.Errorf("显式 LC_SECRET 应生效，实际 %q", got)
	}
}

// 验证空串密钥按「没设」处理并回落开发占位值：静默回落比把空串当合法
// 密钥安全（占位值再由 main.go 拦下）。
func TestLoad_EmptySecretFallsBackToDevPlaceholder(t *testing.T) {
	// 显式空串按「没设」处理：静默回落比把空串当合法密钥安全
	t.Setenv("LC_SECRET", "")
	if got := Load().Secret; got != DevSecret {
		t.Errorf("空 LC_SECRET 应回落占位值，实际 %q", got)
	}
}

// 验证其余装配默认值：货币符号回落 DefaultCurrency、TranslateURL 为空
// （走 MyMemory），默认口径不随代码演进漂移。
func TestLoad_OtherDefaults(t *testing.T) {
	t.Setenv("LC_CURRENCY", "")
	cfg := Load()
	if cfg.Currency != DefaultCurrency {
		t.Errorf("Currency 默认应为 ¥，实际 %q", cfg.Currency)
	}
	if cfg.TranslateURL != "" {
		t.Errorf("TranslateURL 默认应为空（走 MyMemory），实际 %q", cfg.TranslateURL)
	}
}
