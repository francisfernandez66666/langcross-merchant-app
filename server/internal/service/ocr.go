// Package service 承载对外部能力的抽象（OCR / 翻译 / 二维码），
// 使本地无外部服务时也能跑通，生产只需换实现。
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

func (StubOCR) Ready() bool { return false }

func (StubOCR) Extract(context.Context, string, string) (*OCRResult, error) {
	return &OCRResult{Items: []OCRItem{}, NeedManual: true, Engine: "stub"}, nil
}

// HTTPOCR 调用 Python OCR 微服务：POST {base}/api/ocr/extract。
type HTTPOCR struct {
	BaseURL string
	Client  *http.Client
}

// NewHTTPOCR 构造 HTTP OCR 客户端。
func NewHTTPOCR(base string) *HTTPOCR {
	return &HTTPOCR{BaseURL: base, Client: &http.Client{Timeout: 30 * time.Second}}
}

func (h *HTTPOCR) Ready() bool { return h.BaseURL != "" }

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
func NewOCR(serviceURL string) OCRProvider {
	if serviceURL == "" {
		return StubOCR{}
	}
	return NewHTTPOCR(serviceURL)
}
