package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/config"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// 本文件覆盖 2026-10-19 字节级 UAT 的 F-5：健康检查的翻译字段必须诚实。
// 修订前：装配自带词典兜底 → Ready() 恒真 → 链地址配成黑洞也报 live。

// deadAddr 取一个确定没人听的端口（先占再关），避免硬编码撞端口。
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占端口失败: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// healthBody 打一次 /api/health 并把响应体解成 map。
func healthBody(t *testing.T, s *Server) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("健康检查应 200，实际 %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（body=%s）", err, rec.Body.String())
	}
	if body["ok"] != true {
		t.Fatalf("健康检查应 ok=true，实际 %v", body["ok"])
	}
	return body
}

// 锁定（F-5 正面回归）：链地址指向没人听的端口时，健康检查必须四重诚实——
// engine=chain、reachable=false、ready=false、mode=dict-stub。
// 修订前装配含恒可达的词典兜底，这里会谎报 live：运维看着「翻译健康」，
// 实际每次翻译都在静默降级。dev_mode 与 ocr_mode 也必须如实披露。
func TestHandleHealth_BlackHoleChainIsNotLive(t *testing.T) {
	// ★ F-5 的正面回归：链地址指向没人听的端口。
	// 修订前这里会报 translate_mode=live —— 运维看着「翻译很健康」，
	// 实际每一次翻译都在降级到内置词典。
	tr := service.NewTranslatorWithKeys("http://"+deadAddr(t), "", nil, nil)
	s := NewServer(config.Config{DevMode: true}, nil, service.StubOCR{}, tr, nil, nil)

	body := healthBody(t, s)

	if got := body["translate_engine"]; got != service.EngineChain {
		t.Errorf("配了链地址就该报 chain，实际 %v", got)
	}
	if got := body["translate_reachable"]; got != false {
		t.Errorf("黑洞地址必须报不可达，实际 %v", got)
	}
	if got := body["translate_ready"]; got != false {
		t.Errorf("主引擎不可达就不该算就绪，实际 %v", got)
	}
	if got := body["translate_mode"]; got != "dict-stub" {
		t.Errorf("实际在走词典就该报 dict-stub，实际 %v", got)
	}
	if got := body["dev_mode"]; got != true {
		t.Errorf("开发模式应如实披露（固定验证码 / 弱密钥），实际 %v", got)
	}
	if got := body["ocr_mode"]; got != "stub-manual" {
		t.Errorf("未配 OCR 服务应报 stub-manual，实际 %v", got)
	}
}

// 锁定（F-5 反向边界）：链地址有应答（哪怕 405）就应报可达 + live。
// 防止「诚实化」做过头——把能用的引擎也报成不可达，那会引发误报警。
// 顺带锁定非开发模式 dev_mode=false：生产默认值不容含糊。
func TestHandleHealth_LiveChainReportsLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed) // 任何应答都算活着
	}))
	defer srv.Close()

	tr := service.NewTranslatorWithKeys(srv.URL, "", nil, nil)
	s := NewServer(config.Config{}, nil, service.StubOCR{}, tr, nil, nil)

	body := healthBody(t, s)

	if got := body["translate_engine"]; got != service.EngineChain {
		t.Errorf("引擎应为 chain，实际 %v", got)
	}
	if got := body["translate_reachable"]; got != true {
		t.Errorf("链在应答就该报可达，实际 %v", got)
	}
	if got := body["translate_ready"]; got != true {
		t.Errorf("主引擎可达即就绪，实际 %v", got)
	}
	if got := body["translate_mode"]; got != "live" {
		t.Errorf("主引擎在用应报 live，实际 %v", got)
	}
	if got := body["dev_mode"]; got != false {
		t.Errorf("非开发模式应报 false（这才是生产默认），实际 %v", got)
	}
}

// TestHandleHealth_UnprobedTranslatorKeepsLegacySemantics 保证没实现自陈
// 接口的翻译器（测试假件 / 自定义注入）不会让健康检查崩或说胡话：
// 沿用 Ready() 的旧口径，engine 留空表示「未知」。
func TestHandleHealth_UnprobedTranslatorKeepsLegacySemantics(t *testing.T) {
	s := NewServer(config.Config{}, nil, service.StubOCR{}, &stubTranslator{}, nil, nil)

	body := healthBody(t, s)

	if got := body["translate_ready"]; got != true {
		t.Errorf("假翻译器 Ready()=true 时应沿用旧口径报 true，实际 %v", got)
	}
	if got := body["translate_mode"]; got != "live" {
		t.Errorf("旧口径下应报 live，实际 %v", got)
	}
	if got := body["translate_engine"]; got != "" {
		t.Errorf("未实现自陈接口时引擎标识应留空，实际 %v", got)
	}
}

// stubTranslator 只实现 Translator 三件套，故意不实现 TranslateReporter。
type stubTranslator struct{}

// Translate 实现 Translator：永不报错、永不返回结果——
// 本假件只参与健康检查口径判定，不产出真实译文。
func (stubTranslator) Translate(_ context.Context, _ []string, _, _ string) ([]string, error) {
	return nil, nil
}

// Ready 恒真：模拟「自认为就绪」的旧式翻译器，
// 健康检查对这类实现必须沿用旧口径（见下方测试）。
func (stubTranslator) Ready() bool { return true }

// Name 返回假名，供日志与调试识别。
func (stubTranslator) Name() string {
	return "stub"
}
