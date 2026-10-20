package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

/* ── apiNotFound：未知路径与方法不匹配的兜底 ──────────────────
   契约：所有 /api 响应都是 JSON 信封。Go mux 默认的纯文本
   "404 page not found" / "405 method not allowed" 会让前端统一解包
   失败，因此兜底必须自己写信封，并区分「路径不存在」与「方法不对」。 */

// newFallbackMux 造一个带若干代表性路由 + 兜底的小 mux，
// 与 Routes() 的注册方式一致（方法特定路由 + 方法无关的 /api/ 子树兜底）。
func newFallbackMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/stores/{id}", func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/uploads/", func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true})
	})
	nf := s.apiNotFound(mux, fallbackPatterns)
	mux.Handle("/api/", nf)
	mux.Handle("/api", nf)
	return mux
}

// 锁定：四类未知路径（含 /api/ 与 /api 本身）必须返回 JSON 信封的
// 404 + NOT_FOUND/「接口不存在」——Go 默认纯文本 404 会让前端统一解包失败。
func TestAPIFallback_UnknownPathReturns404Envelope(t *testing.T) {
	s := &Server{}
	h := newFallbackMux(s)

	for _, path := range []string{"/api/nonexistent", "/api/stores/abc/unknown-sub", "/api/", "/api"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s 应 404，实际 %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("GET %s 应返回 JSON，实际 Content-Type=%q", path, ct)
		}
		_, code, msg := errBody(t, rec.Body.Bytes())
		if code != CodeNotFound || msg != "接口不存在" {
			t.Errorf("GET %s 应返回 NOT_FOUND/接口不存在，实际 code=%q msg=%q", path, code, msg)
		}
	}
}

// 锁定：路径存在但方法不对 → 405 + Allow 头 + JSON 信封。
// Allow 必须精确到「GET, HEAD」（Go mux 语义：GET 路由自动接受 HEAD），
// 前端靠这个决定提示文案，不能笼统返回 404。
func TestAPIFallback_MethodMismatchReturns405WithAllow(t *testing.T) {
	s := &Server{}
	h := newFallbackMux(s)

	// DELETE 打一个只注册了 GET 的路径：必须 405 + Allow，而不是 404
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/health", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("方法不匹配应 405，实际 %d", rec.Code)
	}
	// GET 路由同时接受 HEAD（Go mux 语义），Allow 必须精确列出这两个
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow 头应为 \"GET, HEAD\"，实际 %q", allow)
	}
	_, code, msg := errBody(t, rec.Body.Bytes())
	if code != CodeValidation || msg != "方法不允许：该路径仅支持 GET, HEAD" {
		t.Errorf("405 应是 JSON 信封且 msg 含允许的方法，实际 code=%q msg=%q", code, msg)
	}
}

// 锁定：兜底（/api/ 子树）不得屏蔽已注册的具体路由——
// 依赖 Go 1.22 起的最长匹配优先，这条是「加兜底别把正路堵了」的保险。
func TestAPIFallback_RegisteredRoutesUnaffected(t *testing.T) {
	s := &Server{}
	h := newFallbackMux(s)

	// 兜底注册不能屏蔽已注册的具体路由（Go 1.22 最长匹配优先）
	for _, path := range []string{"/api/health", "/api/stores/abc", "/api/uploads/x.png"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s 应仍为 200，实际 %d", path, rec.Code)
		}
	}
}

// 锁定：对只注册了 GET 的带参路径（/api/stores/{id}）发 POST 同样 405。
// 与上一条互补：带路径参数的路由也不能被兜底吞成 404。
func TestAPIFallback_POSTToGetOnlyPathIs405(t *testing.T) {
	s := &Server{}
	h := newFallbackMux(s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/stores/abc", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 打 GET-only 路径应 405，实际 %d", rec.Code)
	}
}

// 锁定：兜底走 Routes() 真实装配时，404 信封仍带 CORS 头——
// 否则浏览器端 fetch 收到的是 CORS 错误而不是可解析的 JSON，
// 前端错误分支全部失效。withRecover/withCORS 的嵌套顺序由此被间接锁定。
func TestAPIFallback_ThroughFullMiddlewareChain(t *testing.T) {
	// 走 Routes() 的真实装配：验证兜底在 withLog/withRecover/withCORS 之内，
	// 恢复/兜底响应同样带跨域头（浏览器 fetch 收到 JSON 而非 CORS 错误）。
	s := &Server{}
	h := s.Routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/definitely-not-a-route", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知路由应 404，实际 %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("兜底响应应带 CORS 头，实际 %q", origin)
	}
	_, code, _ := errBody(t, rec.Body.Bytes())
	if code != CodeNotFound {
		t.Errorf("应返回 NOT_FOUND 信封，实际 code=%q", code)
	}
}

// 锁定（刻意边界）：非 /api 路径不属于 API 契约，保持 Go 默认纯文本 404，
// 为将来在同一进程挂 SPA（index.html 回退）留出空间。
func TestAPIFallback_NonAPIPathKeepsGoDefault(t *testing.T) {
	// 刻意的边界：非 /api 路径不属于 API 契约，留给 Go 默认纯文本 404，
	// 为将来在本进程挂 SPA（index.html 回退）留出空间。
	s := &Server{}
	h := s.Routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("根路径应 404，实际 %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "404 page not found") {
		t.Errorf("非 /api 路径应保持 Go 默认纯文本，实际 %q", body)
	}
}
