package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newFakeLexicorn 起一个假能言平台，按 docs 的契约返回：
// 提交任务 202 + task_id；轮询先 processing 两次再 completed。
func newFakeLexicorn(t *testing.T, translation string) (*httptest.Server, *int32) {
	t.Helper()
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key-1234567890" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "invalid_api_key", "message": "Key 无效"})
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "task_id": 1024, "status": "queued", "poll_interval_sec": 1,
		})
	})
	mux.HandleFunc("/openapi/v1/tasks/status", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&polls, 1)
		if n < 3 {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "processing"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			// ★ 注意：能言返回的是 {lang: 译文} 对象，不是数组
			"translations": map[string]string{"en": translation},
			"points_used":  12,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &polls
}

func TestLexicornTranslateBatch(t *testing.T) {
	srv, _ := newFakeLexicorn(t, "Kung Pao Chicken\nMapo Tofu")
	eng := NewLexicornTranslator(srv.URL, "test-key-1234567890", "pro")
	eng.PollInterval = time.Millisecond // 测试里不等平台建议的间隔

	out, err := eng.Translate(context.Background(), []string{"宫保鸡丁", "麻婆豆腐"}, "zh", "en")
	if err != nil {
		t.Fatalf("翻译失败: %v", err)
	}
	if len(out) != 2 || out[0] != "Kung Pao Chicken" || out[1] != "Mapo Tofu" {
		t.Fatalf("译文不对: %v", out)
	}
}

// 空白文本不送译（省额度），但仍要返回等长切片占位。
func TestLexicornSkipsBlankTexts(t *testing.T) {
	srv, _ := newFakeLexicorn(t, "X")
	eng := NewLexicornTranslator(srv.URL, "test-key-1234567890", "pro")
	out, err := eng.Translate(context.Background(), []string{"", "   ", "宫保鸡丁"}, "zh", "en")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("长度应为 3，实际 %d", len(out))
	}
	if out[0] != "" || out[1] != "" {
		t.Error("空白文本必须保持空白，不能塞内容进去")
	}
}

// ★ 行数对不上时整批拒绝：宁可翻译失败交给你上层降级，
// 也不能把 A 的译文安到 B 头上（那是比「未翻译」更糟的错误）。
func TestLexicornRejectsMisalignedResult(t *testing.T) {
	srv, _ := newFakeLexicorn(t, "只有一行") // 提交两句却返回一行
	eng := NewLexicornTranslator(srv.URL, "test-key-1234567890", "pro")
	eng.PollInterval = time.Millisecond
	out, err := eng.Translate(context.Background(), []string{"宫保鸡丁", "麻婆豆腐"}, "zh", "en")
	if err == nil {
		t.Fatal("行数不一致时必须报错")
	}
	if len(out) != 2 || out[0] != "" || out[1] != "" {
		t.Errorf("失败时必须返回全空等长切片，实际 %v", out)
	}
}

func TestLexicornSingleText(t *testing.T) {
	srv, _ := newFakeLexicorn(t, "Kung Pao Chicken")
	eng := NewLexicornTranslator(srv.URL, "test-key-1234567890", "fast")
	eng.PollInterval = time.Millisecond
	got, err := eng.TranslateOne(context.Background(), "宫保鸡丁", "en")
	if err != nil {
		t.Fatalf("失败: %v", err)
	}
	if got != "Kung Pao Chicken" {
		t.Errorf("got=%q", got)
	}
}

// 任务失败必须带上平台错误码，上层据此给人话提示。
func TestLexicornTaskFailureMapsErrorCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": 7, "status": "queued", "poll_interval_sec": 1})
	})
	mux.HandleFunc("/openapi/v1/tasks/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "failed", "error_code": "insufficient_balance", "message": "余额不足",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	eng := NewLexicornTranslator(srv.URL, "test-key-1234567890", "pro")
	eng.PollInterval = time.Millisecond
	_, err := eng.TranslateOne(context.Background(), "宫保鸡丁", "en")
	if !IsBalanceError(err) {
		t.Fatalf("应识别为余额不足，实际 err=%v", err)
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "insufficient_balance" {
		t.Errorf("错误码应为 insufficient_balance，实际 %v", err)
	}
}

// Key 无效必须是可识别的另一类错误（提示商家重新维护 Key）。
func TestLexicornAuthErrorClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "invalid_api_key", "message": "Key 已轮换"})
	}))
	defer srv.Close()

	eng := NewLexicornTranslator(srv.URL, "stale-key-abcdefghij", "pro")
	_, err := eng.TranslateOne(context.Background(), "宫保鸡丁", "en")
	if !IsAuthError(err) {
		t.Fatalf("应识别为鉴权错误，实际 err=%v", err)
	}
}

// 429 必须解析出 Retry-After：否则限流时只能盲猜退避时间。
func TestLexicornRetryAfterParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "rate_limited", "retry_after": 30})
	}))
	defer srv.Close()

	eng := NewLexicornTranslator(srv.URL, "test-key-1234567890", "pro")
	_, err := eng.TranslateOne(context.Background(), "宫保鸡丁", "en")
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("应得到 APIError，实际 %v", err)
	}
	if ae.RetryAfter != 30 {
		t.Errorf("Retry-After 应为 30，实际 %d", ae.RetryAfter)
	}
	if !strings.Contains(ae.Code, "rate_limited") {
		t.Errorf("错误码应为 rate_limited，实际 %s", ae.Code)
	}
}

// 未登记的语种在入口就该被挡掉：否则白提一个注定失败的任务还扣额度。
func TestLexicornRejectsUnsupportedLang(t *testing.T) {
	eng := NewLexicornTranslator("https://example.invalid", "test-key-1234567890", "pro")
	if _, err := eng.Translate(context.Background(), []string{"x"}, "zh", "it"); err == nil {
		t.Error("不支持的语种应被拒绝")
	}
}

// 没配 Key 时 Ready() 必须为 false：上层据此跳过本引擎。
func TestLexicornReadyRequiresKey(t *testing.T) {
	if NewLexicornTranslator("", "", "pro").Ready() {
		t.Error("无 Key 时不应就绪")
	}
	if !NewLexicornTranslator("", "test-key-1234567890", "pro").Ready() {
		t.Error("有 Key 应当就绪")
	}
	// 默认基址不能为空：否则请求会打到本地
	if got := NewLexicornTranslator("", "k", "pro").BaseURL; got != LexicornDefaultBaseURL {
		t.Errorf("默认基址应为 %s，实际 %s", LexicornDefaultBaseURL, got)
	}
}

// mode 兜底：只有 fast/pro 合法，其它值一律按 pro（高质量档，宁可多花不可错译）。
func TestLexicornModeFallback(t *testing.T) {
	if got := NewLexicornTranslator("", "k", "nonsense").Mode; got != "pro" {
		t.Errorf("非法 mode 应兜底为 pro，实际 %s", got)
	}
	if got := NewLexicornTranslator("", "k", "fast").Mode; got != "fast" {
		t.Errorf("fast 应保留，实际 %s", got)
	}
}
