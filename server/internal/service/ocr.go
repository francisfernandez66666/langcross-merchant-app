// Package service 承载对外部能力的抽象（OCR / 翻译 / 二维码），
// 使本地无外部服务时也能跑通，生产只需换实现。
//
// ── ocr.go：可插拔的菜单识别 ────────────────────────────────
//
// 菜单录入是本产品的第一道门槛：图片 → 结构化菜品行。
// 识别实现可插拔，由 LC_OCR_URL 决定：
//   - 未配置 → StubOCR：明确返回 need_manual=true，让商家手工录入（不假装识别成功）；
//   - 已配置 → HTTPOCR：转发 Python PaddleOCR 微服务做真实识别。
//
// ★ 与翻译同一条纪律「绝不编造」：识别不出就如实告知前端走手动保底，
// 绝不返回看似成功的空结果，让商家误以为已识别。
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OCRItem 是从菜单图里提取的一行（菜品名 + 可选价格）。
// Price 用指针是为了区分「价格缺失」与「价格为 0」。
type OCRItem struct {
	Name  string   `json:"name"`
	Price *float64 `json:"price,omitempty"`
}

// OCRResult 是识别结果。
type OCRResult struct {
	Items      []OCRItem `json:"items"`
	Confidence float64   `json:"confidence"`
	NeedManual bool      `json:"need_manual"` // true ⇒ 前端走手动保底（P2d）
	Engine     string    `json:"engine"`
}

// OCRProvider 抽象菜单识别。
// 生产接 Python PaddleOCR 微服务（服务端识别）；本地无服务时回落 StubOCR。
type OCRProvider interface {
	Extract(ctx context.Context, imageURL, imageBase64 string) (*OCRResult, error)
	Ready() bool
}

// StubOCR 在没有 OCR 服务时使用：明确要求前端走「手动保底」，
// 绝不假装识别成功（与「不编造」纪律一致）。
type StubOCR struct{}

// Ready 恒为 false：桩实现永远不算「就绪」，启动日志据此提示为 stub-manual。
func (StubOCR) Ready() bool { return false }

// Extract 返回空结果并置 need_manual=true，引导前端切到手工录入。
// 不返回 error —— 这是「设计如此」的降级路径，不是异常。
func (StubOCR) Extract(context.Context, string, string) (*OCRResult, error) {
	return &OCRResult{Items: []OCRItem{}, NeedManual: true, Engine: "stub"}, nil
}

// HTTPOCR 调用 Python OCR 微服务：POST {base}/api/ocr/extract。
type HTTPOCR struct {
	BaseURL string       // OCR 微服务根地址
	Client  *http.Client // 复用连接池的 HTTP 客户端（含超时）
}

// NewHTTPOCR 构造 HTTP OCR 客户端。
// 超时设 30s：图片识别（尤其大图）耗时明显高于普通接口。
func NewHTTPOCR(base string) *HTTPOCR {
	return &HTTPOCR{BaseURL: base, Client: &http.Client{Timeout: 30 * time.Second}}
}

// Ready 报告 OCR 服务地址是否已配置（非空即视为就绪）。
func (h *HTTPOCR) Ready() bool { return h.BaseURL != "" }

// Extract 把图片（URL 或 base64）转发给 OCR 微服务并解析识别结果。
//
// 入参二选一：imageURL 与 imageBase64 至少有一个非空（由 handler 校验）。
// 返回的 Engine 固定标注为 "paddleocr"，便于前端区分结果来源。
// 任何网络 / 状态码 / 解码错误都返回 error，由 handler 转成
// 「识别服务暂时不可用，请手动录入」并让前端走手动保底。
func (h *HTTPOCR) Extract(ctx context.Context, imageURL, imageBase64 string) (*OCRResult, error) {
	body, err := json.Marshal(map[string]string{"image_url": imageURL, "image_base64": imageBase64})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.BaseURL+"/api/ocr/extract", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocr service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocr service status %d", resp.StatusCode)
	}
	var out OCRResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode ocr response: %w", err)
	}
	out.Engine = "paddleocr"
	return &out, nil
}

// NewOCR 按配置选择 OCR 实现。
// serviceURL 为空（LC_OCR_URL 未配置）→ StubOCR（手动保底）；
// 否则 → HTTPOCR（真实识别）。这保证本地零配置也能跑通全链路。
func NewOCR(serviceURL string) OCRProvider {
	if serviceURL == "" {
		return StubOCR{}
	}
	return NewHTTPOCR(serviceURL)
}
