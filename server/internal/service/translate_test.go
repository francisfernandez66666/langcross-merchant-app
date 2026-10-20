package service

import (
	"context"
	"errors"
	"testing"
)

/* ── 测试替身 ─────────────────────────────────────────────── */

// fakeTranslator 是可控的翻译引擎桩：按预设返回固定结果或错误。
// 用它复现「主引擎硬失败返回 nil」「返回短切片」等边界，无需真实网络。
type fakeTranslator struct {
	name string
	out  []string
	err  error
	got  []string // 记录收到的入参，便于断言「缓存命中时不调引擎」
}

// Translate 实现 Translator：记录收到的入参（缓存测试靠它断言
// 「命中时没调引擎」），并按预设返回结果或错误。
func (f *fakeTranslator) Translate(_ context.Context, texts []string, _, _ string) ([]string, error) {
	f.got = texts
	return f.out, f.err
}

// Ready 恒真：本桩用于行为测试，不参与可达性口径。
func (f *fakeTranslator) Ready() bool { return true }

// Name 返回预设名，供断言「最终用了哪个引擎」。
func (f *fakeTranslator) Name() string { return f.name }

// fakeCache 是内存版翻译记忆，行为与 repo 的 translation_cache 表一致。
type fakeCache struct {
	store map[string]string
	saved map[string]string
}

func ck(from, to, text string) string { return from + "|" + to + "|" + text }

func newFakeCache() *fakeCache {
	return &fakeCache{store: map[string]string{}, saved: map[string]string{}}
}

// LookupTranslations 实现 TranslationCache：内存命中即返回，
// 行为与 repo 的 translation_cache 表一致（ miss 返回空 map 而非错误）。
func (c *fakeCache) LookupTranslations(_ context.Context, from, to string, texts []string) (map[string]string, error) {
	out := map[string]string{}
	for _, t := range texts {
		if v, ok := c.store[ck(from, to, t)]; ok {
			out[t] = v
		}
	}
	return out, nil
}

// SaveTranslations 实现 TranslationCache：只记录不淘汰，
// 测试通过 saved 字段断言「什么被回写了 TM」。
func (c *fakeCache) SaveTranslations(_ context.Context, from, to string, pairs map[string]string, _ string) error {
	for k, v := range pairs {
		c.saved[ck(from, to, k)] = v
	}
	return nil
}

/* ── FallbackTranslator ───────────────────────────────────── */

// ★ 回归测试：主引擎硬失败时返回 nil 切片，兜底结果不能被丢弃。
// 这是真实踩过的坑——`for i := range out` 对 nil 迭代 0 次，导致离线词典兜底整体失效。
func TestFallbackTranslator_PrimaryHardFailStillFillsFallback(t *testing.T) {
	primary := &fakeTranslator{name: "primary", out: nil, err: errors.New("chain unreachable")}
	fallback := &fakeTranslator{name: "dict", out: []string{"Braised Pork Belly", "Mapo Tofu"}}

	f := &FallbackTranslator{primary: primary, fallback: fallback}
	got, err := f.Translate(context.Background(), []string{"红烧肉", "麻婆豆腐"}, "zh", "en")

	if err != nil {
		t.Fatalf("兜底补齐后不应抛错，实际 err=%v", err)
	}
	want := []string{"Braised Pork Belly", "Mapo Tofu"}
	if len(got) != len(want) {
		t.Fatalf("长度不符：got=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项：want=%q got=%q", i, want[i], got[i])
		}
	}
}

// 主引擎返回「短切片」时，缺失位置也要由兜底补上。
func TestFallbackTranslator_PrimaryShortSlice(t *testing.T) {
	primary := &fakeTranslator{name: "primary", out: []string{"Kung Pao Chicken"}}
	fallback := &fakeTranslator{name: "dict", out: []string{"", "Mapo Tofu"}}

	f := &FallbackTranslator{primary: primary, fallback: fallback}
	got, err := f.Translate(context.Background(), []string{"宫保鸡丁", "麻婆豆腐"}, "zh", "en")
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if got[0] != "Kung Pao Chicken" || got[1] != "Mapo Tofu" {
		t.Errorf("回填不正确：%v", got)
	}
}

// 主引擎完全成功时，不应调用兜底引擎（省一次无谓请求）。
func TestFallbackTranslator_PrimarySuccessSkipsFallback(t *testing.T) {
	primary := &fakeTranslator{name: "primary", out: []string{"A", "B"}}
	fallback := &fakeTranslator{name: "dict", err: errors.New("不该被调用")}

	f := &FallbackTranslator{primary: primary, fallback: fallback}
	got, err := f.Translate(context.Background(), []string{"甲", "乙"}, "zh", "en")
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if got[0] != "A" || got[1] != "B" {
		t.Errorf("结果被兜底污染：%v", got)
	}
	if fallback.got != nil {
		t.Errorf("主引擎成功时不应调用兜底引擎")
	}
}

// 两个引擎都失败：错误必须向上抛，让调用方知道「这批没翻成」。
func TestFallbackTranslator_BothFailPropagatesError(t *testing.T) {
	perr := errors.New("primary down")
	primary := &fakeTranslator{name: "primary", out: nil, err: perr}
	fallback := &fakeTranslator{name: "dict", out: nil, err: errors.New("dict down")}

	f := &FallbackTranslator{primary: primary, fallback: fallback}
	got, err := f.Translate(context.Background(), []string{"甲"}, "zh", "en")
	if !errors.Is(err, perr) {
		t.Fatalf("应保留主引擎错误，实际 err=%v", err)
	}
	if len(got) != 1 || got[0] != "" {
		t.Errorf("应返回等长空串切片（不编造），实际 %v", got)
	}
}

// 主引擎返回 nil 且兜底也无结果时，必须返回「等长空串」而不是 nil——
// 上层普遍按下标取值，nil 会引发越界或静默丢数据。
func TestFallbackTranslator_NeverReturnsNilSlice(t *testing.T) {
	f := &FallbackTranslator{
		primary:  &fakeTranslator{name: "p", out: nil, err: errors.New("x")},
		fallback: &fakeTranslator{name: "f", out: nil},
	}
	got, _ := f.Translate(context.Background(), []string{"甲", "乙", "丙"}, "zh", "en")
	if len(got) != 3 {
		t.Fatalf("必须返回等长切片，实际 len=%d", len(got))
	}
}

/* ── DictTranslator（离线兜底） ────────────────────────────── */

// 锁定词典的能力边界：只覆盖中→英；其它语种一律留空，
// 绝不拿英文冒充日语——「留白」比「错译」体面。
func TestDictTranslator_OnlyCoversChineseToEnglish(t *testing.T) {
	d := DictTranslator{}
	// 中→英：命中内置词典
	out, _ := d.Translate(context.Background(), []string{"宫保鸡丁", "米饭"}, "zh", "en")
	if out[0] != "Kung Pao Chicken" || out[1] != "Steamed Rice" {
		t.Errorf("词典未命中：%v", out)
	}
	// 其它语种：一律留空，绝不拿英文冒充
	ja, _ := d.Translate(context.Background(), []string{"宫保鸡丁"}, "zh", "ja")
	if ja[0] != "" {
		t.Errorf("词典不应覆盖非英语，实际 %q", ja[0])
	}
}

// 锁定「绝不编造」纪律：未收录词必须返回空串，
// 由上层标注「未翻译」，而不是猜一个译文。
func TestDictTranslator_UnknownWordReturnsEmpty(t *testing.T) {
	d := DictTranslator{}
	out, _ := d.Translate(context.Background(), []string{"香煎鲈鱼配时蔬"}, "zh", "en")
	if out[0] != "" {
		t.Errorf("未收录词必须留空（不编造），实际 %q", out[0])
	}
}

/* ── CachedTranslator（翻译记忆） ─────────────────────────── */

// 未命中：调引擎 + 回写 TM。
func TestCachedTranslator_MissCallsEngineAndSaves(t *testing.T) {
	cache := newFakeCache()
	inner := &fakeTranslator{name: "engine", out: []string{"Kung Pao Chicken"}}

	c := NewCachedTranslator(inner, cache)
	got, err := c.Translate(context.Background(), []string{"宫保鸡丁"}, "zh", "en")
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if got[0] != "Kung Pao Chicken" {
		t.Errorf("结果不符：%v", got)
	}
	if cache.saved[ck("zh", "en", "宫保鸡丁")] != "Kung Pao Chicken" {
		t.Errorf("未回写翻译记忆：%v", cache.saved)
	}
}

// 命中：直接返回缓存，且不调用引擎（省配额）。
func TestCachedTranslator_HitSkipsEngine(t *testing.T) {
	cache := newFakeCache()
	cache.store[ck("zh", "en", "宫保鸡丁")] = "Kung Pao Chicken"
	inner := &fakeTranslator{name: "engine", err: errors.New("引擎不该被调用")}

	c := NewCachedTranslator(inner, cache)
	got, err := c.Translate(context.Background(), []string{"宫保鸡丁"}, "zh", "en")
	if err != nil {
		t.Fatalf("命中缓存时不应报错：%v", err)
	}
	if got[0] != "Kung Pao Chicken" {
		t.Errorf("未走缓存：%v", got)
	}
	if inner.got != nil {
		t.Errorf("命中缓存却调用了引擎")
	}
}

// 空串入参不应写进 TM（否则会把「未翻译」污染成永久缓存）。
func TestCachedTranslator_SkipsBlankInput(t *testing.T) {
	cache := newFakeCache()
	inner := &fakeTranslator{name: "engine", out: []string{""}}

	c := NewCachedTranslator(inner, cache)
	got, err := c.Translate(context.Background(), []string{"", "  "}, "zh", "en")
	if err != nil {
		t.Fatalf("不应报错：%v", err)
	}
	if got[0] != "" || got[1] != "" {
		t.Errorf("空入参应原样返回空：%v", got)
	}
	if len(cache.saved) != 0 {
		t.Errorf("空入参不应写入 TM：%v", cache.saved)
	}
}

/* ── 装配 ─────────────────────────────────────────────────── */

// NewTranslator：配了自建链就走自建链；没配就走 MyMemory；Name() 反映真实引擎。
func TestNewTranslator_PicksEngineByConfig(t *testing.T) {
	if got := NewTranslator("http://chain.local", "", nil).Name(); got != "langcross-chain" {
		t.Errorf("配了 LC_TRANSLATE_URL 应走自建链，实际 %q", got)
	}
	if got := NewTranslator("", "", nil).Name(); got != "mymemory" {
		t.Errorf("未配自建链应走 MyMemory，实际 %q", got)
	}
}

// 传了 cache 时应包一层 TM：Ready/Name 透传内层，行为不变。
func TestNewTranslator_WithCacheKeepsName(t *testing.T) {
	tr := NewTranslator("", "", newFakeCache())
	if tr.Name() != "mymemory" {
		t.Errorf("包了 TM 后 Name 应透传，实际 %q", tr.Name())
	}
	if !tr.Ready() {
		t.Errorf("Ready 应透传")
	}
}

/* ── 辅助函数 ─────────────────────────────────────────────── */

// 锁定 normalizeLen 三态：nil 补等长空串、短切片补齐保序、
// 超长截断。这是所有兜底路径的地基——上层一律按下标取值。
func TestNormalizeLen(t *testing.T) {
	if got := normalizeLen(nil, 3); len(got) != 3 || got[0] != "" {
		t.Errorf("nil 应补成等长空串：%v", got)
	}
	if got := normalizeLen([]string{"a"}, 3); len(got) != 3 || got[0] != "a" {
		t.Errorf("短切片应补齐且保留原值：%v", got)
	}
	if got := normalizeLen([]string{"a", "b", "c"}, 2); len(got) != 2 {
		t.Errorf("超长应截断：%v", got)
	}
}

// 锁定 hasEmpty 判定：含空串为 true、无空洞为 false、空切片为 false。
// 契约：只在 normalizeLen 之后调用，长度已对齐，不存在歧义。
func TestHasEmpty(t *testing.T) {
	if hasEmpty([]string{"a", "b"}) {
		t.Errorf("无空洞时不应判定为 true")
	}
	if !hasEmpty([]string{"a", ""}) || !hasEmpty([]string{""}) {
		t.Errorf("存在空串时应判定为 true")
	}
	// 空切片 = 没有位置 = 没有空洞。
	// 注意契约：hasEmpty 只在 normalizeLen(out, len(texts)) 之后调用，
	// 此时切片长度已与入参对齐，不会出现「长度不符」的歧义。
	if hasEmpty(nil) {
		t.Errorf("空切片没有位置，应判定为 false")
	}
}

// 主引擎「有值但有空洞」时，空洞也要交给兜底补齐（内置词典是人工校对的，比留空有用）。
func TestFallbackTranslator_FillsHolesFromFallback(t *testing.T) {
	primary := &fakeTranslator{name: "engine", out: []string{"Kung Pao Chicken", ""}}
	fallback := &fakeTranslator{name: "dict", out: []string{"", "Mapo Tofu"}}

	f := &FallbackTranslator{primary: primary, fallback: fallback}
	got, err := f.Translate(context.Background(), []string{"宫保鸡丁", "麻婆豆腐"}, "zh", "en")
	if err != nil {
		t.Fatalf("补齐后不应报错：%v", err)
	}
	if got[0] != "Kung Pao Chicken" || got[1] != "Mapo Tofu" {
		t.Errorf("空洞未补齐：%v", got)
	}
}
