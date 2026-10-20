package assist

import (
	"sort"
	"strconv"
	"strings"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
)

// SourceLangFallback 是界面语言缺失时的回落语种。
//
// ★ 这里刻意再写一遍 "zh" 而不去引 service.SourceLang：service 包依赖 repo，
// repo 依赖本包，引过来会成环。语种真相来源仍是 service.SupportedLangs，
// 本常量只是「拿不到语言时的保底」，两边都不可能单独改动。
const SourceLangFallback = "zh"

// 本文件：客人端 AI 助手的 RAG 知识库 —— 菜品 + 服务。
//
// 设计方向（用户明确要求）：「以菜品内容和服务内容作为 RAG 核心，
// 其他的主要扮演好一个门店客服和指导点菜的角色」。
//
// 因此这个助手与商家端「经营助手」是两条线：
//
//	商家端经营助手   面向商家：怎么翻译菜单、经营数据口径（service 侧平台知识）
//	客人端点菜助手   面向扫桌码的外国客人：菜单有什么、辣不辣、多少钱、怎么点
//
// ── 为什么检索要跨所有语种别名 ─────────────────────────
//
// 客人用英语问 "do you have mapo tofu"，而库里菜名原文是中文。
// 只拿中文名做关键词匹配必然召不回。所以每个菜品把
// name_zh + names_json 里**所有**语种译文一起作为别名（Aliases）参与匹配——
// 反正译文本来就存在库里，不用白不用。
//
// ── 小菜单全量、大菜单检索 ─────────────────────────────
//
// 一家餐厅通常 20–60 道菜，整份菜单塞进提示词也就 2–4k token，
// 全量给比检索更可靠（不存在「该召回的没召回」）。菜太多时才退回检索。
// 阈值见 FullMenuLimit。

// FullMenuLimit 是全量下发与检索下发的分界（按菜品数）。
const FullMenuLimit = 60

// DishDoc 是一份菜品知识（RAG 的最小单元）。
type DishDoc struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`      // 界面语言下的菜名（已翻译；缺失时回落原文）
	Aliases  []string `json:"aliases"`   // 所有语种的菜名（检索用，去重后）
	NameLang bool     `json:"name_lang"` // 界面语言下是否有译文（false = 未翻译，不能露原文）
	Desc     string   `json:"desc"`      // 描述（界面语言；未翻译时为空）
	Price    string   `json:"price"`     // 已格式化价格，如 "¥38"；空 = 未定价
	Category string   `json:"category"`  // 分类名（界面语言；未翻译时回落原文）
	Tags     []string `json:"tags"`      // 辣 / 素食 / 含花生 等，来自 Attrs
	SoldOut  bool     `json:"sold_out"`  // 是否售罄
}

// ServiceDoc 是门店服务信息（WiFi / 营业时间 / 点单方式 / 券规则）。
type ServiceDoc struct {
	StoreName string `json:"store_name"`
	Addr      string `json:"addr,omitempty"`
	Hours     string `json:"hours,omitempty"`
	WiFiSSID  string `json:"wifi_ssid,omitempty"`
	WiFiPwd   string `json:"wifi_pwd,omitempty"`
	OrderMode string `json:"order_mode,omitempty"` // assist 辅助型 / replace 替代型
	OrderHint string `json:"order_hint,omitempty"` // 点单方式的人话说明
	Coupon    string `json:"coupon,omitempty"`     // 券规则人话
	Currency  string `json:"currency,omitempty"`   // 价格符号，默认 ¥
}

// StoreKB 是一个门店的 RAG 知识库（每次请求按需装载，不缓存）。
//
// 刻意不缓存：菜单是商家高频变动的数据（售罄、改价、补翻），
// 缓存一份旧菜单让助手推荐已下架的菜，比多查一次库严重得多。
type StoreKB struct {
	Slug     string
	Lang     string // 界面语言码（如 en）
	LangName string // 界面语言中文名（如 英语），用于提示词里指定作答语言
	Service  ServiceDoc
	Dishes   []DishDoc
}

// NewStoreKB 从门店与菜品构造知识库。
//
// lang 是客人界面语言（决定菜名取哪个语种，以及助手用哪个语言作答）；
// langName 是该语言的中文名（提示词用），由上层从 service.SupportedLangs 取，
// 避免本包反向依赖 service（会成环）。
// currency 由上层给（默认 ¥）——避免在 assist 包里硬编码币种。
func NewStoreKB(st *domain.Store, dishes []domain.Dish, lang, langName, currency string) *StoreKB {
	if lang == "" {
		lang = SourceLangFallback
	}
	if langName == "" {
		langName = lang
	}
	if currency == "" {
		currency = "¥"
	}
	kb := &StoreKB{
		Slug:     st.Slug,
		Lang:     lang,
		LangName: langName,
		Service: ServiceDoc{
			StoreName: st.Name,
			Addr:      st.Addr,
			Hours:     st.BusinessHours,
			WiFiSSID:  st.WifiSSID,
			WiFiPwd:   st.WifiPwd,
			OrderMode: st.OrderMode,
			Currency:  currency,
		},
		Dishes: make([]DishDoc, 0, len(dishes)),
	}
	switch st.OrderMode {
	case "replace":
		kb.Service.OrderHint = "本店为扫码点单：直接在页面里选菜下单，服务员会照单送来。"
	default: // assist
		kb.Service.OrderHint = "本店为辅助点单：在页面里选好想吃的，再由服务员确认后下单。"
	}
	for _, d := range dishes {
		doc := DishDoc{
			ID:      d.ID,
			Desc:    d.Description,
			SoldOut: d.SoldOut,
			Tags:    tagsFromAttrs(d.Attrs),
			Aliases: nameAliases(d),
		}
		// 界面语言菜名：有译文用译文，没有就回落原文，并把 NameLang 置 false
		// ——prompt 与出站守卫都靠这个标志决定「能不能把菜名念给客人听」。
		//
		// ★ 源语言（zh）是特例：name_zh 就是原文真值，它**已经**是「中文名」，
		// 不算未翻译。早先这里只看 Names[lang]，于是中文界面下每道菜都被判成
		// 「暂无中文名称」，规则兜底时整份菜单都显示成空——而菜明明有中文名。
		if n, ok := d.Names[lang]; ok && strings.TrimSpace(n) != "" {
			doc.Name, doc.NameLang = n, true
		} else if lang == SourceLangFallback {
			doc.Name, doc.NameLang = d.NameZh, true
		} else {
			doc.Name, doc.NameLang = d.NameZh, false
		}
		if d.Price != nil {
			doc.Price = currency + formatPrice(*d.Price)
		}
		if c, ok := st.CategoryNames[lang][d.Category]; ok && c != "" {
			doc.Category = c
		} else {
			doc.Category = d.Category
		}
		kb.Dishes = append(kb.Dishes, doc)
	}
	return kb
}

// formatPrice 价格文本化：整数不带小数点，小数保留两位（钱的常规表示）。
func formatPrice(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// tagsFromAttrs 把菜品的 attrs（过敏原 / 辣度 / 宗教饮食）翻成可读标签。
//
// attrs 是商家自由填的 JSON，key 不固定，因此这里只做「值提取」：
// 凡是字符串值都收进来当标签，布尔 true 也收。刻意不解释 key 的含义——
// 那需要产品层约定，而现在约定还没到那一步；拿不准的宁可多给标签。
func tagsFromAttrs(attrs map[string]any) []string {
	if len(attrs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 稳定输出：同一道菜的标签顺序可复现
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		switch v := attrs[k].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				out = append(out, s)
			}
		case bool:
			if v {
				out = append(out, k)
			}
		case float64:
			if v > 0 {
				out = append(out, k+" "+formatPrice(v))
			}
		}
	}
	return out
}

// nameAliases 收集一道菜在所有语种下的名字（含原文），去重且保序。
func nameAliases(d domain.Dish) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(d.Names)+1)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(d.NameZh)
	add(d.NameEn) // 历史字段：早期数据只填了这一列，不能漏
	for _, n := range d.Names {
		add(n)
	}
	return out
}

// AllowedNames 返回全部菜品名（含各语种别名）的集合，供出站守卫做「编造守卫」。
func (k *StoreKB) AllowedNames() map[string]bool {
	m := make(map[string]bool, len(k.Dishes)*3)
	for _, d := range k.Dishes {
		for _, a := range d.Aliases {
			if a != "" {
				m[a] = true
			}
		}
		m[d.Name] = true
	}
	return m
}

// AllowedPrices 返回全部合法价格串（含无符号形式，如 "38" 与 "¥38"）。
func (k *StoreKB) AllowedPrices() map[string]bool {
	m := map[string]bool{k.Service.Currency: true}
	for _, d := range k.Dishes {
		if d.Price == "" {
			continue
		}
		m[d.Price] = true
		m[strings.TrimLeft(d.Price, k.Service.Currency)] = true
	}
	return m
}

// Retrieve 按问题检索候选菜品。
//
// 计分（与知识库同一思路：专词压泛词）：
//   - 菜名别名整串出现在问题里：+4 × 别名字数（越长越具体）
//   - 分类名命中：+2
//   - 标签命中：+2
//
// 返回按分数倒序的前 topN 条；zero 分不返回。
// 注意：本函数只用于「大菜单收窄候选」；小菜单走 FullMenu 全量下发。
//
// ★ 匹配大小写不敏感：客人用英语问 "do you have mapo doufu"，
// 而库里菜名译文是 "Mapo doufu"（句首大写）。strings.Contains 区分大小写，
// 直接比会一条都召不回——这曾让英文提问全部落空，故而统一 toLower 后比较。
func (k *StoreKB) Retrieve(q string, topN int) []DishDoc {
	// scored 是菜品检索用的一条候选（菜品 + 相关度打分），供排序后取 topN。
	type scored struct {
		doc   DishDoc
		score int
	}
	lq := strings.ToLower(q)
	all := make([]scored, 0, len(k.Dishes))
	for _, d := range k.Dishes {
		s := 0
		for _, a := range d.Aliases {
			if a != "" && strings.Contains(lq, strings.ToLower(a)) {
				s += 4 * len([]rune(a))
			}
		}
		if d.Category != "" && strings.Contains(lq, strings.ToLower(d.Category)) {
			s += 2
		}
		for _, t := range d.Tags {
			if t != "" && strings.Contains(lq, strings.ToLower(t)) {
				s += 2
			}
		}
		if s == 0 {
			continue
		}
		all = append(all, scored{d, s})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	if len(all) > topN {
		all = all[:topN]
	}
	out := make([]DishDoc, 0, len(all))
	for _, s := range all {
		out = append(out, s.doc)
	}
	return out
}

// Candidates 决定这次提问要用哪些菜品拼提示词。
//
// 小菜单全量（更可靠，且不存在「该召回的没召回」），大菜单才检索。
// ★ 售罄的菜仍然下发：客人问「有没有 XX」时，答「有但已售罄」
// 比「没有这道菜」更接近真相——隐瞒库存算不上对客人诚实。
func (k *StoreKB) Candidates(q string) []DishDoc {
	if len(k.Dishes) <= FullMenuLimit {
		out := make([]DishDoc, len(k.Dishes))
		copy(out, k.Dishes)
		return out
	}
	return k.Retrieve(q, 15)
}

// FormatMenu 把候选菜品渲染成提示词里的【菜单】一节。
//
// 每行一道菜，字段固定顺序（菜名 / 价格 / 分类 / 标签 / 描述 / 状态），
// 便于模型逐字复制菜名——出站守卫只认「原样出现在【菜单】里的菜名」。
// 未翻译的菜名不直接给原文，改给一个显式标记，让模型知道「这道菜没有当前语言的名称」。
func (k *StoreKB) FormatMenu(docs []DishDoc) string {
	var b strings.Builder
	for _, d := range docs {
		b.WriteString("- ")
		if d.NameLang {
			b.WriteString(d.Name)
		} else {
			// ★ 不写原文菜名：客人界面语言下没有译文时露中文，
			// 正是「未翻译内容暴露出来」这个老问题。给标记，让模型照实说。
			b.WriteString("[[NO_NAME_IN_" + strings.ToUpper(k.Lang) + "]]")
		}
		if d.Price != "" {
			b.WriteString(" | 价格 " + d.Price)
		} else {
			b.WriteString(" | 价格 未定价")
		}
		if d.Category != "" {
			b.WriteString(" | 分类 " + d.Category)
		}
		if len(d.Tags) > 0 {
			b.WriteString(" | 标签 " + strings.Join(d.Tags, "/"))
		}
		if d.Desc != "" {
			b.WriteString(" | 描述 " + d.Desc)
		}
		if d.SoldOut {
			b.WriteString(" | 状态 已售罄")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FormatService 把服务信息渲染成提示词里的【门店服务】一节。
func (k *StoreKB) FormatService() string {
	s := k.Service
	var b strings.Builder
	b.WriteString("店名：" + s.StoreName + "\n")
	if s.Addr != "" {
		b.WriteString("地址：" + s.Addr + "\n")
	}
	if s.Hours != "" {
		b.WriteString("营业时间：" + s.Hours + "\n")
	}
	if s.OrderHint != "" {
		b.WriteString("点单方式：" + s.OrderHint + "\n")
	}
	if s.Coupon != "" {
		b.WriteString("优惠：" + s.Coupon + "\n")
	}
	if s.WiFiSSID != "" {
		// 密码不随提示词下发：整份【门店服务】都会进上下文，而密码只在客人
		// 明确问起时才该出现。这里只给账号名，并提示模型「问起再答」。
		b.WriteString("WiFi 名称：" + s.WiFiSSID + "（密码仅在客人明确询问时提供）\n")
	}
	return b.String()
}
