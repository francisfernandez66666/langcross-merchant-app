package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLMConfig 是一次问答要用的模型配置（由超管在后台维护）。
type LLMConfig struct {
	BaseURL string // 如 https://api.openai.com/v1
	APIKey  string // 明文（调用方从密文解出后传入）
	Model   string // 如 gpt-4o-mini / deepseek-chat
}

// Enabled 判断配置是否可用（三项缺一不可）。
func (c LLMConfig) Enabled() bool {
	return strings.TrimSpace(c.BaseURL) != "" &&
		strings.TrimSpace(c.APIKey) != "" &&
		strings.TrimSpace(c.Model) != ""
}

// llmClient 是 OpenAI 兼容的 /chat/completions 客户端。
// 选兼容协议而不是绑定某一家：国内可用的模型服务（DeepSeek / 通义 / 智谱）
// 基本都提供这个端点，换供应商只改 base_url 与 model。
type llmClient struct {
	cfg    LLMConfig
	client *http.Client
}

// newLLMClient 按凭据新建一个 OpenAI 兼容的对话客户端（base_url / key / model 已就位，45s 超时）。
func newLLMClient(cfg LLMConfig) *llmClient {
	return &llmClient{cfg: cfg, client: &http.Client{Timeout: 45 * time.Second}}
}

// chat 请求一次补全，返回文本。
// 失败一律返回 error，由引擎降级到确定性兜底——不允许把错误当成答案。
func (c *llmClient) chat(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		// 低温：经营助手要的是稳定、可复现的回答，不是创意
		"temperature": 0.2,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm 状态 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm 未返回任何选择")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// truncate 按字节截断字符串到 n（用于兜底限制模型回复长度）；
// 不做 rune 对齐——这里只用于展示前的粗截断，宁可截断也不无限增长。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
