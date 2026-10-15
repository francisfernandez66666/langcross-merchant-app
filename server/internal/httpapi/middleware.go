package httpapi

import (
	"context"
	"log"
	"net/http"
	"time"
)

type ctxKey string

const ctxAccountID ctxKey = "account_id"

// withCORS 允许前端 dev server（Vite）跨域访问。
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
func withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%-6s %-40s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// withAuth 校验 Bearer token，并把 account_id 注入 context。
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

func accountID(r *http.Request) string {
	v, _ := r.Context().Value(ctxAccountID).(string)
	return v
}
