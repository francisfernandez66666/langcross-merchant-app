package assist

import (
	"context"
	"regexp"
	"strings"
)

// AskInput 是一次问答的全部输入。
type AskInput struct {
	AccountID string // 会话归属（流程状态按账号隔离）
	Question  string // 商家的问题
	UI        lang   // 界面语言（预留多语；当前固定 zh）
	Facts     []Fact // 服务端注入的结构化事实（本店经营数字）
	StoreName string // 门店名（提示词语境）
	LLM       LLMConfig
}

// lang 暂时只是字符串别名：预留商家端多语，本期固定 zh。
type lang = string

// Answer 是 Respond 的产出别名（对上层只暴露一个名字）。
type Answer = Reply

// Respond 是唯一出站咽喉：四道主体 + 出站守卫链。
//
// 与翻译助手 engine.Respond 同构，守卫顺序也刻意一致：
// 先跑主体拿到候选稿，再依次过「语言收口 → 数字核验 → 承诺边界」。
func (e *Engine) Respond(ctx context.Context, in AskInput) *Reply {
	q := strings.TrimSpace(in.Question)
	if q == "" {
		return &Reply{Content: "你想问什么？可以问我怎么翻译菜单、怎么核销券，或者这家店现在的经营情况。", Source: "fallback"}
	}

	rep := e.respond(ctx, in, q)
	rep = e.guardNumbers(rep, in.Facts)  // ★ 第一纪律：经营数字只能来自服务端
	rep = e.guardPromises(rep, in.Facts) // 承诺边界：不保证结果，只陈述事实
	return rep
}

// respond 四道主体（与参考实现的 respond 同构）。
func (e *Engine) respond(ctx context.Context, in AskInput, q string) *Reply {
	// 第 1 道：进行中的流程
	if rep := e.advanceFlow(ctx, in.AccountID, q); rep != nil {
		return rep
	}
	// 第 2 道：流程触发
	// ★ 为什么流程触发排在第 2 而不是第 3：「手把手教我」这类意图极其明确，
	// 而它往往同时含「翻译」这类泛词。若先跑话术直配，用户会被一条
	// 「怎么翻译菜单」的长答案接管，永远进不了分步引导——这正是
	// 早期版本的实测问题（问「手把手教我翻译菜单」却答操作三步）。
	if key, ok := e.MatchFlow(q); ok {
		return e.enterFlow(ctx, in.AccountID, key)
	}
	// 第 3 道：话术直配（平台侧知识，免 LLM）
	if sc, ok := e.MatchScript(q); ok {
		// 复合意图（同时问了平台知识 + 本店数据）时不让位直配：
		// 例如「怎么翻菜单 + 我家现在翻了多少」需要两边信息
		if !e.isCompound(q) {
			return &Reply{Content: sc.Content, Actions: e.actionsFor(sc.Key), Source: "rule", Hits: []string{sc.Key}}
		}
		hits := []KnowledgeEntry{sc}
		hits = append(hits, e.RetrieveKB(q, 3)...)
		return e.llmReply(ctx, in, q, hits)
	}
	// 第 4 道：知识库检索 + LLM + 兜底
	hits := e.RetrieveKB(q, 4)
	return e.llmReply(ctx, in, q, hits)
}

// advanceFlow 推进在途流程；不在流程中或被新意图抢占时返回 nil。
func (e *Engine) advanceFlow(ctx context.Context, accountID, q string) *Reply {
	st := e.sessionOf(ctx, accountID)
	if st == nil {
		return nil
	}
	steps := e.flows[st.Flow]
	if len(steps) == 0 {
		e.ClearSession(ctx, accountID)
		return nil
	}
	// 让位：用户明显问了别的（命中话术或别的流程），退出当前流程
	if _, ok := e.MatchScript(q); ok {
		e.ClearSession(ctx, accountID)
		return nil
	}
	if other, ok := e.MatchFlow(q); ok && other != st.Flow {
		e.ClearSession(ctx, accountID)
		return e.enterFlow(ctx, accountID, other)
	}
	next := st.Step + 1
	if next >= len(steps) {
		e.ClearSession(ctx, accountID)
		return &Reply{Content: steps[len(steps)-1].Ask, Flow: st.Flow, Source: "flow"}
	}
	e.setSession(ctx, accountID, st.Flow, next)
	step := steps[next]
	return &Reply{Content: step.Ask, Actions: step.Actions, Flow: st.Flow, Source: "flow"}
}

// enterFlow 进入流程第一步。
func (e *Engine) enterFlow(ctx context.Context, accountID, key string) *Reply {
	steps := e.flows[key]
	if len(steps) == 0 {
		return nil
	}
	e.setSession(ctx, accountID, key, 0)
	return &Reply{Content: steps[0].Ask, Actions: steps[0].Actions, Flow: key, Source: "flow"}
}

// llmReply 第 4 道：有 LLM 且注入了事实时让模型组织语言；
// 否则走确定性兜底（只用知识库命中拼答案，绝不编造）。
func (e *Engine) llmReply(ctx context.Context, in AskInput, q string, hits []KnowledgeEntry) *Reply {
	if in.LLM.Enabled() {
		c := newLLMClient(in.LLM)
		out, err := c.chat(ctx, e.systemPrompt(in, hits), e.userPrompt(q))
		if err == nil && strings.TrimSpace(out) != "" {
			keys := make([]string, 0, len(hits))
			for _, h := range hits {
				keys = append(keys, h.Key)
			}
			return &Reply{Content: out, Model: in.LLM.Model, Source: "llm", Hits: keys}
		}
		// LLM 失败：落到确定性兜底（模型不可用不是停摆的理由）
	}
	return e.fallbackReply(in, hits)
}

// fallbackReply 无 LLM 时的确定性回答。
//
// 三条规则：
//  1. 有知识库命中 → 直接把命中内容并排给出（人写的内容，天然可信）；
//  2. 没有命中但注入了事实 → 只陈述事实，不给解读（解读容易编）；
//  3. 什么都没有 → 明确说不知道，并给下一步建议（绝不编造）。
func (e *Engine) fallbackReply(in AskInput, hits []KnowledgeEntry) *Reply {
	if len(hits) > 0 {
		var b strings.Builder
		keys := make([]string, 0, len(hits))
		first := true // 用「是否已写过内容」而不是循环下标：下标会被 continue 跳过，导致首行前多出空行
		for _, h := range hits {
			if h.Script || h.Content == "" {
				continue
			}
			if !first {
				b.WriteString("\n\n")
			}
			first = false
			b.WriteString(h.Title + "：" + h.Content)
			keys = append(keys, h.Key)
		}
		if b.Len() > 0 {
			return &Reply{Content: b.String(), Source: "fallback", Hits: keys}
		}
	}
	if len(in.Facts) > 0 && e.looksLikeDataQuestion(in.Question) {
		var b strings.Builder
		b.WriteString("我只能给你确定的事实：\n")
		for _, f := range in.Facts {
			b.WriteString("· " + f.Label + "：" + f.Value + "\n")
		}
		b.WriteString("\n解读这些数字需要更多背景，建议你先看「经营洞察」页的趋势。")
		return &Reply{Content: b.String(), Source: "fallback"}
	}
	return &Reply{
		Content: "这个我还不确定，不想给你编一个答案。你可以试试问我：怎么翻译菜单、支持哪些语言、怎么核销券，或者去「经营洞察」页看这家店的数据。",
		Source:  "fallback",
	}
}

// systemPrompt 组装系统提示词。
//
// 三条硬约束写进提示词（等价于翻译助手的承诺边界）：
//  1. 只能用【事实】里的数字，一个都不许多；
//  2. 不许承诺翻译质量、客流提升这类结果；
//  3. 不知道就说不知道。
//
// 提示词是概率性请求，真正的保证靠出站守卫——两件事都要做。
func (e *Engine) systemPrompt(in AskInput, hits []KnowledgeEntry) string {
	var b strings.Builder
	b.WriteString("你是 LangCross 商家店端的经营助手，服务对象是门店商家。\n")
	b.WriteString("回答要求：\n")
	b.WriteString("1. 只使用【事实】一节给出的数字，禁止自己计算、估算或补充任何数字；事实里没有的就说「这个我没有数据」。\n")
	b.WriteString("2. 不承诺结果（不许说「保证提升」「一定有效」），只陈述事实与产品能力。\n")
	b.WriteString("3. 简明、直接、可操作；中文回答；不超过 200 字。\n")
	if in.StoreName != "" {
		b.WriteString("4. 当前门店：" + in.StoreName + "。\n")
	}
	if len(hits) > 0 {
		b.WriteString("\n【参考知识】\n")
		for _, h := range hits {
			if h.Content != "" {
				b.WriteString("- " + h.Title + "：" + h.Content + "\n")
			}
		}
	}
	if len(in.Facts) > 0 {
		b.WriteString("\n【事实】（唯一合法数字来源）\n")
		for _, f := range in.Facts {
			b.WriteString("- " + f.Label + "：" + f.Value + "\n")
		}
	}
	return b.String()
}

func (e *Engine) userPrompt(q string) string { return q }

// ── 出站守卫 ─────────────────────────────────────────────

// 数字正则：整数 / 小数 / 百分数。
var numberRe = regexp.MustCompile(`\d+(?:\.\d+)?%?`)

// guardNumbers 数字核验：正文里出现的每个数字，必须能在注入的事实里找到原样出现。
//
// ★ 这是本助手的第一纪律，也是翻译助手「报价核验」的对应物：
// 经营数字是商家做决策的依据，编一个数的代价比答不上来高得多。
// 判据刻意从严——只要数字没在事实里原样出现，就整句丢弃：
// 「4.2 分」这种事实里有的放行，「大约 4.5」这种没有的删掉。
// fail-soft：没有注入任何事实时本腿不判（此时问题与经营数字无关，
// 硬删会把正常回答删成碎片）。
func (e *Engine) guardNumbers(rep *Reply, facts []Fact) *Reply {
	if rep == nil || len(facts) == 0 {
		return rep
	}
	allowed := map[string]bool{}
	for _, f := range facts {
		for _, m := range numberRe.FindAllString(f.Value, -1) {
			allowed[m] = true
		}
	}
	// 年份、少量常识性数字白名单（避免把「10 个语种」也当编造）
	for _, w := range []string{"10", "5", "1", "2", "3", "14", "100", "2026"} {
		allowed[w] = true
	}
	kept := make([]string, 0, 4)
	for _, line := range splitLines(rep.Content) {
		if lineHasUnauthorizedNumber(line, allowed) {
			continue // 整行丢弃：半句留着比整句假数字更危险
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		// 全被判掉：退回只陈述事实，不留空气泡
		var b strings.Builder
		b.WriteString("我只能给你确定的事实：\n")
		for _, f := range facts {
			b.WriteString("· " + f.Label + "：" + f.Value + "\n")
		}
		rep.Content = b.String()
		return rep
	}
	rep.Content = strings.Join(kept, "\n")
	return rep
}

// lineHasUnauthorizedNumber 判断一行里是否有「事实集合外」的数字。
func lineHasUnauthorizedNumber(line string, allowed map[string]bool) bool {
	for _, m := range numberRe.FindAllString(line, -1) {
		if !allowed[m] {
			return true
		}
	}
	return false
}

// guardPromises 承诺边界：出现保证类措辞的句子整句替换为确定性表述。
// 模型爱说「保证提升客流」——这种话我们不能替商家承诺。
func (e *Engine) guardPromises(rep *Reply, facts []Fact) *Reply {
	if rep == nil {
		return rep
	}
	banned := []string{"保证", "一定会", "百分百", "100%", "绝对", "肯定能"}
	lines := splitLines(rep.Content)
	changed := false
	for i, line := range lines {
		for _, b := range banned {
			if strings.Contains(line, b) {
				lines[i] = "我能做的是把事实讲清楚，具体效果取决于你的菜品与服务。"
				changed = true
				break
			}
		}
	}
	if changed {
		rep.Content = strings.Join(lines, "\n")
	}
	return rep
}

// splitLines 按行切割（保留空行结构，便于拼回时排版不塌）。
func splitLines(s string) []string { return strings.Split(s, "\n") }

// isCompound 判断是否为复合意图（同时问了平台知识和本店数据）。
// 判据很简单：问题里既有平台侧关键词、又有数据向关键词。
func (e *Engine) isCompound(q string) bool {
	dataish := e.normalize(q)
	dataHits := 0
	for _, kw := range []string{"多少", "现在", "我这", "本店", "我家", "数据", "情况", "怎么样"} {
		if strings.Contains(dataish, kw) {
			dataHits++
		}
	}
	return dataHits > 0
}

// looksLikeDataQuestion 判断问题是否在问本店数据（兜底时决定给不给事实）。
//
// ★ 关键词必须「数据向」而不能「语气向」：早期版本收了「怎么样」，
// 于是「明天天气怎么样」也被判定成数据问题，兜底就把整份事实清单倒出来——
// 既没答上问题，又暴露了一堆 0，商家只会觉得助手在敷衍。
// 现在只认「多少 / 现在 / 本店 / 扫码」这类明确指向门店数据的词。
func (e *Engine) looksLikeDataQuestion(q string) bool {
	n := e.normalize(q)
	for _, kw := range []string{
		"多少", "现在", "我这", "本店", "我家", "情况", "数据",
		"评分", "核销", "几张", "几个", "扫码", "评价", "卖", "营业",
	} {
		if strings.Contains(n, kw) {
			return true
		}
	}
	return false
}

// actionsFor 按知识 key 给出建议动作（对应参考实现的 FeatureLinksByKey）。
func (e *Engine) actionsFor(key string) []Action {
	switch key {
	case "how-translate":
		return []Action{{Key: "menu", Name: "去录入菜单", URL: "/admin/menu"}}
	case "coupon-redeem":
		return []Action{{Key: "redeem", Name: "去核销台", URL: "/admin/redeem"}}
	case "qr-material":
		return []Action{{Key: "materials", Name: "去下载二维码", URL: "/admin/materials"}}
	case "dish-image":
		return []Action{{Key: "menu", Name: "去菜单管理", URL: "/admin/menu"}}
	}
	return nil
}
