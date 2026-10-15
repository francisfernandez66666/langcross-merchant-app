package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
)

// 错误码（语义串，与《数据模型与接口契约_全量》§3 一致）。
const (
	CodeUnauth      = "UNAUTH"
	CodeForbidden   = "FORBIDDEN"
	CodeNotFound    = "NOT_FOUND"
	CodeConflict    = "CONFLICT"
	CodeValidation  = "VALIDATION"
	CodeOfflineOnly = "OFFLINE_ONLY"
	CodeRateLimit   = "RATE_LIMIT"
	CodeInternal    = "INTERNAL"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) { writeJSON(w, http.StatusOK, v) }

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": map[string]string{"code": code, "msg": msg},
	})
}

// decodeJSON 解析请求体（限 8MB，防大图 base64 撑爆）。
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v)
}
