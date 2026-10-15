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
	"strings"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// ctxKey 是 context 键的私有类型。
//
// 刻意用自定义类型而不是裸 string：不同包若用同名字符串做键会互相覆盖，
// 私有类型从根上避免了这种冲突（这是 Go 官方推荐的 context 用法）。
type ctxKey string

// ctxAccount 是注入「当前登录账号主体」的 context 键。
//
// 为什么注入整个账号而不只是 ID：角色与租户是每个请求都要用的
// （requireStore 要判权、翻译链要按租户取 Key），若只注入 ID，
// 每个 handler 都得再查一次库；注入主体后一次查询全链路复用。
const ctxAccount ctxKey = "account"

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

// withAuth 校验 Bearer token，加载账号主体并注入 context。
//
// 这是商家端的统一鉴权入口：校验失败直接 401 + UNAUTH，不进入 handler。
// 通过后 handler 用 currentAccount(r) 取账号（含角色 / 租户 / 状态），
// 再结合 requireStore 判断「这个门店是不是你的」，构成
// 「认证（你是谁）+ 授权（你能不能碰）」两层防线。
//
// ★ 每请求都回库取账号而不是只信任 token 里的 ID：角色调整、停用账号
// 必须立刻生效。token 是无状态的（30 天），无法单方面吊销，
// 那就只能让「权限判断」这一步始终读最新事实。
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := verifyToken(s.secret, bearerToken(r))
		if err != nil {
			fail(w, http.StatusUnauthorized, CodeUnauth, "请先登录")
			return
		}
		acc, err := s.repo.GetAccountByID(r.Context(), id)
		if err != nil {
			// token 有效但账号没了：按未登录处理，让前端退回登录页
			fail(w, http.StatusUnauthorized, CodeUnauth, "账号不存在")
			return
		}
		// 停用的账号禁止一切操作（数据保留，只是不能再进系统）
		if acc.Status != "active" {
			fail(w, http.StatusForbidden, CodeForbidden, "账号已停用，请联系管理员")
			return
		}
		ctx := context.WithValue(r.Context(), ctxAccount, acc)
		// 同时把租户写进 context：翻译链据此按租户取 Key（见 service.WithTenantID）
		ctx = service.WithTenantID(ctx, acc.TenantID)
		next(w, r.WithContext(ctx))
	}
}

// currentAccount 取出 withAuth 注入的账号主体；未登录时返回 nil。
//
// 所有 handler 都通过它取账号，不要再去查库——既省一次往返，
// 也保证「权限判断」与「业务查询」看到的是同一个快照。
func currentAccount(r *http.Request) *domain.Account {
	v, _ := r.Context().Value(ctxAccount).(*domain.Account)
	return v
}

// accountID 兼容旧写法：取出当前账号 ID；未登录（未走 withAuth）时返回空串。
//
// 空串不是合法账号 ID，因此即便某个 handler 误漏了鉴权，也会在 requireStore
// 的归属校验处失败，而不会意外读到别人的数据。
// 新代码请优先用 currentAccount(r)：需要角色 / 租户时不必再查库。
func accountID(r *http.Request) string {
	if acc := currentAccount(r); acc != nil {
		return acc.ID
	}
	return ""
}

// requireRole 要求当前账号的角色等级不低于 min（domain.RoleLevel 口径）。
//
// 返回 (nil, false) 时错误响应已经写好，调用方直接 return 即可。
// 刻意分成 401 / 403 两档：没登录是 401，登录了但等级不够是 403——
// 前端要靠这个区别决定「跳登录页」还是「提示无权限」。
//
// ★ 平台级双条件（抄自翻译助手 2026-09-16 安全整改）：当 min 达到最高等级时，
// 除了角色等级还必须 TenantID 为空串（平台级账号）。否则一条
// 「role=super_admin 但挂在某租户下」的旁路数据就能拿到跨租户权能——
// 那种账号在翻译助手的现网里真实存在过，并被用来跨租户退款。
func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, min int) (*domain.Account, bool) {
	acc := currentAccount(r)
	if acc == nil {
		fail(w, http.StatusUnauthorized, CodeUnauth, "请先登录")
		return nil, false
	}
	if domain.RoleLevel(acc.Role) < min {
		fail(w, http.StatusForbidden, CodeForbidden, "权限不足：需要 "+domain.RoleNameByLevel(min)+"及以上")
		return nil, false
	}
	// 最高等级额外要求平台级归属
	if min >= domain.MaxRoleLevel && !domain.IsPlatformSuperAdmin(acc) {
		fail(w, http.StatusForbidden, CodeForbidden, "权限不足：该操作仅限平台级超管")
		return nil, false
	}
	return acc, true
}

// effectiveTenant 解析「这次操作作用在哪个租户上」。
//
//   - 平台超管显式传 ?tenant_id=xxx 时可作用于任意租户（后台跨租户运维）；
//     不传则落到默认租户（平台自营的那份数据）；
//   - 其他人只能作用于自己所属租户，并忽略任何传入的 tenant_id——
//     否则普通商家传别人的租户 ID 就能看到别家数据。
func effectiveTenant(r *http.Request, acc *domain.Account) string {
	if acc.Role == domain.RoleSuperAdmin {
		if v := strings.TrimSpace(r.URL.Query().Get("tenant_id")); v != "" {
			return v
		}
		return domain.DefaultTenantID
	}
	return acc.TenantID
}
