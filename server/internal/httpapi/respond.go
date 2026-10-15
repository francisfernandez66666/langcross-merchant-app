// 本文件：HTTP 层的统一响应契约 —— JSON 序列化、成功/失败封装与错误码常量。
//
// 所有接口共用同一套形状，前端只需实现一次解包逻辑：
//
//	成功：{"ok":true, ...业务字段}
//	失败：{"ok":false,"error":{"code":"<语义串>","msg":"<给用户看的人话>"}}
//
// 本文件是 package httpapi 的响应出口，业务 handler 只应调用 ok / fail，
// 不要自己拼 json.NewEncoder，否则容易出现 Content-Type 漏设等不一致。
package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
)

// 错误码（语义串，与《数据模型与接口契约_全量》§3 一致）。
//
// 为什么用字符串而不是只用 HTTP 状态码：同一个 HTTP 状态可能对应多种业务原因
// （例如 400 既可能是「请求格式错误」也可能是「原文确认稿未确认」），
// 前端需要靠 code 做精确分支，msg 仅用于展示。
const (
	CodeUnauth      = "UNAUTH"       // 未登录，或 token 无效 / 已过期
	CodeForbidden   = "FORBIDDEN"    // 已登录但无权访问该资源（如门店不属于当前账号）
	CodeNotFound    = "NOT_FOUND"    // 资源不存在
	CodeConflict    = "CONFLICT"     // 状态冲突（如券重复核销、券已过期）
	CodeValidation  = "VALIDATION"   // 入参校验失败（最常见的 400）
	CodeOfflineOnly = "OFFLINE_ONLY" // 该能力仅支持线下场景
	CodeRateLimit   = "RATE_LIMIT"   // 触发限流
	CodeInternal    = "INTERNAL"     // 服务端内部错误
)

// writeJSON 是唯一的写响应出口：先定 Content-Type 与状态码，再编码 body。
//
// 统一走这里可以避免「某个接口忘设 Content-Type 导致前端按文本解析」这类低级问题。
// 注意 WriteHeader 必须在 Encode 之前调用，否则头已经发出去了。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ok 输出 200 + 成功体；调用方只需把业务字段塞进 map。
// 约定：map 里要带 "ok": true，与 fail 的输出形状保持对称。
func ok(w http.ResponseWriter, v any) { writeJSON(w, http.StatusOK, v) }

// fail 输出失败体：ok=false + error{code,msg}。
//
// status 是 HTTP 语义状态，code 是业务语义串，两者刻意解耦：
// HTTP 状态服务于通用中间件与日志，code 服务于前端精确分支。
func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": map[string]string{"code": code, "msg": msg},
	})
}

// decodeJSON 解析请求体（限 8MB，防大图 base64 撑爆）。
//
// 8MB 上限是刻意设的：菜单图经 base64 后会膨胀约 1/3，
// 限流在这里可以挡住「恶意超大 body」把内存打满。
// 读取失败一律由调用方转成 VALIDATION，本函数不直接写响应。
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v)
}
