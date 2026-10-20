// Package service 承载对外部能力的抽象（OCR / 翻译 / 二维码），
// 使本地无外部服务时也能跑通，生产只需换实现。
//
// ── translate.go：翻译链装配 ────────────────────────────────
//
// 翻译是本产品的核心能力，也是一条「越用越准」的数据飞轮。装配上分四层，
// 由内到外逐层包裹；任何一层出问题都走降级，而不是让整批翻译直接失败。
//
//	调用方（httpapi handlers：菜单多语化 / 评论回译 / 券面本地化）
//	                    │
//	                    ▼
//	┌──────────────────────────────────────────────────────────────┐
//	│ 第 4 层  CachedTranslator      翻译记忆（TM）                 │
//	│  查 translation_cache：命中即返回；未命中才下探引擎并回写。     │
//	│  职责：省配额 / 省成本，并沉淀「越用越准」的翻译资产。           │
//	└───────────────────────────┬──────────────────────────────────┘
//	                            ▼
//	┌──────────────────────────────────────────────────────────────┐
//	│ 第 3 层  FallbackTranslator    降级编排                       │
//	│  先跑主引擎；把结果规整成等长切片后检查空洞，                   │
//	│  有空洞（或主引擎报错）就调兜底补齐；补上了即视为成功。          │
//	└──────────┬─────────────────────────────────────┬─────────────┘
//	           ▼ 主引擎（生产 / 本地二选一）             ▼ 兜底
//	┌───────────────────────────────┐  ┌───────────────────────────┐
//	│ 第 1 层  主引擎                 │  │ 第 2 层  DictTranslator    │
//	│  HTTPTranslator（自建翻译链）    │  │  内置中→英小词典           │
//	│  或 MemoryTranslator(MyMemory)  │  │  仅覆盖 en，其余一律空串    │
//	└───────────────────────────────┘  └───────────────────────────┘
//
// 每一层的职责与失败降级路径：
//   - 第 1 层 主引擎：配了 LC_TRANSLATE_URL 就走自建翻译链（HTTPTranslator），
//     否则走 MyMemory（MemoryTranslator）。网络错误 / 非 200 均视为失败。
//   - 第 2 层 兜底：主引擎失败或结果有空洞时补位。词典未收录一律留空——
//     ★ 核心纪律「绝不编造」：绝不能用原文或占位符冒充译文。
//   - 第 3 层 编排：两层都失败时保留主引擎错误向上抛，让调用方知道「这批没翻成」；
//     只要兜底补上了一部分就视为成功（剩余空格由前端回落显示原文）。
//   - 第 4 层 TM：查询 / 写入失败只影响缓存收益，不影响翻译本身（缓存是优化，不是依赖）。
//
// 最终产物：与入参等长的 []string；其中空串 =「未翻译」，
// 由前端回落显示原文并标注，绝不编造。
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Translator 抽象翻译能力。三种实现，按优先级组合（见 NewTranslator）：
//   - HTTPTranslator   → 接「既有翻译链」（生产首选；翻译是自建能力）
//   - MemoryTranslator → MyMemory 公开 API（本地开发用：真实可翻译，覆盖 10+ 语种）
//   - DictTranslator   → 内置小词典（离线兜底，只覆盖常见菜名）
//
// ★ 纪律：未命中的词一律返回空串，由调用方回落显示原文——**绝不编造**。
//
// 三个方法的分工：
//   - Translate：批量翻译。入参与出参按下标一一对应，出参长度必须与入参对齐
//     （空洞用空串占位），调用方普遍按下标取值。
//   - Ready：报告该实现当前是否真的可用，用于启动日志与健康判断；
//     注意它只反映「配置是否齐全」，不代表网络此刻可达。
//   - Name：引擎标识，用于日志与 TM 回写时记录 provider。
//
// 「此刻可达」另有专门的接口回答：TranslateReporter（见下）。之所以不把
// 探测并进 Ready：Ready 在每次翻译的装配路径上都会被问（便宜、无 IO），
// 而探测要发网络请求，只有健康检查这种低频运维动作才付得起这个代价。
type Translator interface {
	Translate(ctx context.Context, texts []string, from, to string) ([]string, error)
	Ready() bool
	Name() string
}

/* ── 翻译链自陈：配置是什么 + 此刻可达吗 ─────────────────── */

// TranslateReporter 是翻译链的可选自陈接口（NewTranslatorWithKeys 的返回值实现它）。
//
// 存在理由（2026-10-19 字节级 UAT 的 F-5）：翻译链地址配成黑洞（端口没人听）
// 时，装配照样成功、Ready() 照样为 true，于是 /api/health 报 live——运维
// 看到的是「翻译很健康」，实际每一次翻译都在降级到内置词典。健康检查必须
// 区分「配了」与「连得上」，这两件事只有探测能分开。
type TranslateReporter interface {
	// TranslateEngine 返回配置的主引擎标识：chain（自建翻译链）/ mymemory（公开 API）/
	// dict（仅内置词典）。它是纯配置的，不产生任何 IO。
	TranslateEngine() string
	// TranslateReachable 返回主引擎实时可达性。懒探测 + TTL 缓存：
	// 只在被调用时探测一次，结果缓存 reachTTL（见 reachCache），
	// 不占后台 goroutine、不随进程生命周期摇摆。
	TranslateReachable(ctx context.Context) bool
}

// 引擎标识常量，供 /api/health 的 translate_engine 字段对外输出。
const (
	EngineChain    = "chain"    // 自建翻译链（LC_TRANSLATE_URL）
	EngineMyMemory = "mymemory" // MyMemory 公开 API（本地开发）
	EngineDict     = "dict"     // 仅内置词典（无外部引擎）
	EngineLexicorn = "lexicorn" // 能言平台（租户级 Key，按请求解析）
)

// 可达性探测参数。
//
// reachTTL 为什么是 30s：健康检查是低频运维动作（人工 curl / 负载均衡探活），
// TTL 把它压到「最多每 30s 一次真实探测」，既不会像后台定时任务那样占
// goroutine（测试里会泄漏、优雅停机要多等一轮），又能及时反映宕机。
//
// reachTimeout 为什么是 2s：探活要快，宁可判「不可达」也不让健康检查挂住；
// 黑洞地址（连接被拒 / 被防火墙丢包）通常在毫秒级或超时后失败。
const (
	reachTTL     = 30 * time.Second
	reachTimeout = 2 * time.Second
)

// reachCache 收敛「懒探测 + TTL」的通用骨架：TTL 内的旧结果直接复用，
// 过期则现场探测并写回。
//
// 并发口径：探测在锁外执行——慢探测不该阻塞其它健康检查请求；最坏情况是
// 并发多打一次幂等的只读请求，结果照写，不影响正确性。mu 只保护时间戳与结果。
type reachCache struct {
	mu sync.Mutex
	at time.Time
	ok bool
}

// get 返回缓存内的探测结果，或在过期时调 probe 现场探测。
func (c *reachCache) get(probe func() bool) bool {
	c.mu.Lock()
	at, ok := c.at, c.ok
	c.mu.Unlock()
	if !at.IsZero() && time.Since(at) < reachTTL {
		return ok
	}
	ok = probe()
	c.mu.Lock()
	c.at, c.ok = time.Now(), ok
	c.mu.Unlock()
	return ok
}

// engineOf 取内层实现的引擎标识。纯配置读取，不产生任何 IO——
// 引擎名不该为了报个名字就去探测一次网络。
//
// 没实现 TranslateReporter 的实现（多为测试假件）按 dict 回落：本地词典
// 恒可用，这个答案不会让健康检查说谎。
func engineOf(t Translator) string {
	if r, ok := t.(TranslateReporter); ok {
		return r.TranslateEngine()
	}
	return EngineDict
}

// reachableOf 问内层实现的可达性（懒探测 + TTL）。没实现自陈接口的实现
// 按「本地词典恒可达」回落。
func reachableOf(ctx context.Context, t Translator) bool {
	if r, ok := t.(TranslateReporter); ok {
		return r.TranslateReachable(ctx)
	}
	return true
}

/* ── 1) 既有翻译链（生产首选） ────────────────────────────── */

// HTTPTranslator 调用既有翻译链 POST {base}/api/translation。
// 这是生产首选的主引擎：翻译是本产品的自建能力，质量与配额都可控。
type HTTPTranslator struct {
	BaseURL string       // 翻译链根地址，如 http://translate.internal:9000
	Client  *http.Client // 复用连接池的 HTTP 客户端（含超时）

	probe reachCache // 可达性探测的懒缓存（见 TranslateReachable）
}

// NewHTTPTranslator 构造 HTTP 翻译客户端。
// 超时设 20s：一次请求携带整批文本，过短会在网络抖动时误判为失败。
func NewHTTPTranslator(base string) *HTTPTranslator {
	return &HTTPTranslator{BaseURL: base, Client: &http.Client{Timeout: 20 * time.Second}}
}

// Ready 报告翻译链地址是否已配置（非空即视为就绪）。
func (h *HTTPTranslator) Ready() bool { return h.BaseURL != "" }

// TranslateEngine 报告本实现是自建翻译链。
func (h *HTTPTranslator) TranslateEngine() string { return EngineChain }

// TranslateReachable 探测自建链此刻是否应答（懒探测 + 30s TTL）。
func (h *HTTPTranslator) TranslateReachable(ctx context.Context) bool {
	if h.BaseURL == "" {
		return false // 没配地址 = 没有可达的链，诚实报不可达
	}
	return h.probe.get(func() bool {
		cctx, cancel := context.WithTimeout(ctx, reachTimeout)
		defer cancel()
		// HEAD {base}/api/translation：只要拿到任何 HTTP 应答（哪怕是
		// 404/405，说明 HEAD 不被该路由支持）就证明服务活着；连接被拒、
		// 超时、DNS 失败才是不可达。用 HEAD 而非真实 POST 是为了零成本——
		// 探活不该消耗翻译链的配额，也不该在对方日志里留下一串垃圾请求。
		req, err := http.NewRequestWithContext(cctx, http.MethodHead, h.BaseURL+"/api/translation", nil)
		if err != nil {
			return false
		}
		resp, err := h.Client.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	})
}

// Name 返回引擎标识，用于日志与 TM 回写记录 provider。
func (h *HTTPTranslator) Name() string { return "langcross-chain" }

// Translate 批量调用翻译链。请求体形如
// {"texts":[...],"source_lang":"zh","target_lang":"en"}，
// 响应体取 {"translations":[...]}。
//
// 失败即返回 nil + error：非 200、网络错误、响应解码失败都算硬失败，
// 由上层 FallbackTranslator 规整长度后交给兜底补齐。
func (h *HTTPTranslator) Translate(ctx context.Context, texts []string, from, to string) ([]string, error) {
	body, err := json.Marshal(map[string]any{"texts": texts, "source_lang": from, "target_lang": to})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.BaseURL+"/api/translation", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("translate chain: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("translate chain status %d", resp.StatusCode)
	}
	var out struct {
		Translations []string `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode translate response: %w", err)
	}
	return out.Translations, nil
}

/* ── 2) MyMemory（本地开发：真实翻译） ─────────────────────── */

// MemoryTranslator 走 MyMemory 公开 API。它不保证质量与配额，
// 只用于本地把链路跑通；生产请把 LC_TRANSLATE_URL 指向自建翻译链。
type MemoryTranslator struct {
	Client *http.Client
	Email  string        // 可选：填了配额更高
	Delay  time.Duration // 请求间隔，避免触发限流

	probe reachCache // 可达性探测的懒缓存
}

// NewMemoryTranslator 构造 MyMemory 客户端。
// 逐条串行请求 + 120ms 间隔，是为规避免费接口的速率限制（QPS 很低）；
// 批量翻译时会明显变慢，这也是它只作本地开发用途的原因。
func NewMemoryTranslator(email string) *MemoryTranslator {
	return &MemoryTranslator{
		Client: &http.Client{Timeout: 15 * time.Second},
		Email:  email,
		Delay:  120 * time.Millisecond,
	}
}

// Ready 恒为 true：MyMemory 是公开接口，无需额外配置即可用。
func (m *MemoryTranslator) Ready() bool { return true }

// TranslateEngine 报告本实现走 MyMemory 公开 API。
func (m *MemoryTranslator) TranslateEngine() string { return EngineMyMemory }

// TranslateReachable 探测 MyMemory 此刻是否应答（懒探测 + 30s TTL）。
func (m *MemoryTranslator) TranslateReachable(ctx context.Context) bool {
	return m.probe.get(func() bool {
		cctx, cancel := context.WithTimeout(ctx, reachTimeout)
		defer cancel()
		// 发一个单词的极小请求：MyMemory 的免费额度足够，一个词不值一提，
		// 换来的是「这条链路真的能翻」而不是「主机 ping 得通」。
		u := "https://api.mymemory.translated.net/get?q=ping&langpair=en%7Ces"
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
		if err != nil {
			return false
		}
		resp, err := m.Client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
}

// Name 返回引擎标识，用于日志与 TM 回写记录 provider。
func (m *MemoryTranslator) Name() string { return "mymemory" }

// mmCode 把内部语种码映射为 MyMemory 的 langpair 码。
// 目前只有中文需要特判：内部统一用 "zh"，而 MyMemory 认 "zh-CN"；
// 其余语种码两者一致，原样透传。
func mmCode(code string) string {
	if code == "zh" {
		return "zh-CN"
	}
	return code
}

// Translate 逐条调用 MyMemory 完成批量翻译。
//
// 关键口径：单条失败不中断整批——该位置留空，由前端回落显示原文。
// 这样一条生僻词不会拖垮整张菜单的多语化。
func (m *MemoryTranslator) Translate(ctx context.Context, texts []string, from, to string) ([]string, error) {
	out := make([]string, len(texts))
	for i, t := range texts {
		if strings.TrimSpace(t) == "" {
			continue
		}
		if i > 0 && m.Delay > 0 {
			select {
			case <-time.After(m.Delay):
			case <-ctx.Done():
				return out, ctx.Err()
			}
		}
		v, err := m.one(ctx, t, from, to)
		if err != nil {
			continue // 单条失败不中断整批：留空，前端回落显示原文
		}
		out[i] = v
	}
	return out, nil
}

// one 翻译单条文本。返回空串或 error 都表示「这条没翻成」。
func (m *MemoryTranslator) one(ctx context.Context, text, from, to string) (string, error) {
	u := fmt.Sprintf("https://api.mymemory.translated.net/get?q=%s&langpair=%s%%7C%s",
		url.QueryEscape(text), mmCode(from), mmCode(to))
	if m.Email != "" {
		u += "&de=" + url.QueryEscape(m.Email)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := m.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mymemory status %d", resp.StatusCode)
	}
	var payload struct {
		ResponseData struct {
			TranslatedText string `json:"translatedText"`
		} `json:"responseData"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	v := strings.TrimSpace(payload.ResponseData.TranslatedText)

	// MyMemory 在配额用尽 / 出错时会把提示语塞进 translatedText，必须过滤掉：
	// 这类「看似有值」的文本若原样透传，等于把英文告警当成译文展示给客人，
	// 属于变相的「编造」。命中任一特征一律当失败处理，交给上层兜底/留空。
	up := strings.ToUpper(v)
	if v == "" ||
		strings.Contains(up, "MYMEMORY WARNING") ||
		strings.Contains(up, "QUERY LENGTH LIMIT") ||
		strings.Contains(up, "INVALID LANGUAGE PAIR") {
		return "", fmt.Errorf("mymemory no usable result")
	}
	return v, nil
}

/* ── 3) 内置词典（离线兜底） ──────────────────────────────── */

// DictTranslator 是内置最小中→英词典，仅覆盖常见菜名，用于完全离线时兜底。
// 词条是人工校对过的，命中即高质量；未收录一律留空，绝不拿原文或拼音冒充。
type DictTranslator struct{}

// Ready 恒为 true：词典内嵌在二进制里，不依赖任何外部服务。
func (DictTranslator) Ready() bool { return true }

// TranslateEngine 报告本实现只有内置词典。
func (DictTranslator) TranslateEngine() string { return EngineDict }

// TranslateReachable 恒为 true：词典内嵌在二进制里，不需要网络探测。
func (DictTranslator) TranslateReachable(context.Context) bool { return true }

// Name 返回引擎标识，用于日志与 TM 回写记录 provider。
func (DictTranslator) Name() string { return "builtin-dict" }

// zh2en 是内置中→英词典，覆盖最常见的菜名 / 分类名 / 饮品。
// 之所以只做中→英：菜单原文固定为 zh（见 SourceLang），
// 而英语是 T1 承诺语种，人工兜底到 en 的收益最高、成本最低。
var zh2en = map[string]string{
	"宫保鸡丁": "Kung Pao Chicken",
	"麻婆豆腐": "Mapo Tofu",
	"水煮鱼":  "Sichuan Boiled Fish",
	"番茄炒蛋": "Scrambled Eggs with Tomato",
	"糖醋里脊": "Sweet and Sour Pork",
	"红烧肉":  "Braised Pork Belly",
	"清炒时蔬": "Stir-fried Seasonal Greens",
	"蛋炒饭":  "Egg Fried Rice",
	"米饭":   "Steamed Rice",
	"可乐":   "Cola",
	"雪碧":   "Sprite",
	"啤酒":   "Beer",
	"酸辣汤":  "Hot and Sour Soup",
	"小食":   "Side dish",
	"本店招牌": "House specialty",
	"招牌":   "Signature",
	"热菜":   "Hot dishes",
	"凉菜":   "Cold dishes",
	"素菜":   "Vegetarian",
	"主食":   "Staples",
	"汤":    "Soups",
	"饮品":   "Drinks",
	"甜点":   "Desserts",
}

// Translate 按词典查表返回译文。
//
// ★ 只覆盖中→英：目标语种不是 en 时直接返回等长空串切片（不编造）。
// 绝不把英文译文当成日语 / 法语等其它语种的译文返回。
func (DictTranslator) Translate(_ context.Context, texts []string, _, to string) ([]string, error) {
	out := make([]string, len(texts))
	if to != "en" {
		return out, nil // 词典只覆盖中→英
	}
	for i, t := range texts {
		out[i] = zh2en[strings.TrimSpace(t)]
	}
	return out, nil
}

/* ── 4) 组合：TM 缓存 + 降级 ─────────────────────────────── */

// TranslationCache 是翻译记忆（TM）的存取接口，由 repo 实现。
// 生产实现落在 translation_cache 表上（见 repo.LookupTranslations / SaveTranslations）。
type TranslationCache interface {
	LookupTranslations(ctx context.Context, from, to string, texts []string) (map[string]string, error)
	SaveTranslations(ctx context.Context, from, to string, pairs map[string]string, provider string) error
}

// CachedTranslator 在引擎前挂一层 TM：命中即返回，未命中才调引擎并回写。
// 这既省配额/成本，也是「翻译越用越准」的数据飞轮起点。
//
// 不变量：只回写「非空」译文。空串代表「未翻译」，若写进 TM
// 会把一次性的失败永久固化成缓存，后续永远命中空值。
type CachedTranslator struct {
	inner Translator       // 被包裹的内层引擎（通常是 FallbackTranslator）
	cache TranslationCache // 翻译记忆；构造时保证非 nil
}

// NewCachedTranslator 包装带缓存的翻译器。
// 注意：cache 为 nil 时不应调用本函数——NewTranslator 会跳过这一层。
func NewCachedTranslator(inner Translator, cache TranslationCache) *CachedTranslator {
	return &CachedTranslator{inner: inner, cache: cache}
}

// Ready 透传内层引擎的就绪状态（缓存不改变引擎可用性）。
func (c *CachedTranslator) Ready() bool { return c.inner.Ready() }

// Name 透传内层引擎标识，保证日志 / TM provider 反映真实引擎而非「cache」。
func (c *CachedTranslator) Name() string { return c.inner.Name() }

// TranslateEngine 透传内层引擎标识（缓存不改变引擎形态）。
func (c *CachedTranslator) TranslateEngine() string { return engineOf(c.inner) }

// TranslateReachable 透传内层可达性（缓存命中不影响「引擎是否活着」）。
func (c *CachedTranslator) TranslateReachable(ctx context.Context) bool {
	return reachableOf(ctx, c.inner)
}

// Translate 先查 TM，再对未命中部分调内层引擎，最后回写新译文。
//
// 关键口径：
//   - 空白入参（空串 / 纯空格）直接原样留空，既不查缓存也不写缓存；
//   - 缓存查询失败不致命，退化为「全部未命中」，照常调引擎；
//   - 只要拿到了部分译文就回写 TM；仅当「引擎报错且一条都没翻成」时才向上抛错。
func (c *CachedTranslator) Translate(ctx context.Context, texts []string, from, to string) ([]string, error) {
	out := make([]string, len(texts))
	if hit, err := c.cache.LookupTranslations(ctx, from, to, texts); err == nil {
		for i, t := range texts {
			if v, ok := hit[t]; ok {
				out[i] = v
			}
		}
	}

	missIdx := make([]int, 0, len(texts))
	missTexts := make([]string, 0, len(texts))
	for i, t := range texts {
		if out[i] == "" && strings.TrimSpace(t) != "" {
			missIdx = append(missIdx, i)
			missTexts = append(missTexts, t)
		}
	}
	if len(missTexts) == 0 {
		return out, nil
	}

	translated, err := c.inner.Translate(ctx, missTexts, from, to)
	pairs := map[string]string{}
	for j, idx := range missIdx {
		if j < len(translated) && translated[j] != "" {
			out[idx] = translated[j]
			pairs[missTexts[j]] = translated[j]
		}
	}
	if len(pairs) > 0 {
		_ = c.cache.SaveTranslations(ctx, from, to, pairs, c.inner.Name())
	}
	if err != nil && len(pairs) == 0 {
		return out, err
	}
	return out, nil
}

// FallbackTranslator 主引擎失败时用兜底引擎补齐空缺。
//
// ★ 踩过的坑：主引擎「硬失败」时会返回 nil 切片（而不是等长空串切片）。
// 若直接 `for i := range out` 按下标回填，nil 会让循环迭代 0 次，
// 兜底结果被静默丢弃 —— 表现为「离线词典兜底完全失效」。
// 因此这里先把主引擎结果规整成与 texts 等长的切片，再逐位补缺。
//
// 另一条口径：主引擎「有值但有空洞」（未命中、或返回短切片）也算未完成，
// 一样要交给兜底补齐 —— 内置词典对常见菜名是人工校对过的，比留空更有用。
type FallbackTranslator struct {
	primary  Translator // 主引擎：自建翻译链或 MyMemory
	fallback Translator // 兜底引擎：通常是内置中→英词典
}

// Ready 只要主引擎或兜底引擎任一可用即为就绪。
func (f *FallbackTranslator) Ready() bool { return f.primary.Ready() || f.fallback.Ready() }

// Name 返回主引擎标识：对外呈现的是主引擎，兜底只在内部生效。
func (f *FallbackTranslator) Name() string { return f.primary.Name() }

// TranslateEngine 报主引擎——对外呈现的是主引擎，兜底不参与对外形态。
func (f *FallbackTranslator) TranslateEngine() string { return engineOf(f.primary) }

// TranslateReachable 问主引擎的可达性：主引擎连不上才是「实际在走词典」，
// 兜底词典本身永远可达，若把它算进来，健康检查会永远报 live（F-5 的老毛病）。
func (f *FallbackTranslator) TranslateReachable(ctx context.Context) bool {
	return reachableOf(ctx, f.primary)
}

// Translate 先跑主引擎，再按需用兜底补齐空洞。
//
// 返回契约：永远返回与 texts 等长的切片（空串代表未翻译），绝不返回 nil。
// 错误契约：仅当「兜底也失败」或「兜底一条都没补上」时才向上抛主引擎错误。
func (f *FallbackTranslator) Translate(ctx context.Context, texts []string, from, to string) ([]string, error) {
	out, err := f.primary.Translate(ctx, texts, from, to)
	out = normalizeLen(out, len(texts)) // 规整长度，杜绝 nil / 短切片导致回填失效

	// 主引擎完整成功且无空缺：直接用，省一次兜底调用
	if err == nil && !hasEmpty(out) {
		return out, nil
	}

	fb, ferr := f.fallback.Translate(ctx, texts, from, to)
	if ferr != nil {
		return out, err // 兜底也失败：保留主引擎错误向上抛，交由调用方决定降级
	}

	filled := false
	for i := range out {
		if out[i] == "" && i < len(fb) && fb[i] != "" {
			out[i] = fb[i]
			filled = true
		}
	}
	if filled {
		// 兜底补上了空缺（哪怕只补了一部分）：视为成功，不再抛错。
		// 剩余仍为空的项由前端回落显示原文并标注「未翻译」——不编造。
		return out, nil
	}
	return out, err
}

// normalizeLen 把切片规整成指定长度：不足则补空串，超长则截断。
// 用于抹平「引擎返回 nil / 短切片」与「调用方按下标取值」之间的不匹配。
//
// ★ 契约：必须先调用 normalizeLen(out, len(texts))，再调用 hasEmpty(out)。
// 因为 hasEmpty(nil) 返回 false（长度为 0 = 没有位置 = 没有空洞），
// 直接对未规整的 nil / 短切片判空洞会把「整体失败」误判成「没有空缺」。
func normalizeLen(v []string, n int) []string {
	if len(v) == n {
		return v
	}
	out := make([]string, n)
	copy(out, v)
	return out
}

// hasEmpty 判断结果里是否还有未命中的位置（空洞）。
// 空串即空洞；空切片 / nil 没有位置，因此返回 false（见 normalizeLen 的契约）。
func hasEmpty(v []string) bool {
	for _, s := range v {
		if s == "" {
			return true
		}
	}
	return false
}

// NewTranslator 组装生产可用的翻译链：
// 主引擎 = 既有翻译链（若配置）否则 MyMemory；挂内置词典兜底；最外层套 TM 缓存。
//
// 参数：
//   - chainURL：LC_TRANSLATE_URL，非空则走自建链，否则走 MyMemory；
//   - email：MyMemory 联系邮箱（可选，仅在使用 MyMemory 时生效）；
//   - cache：翻译记忆实现；为 nil 时跳过 TM 层（本地 / 测试场景）。
//
// 返回的 Translator 的 Name() 始终反映真实主引擎，便于日志与健康检查。
//
// 注意：本函数不启用「租户级能言 Key」。生产装配请用 NewTranslatorWithKeys，
// 它会把主引擎换成按请求解析 Key 的 TenantPrimary。
func NewTranslator(chainURL, email string, cache TranslationCache) Translator {
	return NewTranslatorWithKeys(chainURL, email, cache, nil)
}

// NewTranslatorWithKeys 是带「租户级能言 Key」的完整装配：
// 主引擎 = TenantPrimary（有 Key 走能言，否则退既有链 / MyMemory），
// 兜底 = 内置词典，最外层 = TM 缓存。
//
// vault 为 nil 时退化为 NewTranslator 的行为（本地 / 测试场景）。
func NewTranslatorWithKeys(chainURL, email string, cache TranslationCache, vault *KeyVault) Translator {
	var inner Translator
	if chainURL != "" {
		inner = NewHTTPTranslator(chainURL)
	} else {
		inner = NewMemoryTranslator(email)
	}
	primary := inner
	if vault != nil {
		primary = NewTenantPrimary(vault, inner)
	}
	var t Translator = &FallbackTranslator{primary: primary, fallback: DictTranslator{}}
	if cache != nil {
		t = NewCachedTranslator(t, cache)
	}
	return t
}
