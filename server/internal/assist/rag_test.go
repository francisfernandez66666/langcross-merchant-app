package assist

import (
	"strings"
	"testing"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
)

// 构造一个最小的门店 + 三道菜，作为 RAG 知识库夹具。
// 菜名刻意带「句首大写」的英文译文（Mapo doufu），用于验证大小写不敏感检索。
func sampleStore() *domain.Store {
	return &domain.Store{
		ID:            "store_test",
		Name:          "川味小馆",
		Slug:          "test",
		OrderMode:     "assist",
		WifiSSID:      "LangCross-WiFi",
		WifiPwd:       "88888888",
		BusinessHours: "10:00-22:00",
		CategoryNames: map[string]map[string]string{
			"en": {"热菜": "Main", "面": "Noodles"},
		},
	}
}

func sampleDishes() []domain.Dish {
	p := func(v float64) *float64 { return &v }
	return []domain.Dish{
		{
			ID:       "d_kungpao",
			NameZh:   "宫保鸡丁",
			NameEn:   "Kung Pao Chicken",
			Names:    map[string]string{"en": "Kung Pao Chicken", "ja": "宮保鶏丁"},
			Price:    p(38),
			Category: "热菜",
			Attrs:    map[string]any{"spicy": "中辣"},
		},
		{
			ID:       "d_mapo",
			NameZh:   "麻婆豆腐",
			NameEn:   "Mapo doufu",
			Names:    map[string]string{"en": "Mapo doufu"},
			Price:    p(28),
			Category: "热菜",
			Attrs:    map[string]any{"spicy": "辣"},
		},
		{
			ID:       "d_noodles",
			NameZh:   "西红柿鸡蛋面",
			NameEn:   "Egg noodles with tomatoes",
			Names:    map[string]string{"en": "Egg noodles with tomatoes"},
			Price:    p(22),
			Category: "面",
			Attrs:    map[string]any{},
		},
	}
}

// 源语言（zh）下，没有翻译的菜也应被视为「有中文名」，NameLang=true，
// 不能误判成「暂无中文名称」——这是早先让整份中文菜单显示为空的根因。
func TestStoreKB_SourceLangIsValidName(t *testing.T) {
	zhByName := map[string]string{}
	for _, d := range sampleDishes() {
		zhByName[d.ID] = d.NameZh
	}
	kb := NewStoreKB(sampleStore(), sampleDishes(), "zh", "中文", "¥")
	for _, d := range kb.Dishes {
		if !d.NameLang {
			t.Errorf("lang=zh 时 %q 应判为已译名(NameLang=true), 实际为 false", d.Name)
		}
		if d.Name != zhByName[d.ID] {
			t.Errorf("lang=zh 时 Name 应回退成原文 %q, 实际 %q", zhByName[d.ID], d.Name)
		}
	}
}

// 有译文时正常命中；无译文（如某语种没翻）时 NameLang=false，且别名仍含原文。
func TestStoreKB_NameLangPerLang(t *testing.T) {
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	byID := map[string]DishDoc{}
	for _, d := range kb.Dishes {
		byID[d.ID] = d
	}
	if !byID["d_kungpao"].NameLang || byID["d_kungpao"].Name != "Kung Pao Chicken" {
		t.Errorf("en 下有译文应为真名, got %+v", byID["d_kungpao"])
	}
	// 构造一道「只有中文、没翻 en」的菜
	noEn := []domain.Dish{{
		ID: "d_x", NameZh: "葱油饼", Names: map[string]string{"ja": "ねぎ餅"}, Price: nil, Category: "面",
	}}
	kb2 := NewStoreKB(sampleStore(), noEn, "en", "英语", "¥")
	if kb2.Dishes[0].NameLang {
		t.Errorf("en 下无译文应为 NameLang=false, 实际 true")
	}
	if kb2.Dishes[0].Name != "葱油饼" {
		t.Errorf("未译时应回落原文作 Name, got %q", kb2.Dishes[0].Name)
	}
	// 别名必须含原文，否则出站守卫无法识别「这是本店菜」
	if !kb2.AllowedNames()["葱油饼"] {
		t.Errorf("未译菜的原文应进 AllowedNames, 实际缺失")
	}
}

// 检索大小写不敏感："mapo doufu" 的小写查询必须召回到库里的 "Mapo doufu"。
func TestStoreKB_RetrieveCaseInsensitive(t *testing.T) {
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	hits := kb.Retrieve("do you have mapo doufu?", 5)
	if len(hits) == 0 {
		t.Fatal("大小写不敏感检索失败：未召回到 Mapo doufu")
	}
	if hits[0].ID != "d_mapo" {
		t.Errorf("期望召回 d_mapo, 实际 %s", hits[0].ID)
	}
	// 全大写也应命中
	hits2 := kb.Retrieve("MAPO DOUFU please", 5)
	if len(hits2) == 0 || hits2[0].ID != "d_mapo" {
		t.Errorf("全大写检索也应命中 d_mapo, got %v", hits2)
	}
}

// 无命中返回空；不返回 0 分噪声。
func TestStoreKB_RetrieveNoMatch(t *testing.T) {
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	if h := kb.Retrieve("今天天气怎么样", 5); len(h) != 0 {
		t.Errorf("无关查询应返回空, got %v", h)
	}
}

// AllowedNames / AllowedPrices 要覆盖所有别名与无符号价格。
func TestStoreKB_AllowedSets(t *testing.T) {
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	names := kb.AllowedNames()
	for _, want := range []string{"Kung Pao Chicken", "宫保鸡丁", "Mapo doufu", "麻婆豆腐", "Egg noodles with tomatoes"} {
		if !names[want] {
			t.Errorf("AllowedNames 缺失 %q", want)
		}
	}
	prices := kb.AllowedPrices()
	for _, want := range []string{"¥38", "38", "¥28", "28", "¥22", "22"} {
		if !prices[want] {
			t.Errorf("AllowedPrices 缺失 %q", want)
		}
	}
}

// 未译菜的菜名在【菜单】里应为 [[NO_NAME_IN_EN]] 占位，而非原文。
func TestStoreKB_FormatMenuUntranslated(t *testing.T) {
	noEn := []domain.Dish{{
		ID: "d_x", NameZh: "葱油饼", Names: map[string]string{"ja": "ねぎ餅"}, Price: nil, Category: "面",
	}}
	kb := NewStoreKB(sampleStore(), noEn, "en", "英语", "¥")
	out := kb.FormatMenu(kb.Candidates(""))
	if !strings.Contains(out, "[[NO_NAME_IN_EN]]") {
		t.Errorf("未译菜应渲染成占位标记, got %q", out)
	}
	if strings.Contains(out, "葱油饼") {
		t.Errorf("【菜单】里不应露出未译原文, 却出现了 葱油饼")
	}
}

// 小菜单走全量、大菜单走检索：Candidates 行为正确。
func TestStoreKB_CandidatesLimit(t *testing.T) {
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	if len(kb.Candidates("")) != 3 {
		t.Errorf("小菜单应全量下发, got %d", len(kb.Candidates("")))
	}
	big := make([]domain.Dish, 0, FullMenuLimit+10)
	for i := 0; i < FullMenuLimit+10; i++ {
		big = append(big, domain.Dish{ID: "d_big", NameZh: "菜", Names: map[string]string{"en": "Dish"}})
	}
	kbBig := NewStoreKB(sampleStore(), big, "en", "英语", "¥")
	if got := len(kbBig.Candidates("dish")); got > 15 {
		t.Errorf("大菜单应检索收窄到 ≤15, got %d", got)
	}
}
