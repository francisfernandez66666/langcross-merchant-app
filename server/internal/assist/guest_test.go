package assist

import (
	"context"
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

func TestGuestFallback_UnknownAdmits(t *testing.T) {
	e := New()
	kb := NewStoreKB(sampleStore(), sampleDishes(), "en", "英语", "¥")
	rep := e.guestFallback(GuestAskInput{KB: kb}, "what is the meaning of life")
	if !strings.Contains(rep.Content, "not sure") {
		t.Errorf("答不上来应诚实承认, 实际: %q", rep.Content)
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
