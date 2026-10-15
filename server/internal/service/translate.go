package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Translator 抽象翻译能力。三种实现，按优先级组合（见 NewTranslator）：
//   - HTTPTranslator   → 接「既有翻译链」（生产首选；翻译是自建能力）
//   - MemoryTranslator → MyMemory 公开 API（本地开发用：真实可翻译，覆盖 10+ 语种）
//   - DictTranslator   → 内置小词典（离线兜底，只覆盖常见菜名）
//
// ★ 纪律：未命中的词一律返回空串，由调用方回落显示原文——**绝不编造**。
type Translator interface {
	Translate(ctx context.Context, texts []string, from, to string) ([]string, error)
	Ready() bool
	Name() string
}

/* ── 1) 既有翻译链（生产首选） ────────────────────────────── */

// HTTPTranslator 调用既有翻译链 POST {base}/api/translation。
type HTTPTranslator struct {
	BaseURL string
	Client  *http.Client
}

// NewHTTPTranslator 构造 HTTP 翻译客户端。
func NewHTTPTranslator(base string) *HTTPTranslator {
	return &HTTPTranslator{BaseURL: base, Client: &http.Client{Timeout: 20 * time.Second}}
}

func (h *HTTPTranslator) Ready() bool  { return h.BaseURL != "" }
func (h *HTTPTranslator) Name() string { return "langcross-chain" }

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
}

// NewMemoryTranslator 构造 MyMemory 客户端。
func NewMemoryTranslator(email string) *MemoryTranslator {
	return &MemoryTranslator{
		Client: &http.Client{Timeout: 15 * time.Second},
		Email:  email,
		Delay:  120 * time.Millisecond,
	}
}

func (m *MemoryTranslator) Ready() bool  { return true }
func (m *MemoryTranslator) Name() string { return "mymemory" }

// mmCode 把内部语种码映射为 MyMemory 的 langpair 码。
func mmCode(code string) string {
	if code == "zh" {
		return "zh-CN"
	}
	return code
}

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

	// MyMemory 在配额用尽 / 出错时会把提示语塞进 translatedText，必须过滤掉
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
type DictTranslator struct{}

func (DictTranslator) Ready() bool  { return true }
func (DictTranslator) Name() string { return "builtin-dict" }

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
type TranslationCache interface {
	LookupTranslations(ctx context.Context, from, to string, texts []string) (map[string]string, error)
	SaveTranslations(ctx context.Context, from, to string, pairs map[string]string, provider string) error
}

// CachedTranslator 在引擎前挂一层 TM：命中即返回，未命中才调引擎并回写。
// 这既省配额/成本，也是「翻译越用越准」的数据飞轮起点。
type CachedTranslator struct {
	inner Translator
	cache TranslationCache
}

// NewCachedTranslator 包装带缓存的翻译器。
func NewCachedTranslator(inner Translator, cache TranslationCache) *CachedTranslator {
	return &CachedTranslator{inner: inner, cache: cache}
}

func (c *CachedTranslator) Ready() bool  { return c.inner.Ready() }
func (c *CachedTranslator) Name() string { return c.inner.Name() }

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
type FallbackTranslator struct {
	primary  Translator
	fallback Translator
}

func (f *FallbackTranslator) Ready() bool  { return f.primary.Ready() || f.fallback.Ready() }
func (f *FallbackTranslator) Name() string { return f.primary.Name() }

func (f *FallbackTranslator) Translate(ctx context.Context, texts []string, from, to string) ([]string, error) {
	out, err := f.primary.Translate(ctx, texts, from, to)
	if err == nil && !allEmpty(out) {
		return out, nil
	}
	fb, ferr := f.fallback.Translate(ctx, texts, from, to)
	if ferr != nil {
		return out, err
	}
	for i := range out {
		if out[i] == "" && i < len(fb) {
			out[i] = fb[i]
		}
	}
	return out, err
}

func allEmpty(v []string) bool {
	for _, s := range v {
		if s != "" {
			return false
		}
	}
	return true
}

// NewTranslator 组装生产可用的翻译链：
// 主引擎 = 既有翻译链（若配置）否则 MyMemory；挂内置词典兜底；最外层套 TM 缓存。
func NewTranslator(chainURL, email string, cache TranslationCache) Translator {
	var primary Translator
	if chainURL != "" {
		primary = NewHTTPTranslator(chainURL)
	} else {
		primary = NewMemoryTranslator(email)
	}
	var t Translator = &FallbackTranslator{primary: primary, fallback: DictTranslator{}}
	if cache != nil {
		t = NewCachedTranslator(t, cache)
	}
	return t
}
