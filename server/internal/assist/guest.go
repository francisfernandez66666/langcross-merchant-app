package assist

import (
	"context"
	"regexp"
	"strings"
)

// 本文件：客人端 AI 助手（门店客服 + 指导点菜）的引擎与出站守卫。
//
// 设计方向（用户明确要求）：「以菜品内容和服务内容作为 RAG 核心，
// 其他的主要扮演好一个门店客服和指导点菜的角色」，
// 并可接受语音输入、代客人执行服务动作（加菜 / 服务铃 / 加水）。
//
// ── 与商家端经营助手的分工 ─────────────────────────────
//
//	经营助手（assistant to 商家）  答「怎么翻译菜单」「数据口径」，知识来自平台
//	点菜助手（本文件，assistant to 客人） 答「有什么菜」「辣不辣」「多少钱」，
//	                                    知识来自本店菜品与服务（RAG）
//
// 二者共用同一套引擎骨架（四道主体 + 出站守卫），但知识源、提示词、
// 出站守卫都不同——最关键的差别在守卫：经营助手要守「经营数字不许编」，
// 点菜助手要守「菜品与价格不许编」。
//
// ── 助手能做什么、不能做什么 ───────────────────────────
//
// 能：介绍菜品、按忌口/辣度/预算推荐、说明价格与点单方式、答 WiFi 与营业时间、
//
//	给出「加菜 / 服务铃 / 加水」等动作按钮。
//
// 不能：直接替客人下单。所有改变门店状态的动作都由助手**提议**、客人**点一下确认**
//
//	才执行（见 GuestAction）。理由：客人可能只是问「这个辣吗」，
//	助手顺势就把菜加进订单，是对客人钱包的不尊重，也无法撤销。
//
// 这个「提议—确认」边界是与参考项目（能言 AI 销售助手）一致的纪律：
// 出站的所有承诺都要可验证、可撤销。
//
// ★ 绝不编造：菜单里没有的菜、没有的价格、没做过的优惠，一律不许出现。
// 出站守卫会逐条核对，对不上就整行丢弃——宁可答「这个我没有」，
// 也不能让外国客人按着一个不存在的菜名点单。

// GuestAskInput 是客人端一次问答的输入。
type GuestAskInput struct {
	SessionID string    // 会话标识（客人匿名，用 cookie 派生的 ID；预留给多轮点菜流程按它隔离）
	Question  string    // 客人的问题（语音转文字后也走这里）
	KB        *StoreKB  // 本店 RAG 知识库（菜品 + 服务）
	LLM       LLMConfig // 模型凭据（无则走规则兜底）
}

// ★ 客人端动作复用 Action（{Key,Name,URL,Arg}）而不新造一个类型：
// 商家端与客人端的按钮对前端是同一套渲染逻辑，拆两个类型只会让前端写两遍。
// 客人端只填 Key 与 Name，参数放 Arg（如加菜时的菜品 ID），URL 留空。
// Action 的定义见 assist.go。（商家端 / 客人端动作 key 常量见本文件末尾。）

// 动作 key 常量（与 httpapi 的 guest_request kind 对齐）。
const (
	ActionAddDish     = "add_dish"
	ActionServiceBell = "service_bell"
	ActionWater       = "water"
	ActionBill        = "bill"
)

// RespondGuest 是客人端助手的唯一出站咽喉。
//
// 主体只有两道（点菜场景不需要多轮流程编排那么复杂）：
//  1. 菜品检索：从 RAG 里取出与问题相关的候选菜品；
//  2. LLM 组织回答；无 LLM 时用确定性兜底（只陈述菜单里的事实）。
//
// 之后固定过三道守卫：菜品核验 → 价格核验 → 承诺边界。
func (e *Engine) RespondGuest(ctx context.Context, in GuestAskInput) *Reply {
	q := strings.TrimSpace(in.Question)
	if q == "" {
		return &Reply{Content: guestEmptyHint(in.KB), Source: "fallback"}
	}
	if in.KB == nil {
		return &Reply{Content: "我这边还没拿到这家店的菜单信息，请稍后再试或直接招手叫服务员。", Source: "fallback"}
	}

	rep := e.guestMain(ctx, in, q)
	if rep == nil {
		return &Reply{Content: guestUnknownHint(in.KB), Source: "fallback"}
	}
	// 出站守卫链（顺序固定，与经营助手同构）
	rep = e.guardCitedDishes(rep, in.KB)
	rep = e.guardPrices(rep, in.KB)
	rep = e.guardPromises(rep, nil)
	rep.Actions = e.guestActions(rep, in.KB)
	return rep
}

// guestMain 主体：检索候选菜品 → 让模型用【菜单】里的内容组织回答。
func (e *Engine) guestMain(ctx context.Context, in GuestAskInput, q string) *Reply {
	if in.LLM.Enabled() {
		c := newLLMClient(in.LLM)
		out, err := c.chat(ctx, e.guestSystemPrompt(in, q), q)
		if err == nil && strings.TrimSpace(out) != "" {
			return &Reply{Content: out, Source: "llm", Model: in.LLM.Model}
		}
		// LLM 失败：落确定性兜底（模型不可用不是让客人干等的理由）
	}
	return e.guestFallback(in, q)
}

// guestSystemPrompt 组装系统提示词。
//
// 硬约束按「最容易出事的排最前」排列：
//  1. 只能用【菜单】里的菜（这是防编造的第一道闸）
//  2. 价格必须原样复制（第二道闸）
//  3. 未翻译的菜不许露原文（呼应「未翻译内容暴露出来」这个老问题）
//  4. 用客人语言回答
//  5. 不承诺菜单外的事（折扣、上菜时间、过敏保证）
func (e *Engine) guestSystemPrompt(in GuestAskInput, q string) string {
	kb := in.KB
	var b strings.Builder
	b.WriteString("你是「" + kb.Service.StoreName + "」的点菜助手，服务来店的外国客人。\n")
	b.WriteString("回答要求（按优先级，违反任意一条都算失败）：\n")
	b.WriteString("1. 只能介绍【菜单】里出现的菜品。菜单里没有的菜，一律回答「这道菜不在今天的菜单上」。\n")
	b.WriteString("2. 价格必须从【菜单】原样复制，一个数字都不许多算、少算或换算；未定价的就说「价格请询问服务员」。\n")
	b.WriteString("3. 【菜单】里标记为 [[NO_NAME_IN_" + strings.ToUpper(kb.Lang) + "]] 的菜品，说明它还没有" + kb.LangName + "名字。这种情况下不要写出这道菜的原文名，改说「这道菜暂时没有" + kb.LangName + "介绍，可以问服务员」。\n")
	b.WriteString("4. 全程用" + kb.LangName + "回答；提到菜品时，菜名必须用 [[菜名]] 双方括号原样包裹，且菜名只能取自【菜单】。\n")
	b.WriteString("5. 不承诺菜单与服务信息里没有的事：不许承诺折扣、上菜时间、过敏绝对安全。涉及过敏只能说「这道菜标注了含 XX，请谨慎选择并告知服务员」。\n")
	b.WriteString("6. 客人问忌口 / 素食 / 辣度时，只依据【菜单】的「标签」判断，没有标签就说「菜单没标注，请问服务员」。\n")
	b.WriteString("7. 简短、口语、可直接照做；不超过 150 字。\n")

	b.WriteString("\n【门店服务】\n" + kb.FormatService())
	b.WriteString("\n【菜单】\n" + kb.FormatMenu(kb.Candidates(q)))
	return b.String()
}

// guestFallback 无 LLM 时的确定性回答：只陈述菜单里的事实。
//
// 三条规则与经营助手同构，但内容换成菜品：
//  1. 检索到候选菜品 → 列出来（菜名 / 价格 / 标签），不解读；
//  2. 检索不到 → 明说不知道，并建议叫服务员；
//  3. 菜单整个是空的 → 直说，并给服务铃动作。
func (e *Engine) guestFallback(in GuestAskInput, q string) *Reply {
	kb := in.KB
	hits := kb.Retrieve(q, 5)
	if len(hits) == 0 {
		return &Reply{Content: guestUnknownHint(kb), Source: "fallback"}
	}
	var b strings.Builder
	b.WriteString("菜单里这些可能相关：\n")
	for _, d := range hits {
		if d.SoldOut {
			continue // 兜底只列可点的：售罄的菜列出来只会让白跑一趟
		}
		b.WriteString("· " + guestDishLabel(d, kb) + "\n")
	}
	return &Reply{Content: b.String(), Source: "fallback"}
}

// guestDishLabel 一道菜的一行展示（含价格与标签；未翻译则不给原文名）。
func guestDishLabel(d DishDoc, kb *StoreKB) string {
	s := d.Name
	if !d.NameLang {
		s = "（暂无" + kb.LangName + "名称）"
	}
	if d.Price != "" {
		s += " " + d.Price
	}
	if len(d.Tags) > 0 {
		s += "（" + strings.Join(d.Tags, "/") + "）"
	}
	if d.SoldOut {
		s += " 已售罄"
	}
	return s
}

// guestEmptyHint 空问题的引导语（客人语言；没有模型时至少中文可用）。
func guestEmptyHint(kb *StoreKB) string {
	if kb != nil && kb.Lang != SourceLangFallback {
		return "Please tell me what you'd like to know about the menu."
	}
	return "想吃什么可以直接问我，比如「有没有不辣的」「推荐一道招牌菜」。"
}

// guestUnknownHint 答不上来时的诚实回答 + 兜底动作。
func guestUnknownHint(kb *StoreKB) string {
	if kb != nil && kb.Lang != SourceLangFallback {
		return "I'm not sure about that one — please press the service bell and our staff will help you."
	}
	return "这个我不太确定，不想给你编一个答案。可以按下面的服务铃叫服务员，或直接问我菜单里有什么。"
}

/* ── 出站守卫 ─────────────────────────────────────── */

// citedNameRe 匹配 [[菜名]] 标记。
// 提示词明确要求「提到菜品必须用 [[菜名]] 包裹」，因此这里能精确提取
// 模型到底引用了哪些菜——比从自然语言里猜菜名可靠得多。
var citedNameRe = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

// guardCitedDishes 菜品核验：模型引用的每个菜名，必须在【菜单】里原样存在。
//
// ★ 这是点菜助手的第一纪律，也是经营助手「数字核验」的对应物：
// 外国客人按着一个不存在的菜名点单，比答不上来糟糕得多。
// 判据从严——只认「菜单里出现过的完整菜名」；对不上就整行丢弃。
//
// 顺带 strips 掉 [[ ]] 标记本身：那是给守卫用的内部协议，不该展示给客人。
func (e *Engine) guardCitedDishes(rep *Reply, kb *StoreKB) *Reply {
	if rep == nil {
		return rep
	}
	allowed := kb.AllowedNames()
	kept := make([]string, 0, 4)
	for _, line := range splitLines(rep.Content) {
		bad := false
		for _, m := range citedNameRe.FindAllStringSubmatch(line, -1) {
			if !allowed[m[1]] {
				bad = true
				break
			}
		}
		if bad {
			continue // 整行丢弃：半句留着一个假菜名更危险
		}
		kept = append(kept, citedNameRe.ReplaceAllString(line, "$1"))
	}
	if len(kept) == 0 {
		rep.Content = guestUnknownHint(kb)
		return rep
	}
	rep.Content = strings.Join(kept, "\n")
	return rep
}

// priceRe 匹配带货币符号的价格（¥38 / ￥38 / $12 / 38元）。
var priceRe = regexp.MustCompile(`(?:¥|￥|\$)\s?\d+(?:\.\d+)?|\d+(?:\.\d+)?\s?元`)

// guardPrices 价格核验：出现过的每个价格，必须是菜单里某个菜的价格。
//
// fail-soft：菜单里一道菜都没定价时本腿不判（此时问价也只能答「问服务员」，
// 硬删会把整段回答删空）。
func (e *Engine) guardPrices(rep *Reply, kb *StoreKB) *Reply {
	if rep == nil {
		return rep
	}
	allowed := kb.AllowedPrices()
	if len(allowed) <= 1 { // 只有货币符号本身 = 没有任何菜定价
		return rep
	}
	kept := make([]string, 0, 4)
	for _, line := range splitLines(rep.Content) {
		bad := false
		for _, m := range priceRe.FindAllString(line, -1) {
			norm := strings.ReplaceAll(strings.TrimSpace(m), " ", "")
			if !allowed[norm] && !allowed[strings.TrimRight(norm, "元")] {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		rep.Content = "价格请以菜单标注为准，或询问服务员。"
	}
	rep.Content = strings.Join(kept, "\n")
	return rep
}

// guardNoRawChineseName 兜底：即便模型没按约定打标记，也不该把未翻译菜的
// 中文原名直接写给客人。这一腿是「提示词第 3 条」的兜底，双保险。
//
// 做法很克制：只删「[[NO_NAME_IN_XX]] 被替换成了原文」这种情况——
// 即回答里出现了某个未翻译菜的中文名。正常译文不受影响。
func (e *Engine) guardNoRawChineseName(rep *Reply, kb *StoreKB) *Reply {
	if rep == nil || kb == nil {
		return rep
	}
	untranslated := map[string]bool{}
	for _, d := range kb.Dishes {
		if d.NameLang {
			continue
		}
		for _, a := range d.Aliases {
			untranslated[a] = true
		}
	}
	if len(untranslated) == 0 {
		return rep
	}
	kept := make([]string, 0, 4)
	for _, line := range splitLines(rep.Content) {
		leak := false
		for name := range untranslated {
			// 只在「该行没有任何已译名」时才判泄露：否则可能是正常引用译文
			if strings.Contains(line, name) && !kb.AllowedNames()[name] {
				leak = true
				break
			}
		}
		if leak {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) > 0 {
		rep.Content = strings.Join(kept, "\n")
	}
	return rep
}

/* ── 动作提议（助手只提议，客人点一下才执行） ───────── */

// guestActions 根据回答内容提议服务动作。
//
// ★ 提议—确认边界：动作只以按钮形式给出，由客人点一下才真正发起请求。
// 助手永远不会代替客人下单，这是对客人钱包的基本尊重（也无法撤销）。
func (e *Engine) guestActions(rep *Reply, kb *StoreKB) []Action {
	if rep == nil || kb == nil {
		return nil
	}
	content := rep.Content
	out := []Action{}
	// 提到了具体菜品 → 提议「加菜」
	for _, d := range kb.Dishes {
		if !d.NameLang || d.SoldOut {
			continue
		}
		if strings.Contains(content, d.Name) {
			out = append(out, Action{
				Key: ActionAddDish, Name: addDishLabel(kb, d.Name), Arg: d.ID,
			})
			if len(out) >= 2 {
				break // 一次最多提议两道，避免按钮把回答挤出屏幕
			}
		}
	}
	// 问价 / 问忌口 / 问不上来的 → 提议服务铃（这些场景最需要真人）
	if strings.Contains(content, "服务员") || strings.Contains(content, "staff") {
		out = append(out, Action{Key: ActionServiceBell, Name: serviceBellLabel(kb)})
	}
	return out
}

// addDishLabel 「加菜」按钮文案：跟随客人语种——中文「加入订单：菜名」、外语「Add: 菜名」。
func addDishLabel(kb *StoreKB, dish string) string {
	if kb.Lang != SourceLangFallback {
		return "Add: " + dish
	}
	return "加入订单：" + dish
}

// serviceBellLabel 「服务铃」按钮文案：跟随客人语种——中文「叫服务员」、外语「Call staff」。
func serviceBellLabel(kb *StoreKB) string {
	if kb.Lang != SourceLangFallback {
		return "Call staff"
	}
	return "叫服务员"
}
