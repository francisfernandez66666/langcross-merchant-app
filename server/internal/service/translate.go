package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Translator 抽象翻译能力。
// 生产调「既有翻译链」（KB/TM + 敏感词闸，零改动）；本地无链路时用内置小词典，
// 保证引导流 P4 可完整跑通。未命中的词返回空串，由调用方回落显示原文——绝不编造。
type Translator interface {
	Translate(ctx context.Context, texts []string, from, to string) ([]string, error)
	Ready() bool
}

// DictTranslator 是内置最小中→英词典（仅本地演示 / 离线兜底）。
type DictTranslator struct{}

func (DictTranslator) Ready() bool { return true }

var zh2en = map[string]string{
	"宫保鸡丁":  "Kung Pao Chicken",
	"麻婆豆腐":  "Mapo Tofu",
	"水煮鱼":   "Sichuan Boiled Fish",
	"番茄炒蛋":  "Scrambled Eggs with Tomato",
	"糖醋里脊":  "Sweet and Sour Pork",
	"红烧肉":   "Braised Pork Belly",
	"清炒时蔬":  "Stir-fried Seasonal Greens",
	"蛋炒饭":   "Egg Fried Rice",
	"米饭":    "Steamed Rice",
	"可乐":    "Cola",
	"雪碧":    "Sprite",
	"啤酒":    "Beer",
	"酸辣汤":   "Hot and Sour Soup",
	"小食":    "Side dish",
	"本店招牌":  "House specialty",
	"招牌":    "Signature",
	"热菜":    "Hot dishes",
	"主食":    "Staples",
	"汤":     "Soups",
	"饮品":    "Drinks",
	"甜点":    "Desserts",
}

func (DictTranslator) Translate(_ context.Context, texts []string, _, _ string) ([]string, error) {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = zh2en[strings.TrimSpace(t)] // 未命中 ⇒ ""
	}
	return out, nil
}

// HTTPTranslator 调用既有翻译链 POST {base}/api/translation。
type HTTPTranslator struct {
	BaseURL string
	Client  *http.Client
}

// NewHTTPTranslator 构造 HTTP 翻译客户端。
func NewHTTPTranslator(base string) *HTTPTranslator {
	return &HTTPTranslator{BaseURL: base, Client: &http.Client{Timeout: 20 * time.Second}}
}

func (h *HTTPTranslator) Ready() bool { return h.BaseURL != "" }

func (h *HTTPTranslator) Translate(ctx context.Context, texts []string, from, to string) ([]string, error) {
	payload := map[string]any{"texts": texts, "source_lang": from, "target_lang": to}
	body, err := json.Marshal(payload)
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
		return nil, fmt.Errorf("translate service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("translate service status %d", resp.StatusCode)
	}
	var out struct {
		Translations []string `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode translate response: %w", err)
	}
	return out.Translations, nil
}

// NewTranslator 按配置选择翻译实现。
func NewTranslator(serviceURL string) Translator {
	if serviceURL == "" {
		return DictTranslator{}
	}
	return NewHTTPTranslator(serviceURL)
}
