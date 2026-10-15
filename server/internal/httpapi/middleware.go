// 本文件：HTTP 中间件 —— 跨域（CORS）、访问日志与鉴权。
//
// 包裹顺序为 withLog(withCORS(mux))，见 router.go 的 Routes：
// 日志在最外层（覆盖所有请求），CORS 在内层（给真实响应加跨域头），
// 鉴权则不是全局中间件，而是按路由逐个包 s.withAuth——因为客人端接口必须公开。
package httpapi

import (
	"context"
	"log"
	"net/http"
	"time"
)

// ctxKey 是 context 键的私有类型。
//
// 刻意用自定义类型而不是裸 string：不同包若用同名字符串做键会互相覆盖，
// 私有类型从根上避免了这种冲突（这是 Go 官方推荐的 context 用法）。
type ctxKey string

// ctxAccountID 是注入「当前登录账号 ID」的 context 键。
// 只有 withAuth 会写它，业务 handler 一律通过 accountID(r) 读，不要直接拼 key。
const ctxAccountID ctxKey = "account_id"

// withCORS 允许前端 dev server（Vite）跨域访问。
//
// 注意这里用的是通配符 *，前提是本接口靠 Bearer token 鉴权而非 Cookie，
// 浏览器不会自动携带凭证，因此不存在 CSRF 风险。
// 如果将来改成 Cookie 鉴权，必须把这里收紧为白名单并开启 Allow-Credentials。
// OPTIONS 预检直接 204 返回，不进入业务逻辑。
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withLog 记录每个请求的方法、路径与耗时。
//
// 放在最外层是为了覆盖所有请求（含 CORS 预检与鉴权失败），
// 它是排查「前端说接口不通 / 很慢」时的第一手事实依据。
// 只记录方法与路径，不记录 body 与 header，避免把 token 或菜单内容写进日志。
func withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%-6s %-40s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// withAuth 校验 Bearer token，并把 account_id 注入 context。
//
// 这是商家端的统一鉴权入口：校验失败直接 401 + UNAUTH，不进入 handler。
// 通过后 handler 用 accountID(r) 取当前账号，再结合 requireStore 判断
// 「这个门店是不是你的」，就构成了「认证（你是谁）+ 授权（你能不能碰）」两层防线。
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := verifyToken(s.secret, bearerToken(r))
		if err != nil {
			fail(w, http.StatusUnauthorized, CodeUnauth, "请先登录")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxAccountID, id)))
	}
}

// accountID 取出 withAuth 注入的账号 ID；未登录（未走 withAuth）时返回空串。
//
// 空串不是合法账号 ID，因此即便某个 handler 误漏了鉴权，也会在 requireStore
// 的归属校验处失败，而不会意外读到别人的数据。
func accountID(r *http.Request) string {
	v, _ := r.Context().Value(ctxAccountID).(string)
	return v
}
