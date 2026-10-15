// Package service 的能言（LangCross）开放 API 引擎实现。
//
// 能言开放平台是本产品的翻译上游：https://langcross.lexicorn.cn/openapi/docs
// 调用方是「租户在后台自己维护的 API Key」，因此本实现刻意不持有 Key——
// Key 由上层（TenantPrimary）按请求解析后注入，进程本身不长期保存任何租户凭据。
//
// 能言的三个关键约定（实现必须严格遵守，否则会白扣额度或误判失败）：
//  1. 异步任务模型：POST /openapi/v1/tasks 提交后拿 task_id，
//     GET /openapi/v1/tasks/status?id= 轮询到 completed / failed 才是终态；
//  2. 失败一律用真实 HTTP 状态码（2026-09-26 起），body 里 code 是正主，
//     429 额外给 Retry-After 头与同值 retry_after 字段，必须按它退避；
//  3. 文本任务的响应是 translations: {lang: 译文} 的对象，不是数组。
//
// 批量口径：能言的文本任务一次只收一个 text 字段，因此多句批量采用
// 「换行拼接提交 + 按行拆回」。拆回来行数对不上就整批判失败，
// 交给上层降级到其它引擎——绝不对不齐还硬塞（那等于把 A 的译文安到 B 头上）。
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 能言开放 API 默认值。
const (
	// LexicornDefaultBaseURL 是能言开放平台的默认地址。
	LexicornDefaultBaseURL = "https://langcross.lexicorn.cn"
	// LexicornTextPollInterval 是文本任务的轮询间隔。
	//
	// 文档建议 15s，那是「别把平台打挂」的礼貌值；本产品的文本都是短句
	// （菜名 / 分类名 / 评论），任务实际几秒就完成，全程等 15s 会让
	// 商家在页面上干等。故取 3s：请求量仍在个位数级，且设置上限兜底。
	LexicornTextPollInterval = 3 * time.Second
	// LexicornMaxWait 是单个任务的最长等待时间。
	LexicornMaxWait = 90 * time.Second
)

// APIError 是能言接口返回的业务错误（带平台错误码，便于上层区分处理）。
//
// 为什么要单独的类型而不是 error 字符串：上层需要按 code 决策——
// 余额不足要提示充值、Key 失效要提示重新维护、限流要按 Retry-After 退避，
// 这些用字符串包含判断既脆弱又难测试。
type APIError struct {
	Code       string // 平台错误码，如 insufficient_balance / invalid_api_key / key_quota_exceeded
	Status     int    // HTTP 状态码
	Message    string // 平台给人看的文案（已按 Accept-Language 本地化）
	RetryAfter int    // 429 时的建议等待秒数；0 表示未提供
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return fmt.Sprintf("lexicorn %s(%d): %s", e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("lexicorn %s(%d)", e.Code, e.Status)
}

// IsQuotaExceeded 判断是否为「今日调用次数 / 用量到顶」。
// 这类错误重试无意义，应立即降级并告知用户明天再来或提额。
func IsQuotaExceeded(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == "key_quota_exceeded" || ae.Code == "daily_quota_exceeded"
	}
	return false
}

// IsAuthError 判断是否为 Key 无效 / 无权限。
// 商家在后台录错了 Key、或者 Key 被平台侧轮换过，都会落在这里。
func IsAuthError(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == "invalid_api_key" || ae.Code == "forbidden" || ae.Status == http.StatusUnauthorized
	}
	return false
}

// IsBalanceError 判断是否为积分余额不足。
func IsBalanceError(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == "insufficient_balance" || ae.Status == http.StatusPaymentRequired
	}
	return false
}

// lexiLang 把内部语种码映射为能言的目标语言码。
//
// 我方 10 语种与能言 34 语种是同一套 ISO 码（en/ja/ko/th/fr/es/de/pt/vi + zh），
// 因此这里是恒等映射；保留函数是为了：
//   - 将来内部改名（或接入别的平台）只改这一处；
//   - 在入口就挡掉能言不认识的语种，避免白提一个注定失败的任务。
func lexiLang(code string) (string, bool) {
	switch code {
	case "zh", "en", "ja", "ko", "th", "fr", "es", "de", "pt", "vi":
		return code, true
	default:
		return "", false
	}
}

// LexicornTranslator 调用能言开放 API 完成翻译。
//
// 零值不可用：必须经 NewLexicornTranslator 构造（带上 Key 与超时）。
type LexicornTranslator struct {
	BaseURL string       // 平台根地址，默认 https://langcross.lexicorn.cn
	APIKey  string       // 租户维护的 API Key
	Mode    string       // fast / pro（缺省 pro）
	Client  *http.Client // 复用连接池的 HTTP 客户端

	PollInterval time.Duration // 轮询间隔；零值用 LexicornTextPollInterval
	MaxWait      time.Duration // 最长等待；零值用 LexicornMaxWait
}

// NewLexicornTranslator 构造能言翻译客户端。
// apiKey 为空时 Ready() 为 false，上层据此跳过本引擎。
func NewLexicornTranslator(baseURL, apiKey, mode string) *LexicornTranslator {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = LexicornDefaultBaseURL
	}
	if mode != "fast" {
		mode = "pro"
	}
	return &LexicornTranslator{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  strings.TrimSpace(apiKey),
		Mode:    mode,
		// 90s 覆盖「提交 + 轮询到出结果」的全过程，比单次 HTTP 超时更重要：
		// 任务期间会有多次短请求，卡死的应当是整个过程而不是某一次请求。
		Client: &http.Client{Timeout: LexicornMaxWait + 30*time.Second},
	}
}

// Ready 只有拿到了 Key 才算就绪（配置是否齐全，不代表网络此刻可达）。
func (l *LexicornTranslator) Ready() bool { return l.APIKey != "" }

// Name 返回引擎标识，用于日志、健康检查与 TM 回写时的 provider。
func (l *LexicornTranslator) Name() string { return "lexicorn" }

// Translate 批量翻译：按目标语种提交一次任务，拆行回填。
//
// 返回契约与其它引擎一致：与 texts 等长的切片，空串 = 未翻译；
// 任何失败都返回 error，由上层 FallbackTranslator 兜底（绝不编造）。
//
// 注意：能言会自动探测源语言，因此不传 source_lang——我们的原文固定中文，
// 传了反而在多原文语种场景下限制平台的能力。
func (l *LexicornTranslator) Translate(ctx context.Context, texts []string, _, to string) ([]string, error) {
	out := make([]string, len(texts))
	if l.APIKey == "" || len(texts) == 0 {
		return out, nil
	}
	lang, ok := lexiLang(to)
	if !ok {
		return out, fmt.Errorf("lexicorn: 不支持的语种 %q", to)
	}

	// 空白文本不送译：既省额度，也避免平台对空串返回奇怪结果
	joined := make([]string, 0, len(texts))
	idx := make([]int, 0, len(texts))
	for i, t := range texts {
		if strings.TrimSpace(t) == "" {
			continue
		}
		joined = append(joined, t)
		idx = append(idx, i)
	}
	if len(joined) == 0 {
		return out, nil
	}

	res, err := l.translateBlock(ctx, strings.Join(joined, "\n"), lang)
	if err != nil {
		return out, err
	}
	parts := strings.Split(res, "\n")
	// ★ 对齐校验：行数不一致说明平台重排 / 合并 / 拆分了句子，
	//   此时任何回填都是张冠李戴，宁可整批失败交给你上层降级。
	if len(parts) != len(joined) {
		return out, fmt.Errorf("lexicorn: 返回行数 %d 与提交 %d 不一致，拒绝回填", len(parts), len(joined))
	}
	for j, i := range idx {
		out[i] = strings.TrimSpace(parts[j])
	}
	return out, nil
}

// TranslateOne 翻译单条文本，返回原文（失败时）或译文。
//
// 供「试译」接口使用：单句不存在对齐问题，结果最干净，
// 也最能在后台里给出「这把 Key 到底能不能用」的确定答案。
func (l *LexicornTranslator) TranslateOne(ctx context.Context, text, to string) (string, error) {
	if l.APIKey == "" {
		return "", errors.New("lexicorn: 未配置 API Key")
	}
	lang, ok := lexiLang(to)
	if !ok {
		return "", fmt.Errorf("lexicorn: 不支持的语种 %q", to)
	}
	v, err := l.translateBlock(ctx, text, lang)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// translateBlock 提交一个文本块并等待结果（一次任务、一个目标语种）。
func (l *LexicornTranslator) translateBlock(ctx context.Context, text, lang string) (string, error) {
	id, interval, err := l.createTask(ctx, text, lang)
	if err != nil {
		return "", err
	}
	return l.poll(ctx, id, lang, interval)
}

// createTask 提交文本任务，返回 task_id 与平台建议的轮询间隔（秒）。
func (l *LexicornTranslator) createTask(ctx context.Context, text, lang string) (int64, int, error) {
	body, err := json.Marshal(map[string]any{
		"text":         text,
		"target_langs": []string{lang},
		"mode":         l.Mode,
	})
	if err != nil {
		return 0, 0, err
	}
	var out struct {
		TaskID          int64  `json:"task_id"`
		Status          string `json:"status"`
		PollIntervalSec int    `json:"poll_interval_sec"`
		BalancePoints   int64  `json:"balance_points"`
	}
	if err := l.call(ctx, http.MethodPost, "/openapi/v1/tasks", body, &out); err != nil {
		return 0, 0, err
	}
	if out.TaskID <= 0 {
		return 0, 0, fmt.Errorf("lexicorn: 响应缺少 task_id")
	}
	return out.TaskID, out.PollIntervalSec, nil
}

// poll 轮询任务直到终态，返回该语种的译文。
//
// 退避口径：首次立即查一次（任务可能极快完成），之后按平台建议间隔，
// 但不低于 1s、不高于 3s（见 LexicornTextPollInterval 的取舍说明）。
// 全程受 MaxWait 约束：超时即失败，绝不无限占用商家的一次点击。
func (l *LexicornTranslator) poll(ctx context.Context, id int64, lang string, suggested int) (string, error) {
	interval := l.pollInterval()
	if suggested > 0 && time.Duration(suggested)*time.Second < interval {
		interval = time.Duration(suggested) * time.Second
	}
	if interval < time.Second {
		interval = time.Second
	}
	maxWait := l.MaxWait
	if maxWait <= 0 {
		maxWait = LexicornMaxWait
	}
	deadline := time.Now().Add(maxWait)
	url := fmt.Sprintf("/openapi/v1/tasks/status?id=%d", id)

	for {
		var st struct {
			Status       string            `json:"status"`
			Translations map[string]string `json:"translations"`
			Code         string            `json:"code"`
			ErrorCode    string            `json:"error_code"` // 历史别名，某些通道仍会下发
			Message      string            `json:"message"`
			PointsUsed   int64             `json:"points_used"`
		}
		if err := l.call(ctx, http.MethodGet, url, nil, &st); err != nil {
			return "", err
		}
		switch st.Status {
		case "completed":
			v := strings.TrimSpace(st.Translations[lang])
			if v == "" {
				// 任务是完成了，但这个语种没有结果：等价于没翻成
				return "", fmt.Errorf("lexicorn: 任务完成但语种 %s 无译文", lang)
			}
			return v, nil
		case "failed":
			code := st.Code
			if code == "" {
				code = st.ErrorCode
			}
			if code == "" {
				code = "task_failed"
			}
			return "", &APIError{Code: code, Status: http.StatusConflict, Message: st.Message}
		}
		// queued / processing / 未知值：继续等
		if time.Now().After(deadline) {
			return "", fmt.Errorf("lexicorn: 等待任务 %d 超时（%s）", id, maxWait)
		}
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// Balance 查询积分余额（后台 Key 面板展示「还能翻多少」）。
func (l *LexicornTranslator) Balance(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := l.call(ctx, http.MethodGet, "/openapi/v1/balance", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RotateKey 调用平台侧 Key 轮换（旧 Key 立即失效）。
// 商家在后台点「轮换」时调用：平台换一把新的，我们再把新 Key 存回本地。
func (l *LexicornTranslator) RotateKey(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := l.call(ctx, http.MethodPost, "/openapi/apikey/rotate", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// pollInterval 返回轮询间隔（零值兜底为默认值）。
func (l *LexicornTranslator) pollInterval() time.Duration {
	if l.PollInterval > 0 {
		return l.PollInterval
	}
	return LexicornTextPollInterval
}

// call 调一次能言接口：带认证头、解析统一错误、把成功响应解进 out。
//
// 错误处理的三条纪律：
//  1. 非 2xx 一律解析成 *APIError（429 时带上 Retry-After），上层按 code 决策；
//  2. 响应体读取后关闭，且限制读取量（平台故障时不至于把内存撑爆）；
//  3. 传给 out 为 nil 时只读 body 不做解码（调用方不关心响应体）。
func (l *LexicornTranslator) call(ctx context.Context, method, path string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, l.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+l.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept-Language", "zh-CN")

	resp, err := l.Client.Do(req)
	if err != nil {
		return fmt.Errorf("lexicorn 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 最多读 1MB：平台的错误 / 成功响应都远小于此，超出即异常
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Status: resp.StatusCode}
		var payload struct {
			Code       string `json:"code"`
			ErrorCode  string `json:"error_code"`
			Message    string `json:"message"`
			RetryAfter int    `json:"retry_after"`
		}
		_ = json.Unmarshal(raw, &payload)
		ae.Code = payload.Code
		if ae.Code == "" {
			ae.Code = payload.ErrorCode // 历史通道只给 error_code
		}
		if ae.Code == "" {
			ae.Code = "http_" + strconv.Itoa(resp.StatusCode)
		}
		ae.Message = payload.Message
		ae.RetryAfter = payload.RetryAfter
		if ra := resp.Header.Get("Retry-After"); ra != "" && ae.RetryAfter == 0 {
			if n, convErr := strconv.Atoi(strings.TrimSpace(ra)); convErr == nil {
				ae.RetryAfter = n
			}
		}
		return ae
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	return nil
}
