package assist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
)

// guardCitedDishes：模型引用的菜名若不在【菜单】里，整行丢弃；
// 命中的菜名去掉 [[ ]] 标记。这是点菜助手「绝不编造菜品」的第一栅栏。
func TestGuardCitedDishes_StripsFakeAndUnwraps(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := &Reply{Content: "Try [[Kung Pao Chicken]] it's good.\nAlso [[Fake Dish]] on page 3."}
	out := e.guardCitedDishes(rep, kb)
	if strings.Contains(out.Content, "Fake Dish") {
		t.Errorf("编造的菜名所在行应被整行丢弃, 实际仍在: %q", out.Content)
	}
	if strings.Contains(out.Content, "[[") || strings.Contains(out.Content, "]]") {
		t.Errorf("出站文本不应残留 [[ ]] 内部标记, 实际: %q", out.Content)
	}
	if !strings.Contains(out.Content, "Kung Pao Chicken") {
		t.Errorf("真实菜名应保留且去标记, 实际: %q", out.Content)
	}
}

// 全部是假菜名 → 丢弃后变成空，应回落「诚实不知道」提示。
func TestGuardCitedDishes_AllFakeFallsToUnknown(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := &Reply{Content: "[[Nonexistent A]] and [[Nonexistent B]]."}
	out := e.guardCitedDishes(rep, kb)
	if !strings.Contains(out.Content, "not sure") && !strings.Contains(out.Content, "不确定") {
		t.Errorf("全假菜名应回落诚实提示, 实际: %q", out.Content)
	}
}

// guardPrices：出现菜单里没有的价格 → 整行丢弃；带货币符号与无符号都应被识别。
func TestGuardPrices_StripsWrongPrice(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := &Reply{Content: "Kung Pao is ¥38.\nMapo is ¥99 on sale."}
	out := e.guardPrices(rep, kb)
	if strings.Contains(out.Content, "¥99") {
		t.Errorf("菜单没有 ¥99, 该行应被丢弃, 实际: %q", out.Content)
	}
	if !strings.Contains(out.Content, "¥38") {
		t.Errorf("合法价格 ¥38 应保留, 实际: %q", out.Content)
	}
}

// 菜单完全没有定价的极端情况下，guardPrices 不能把整段回答删空（fail-soft）。
func TestGuardPrices_EmptyMenuNoOp(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), []domain.Dish{{ID: "x", NameZh: "无价菜", Names: map[string]string{"en": "NoPrice"}}}, "en", "英语", "¥")
	rep := &Reply{Content: "This dish has no price listed."}
	out := e.guardPrices(rep, kb)
	if out.Content == "" {
		t.Errorf("空菜单时不应删空回答")
	}
}

// guestFallback：检索到候选 → 列出（菜名/价格/标签）；检索不到 → 诚实提示。
func TestGuestFallback_ReturnsHits(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := e.guestFallback(GuestAskInput{KB: kb}, "do you have mapo doufu")
	if rep.Source != "fallback" {
		t.Errorf("无 LLM 应走 fallback, got %s", rep.Source)
	}
	if !strings.Contains(rep.Content, "Mapo doufu") {
		t.Errorf("命中候选应列出菜名, 实际: %q", rep.Content)
	}
	if !strings.Contains(rep.Content, "¥28") {
		t.Errorf("应带上价格, 实际: %q", rep.Content)
	}
}

// 验证检索不到候选时客人端兜底必须诚实说 not sure，不能把不相干的菜品
// 硬推给客人。
func TestGuestFallback_UnknownAdmits(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := e.guestFallback(GuestAskInput{KB: kb}, "what is the meaning of life")
	if !strings.Contains(rep.Content, "not sure") {
		t.Errorf("答不上来应诚实承认, 实际: %q", rep.Content)
	}
}

// 兜底引导语必须跟随客人语种（渲染纪律 L1 对助手兜底路径同样生效）：
// 曾硬编码中文，英文客人会看到半中半英的回答——2026-10-19 修。
func TestGuestFallback_IntroFollowsGuestLang(t *testing.T) {
	e := New()
	en := e.guestFallback(GuestAskInput{KB: NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")}, "do you have mapo doufu")
	if !strings.HasPrefix(en.Content, "Here's what might match") {
		t.Errorf("英文客人的兜底引导语应为英文, 实际: %q", en.Content)
	}
	zh := e.guestFallback(GuestAskInput{KB: NewStoreKB(sampleStore(), sampleDishes(), "zh", "中文", "¥")}, "有没有麻婆豆腐")
	if !strings.HasPrefix(zh.Content, "菜单里这些可能相关") {
		t.Errorf("中文客人应保持中文引导语, 实际: %q", zh.Content)
	}
}

// guestActions：回答里出现真实菜名 → 提议加菜（带 dish ID）；出现 staff → 提议服务铃。
func TestGuestActions_SuggestsAddDishAndBell(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := &Reply{Content: "I recommend [[Kung Pao Chicken]]. If unsure, please ask our staff."}
	acts := e.guestActions(rep, kb)
	var hasAdd, hasBell bool
	for _, a := range acts {
		if a.Key == ActionAddDish && a.Arg == "d_kungpao" {
			hasAdd = true
		}
		if a.Key == ActionServiceBell {
			hasBell = true
		}
	}
	if !hasAdd {
		t.Errorf("提到真实菜名应提议加菜(带 dish id), acts=%+v", acts)
	}
	if !hasBell {
		t.Errorf("出现 staff 应提议服务铃, acts=%+v", acts)
	}
}

// RespondGuest 端到端（无 LLM）：命中菜名的推荐应走 fallback 并产出加菜动作。
func TestRespondGuest_NoLLM_Fallback(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := e.RespondGuest(context.Background(), GuestAskInput{
		SessionID: "sess_1",
		Question:  "do you have mapo doufu and how much",
		KB:        kb,
		LLM:       LLMConfig{}, // 禁用 → 走确定性兜底
	})
	if rep == nil {
		t.Fatal("应返回非空回复")
	}
	if !strings.Contains(rep.Content, "Mapo doufu") {
		t.Errorf("应召回并介绍 Mapo doufu, 实际: %q", rep.Content)
	}
	// 兜底路径不应编造价格：只应出现菜单里真实存在的 ¥28
	if strings.Contains(rep.Content, "¥99") {
		t.Errorf("兜底不应出现菜单外的价格")
	}
}

// 空问题：返回引导语而非崩溃。
func TestRespondGuest_EmptyQuestion(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := e.RespondGuest(context.Background(), GuestAskInput{Question: "   ", KB: kb, LLM: LLMConfig{}})
	if rep == nil || rep.Content == "" {
		t.Fatal("空问题应返回引导语")
	}
}

/* ── guardNoRawChineseName：未翻译菜不许露原名 ─────────────────
   2026-10-19 审计实证：这一腿原先「定义了却没接进出站链」（死代码），
   且判据拿 AllowedNames() 做差集——该集合包含未翻译菜自己的别名，
   差集恒为空，接了也永不触发。现已修判据（未翻译别名 − 当前语言已译名）
   并接在 guardCitedDishes 之后。 */

// dishesWithUntranslated 在样例菜单上加一道「有日文译名、无英文译名」的菜，
// 构造英文界面下的「未翻译」态（NameLang=false，Name 回落中文原名）。
func dishesWithUntranslated() []domain.Dish {
	pv := func(v float64) *float64 { return &v }
	out := append([]domain.Dish{}, sampleDishes()...)
	return append(out, domain.Dish{
		ID: "d_sour", NameZh: "酸辣汤", Names: map[string]string{"ja": "サンラータン"}, Price: pv(18),
	})
}

// 未翻译菜的中文原名出现 → 整行丢弃；同段的已译名行不受影响。
func TestGuardNoRawChineseName_DropsLeakedLine(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), dishesWithUntranslated(), "en", "英语", "¥")
	rep := &Reply{Content: "We have 酸辣汤, it's great.\nMapo doufu is also good."}
	out := e.guardNoRawChineseName(rep, kb)
	if strings.Contains(out.Content, "酸辣汤") {
		t.Errorf("未翻译菜的中文原名应被丢弃, 实际: %q", out.Content)
	}
	if !strings.Contains(out.Content, "Mapo doufu") {
		t.Errorf("已译名行应保留, 实际: %q", out.Content)
	}
}

// 整段都在泄露 → 不能把带原文的回答留给客人，应回落诚实提示。
func TestGuardNoRawChineseName_AllLeakFallsBackToHint(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), dishesWithUntranslated(), "en", "英语", "¥")
	rep := &Reply{Content: "推荐 酸辣汤。\n今天的 酸辣汤 特别好喝。"}
	out := e.guardNoRawChineseName(rep, kb)
	if strings.Contains(out.Content, "酸辣汤") {
		t.Errorf("全泄露时不应留下任何原文, 实际: %q", out.Content)
	}
	if !strings.Contains(out.Content, "not sure") {
		t.Errorf("全泄露应回落诚实提示, 实际: %q", out.Content)
	}
}

// 碰撞逃生口：同一串既是未翻译菜的别名、又是另一道菜的当前语言已译名时，
// 它是合法内容，不能删（否则会误删正常回答）。
func TestGuardNoRawChineseName_TranslatedNameWinsOverAlias(t *testing.T) {
	e := New()
	dishes := append([]domain.Dish{}, sampleDishes()...)
	// A：英文译名恰好叫「酸辣汤」（合法已译名）
	dishes = append(dishes, domain.Dish{ID: "d_a", NameZh: "甲汤", Names: map[string]string{"en": "酸辣汤"}})
	// B：未翻译（无英文译名），中文原名也是「酸辣汤」
	dishes = append(dishes, domain.Dish{ID: "d_b", NameZh: "酸辣汤", Names: map[string]string{"ja": "サンラータン"}})
	kb := NewStoreKB(sampleStore(), dishes, "en", "英语", "¥")

	rep := &Reply{Content: "Today's special is 酸辣汤."}
	out := e.guardNoRawChineseName(rep, kb)
	if !strings.Contains(out.Content, "酸辣汤") {
		t.Errorf("该串同时是已译名, 应保留, 实际: %q", out.Content)
	}
}

// 没有未翻译菜时本腿不动作（正常菜单零误伤）。
func TestGuardNoRawChineseName_NoUntranslatedNoOp(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := &Reply{Content: "Kung Pao Chicken is ¥38."}
	out := e.guardNoRawChineseName(rep, kb)
	if out.Content != rep.Content {
		t.Errorf("无未翻译菜时不应改动回答, 实际: %q", out.Content)
	}
}

// 端到端接线验证：假 LLM 故意露未翻译菜的中文原名，
// RespondGuest 的出站链必须把它拦下（证明这一腿真接进了链上）。
func TestRespondGuest_LLMLeakIsGuarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "推荐 酸辣汤，很好喝。"}}},
		})
	}))
	defer srv.Close()

	e := New()
	kb := NewStoreKB(sampleStore(), dishesWithUntranslated(), "en", "英语", "¥")
	rep := e.RespondGuest(context.Background(), GuestAskInput{
		Question: "what soup do you have",
		KB:       kb,
		LLM:      LLMConfig{BaseURL: srv.URL, APIKey: "k", Model: "m"},
	})
	if rep.Source != "llm" {
		t.Fatalf("假 LLM 应被调用, 实际走了 %s", rep.Source)
	}
	if strings.Contains(rep.Content, "酸辣汤") {
		t.Errorf("LLM 泄露的未翻译菜中文原名应被守卫拦下, 实际: %q", rep.Content)
	}
	if !strings.Contains(rep.Content, "not sure") {
		t.Errorf("唯一一行被拦下后应回落诚实提示, 实际: %q", rep.Content)
	}
}
