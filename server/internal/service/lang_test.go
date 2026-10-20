package service

import "testing"

// 产品承诺的 10 个语种：改动这个清单等于改对外承诺，故用测试钉死。
var requiredLangs = []string{"zh", "en", "ja", "ko", "th", "fr", "es", "de", "pt", "vi"}

// 语种注册表的完整性：数量、必需语种、码唯一、Tier 合法、母语写法非空。
// 新增语种时若漏填字段，这里会直接失败。
func TestSupportedLangs_RegistryIntegrity(t *testing.T) {
	if len(SupportedLangs) != len(requiredLangs) {
		t.Errorf("语种数量应为 %d，实际 %d", len(requiredLangs), len(SupportedLangs))
	}

	seen := map[string]bool{}
	for _, l := range SupportedLangs {
		if l.Code == "" || l.Native == "" || l.NameZh == "" {
			t.Errorf("语种字段不可为空：%+v", l)
		}
		if seen[l.Code] {
			t.Errorf("语种码重复：%s", l.Code)
		}
		seen[l.Code] = true
		if l.Tier < 1 || l.Tier > 3 {
			t.Errorf("%s 的 Tier 越界（应为 1–3）：%d", l.Code, l.Tier)
		}
	}

	for _, code := range requiredLangs {
		if !seen[code] {
			t.Errorf("缺少必需语种：%s", code)
		}
	}
}

// 原文语种必须是注册表里的成员，否则 normalizeTargets 的「剔除原文语种」逻辑会失效。
func TestSourceLangIsRegistered(t *testing.T) {
	if !IsSupportedLang(SourceLang) {
		t.Fatalf("SourceLang=%q 未登记在 SupportedLangs 中", SourceLang)
	}
}

// 中文是原文语种：客人端不应把它当作「目标语言」出现在译文里。
func TestSourceLangIsChinese(t *testing.T) {
	if SourceLang != "zh" {
		t.Errorf("本期原文语种应为 zh，实际 %q", SourceLang)
	}
}

// 锁定 IsSupportedLang 的判定边界：登记的 10 语种全放行；未登记的（it/ru）、
// 空串、大写变体（EN）、带地区后缀（en-US）一律 false。
// 大小写敏感是刻意设计：入参的规整责任在 normalizeTargets，不在这里放水。
func TestIsSupportedLang(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{"en", true}, {"ja", true}, {"vi", true}, {"zh", true},
		{"it", false}, // 意大利语：验证过质量差，本期不登记
		{"ru", false},
		{"", false},
		{"EN", false}, // 大小写敏感：入参应先在 normalizeTargets 里规整
		{"en-US", false},
	}
	for _, c := range cases {
		if got := IsSupportedLang(c.code); got != c.want {
			t.Errorf("IsSupportedLang(%q)=%v，期望 %v", c.code, got, c.want)
		}
	}
}

// 质量分层口径：Tier 1 只应包含中文与英语（对外可承诺质量的两门）。
func TestTier1IsOnlyChineseAndEnglish(t *testing.T) {
	for _, l := range SupportedLangs {
		if l.Tier == 1 && l.Code != "zh" && l.Code != "en" {
			t.Errorf("%s 被划入 Tier 1，但本期只承诺 zh/en", l.Code)
		}
	}
}
