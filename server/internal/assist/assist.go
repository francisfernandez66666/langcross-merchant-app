// Package assist 是 LangCross 商家店端的「AI 经营助手」。
//
// 设计来源：/Users/zhangzifei/Desktop/翻译助手 的 internal/assist（接待引擎）。
// 逻辑机制一致，服务对象不同：那边接待 C 端访客（卖翻译服务），
// 这边接待商家（帮他看懂外语客数据、把菜单多语化做对）。
//
// ── 引擎四道（与参考实现的 respond 主体同构） ─────────────
//
//	第 1 道  进行中的多轮流程（如「怎么翻日语」分步引导）
//	第 2 道  关键词话术直配（命中即答，免 LLM，毫秒级）
//	第 3 道  流程触发（命中触发词则进入流程）
//	第 4 道  LLM + 知识库检索 + 确定性兜底（无 LLM 时也不许编造）
//
// ── 出站守卫链（本包里最关键的部分） ─────────────────────
//
//	Respond 是唯一出站咽喉，顺序固定：
//	  1. 语言收口：本轮作答语言 = 界面语言（商家端固定中文，预留）
//	  2. 数字核验：★ 本助手的第一纪律——经营数字只能来自服务端注入的事实，
//	     模型自己算的、猜的一律整句丢弃（翻译助手那里叫报价核验）
//	  3. 承诺边界：出现「保证 / 一定 / 百分百」等词时降级为确定性回答
//	  4. 兜底拼装：无 LLM 或 LLM 失败时，只用知识库命中拼答案
//
// ★ 绝不编造：没有知识库命中、没有 LLM、也没有注入口径时，
// 明确回答「这个我还不确定，建议……」——宁可承认不知道，也不给假答案。
// 这与产品主链路的翻译纪律（未命中留空、前端回落原文）是同一条价值观。
package assist

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reply 一次问答的产出。
type Reply struct {
	Content string   `json:"content"`           // 正文
	Actions []Action `json:"actions,omitempty"` // 建议动作（前端渲染成按钮）
	Source  string   `json:"source"`            // llm / rule / flow / fallback
	Flow    string   `json:"flow,omitempty"`    // 命中的流程 key
	Model   string   `json:"model,omitempty"`   // 实际使用的模型（排障用）
	Hits    []string `json:"hits,omitempty"`    // 命中的知识条目 key（排障/观测用）
}

// Action 建议动作（把商家导向对应的功能入口 / 向客人提议服务动作）。
//
// ★ 商家端与客人端共用这一个类型，前端只需一套按钮渲染：
//   - 商家端填 Key/Name/URL（跳站内路由）；
//   - 客人端填 Key/Name/Arg（Arg 是参数，如加菜时的菜品 ID，URL 留空）。
type Action struct {
	Key  string `json:"key"`           // 动作标识（商家端 how-translate 等 / 客人端 add_dish 等）
	Name string `json:"name"`          // 按钮文案（跟着作答语言）
	URL  string `json:"url,omitempty"` // 站内路由或外链（商家端用）
	Arg  string `json:"arg,omitempty"` // 动作参数（客人端用：add_dish 的菜品 ID）
}

// Fact 是服务端注入的结构化事实（经营数字的唯一合法来源）。
//
// 为什么必须是「服务端算好再注入」而不是让模型自己去理解数据库：
// 模型会把「最近 10 条评价」讲成「所有客人」，会把 4.2 分讲成 4.5 分。
// 数字一旦由模型生成，就没有任何机制能验证它对不对——
// 所以事实只能由我们算，模型只负责组织语言。
type Fact struct {
	Key   string `json:"key"`   // 事实键，如 store.rating_avg
	Label string `json:"label"` // 人类可读名称（提示词里用）
	Value string `json:"value"` // 文本化后的值（提示词里直接用）
}

// FlowStep 是多轮流程的一步。
type FlowStep struct {
	Ask     string   `json:"ask"`     // 这一步问什么 / 说什么
	Actions []Action `json:"actions"` // 这一步附带的功能入口
}

// KnowledgeEntry 是知识库 / 话术的一条。
// 二者同构：话术是「运营写死的固定答」，知识是「给模型的素材」。
type KnowledgeEntry struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`  // 正文（固定答案或知识素材）
	Keywords []string `json:"keywords"` // 触发词 / 检索词
	Weight   int      `json:"weight"`   // 权重（同分时的排序依据）
	Script   bool     `json:"script"`   // true = 话术直配（免 LLM）
	Flow     string   `json:"flow"`     // 非空 = 命中后进入该流程
}

// Engine 经营助手引擎。
//
// 刻意不持有数据库连接：知识库是内置种子，流程状态抽成 SessionStore 接缝
// （内存版 / DB 版可换）。这样引擎可在单测里裸跑，生产又能多实例一致。
type Engine struct {
	kb     []KnowledgeEntry
	flows  map[string][]FlowStep
	synMap map[string]string // 同义词归一：用户说法 → 标准词
	sess   SessionStore      // 在途流程状态（默认内存版，见 NewWithStore）
}

// sessionState 保留为类型别名，兼容旧引用（语义不变）。
type sessionState = SessionState

// sessionTTL 是流程在途状态的存活时间。
// 商家被打断（去接客）几分钟后回来，流程应当自然结束而不是接着追问。
const sessionTTL = 10 * time.Minute

// New 构造引擎：装入内置知识库、话术与流程，流程状态用内存版存储。
// 生产环境请用 NewWithStore 注入 DB 版（多实例下流程不丢）。
func New() *Engine { return NewWithStore(NewMemorySessionStore()) }

// NewWithStore 构造引擎并指定流程状态存储。
func NewWithStore(st SessionStore) *Engine {
	if st == nil {
		st = NewMemorySessionStore()
	}
	e := &Engine{
		kb:    seedKnowledge(),
		flows: seedFlows(),
		sess:  st,
	}
	e.synMap = seedSynonyms()
	return e
}

// Knowledge 返回内置知识库（供上层观测 / 测试断言）。
func (e *Engine) Knowledge() []KnowledgeEntry { return e.kb }

// MatchScore 计算输入与某条知识的匹配分（0 = 不匹配）。
//
// 计分规则（刻意让「专词压过泛词」）：
//
//	命中一个关键词    +3 × 该关键词的字数
//	标题整串出现      +1
//	同分时用权重排序   + 权重/10
//
// ★ 为什么按关键词字数加权：早期版本每个命中关键词只 +2，于是「未翻译」
// （专词，问的是客人端显示问题）只有 2 分，而 how-translate 靠泛词「翻译」
// 也拿 2 分，再叠加更高权重就反超——结果是问「未翻译怎么办」却答「怎么翻译菜单」。
// 关键词越长越具体，越具体越该说了算，这是检索的基本常识。
//
// ★ 权重只能是同分时的排序依据，不能当基础分：早期版本写过
// `return score + k.Weight/10`，于是一条零命中的知识也能拿到分，
// 「完全没提到」与「弱命中」无法区分，「查无素材」这个信号被彻底抹掉。
func MatchScore(input string, k KnowledgeEntry) int {
	score := 0
	for _, kw := range k.Keywords {
		if kw != "" && strings.Contains(input, kw) {
			score += 3 * len([]rune(kw))
		}
	}
	if k.Title != "" && strings.Contains(input, k.Title) {
		score++
	}
	if score == 0 {
		return 0 // 零命中即零分：宁可答不上，也不要答错
	}
	return score + k.Weight/10
}

// RetrieveKB 检索知识库：同义词归一后按匹配分排序，取前 topN。
// 返回空切片表示「库里没有能答这条的素材」——上层据此决定兜底话术。
func (e *Engine) RetrieveKB(input string, topN int) []KnowledgeEntry {
	norm := e.normalize(input)
	// scored 是知识库检索用的一条候选（条目 + 相关度打分），供排序后取 topN。
	type scored struct {
		entry KnowledgeEntry
		score int
	}
	all := make([]scored, 0, len(e.kb))
	for _, k := range e.kb {
		if s := MatchScore(norm, k); s > 0 {
			all = append(all, scored{k, s})
		}
	}
	// 分数倒序；同分按权重倒序，保证结果稳定（不依赖 map 遍历顺序）
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].entry.Weight > all[j].entry.Weight
	})
	if len(all) > topN {
		all = all[:topN]
	}
	out := make([]KnowledgeEntry, 0, len(all))
	for _, s := range all {
		out = append(out, s.entry)
	}
	return out
}

// MatchScript 命中话术直配（免 LLM）：返回第一条 Script=true 且匹配的知识。
// 直配的语义是「运营写死的答案」，不走模型，因此永远确定性一致。
func (e *Engine) MatchScript(input string) (KnowledgeEntry, bool) {
	hits := e.RetrieveKB(input, 5)
	for _, h := range hits {
		if h.Script {
			return h, true
		}
	}
	return KnowledgeEntry{}, false
}

// MatchFlow 命中流程触发词。
//
// ★ 用「检索 top1 恰好是流程条目」而不是单独扫一遍关键词表：
// 这样流程触发与话术直配共用同一套计分，谁的意图更强谁说话。
// 早期版本是「只要有流程关键词命中就进流程」，于是「评价维度是什么，教教我」
// 这种问题也会被「教我」拽进翻译引导——单点匹配太贪，必须让分数裁决。
func (e *Engine) MatchFlow(input string) (string, bool) {
	hits := e.RetrieveKB(input, 1)
	if len(hits) == 0 {
		return "", false
	}
	if hits[0].Flow != "" {
		return hits[0].Flow, true
	}
	return "", false
}

// normalize 同义词归一：把用户的说法折叠到标准词，提高检索召回。
// 例：「菜单」与「菜谱」、「翻译」与「译」视为一类。
func (e *Engine) normalize(input string) string {
	out := input
	for from, to := range e.synMap {
		out = strings.ReplaceAll(out, from, to)
	}
	return out
}

// sessionOf 取某账号的在途流程状态；不在流程中或已过期返回 nil。
// ★ 存储失败时按「无流程」处理：宁可重新开始，也不让一次读库失败中断问答。
func (e *Engine) sessionOf(ctx context.Context, accountID string) *sessionState {
	if e.sess == nil || accountID == "" {
		return nil
	}
	st, err := e.sess.GetSession(ctx, accountID)
	if err != nil {
		return nil
	}
	return st
}

// setSession 记录流程进度（flow 为空或 step<0 表示结束流程）。
func (e *Engine) setSession(ctx context.Context, accountID, flow string, step int) {
	if e.sess == nil || accountID == "" {
		return
	}
	if flow == "" || step < 0 {
		_ = e.sess.ClearSession(ctx, accountID)
		return
	}
	_ = e.sess.SaveSession(ctx, accountID, flow, step)
}

// clearSession 结束某账号的流程（公开方法，供上层在「退出流程」时调用）。
func (e *Engine) ClearSession(ctx context.Context, accountID string) {
	e.setSession(ctx, accountID, "", -1)
}

// ActiveSessions 返回在途流程数（观测用：流程卡住的比例是产品质量信号）。
// 内存存储返回精确计数；DB 存储由上层直接查表，不走这里。
func (e *Engine) ActiveSessions(ctx context.Context) int {
	if m, ok := e.sess.(*MemorySessionStore); ok {
		n, err := m.Count(ctx)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// FactsFromStats 把商品化统计转成事实表（供提示词注入与数字核验）。
// ★ 这里的每个数字都必须来自数据库统计，不允许上层手写。
func FactsFromStats(st map[string]any) []Fact {
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []Fact{}
	for _, k := range keys {
		out = append(out, Fact{Key: k, Label: k, Value: formatValue(st[k])})
	}
	return out
}

// formatValue 把任意统计值转成文本（提示词里直接用）。
func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// 保留一位小数：经营指标（评分 / 均值）一位足够，也能避免 4.199999 之类
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', 1, 64)
	case int:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		if t {
			return "是"
		}
		return "否"
	default:
		return ""
	}
}
