package assist

import (
	"context"
	"strings"
	"testing"
)

// 本文件：经营助手引擎的行为测试。
//
// 覆盖四条最容易回归的纪律：
//  1. 专词必须压过泛词（问「未翻译」不能答成「怎么翻译菜单」）；
//  2. 零命中就必须零分（不能拿不相干的知识硬答）；
//  3. ★ 数字只能来自注入的事实，模型 / 上层说的数字整行丢弃；
//  4. 无 LLM、无知识命中时明确说不知道，而不是编一个答案。

func newTestEngine() *Engine { return New() }

func respond(e *Engine, q string, facts []Fact) *Reply {
	return e.Respond(context.Background(), AskInput{AccountID: "acc-test", Question: q, UI: "zh", Facts: facts})
}

/* ── 第 2 道：话术直配 ─────────────────────────────── */

// 验证专词优先于泛词：「客人端显示未翻译怎么办」同时含泛词「翻译」，必须
// 命中 untranslated-mark 而非 how-translate，否则答非所问。
func TestMatchScript_SpecificBeatsGeneric(t *testing.T) {
	e := newTestEngine()
	// 「未翻译」同时含泛词「翻译」；专词必须赢，否则答非所问
	sc, ok := e.MatchScript("客人端显示未翻译怎么办")
	if !ok {
		t.Fatal("应命中话术直配")
	}
	if sc.Key != "untranslated-mark" {
		t.Errorf("专词未压过泛词：命中 %q，期望 untranslated-mark", sc.Key)
	}
}

// 验证专词缺席时泛词话术仍能命中：纯「怎么翻译菜单」类问题应落到
// how-translate，防止专词优先规则把泛词问题误伤成不命中。
func TestMatchScript_GenericStillWorks(t *testing.T) {
	e := newTestEngine()
	for _, q := range []string{"怎么翻译菜单", "菜单怎么翻成外语"} {
		sc, ok := e.MatchScript(q)
		if !ok {
			t.Fatalf("%q 应命中 how-translate", q)
		}
		if sc.Key != "how-translate" {
			t.Errorf("%q 命中 %q，期望 how-translate", q, sc.Key)
		}
	}
}

// 验证知识库零命中时返回空列表：检索不到就不能拿不相干条目硬凑，
// 「零命中」是上层选择承认不知道而非硬答的前提。
func TestRetrieveKB_NoHitReturnsEmpty(t *testing.T) {
	e := newTestEngine()
	if hits := e.RetrieveKB("明天天气怎么样适合洗车吗", 5); len(hits) != 0 {
		t.Errorf("零命中应返回空，实际 %d 条：%v", len(hits), hits)
	}
}

// 验证多轮流程的触发识别：「手把手教我Translation一遍」应命中
// translate-guide 流程并返回流程 key。
func TestMatchFlow_Triggers(t *testing.T) {
	e := newTestEngine()
	key, ok := e.MatchFlow("手把手教我Translation一遍")
	if !ok || key != "translate-guide" {
		t.Errorf("应触发 translate-guide，实际 ok=%v key=%q", ok, key)
	}
}

/* ── 第 1 道：多轮流程 ─────────────────────────────── */

// 验证进入 translate-guide 后「下一步」「继续」等推进语应留在流程内
// 继续走，而不是每一轮都重新匹配意图、把用户打回入口。
func TestFlow_MultiTurnAdvances(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()
	// 第一步：进入流程
	r1 := e.Respond(ctx, AskInput{AccountID: "acc-1", Question: "手把手教我翻译菜单"})
	if r1.Source != "flow" || r1.Flow != "translate-guide" {
		t.Fatalf("应进入 translate-guide，实际 source=%q flow=%q", r1.Source, r1.Flow)
	}
	// 第二步：任意推进
	r2 := e.Respond(ctx, AskInput{AccountID: "acc-1", Question: "下一步"})
	if r2.Source != "flow" || r2.Flow != "translate-guide" {
		t.Errorf("第二步应仍在流程内，实际 source=%q flow=%q", r2.Source, r2.Flow)
	}
	// 第三步：仍应推进
	r3 := e.Respond(ctx, AskInput{AccountID: "acc-1", Question: "继续"})
	if r3.Source != "flow" {
		t.Errorf("第三步应仍在流程内，实际 source=%q", r3.Source)
	}
}

// 验证流程进行中遇到新的明确话术问题（「怎么核销优惠券」）应退出流程
// 并直配回答：引导不能把用户卡死在多轮里。
func TestFlow_YieldsToNewIntent(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()
	e.Respond(ctx, AskInput{AccountID: "acc-2", Question: "手把手教我翻译菜单"})
	// 问了一个明确的话术问题：应退出流程、直配回答
	r := e.Respond(ctx, AskInput{AccountID: "acc-2", Question: "怎么核销优惠券"})
	if r.Flow != "" {
		t.Errorf("命中话术后应退出流程，实际仍在 %q", r.Flow)
	}
	if r.Source != "rule" {
		t.Errorf("应命中话术直配，实际 source=%q", r.Source)
	}
}

// 验证流程会话按账号隔离：acc-B 发「下一步」不应继承 acc-A 已进入的
// translate-guide，多商家共用实例时不能串会话。
func TestFlow_SessionIsolatedByAccount(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()
	e.Respond(ctx, AskInput{AccountID: "acc-A", Question: "手把手教我翻译菜单"})
	// 另一个账号不应看到 A 的流程
	r := e.Respond(ctx, AskInput{AccountID: "acc-B", Question: "下一步"})
	if r.Flow != "" {
		t.Error("会话必须按账号隔离，B 不应继承 A 的流程")
	}
}

// 验证 SessionStore 抽象的初衷：两个引擎实例共享同一存储时，流程状态
// 跨实例延续，支撑多实例部署下请求落到不同进程仍能续上。
func TestFlow_DBStorePersistsAcrossEngineInstances(t *testing.T) {
	// 同一份 SessionStore、两个引擎实例（模拟多实例部署下的两次请求）
	store := NewMemorySessionStore()
	e1 := NewWithStore(store)
	e2 := NewWithStore(store)
	ctx := context.Background()

	if r := e1.Respond(ctx, AskInput{AccountID: "acc-x", Question: "手把手教我翻译菜单"}); r.Source != "flow" {
		t.Fatalf("e1 应进入流程，实际 %q", r.Source)
	}
	// e2 是新实例，但共享存储：应能接着推进（这正是抽 SessionStore 的目的）
	r := e2.Respond(ctx, AskInput{AccountID: "acc-x", Question: "下一步"})
	if r.Source != "flow" || r.Flow != "translate-guide" {
		t.Errorf("共享存储应让流程跨实例延续，实际 source=%q flow=%q", r.Source, r.Flow)
	}
}

/* ── 出站守卫：数字核验（第一纪律） ────────────────── */

func factsWith(vals ...string) []Fact {
	out := make([]Fact, 0, len(vals))
	for _, v := range vals {
		out = append(out, Fact{Key: "k", Label: "项目", Value: v})
	}
	return out
}

// 验证出站数字核验：事实里没有的数字（4.8）所在行整行丢弃，注入过的
// （4.2）保留，常识性白名单数字（10）不误删——回答只能引用注入的事实。
func TestGuardNumbers_UnauthorizedNumberDropsLine(t *testing.T) {
	e := newTestEngine()
	facts := factsWith("4.2")
	rep := &Reply{Content: "菜品评分 4.2 分，很不错。\n服务评分 4.8 分，需要改进。\n总分 9 分。"}
	got := e.guardNumbers(rep, facts)
	if strings.Contains(got.Content, "4.8") {
		t.Error("事实里没有的数字必须被丢弃")
	}
	if !strings.Contains(got.Content, "4.2") {
		t.Error("事实里有的数字必须保留")
	}
	// 白名单数字（10 / 2026 等）应保留，否则正常回答会被删成碎片
	rep2 := &Reply{Content: "目前支持 10 个语种。"}
	if got2 := e.guardNumbers(rep2, facts); !strings.Contains(got2.Content, "10") {
		t.Error("常识性白名单数字不应被误删")
	}
}

// 验证 fail-soft 边界：未注入任何事实时本腿不判，否则正常回答里的
// 数字会被删成碎片。
func TestGuardNumbers_NoFactsSkipsGuard(t *testing.T) {
	e := newTestEngine()
	rep := &Reply{Content: "建店只需要一个店名，大约 3 分钟搞定。"}
	got := e.guardNumbers(rep, nil)
	if got.Content != rep.Content {
		t.Error("未注入事实时本腿不判（fail-soft），否则会把正常回答删成碎片")
	}
}

// 验证整段数字都未授权时（只说了编造的 5.0）必须弃用该回答、退回只
// 陈述注入的事实，编造的数字不得原样露出。
func TestGuardNumbers_AllDroppedFallsBackToFacts(t *testing.T) {
	e := newTestEngine()
	facts := factsWith("4.2")
	rep := &Reply{Content: "评分 5.0 分。"}
	got := e.guardNumbers(rep, facts)
	if got.Content == "评分 5.0 分。" {
		t.Fatal("编造的数字不该原样留下")
	}
	if !strings.Contains(got.Content, "4.2") {
		t.Error("全判掉时应退回只陈述事实")
	}
}

// 验证承诺类措辞（「保证提升客流」）必须被替换，且未命中的行原样不动，
// 防止助手替商家做效果担保。
func TestGuardPromises_ReplacesGuaranteeWording(t *testing.T) {
	e := newTestEngine()
	rep := &Reply{Content: "用我们翻译，保证提升客流。\n第二行保持原样。"}
	got := e.guardPromises(rep, nil)
	if strings.Contains(got.Content, "保证提升") {
		t.Error("承诺类措辞必须被替换")
	}
	if !strings.Contains(got.Content, "第二行保持原样") {
		t.Error("未命中的行不应被动到")
	}
}

/* ── 兜底：绝不编造 ────────────────────────────────── */

// 验证无话术、无知识命中的不相干问题走 fallback 且明说「还不确定」，
// 绝不编一个答案。
func TestFallback_UnrelatedQuestionAdmitsUnknown(t *testing.T) {
	e := newTestEngine()
	r := respond(e, "明天天气怎么样适合洗车吗", nil)
	if r.Source != "fallback" {
		t.Errorf("无知识命中应走 fallback，实际 %q", r.Source)
	}
	if !strings.Contains(r.Content, "还不确定") {
		t.Errorf("不该编答案，实际：%s", r.Content)
	}
}

// 验证数据类问题（「有多少扫码」）在 fallback 路径应给出注入的事实值
// 12，让商家看到真实口径的数据。
func TestFallback_DataQuestionGetsFacts(t *testing.T) {
	e := newTestEngine()
	r := respond(e, "我家店现在有多少扫码", factsWith("12"))
	if !strings.Contains(r.Content, "12") {
		t.Errorf("数据类问题应给出注入的事实，实际：%s", r.Content)
	}
	if strings.Contains(r.Content, "解读") {
		t.Log("提示解读需更多背景，属预期")
	}
}

// 回归点：「怎么样」曾被当成数据向关键词，导致天气问题也倒一整套事实
// 清单；验证非数据类问题不得倾倒口径说明。
func TestFallback_UnrelatedQuestionDoesNotDumpFacts(t *testing.T) {
	e := newTestEngine()
	// 回归测试：早期「怎么样」被当成数据向关键词，天气问题也倒一整套事实
	r := respond(e, "明天天气怎么样", factsWith("0"))
	if strings.Contains(r.Content, "口径说明") {
		t.Errorf("非数据类问题不该倒事实清单，实际：%s", r.Content)
	}
}

// 验证空白问题返回引导语而非空回复：trim 后为空时仍走 fallback 且内容
// 非空，防止前端渲染出空气泡。
func TestRespond_EmptyQuestionGetsHint(t *testing.T) {
	e := newTestEngine()
	r := respond(e, "   ", nil)
	if r.Source != "fallback" || strings.TrimSpace(r.Content) == "" {
		t.Error("空问题应给出引导而不是空白")
	}
}

/* ── 事实构造 ─────────────────────────────────────── */

// 验证统计值转事实的排序与文本化：按 key 排序保证提示词可复现，小数
// 保留一位、整数不带小数点，否则提示词里的数字每次生成都不一样。
func TestFactsFromStats_SortedAndTextified(t *testing.T) {
	facts := FactsFromStats(map[string]any{"b": 2, "a": 1.5, "c": "x"})
	if len(facts) != 3 {
		t.Fatalf("应转出 3 条事实，实际 %d", len(facts))
	}
	// 排序稳定（不依赖 map 遍历顺序）：提示词里的事实顺序可复现
	if facts[0].Key != "a" || facts[1].Key != "b" || facts[2].Key != "c" {
		t.Errorf("事实应按 key 排序，实际 %s/%s/%s", facts[0].Key, facts[1].Key, facts[2].Key)
	}
	if facts[0].Value != "1.5" {
		t.Errorf("小数应保留一位，实际 %q", facts[0].Value)
	}
	if facts[1].Value != "2" {
		t.Errorf("整数值不该带小数点，实际 %q", facts[1].Value)
	}
}

// 表格驱动锁定 formatValue 的类型映射：float 整数去小数点、小数留一位、
// 布尔转「是/否」、字符串原样，任何类型的文本化口径都不能漂移。
func TestFormatValue_IntegersHaveNoDecimalPoint(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{float64(4), "4"}, {float64(4.25), "4.2"}, {7, "7"}, {int64(9), "9"}, {true, "是"}, {false, "否"}, {"原文", "原文"},
	}
	for _, c := range cases {
		if got := formatValue(c.in); got != c.want {
			t.Errorf("formatValue(%v) = %q，期望 %q", c.in, got, c.want)
		}
	}
}
