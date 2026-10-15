// Package service 的请求上下文约定：把「当前租户」带进 translation 链路。
//
// 为什么翻译要按请求解析租户，而不是启动时装配一个固定引擎：
// 翻译 Key 由每个租户自己在后台维护，不同租户用不同的 Key（甚至不同的模式）。
// 一个进程同时服务多个租户时，Key 只能在请求层解析，
// 因此鉴权中间件要把账号的 tenant_id 写进 context，翻译链再据此刻画。
package service

import "context"

// tenantKey 是 context 键的私有类型（与 httpapi.ctxKey 同理：私有类型避免跨包串键）。
type tenantKey struct{}

// WithTenantID 把当前租户 ID 写入 context。tenantID 为空串表示平台级（无租户隔离需求）。
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenantID)
}

// TenantFromContext 取出当前租户 ID；未注入时返回空串。
// 空串是合法值：平台级请求（超管操作）与未登录的客人端预览都落在这一档。
func TenantFromContext(ctx context.Context) string {
	v, _ := ctx.Value(tenantKey{}).(string)
	return v
}
