// 本文件：后台管理接口 —— 租户、账号角色、翻译 API Key、审计日志。
//
// 权限模型（四级角色，见 domain/tenant.go）：
//
//	super_admin  平台超管：租户增删改、平台统计、跨租户查看、调整任意账号角色
//	tenant_admin 租户管理员：本租户账号管理、维护本租户翻译 Key
//	manager      门店管理员：无后台管理权限（经营入口在 /api/stores/*）
//	staff        店员：同上
//
// 两条铁律：
//  1. 写操作必须落 admin_audit（谁是操作者、对谁、做了什么），这是「超管可动一切」的唯一制衡；
//  2. Key 明文只在「创建后查看 / 轮换后查看」这两个一次性响应里出现，
//     列表、日志、审计 detail 一律只有脱敏串。
package httpapi

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/crypt"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// registerAdmin 注册后台管理路由。
//
// 统一走 withAuth（先认证），角色判定放在各 handler 内：
// 这样 401 / 403 才能分开返回，前端也才好决定「跳登录」还是「提示无权限」。
func (s *Server) registerAdmin(mux *http.ServeMux) {
	// ── 超管：租户与平台 ──
	mux.HandleFunc("GET /api/admin/tenants", s.withAuth(s.handleListTenants))
	mux.HandleFunc("POST /api/admin/tenants", s.withAuth(s.handleCreateTenant))
	mux.HandleFunc("PATCH /api/admin/tenants/{id}", s.withAuth(s.handlePatchTenant))
	mux.HandleFunc("GET /api/admin/tenants/{id}/stores", s.withAuth(s.handleTenantStores))
	mux.HandleFunc("GET /api/admin/stats", s.withAuth(s.handlePlatformStats))
	mux.HandleFunc("GET /api/admin/my-tenant", s.withAuth(s.handleMyTenant))

	// ── 组织（品牌 / 区域） ──
	mux.HandleFunc("GET /api/admin/orgs", s.withAuth(s.handleListOrgs))
	mux.HandleFunc("POST /api/admin/orgs", s.withAuth(s.handleCreateOrg))
	mux.HandleFunc("PATCH /api/admin/orgs/{id}", s.withAuth(s.handlePatchOrg))
	mux.HandleFunc("PATCH /api/admin/accounts/{id}/org", s.withAuth(s.handleSetAccountOrg))

	// ── 账号与角色 ──
	mux.HandleFunc("GET /api/admin/accounts", s.withAuth(s.handleListAccounts))
	mux.HandleFunc("POST /api/admin/accounts", s.withAuth(s.handleCreateAccount))
	mux.HandleFunc("PATCH /api/admin/accounts/{id}/role", s.withAuth(s.handlePatchAccountRole))
	mux.HandleFunc("PATCH /api/admin/accounts/{id}/status", s.withAuth(s.handlePatchAccountStatus))

	// ── 翻译 API Key（★ 超管专属增删改查，租户一律不接触） ──
	mux.HandleFunc("GET /api/admin/translate-keys", s.withAuth(s.handleListTranslateKeys))
	mux.HandleFunc("POST /api/admin/translate-keys", s.withAuth(s.handleCreateTranslateKey))
	mux.HandleFunc("DELETE /api/admin/translate-keys/{id}", s.withAuth(s.handleDeleteTranslateKey))
	mux.HandleFunc("POST /api/admin/translate-keys/{id}/reveal", s.withAuth(s.handleRevealTranslateKey))
	mux.HandleFunc("POST /api/admin/translate-keys/{id}/rotate", s.withAuth(s.handleRotateTranslateKey))
	mux.HandleFunc("POST /api/admin/translate-keys/{id}/status", s.withAuth(s.handleSetTranslateKeyStatus))
	mux.HandleFunc("POST /api/admin/translate-keys/{id}/test", s.withAuth(s.handleTestTranslateKey))

	// ── 审计 ──
	mux.HandleFunc("GET /api/admin/audit", s.withAuth(s.handleListAudit))
}

// keysReady 检查当前进程是否具备翻译 Key 的加解密能力。
//
// 没配 LC_SECRET（或密钥过短）时 crypt 建不起来，Key 管理就整体不可用。
// 此时必须明确报错，而不是让请求走到一半才在解密处炸掉。
func (s *Server) keysReady(w http.ResponseWriter) bool {
	if s.keys == nil || s.keys.Cipher() == nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "服务未启用翻译 Key 管理（缺少 LC_SECRET）")
		return false
	}
	return true
}

// tenantAdmin 后台管理接口的准入门槛：租户管理员及以上。
// 超管天然通过（等级 4 > 3）。
func (s *Server) tenantAdmin(w http.ResponseWriter, r *http.Request) (*domain.Account, string, bool) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleTenantAdmin))
	if !allowed {
		return nil, "", false
	}
	return acc, effectiveTenant(r, acc), true
}

/* ── 租户（超管） ─────────────────────────────────────── */

// handleListTenants 列出租户（超管），附带每个租户的门店数与账号数。
//
// 计数故意在应用层做而不是写成一条联表 SQL：租户数量是个位数到几十，
// 两次分组查询 + 内存合并的复杂度远低于把 SQL 写花，
// 且将来加「按条件筛选」时更容易改。
func (s *Server) handleListTenants(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleSuperAdmin))
	if !allowed {
		return
	}
	tenants, err := s.repo.ListTenants(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取租户失败")
		return
	}
	for i := range tenants {
		stores, _ := s.repo.CountStores(r.Context(), tenants[i].ID)
		accounts, _ := s.repo.CountAccountsByTenant(r.Context(), tenants[i].ID)
		tenants[i].Stores = stores
		tenants[i].Accounts = accounts
	}
	s.repo.LogAudit(r.Context(), acc.TenantID, acc.ID, acc.Role, "tenant_list", "", "")
	ok(w, map[string]any{"ok": true, "tenants": tenants})
}

// handleCreateTenant 新建租户（超管）。
//
// code 是机器用的短标识（建议小写字母 / 数字 / 中划线），会出现在
// 内部定位与日志里，因此收紧字符集：避免注入奇怪字符后难以排查。
// perms 是配额（允许语种、日字符上限等），本期只原样存取，不强制执行。
func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleSuperAdmin))
	if !allowed {
		return
	}
	var req struct {
		Code      string         `json:"code"`
		Name      string         `json:"name"`
		ExpiresAt *int64         `json:"expires_at"`
		Perms     map[string]any `json:"perms"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	code := strings.ToLower(strings.TrimSpace(req.Code))
	if len(code) < 3 || len(code) > 32 || !isTenantCode(code) {
		fail(w, http.StatusBadRequest, CodeValidation, "租户编码需为 3-32 位小写字母 / 数字 / 中划线")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "租户名称不能为空")
		return
	}
	t, err := s.repo.CreateTenant(r.Context(), code, req.Name, req.ExpiresAt, req.Perms)
	if err != nil {
		// 唯一约束冲突多半是编码重复（pq 23505），给人话提示
		if strings.Contains(err.Error(), "duplicate key") {
			fail(w, http.StatusConflict, CodeConflict, "租户编码已存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "创建租户失败")
		return
	}
	// 建租户即建根组织：没有根组织，品牌区域管理员的作用域无处安放
	root, rerr := s.repo.EnsureRootOrg(r.Context(), t.ID, t.Name)
	if rerr != nil {
		log.Printf("创建租户 %s 的根组织失败（不致命，可在组织页手动补建）: %v", t.ID, rerr)
	}
	s.repo.LogAudit(r.Context(), t.ID, acc.ID, acc.Role, "tenant_create", t.ID, code)
	resp := map[string]any{"ok": true, "tenant": t}
	if root != nil {
		resp["root_org"] = root
	}
	ok(w, resp)
}

// isTenantCode 校验租户编码字符集（小写字母 / 数字 / 中划线）。
func isTenantCode(code string) bool {
	for _, c := range code {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return true
}

// handlePatchTenant 修改租户（超管）：名称 / 状态 / 到期时间 / 配额。
//
// 停用（disabled）与到期（expired）是两种不同的商业动作：前者是人工止损，
// 后者是合同自然结束。两者都保留数据，只是不再允许开展新业务。
func (s *Server) handlePatchTenant(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleSuperAdmin))
	if !allowed {
		return
	}
	var req struct {
		Name      *string        `json:"name"`
		Status    *string        `json:"status"`
		ExpiresAt *int64         `json:"expires_at"`
		Perms     map[string]any `json:"perms"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	if req.Status != nil {
		switch *req.Status {
		case domain.TenantActive, domain.TenantDisabled, domain.TenantExpired:
		default:
			fail(w, http.StatusBadRequest, CodeValidation, "租户状态只能是 active / disabled / expired")
			return
		}
	}
	t, err := s.repo.UpdateTenant(r.Context(), r.PathValue("id"), repo.TenantPatch{
		Name: req.Name, Status: req.Status, ExpiresAt: req.ExpiresAt, Perms: &req.Perms,
	})
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "租户不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "更新租户失败")
		return
	}
	detail := "更新租户"
	if req.Status != nil {
		detail = "状态改为 " + *req.Status
	}
	s.repo.LogAudit(r.Context(), t.ID, acc.ID, acc.Role, "tenant_update", t.ID, detail)
	ok(w, map[string]any{"ok": true, "tenant": t})
}

// handleTenantStores 查看某租户的门店（超管，只读）。
// 超管排查「某租户的店怎么打不开」时用，不做任何修改。
func (s *Server) handleTenantStores(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleSuperAdmin))
	if !allowed {
		return
	}
	stores, err := s.repo.ListStoresByTenant(r.Context(), r.PathValue("id"), 100)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取门店失败")
		return
	}
	s.repo.LogAudit(r.Context(), r.PathValue("id"), acc.ID, acc.Role, "tenant_stores_view", r.PathValue("id"), "")
	ok(w, map[string]any{"ok": true, "stores": stores})
}

// handlePlatformStats 平台总览（超管首页）。
// 只给计数，不给经营明细：超管关心的是「规模与健康度」，不是别家的菜单。
func (s *Server) handlePlatformStats(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleSuperAdmin))
	if !allowed {
		return
	}
	tenants, err := s.repo.CountTenants(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "统计失败")
		return
	}
	accounts, _ := s.repo.CountAccounts(r.Context())
	stores, _ := s.repo.CountStores(r.Context(), "")
	keys, _ := s.repo.CountTranslationKeys(r.Context(), "")
	s.repo.LogAudit(r.Context(), acc.TenantID, acc.ID, acc.Role, "platform_stats", "", "")
	ok(w, map[string]any{
		"ok":               true,
		"tenants":          tenants,
		"accounts":         accounts,
		"stores":           stores,
		"translation_keys": keys,
	})
}

// handleMyTenant 返回「我所在的租户」（后台首页的租户信息卡）。
// 超管不传 tenant_id 时返回默认租户；租户管理员永远返回自己那个。
func (s *Server) handleMyTenant(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.tenantAdmin(w, r)
	if !allowed {
		return
	}
	t, err := s.repo.GetTenant(r.Context(), tid)
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "租户不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取租户失败")
		return
	}
	if stores, err := s.repo.CountStores(r.Context(), tid); err == nil {
		t.Stores = stores
	}
	if accounts, err := s.repo.CountAccountsByTenant(r.Context(), tid); err == nil {
		t.Accounts = accounts
	}
	ok(w, map[string]any{"ok": true, "tenant": t, "my_role": acc.Role})
}

/* ── 账号与角色 ───────────────────────────────────────── */

// handleListAccounts 列账号（后台成员管理）。
// 租户管理员只看本租户；超管可用 ?tenant_id= 看任意租户。
func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	_, tid, allowed := s.tenantAdmin(w, r)
	if !allowed {
		return
	}
	accounts, err := s.repo.ListAccountsByTenant(r.Context(), tid)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取账号失败")
		return
	}
	ok(w, map[string]any{"ok": true, "accounts": accounts})
}

// handleCreateAccount 邀请账号（租户管理员及以上）。
//
// 本产品没有密码：被邀请人用自己的手机号 + 验证码登录即自动获得该角色，
// 因此「邀请」在系统里就是「按手机号预置一个账号 + 指定角色」。
// 手机号已存在时转为「调整角色」，返回 created=false，让前端提示「该手机号已是成员」。
//
// ★ 防提权：创建者不能创建比自己等级更高或相等的角色
// （否则一个租户管理员就能批量造出超管，权限体系形同虚设）。
func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.tenantAdmin(w, r)
	if !allowed {
		return
	}
	var req struct {
		Phone       string `json:"phone"`
		Role        string `json:"role"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	phone := strings.TrimSpace(req.Phone)
	role := strings.TrimSpace(req.Role)
	if len(phone) < 6 {
		fail(w, http.StatusBadRequest, CodeValidation, "手机号不正确")
		return
	}
	if !domain.ValidRole(role) {
		fail(w, http.StatusBadRequest, CodeValidation, "角色只能是 super_admin / tenant_admin / manager / staff")
		return
	}
	if domain.RoleLevel(role) >= domain.RoleLevel(acc.Role) {
		fail(w, http.StatusForbidden, CodeForbidden, "不能创建与自己同级或更高级的角色")
		return
	}

	existing, err := s.repo.GetAccountByPhone(r.Context(), phone)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusInternalServerError, CodeInternal, "查询账号失败")
		return
	}
	if existing != nil {
		// 已存在：仅允许调整本租户内成员的权限
		if existing.TenantID != tid {
			fail(w, http.StatusConflict, CodeConflict, "该手机号已属于其它租户")
			return
		}
		if existing.ID != acc.ID && domain.RoleLevel(existing.Role) >= domain.RoleLevel(acc.Role) {
			fail(w, http.StatusForbidden, CodeForbidden, "不能修改同级或更高级成员的权限")
			return
		}
		if err := s.repo.UpdateAccountRole(r.Context(), existing.ID, role); err != nil {
			fail(w, http.StatusInternalServerError, CodeInternal, "设置角色失败")
			return
		}
		updated, _ := s.repo.GetAccountByID(r.Context(), existing.ID)
		s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "account_role_set", existing.ID,
			existing.Role+" → "+role)
		ok(w, map[string]any{"ok": true, "created": false, "account": updated})
		return
	}

	created, err := s.repo.CreateAccount(r.Context(), tid, "", role, phone, strings.TrimSpace(req.DisplayName))
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "创建账号失败")
		return
	}
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "account_invite", created.ID, role)
	ok(w, map[string]any{"ok": true, "created": true, "account": created})
}

// handlePatchAccountRole 调整账号角色。
//
// 权限规则（两层）：
//   - 超管可以调整任何人的任何角色；
//   - 租户管理员只能调整本租户内、等级低于自己的成员，
//     且不能调整自己（防止自己降级丢权，也防止借他人之手提权）。
func (s *Server) handlePatchAccountRole(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleTenantAdmin))
	if !allowed {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil || !domain.ValidRole(strings.TrimSpace(req.Role)) {
		fail(w, http.StatusBadRequest, CodeValidation, "角色不合法")
		return
	}
	role := strings.TrimSpace(req.Role)
	target, err := s.repo.GetAccountByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "账号不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取账号失败")
		return
	}
	// 非超管：必须同租户、目标等级低于自己、不能动自己、不能授予更高角色
	if acc.Role != domain.RoleSuperAdmin {
		if target.TenantID != acc.TenantID {
			fail(w, http.StatusForbidden, CodeForbidden, "无权修改其它租户的成员")
			return
		}
		if target.ID == acc.ID {
			fail(w, http.StatusForbidden, CodeForbidden, "不能修改自己的角色")
			return
		}
		if domain.RoleLevel(target.Role) >= domain.RoleLevel(acc.Role) {
			fail(w, http.StatusForbidden, CodeForbidden, "无权修改同级或更高级成员")
			return
		}
		if domain.RoleLevel(role) >= domain.RoleLevel(acc.Role) {
			fail(w, http.StatusForbidden, CodeForbidden, "不能授予同级或更高级的角色")
			return
		}
	}
	if err := s.repo.UpdateAccountRole(r.Context(), target.ID, role); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "修改角色失败")
		return
	}
	s.repo.LogAudit(r.Context(), target.TenantID, acc.ID, acc.Role, "role_change", target.ID,
		target.Role+" → "+role)
	updated, _ := s.repo.GetAccountByID(r.Context(), target.ID)
	ok(w, map[string]any{"ok": true, "account": updated})
}

// handlePatchAccountStatus 停用 / 启用账号。
//
// 停用是「最常用的止血手段」：员工离职、手机号易主，先停用保留数据。
// 停用后该账号的旧 token 仍在，但 withAuth 每请求校验状态，会立即失效——
// 这是无状态 token 唯一能实现「即时吊销」的办法。
func (s *Server) handlePatchAccountStatus(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleTenantAdmin))
	if !allowed {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	status := strings.TrimSpace(req.Status)
	if status != "active" && status != "disabled" {
		fail(w, http.StatusBadRequest, CodeValidation, "状态只能是 active / disabled")
		return
	}
	target, err := s.repo.GetAccountByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "账号不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取账号失败")
		return
	}
	if acc.Role != domain.RoleSuperAdmin {
		if target.TenantID != acc.TenantID || target.ID == acc.ID {
			fail(w, http.StatusForbidden, CodeForbidden, "只能停用本租户的其它成员")
			return
		}
		if domain.RoleLevel(target.Role) >= domain.RoleLevel(acc.Role) {
			fail(w, http.StatusForbidden, CodeForbidden, "无权停用同级或更高级成员")
			return
		}
	}
	if err := s.repo.UpdateAccountStatus(r.Context(), target.ID, status); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "更新状态失败")
		return
	}
	s.repo.LogAudit(r.Context(), target.TenantID, acc.ID, acc.Role, "account_status", target.ID,
		"状态改为 "+status)
	updated, _ := s.repo.GetAccountByID(r.Context(), target.ID)
	ok(w, map[string]any{"ok": true, "account": updated})
}

/* ── 翻译 API Key（★ 由平台超管维护） ────────────────── */

// 权限划分（按产品决策，勿随意放宽）：
//
//	超管        全权：新增 / 删除 / 轮换 / 启停 / 查看明文 / 试译，可跨租户（?tenant_id=）
//	租户管理员  只读：只能看本租户的脱敏状态（知道翻译能不能用），拿不到明文、不能改
//	manager / staff  无权限
//
// 为什么 Key 不给商家自己维护：Key 是平台与能言之间的结算凭据，
// 归属与计费都在平台侧；商家自维护会导致「谁付钱、谁超额」说不清，
// 也会让一个店员的误操作直接搞垮全平台配额。商家只看状态，足够排障。
func (s *Server) superKeyAdmin(w http.ResponseWriter, r *http.Request) (*domain.Account, string, bool) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleSuperAdmin))
	if !allowed {
		return nil, "", false
	}
	return acc, effectiveTenant(r, acc), true
}

// handleListTranslateKeys 列出翻译 Key（脱敏）。
//
// ★ 明文绝不进这个接口：列表数据会被浏览器缓存、被截图、被接入前端日志，
// 只有脱敏串是安全的。要看明文必须走 reveal（一次性 + 落审计）。
// 租户管理员可调用（只读自己租户），用于确认「本店翻译是否已配好」。
// handleListTranslateKeys 列翻译 Key（★ 超管专属，与其余 Key 操作同门禁）。
//
// 为什么列表也不给租户管理员看：Key 本身就是平台成本凭据，脱敏列表对租户没有
// 实际用途（他既不能用也不能改），平白多一个信息暴露面。能言 Key 的增删改查
// 一律只归超管——与 aiCredViewer 的双写 403 教训同理，这里只判一次等级。
func (s *Server) handleListTranslateKeys(w http.ResponseWriter, r *http.Request) {
	_, tid, allowed := s.superKeyAdmin(w, r)
	if !allowed || !s.keysReady(w) {
		return
	}
	keys, err := s.repo.ListTranslationKeys(r.Context(), tid)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取 Key 失败")
		return
	}
	// 标出「当前生效的那把」：一个租户可能配了多把，翻译时取第一把启用的
	activeID := ""
	for _, k := range keys {
		if k.Status == domain.KeyActive {
			activeID = k.ID
			break
		}
	}
	ok(w, map[string]any{"ok": true, "keys": keys, "active_key_id": activeID})
}

// handleCreateTranslateKey 新增翻译 Key（密文落库）。
func (s *Server) handleCreateTranslateKey(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.superKeyAdmin(w, r)
	if !allowed || !s.keysReady(w) {
		return
	}
	var req struct {
		Name       string `json:"name"`
		Key        string `json:"key"`
		Mode       string `json:"mode"`
		DailyLimit int    `json:"daily_limit"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	key := strings.TrimSpace(req.Key)
	if name == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "请给 Key 起个名字，便于日后识别")
		return
	}
	// 能言的 Key 是较长随机串；过短的几乎一定是复制截断了，
	// 这类 Key 存进去也只会在试译时报 401，不如在入口就挡住。
	if len(key) < 12 {
		fail(w, http.StatusBadRequest, CodeValidation, "Key 长度看起来不完整，请重新复制")
		return
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = domain.KeyModePro
	}
	if !domain.ValidKeyMode(mode) {
		fail(w, http.StatusBadRequest, CodeValidation, "模式只能是 fast / pro")
		return
	}
	if req.DailyLimit < 0 {
		fail(w, http.StatusBadRequest, CodeValidation, "日调用上限不能为负")
		return
	}

	enc, err := s.keys.Cipher().Encrypt(key)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "加密失败")
		return
	}
	k := &domain.TranslationKey{
		ID: idgen.New(), TenantID: tid, Name: name, Provider: domain.KeyProviderLexicorn,
		Mode: mode, Status: domain.KeyActive, DailyLimit: req.DailyLimit, CreatedBy: acc.ID,
	}
	masked := crypt.Mask(key)
	if err := s.repo.CreateTranslationKey(r.Context(), k, enc, masked); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "保存 Key 失败")
		return
	}
	s.keys.Invalidate(tid)
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "key_create", k.ID,
		"别名="+name+" 脱敏="+masked)
	ok(w, map[string]any{"ok": true, "key": k, "note": "Key 已加密保存；此后只能通过「查看明文」复制"})
}

// handleRevealTranslateKey 查看 Key 明文（一次性返回 + 落审计）。
//
// 为什么不像创建时那样直接回显：查看动作可能来自「过段时间想抄一遍」，
// 它与创建是不同意图，必须留痕才能回答「谁在什么时候拿过这把 Key」。
func (s *Server) handleRevealTranslateKey(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.superKeyAdmin(w, r)
	if !allowed || !s.keysReady(w) {
		return
	}
	blob, err := s.repo.KeyCiphertext(r.Context(), tid, r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "Key 不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取 Key 失败")
		return
	}
	plain, err := s.keys.Cipher().Decrypt(blob)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "Key 解密失败，可能已更换服务密钥")
		return
	}
	_ = s.repo.TouchTranslationKey(r.Context(), r.PathValue("id"))
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "key_reveal", r.PathValue("id"), "")
	ok(w, map[string]any{"ok": true, "api_key": plain})
}

// handleRotateTranslateKey 轮换 Key：写入新密文，旧的立即失效。
//
// 轮换是标准的安全动作（怀疑泄露、定期更换、平台侧强制）。
// 与「删除再新增」的区别：ID 与别名不变，历史用量与审计仍挂在同一把 Key 上。
func (s *Server) handleRotateTranslateKey(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.superKeyAdmin(w, r)
	if !allowed || !s.keysReady(w) {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &req); err != nil || len(strings.TrimSpace(req.Key)) < 12 {
		fail(w, http.StatusBadRequest, CodeValidation, "新 Key 长度看起来不完整，请重新复制")
		return
	}
	key := strings.TrimSpace(req.Key)
	enc, err := s.keys.Cipher().Encrypt(key)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "加密失败")
		return
	}
	if err := s.repo.RotateTranslationKey(r.Context(), tid, r.PathValue("id"), enc, crypt.Mask(key)); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "Key 不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "轮换失败")
		return
	}
	s.keys.Invalidate(tid)
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "key_rotate", r.PathValue("id"),
		"脱敏="+crypt.Mask(key))
	ok(w, map[string]any{"ok": true})
}

// handleSetTranslateKeyStatus 启用 / 停用 Key。
// 停用立即生效（缓存失效），翻译会自动退到次级引擎，
// 不会因为一把坏 Key 让全店翻译失败。
func (s *Server) handleSetTranslateKeyStatus(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.superKeyAdmin(w, r)
	if !allowed {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	status := strings.TrimSpace(req.Status)
	if status != domain.KeyActive && status != domain.KeyDisabled {
		fail(w, http.StatusBadRequest, CodeValidation, "状态只能是 active / disabled")
		return
	}
	if err := s.repo.SetTranslationKeyStatus(r.Context(), tid, r.PathValue("id"), status); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "Key 不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "更新失败")
		return
	}
	s.keys.Invalidate(tid)
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "key_status", r.PathValue("id"),
		"状态改为 "+status)
	ok(w, map[string]any{"ok": true})
}

// handleDeleteTranslateKey 删除 Key（密文一并消失，不可恢复）。
func (s *Server) handleDeleteTranslateKey(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.superKeyAdmin(w, r)
	if !allowed {
		return
	}
	if err := s.repo.DeleteTranslationKey(r.Context(), tid, r.PathValue("id")); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "Key 不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "删除失败")
		return
	}
	s.keys.Invalidate(tid)
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "key_delete", r.PathValue("id"), "")
	ok(w, map[string]any{"ok": true})
}

// handleTestTranslateKey 试译：用这把 Key 翻一句，验证可用性与译文质量。
//
// 这是「Key 录得对不对」的唯一确定答案——比任何健康检查都可信，
// 因为它真的走了平台流水线。默认用「宫保鸡丁 → 英语」：
// 这道菜名在不同引擎下差异极大，一眼就能看出翻译质量
// （好引擎给 Kung Pao Chicken，差引擎会给回中文或英语词）。
//
// 同时拉一次余额：商家最关心的「还能翻多少」在这里一并回答。
func (s *Server) handleTestTranslateKey(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.superKeyAdmin(w, r)
	if !allowed || !s.keysReady(w) {
		return
	}
	var req struct {
		Text string `json:"text"`
		To   string `json:"to"`
	}
	_ = decodeJSON(r, &req) // 允许空 body：用默认例句
	text := strings.TrimSpace(req.Text)
	if text == "" {
		text = "宫保鸡丁"
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		to = "en"
	}

	blob, err := s.repo.KeyCiphertext(r.Context(), tid, r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "Key 不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取 Key 失败")
		return
	}
	plain, err := s.keys.Cipher().Decrypt(blob)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "Key 解密失败")
		return
	}
	k, _ := s.repo.GetTranslationKey(r.Context(), tid, r.PathValue("id"))
	mode := domain.KeyModePro
	if k != nil && k.Mode != "" {
		mode = k.Mode
	}

	eng := service.NewLexicornTranslator("", plain, mode)
	start := time.Now()
	result, terr := eng.TranslateOne(r.Context(), text, to)
	elapsed := time.Since(start).Milliseconds()
	if terr != nil {
		// 把平台错误码翻译成人话：商家需要知道该找谁解决问题
		msg := friendlyLexicornError(terr)
		s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "key_test", r.PathValue("id"), "失败："+msg)
		ok(w, map[string]any{"ok": false, "error": msg, "elapsed_ms": elapsed})
		return
	}
	_ = s.repo.TouchTranslationKey(r.Context(), r.PathValue("id"))

	// 余额是附加信息：拉不到不影响「试译成功」这个主结论
	balance := map[string]any{}
	if bal, berr := eng.Balance(r.Context()); berr == nil {
		balance = bal
	}
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "key_test", r.PathValue("id"),
		"成功："+text+" → "+to)
	ok(w, map[string]any{
		"ok":         true,
		"source":     text,
		"target":     result,
		"to":         to,
		"elapsed_ms": elapsed,
		"balance":    balance,
	})
}

// friendlyLexicornError 把能言的错误码转成商家能行动的话。
// 每条都给「下一步做什么」——只报错误码等于没报。
func friendlyLexicornError(err error) string {
	var ae *service.APIError
	if !errors.As(err, &ae) {
		return "试译失败：" + err.Error()
	}
	switch ae.Code {
	case "invalid_api_key":
		return "Key 无效或已被平台轮换，请在后台更新"
	case "insufficient_balance":
		return "能言平台积分不足，请充值后重试"
	case "key_quota_exceeded", "daily_quota_exceeded":
		return "该 Key 今日调用次数已用完，请提高日限或明天再试"
	case "rate_limited":
		return "请求过于频繁，请稍后重试"
	case "forbidden", "rejected":
		return "平台拒绝了该请求（可能命中敏感词或权限不足）"
	case "task_failed":
		return "平台处理失败，请稍后重试或提交工单"
	case "text_too_long", "bad_request":
		return "请求参数不被接受：" + ae.Message
	default:
		if ae.Message != "" {
			return "试译失败：" + ae.Message
		}
		return "试译失败（" + ae.Code + "）"
	}
}

/* ── 审计 ─────────────────────────────────────────────── */

// handleListAudit 管理操作日志。
// 超管看全平台；租户管理员只看本租户（看不到别人的动作，也看不到平台侧操作）。
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleTenantAdmin))
	if !allowed {
		return
	}
	entries, err := s.repo.ListAudit(r.Context(), 200)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取日志失败")
		return
	}
	if acc.Role == domain.RoleSuperAdmin {
		ok(w, map[string]any{"ok": true, "audit": entries})
		return
	}
	// 租户管理员：按租户过滤（租户级操作只可能作用于自己租户）
	filtered := make([]repo.AuditEntry, 0, len(entries))
	for _, e := range entries {
		if e.TenantID == acc.TenantID {
			filtered = append(filtered, e)
		}
	}
	ok(w, map[string]any{"ok": true, "audit": filtered})
}

/* ── 组织（品牌 / 区域） ───────────────────────────────── */

// handleListOrgs 列出本租户的组织树（后台「组织架构」页）。
// 返回平铺列表，父子关系由前端组装——组织数量是个位数到几十，
// 平铺 + 前端建树的复杂度和可调试性都优于后端递归组装。
func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	_, tid, allowed := s.tenantAdmin(w, r)
	if !allowed {
		return
	}
	orgs, err := s.repo.ListOrgs(r.Context(), tid)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取组织失败")
		return
	}
	// 每个组织挂上门店数：组织树上直接显示规模，省得再点进去数
	counts := map[string]int{}
	for _, o := range orgs {
		if n, err := s.repo.CountStoresInOrg(r.Context(), o.ID); err == nil {
			counts[o.ID] = n
		}
	}
	for i := range orgs {
		orgs[i].Stores = counts[orgs[i].ID]
	}
	// 确保根组织一定在列表里（新建租户会自动建；老数据兜底）
	if len(orgs) == 0 {
		if root, rerr := s.repo.EnsureRootOrg(r.Context(), tid, "集团根"); rerr == nil {
			orgs = append(orgs, *root)
		}
	}
	ok(w, map[string]any{"ok": true, "orgs": orgs})
}

// handleCreateOrg 新建品牌 / 区域组织（集团管理员及以上）。
// parentID 可省略：省略时挂到本租户根组织下（即「集团直属品牌」）。
func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.tenantAdmin(w, r)
	if !allowed {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		ParentID string `json:"parent_id"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "组织名称不能为空")
		return
	}
	orgType := strings.TrimSpace(req.Type)
	if orgType == "" {
		orgType = domain.OrgTypeBrand
	}
	if !domain.ValidOrgType(orgType) || orgType == domain.OrgTypeRoot {
		fail(w, http.StatusBadRequest, CodeValidation, "组织类型只能是 brand / region（根组织由系统创建）")
		return
	}
	if req.ParentID != "" {
		// 父组织必须同租户：否则能把门店挂到别人家的树上去
		if _, err := s.repo.GetOrgInTenant(r.Context(), tid, req.ParentID); errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusBadRequest, CodeValidation, "上级组织不存在")
			return
		} else if err != nil {
			fail(w, http.StatusInternalServerError, CodeInternal, "读取上级组织失败")
			return
		}
	}
	o, err := s.repo.CreateOrg(r.Context(), tid, strings.TrimSpace(req.ParentID), strings.TrimSpace(req.Name), orgType)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "创建组织失败")
		return
	}
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "org_create", o.ID, o.Name)
	ok(w, map[string]any{"ok": true, "org": o})
}

// handlePatchOrg 改组织名 / 移动父节点 / 删除空组织。
//
// 三件事合成一个 PATCH：都是「组织结构微调」，前端一个表单就能改完。
// 删除走单独字段 delete=true，且只允许删空组织（有子组织或门店时返回 CONFLICT）。
func (s *Server) handlePatchOrg(w http.ResponseWriter, r *http.Request) {
	acc, tid, allowed := s.tenantAdmin(w, r)
	if !allowed {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		ParentID *string `json:"parent_id"`
		Delete   bool    `json:"delete"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	orgID := r.PathValue("id")
	if req.Delete {
		if err := s.repo.DeleteOrg(r.Context(), tid, orgID); err != nil {
			if errors.Is(err, repo.ErrConflict) {
				fail(w, http.StatusConflict, CodeConflict, "该组织下还有子组织或门店，请先清空")
				return
			}
			if errors.Is(err, repo.ErrNotFound) {
				fail(w, http.StatusNotFound, CodeNotFound, "组织不存在")
				return
			}
			fail(w, http.StatusInternalServerError, CodeInternal, "删除失败")
			return
		}
		s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "org_delete", orgID, "")
		ok(w, map[string]any{"ok": true})
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		if err := s.repo.RenameOrg(r.Context(), orgID, strings.TrimSpace(*req.Name)); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				fail(w, http.StatusNotFound, CodeNotFound, "组织不存在")
				return
			}
			fail(w, http.StatusInternalServerError, CodeInternal, "改名失败")
			return
		}
	}
	if req.ParentID != nil {
		if err := s.repo.MoveOrg(r.Context(), tid, orgID, strings.TrimSpace(*req.ParentID)); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				fail(w, http.StatusBadRequest, CodeValidation, "组织或上级不存在")
				return
			}
			fail(w, http.StatusInternalServerError, CodeInternal, "移动失败")
			return
		}
	}
	o, err := s.repo.GetOrg(r.Context(), orgID)
	if err != nil {
		fail(w, http.StatusNotFound, CodeNotFound, "组织不存在")
		return
	}
	s.repo.LogAudit(r.Context(), tid, acc.ID, acc.Role, "org_update", orgID, o.Name)
	ok(w, map[string]any{"ok": true, "org": o})
}

// handleSetAccountOrg 调整成员的组织归属（决定品牌区域管理员的作用域）。
// 只能把本租户成员分配到本租户的组织下。
func (s *Server) handleSetAccountOrg(w http.ResponseWriter, r *http.Request) {
	acc, _, allowed := s.tenantAdmin(w, r)
	if !allowed {
		return
	}
	var req struct {
		OrgID string `json:"org_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	orgID := strings.TrimSpace(req.OrgID)
	target, err := s.repo.GetAccountByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "账号不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取账号失败")
		return
	}
	if acc.Role != domain.RoleSuperAdmin && target.TenantID != acc.TenantID {
		fail(w, http.StatusForbidden, CodeForbidden, "无权调整其它租户的成员")
		return
	}
	if orgID != "" {
		if _, err := s.repo.GetOrgInTenant(r.Context(), target.TenantID, orgID); errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusBadRequest, CodeValidation, "组织不存在")
			return
		} else if err != nil {
			fail(w, http.StatusInternalServerError, CodeInternal, "读取组织失败")
			return
		}
	}
	if err := s.repo.SetAccountOrg(r.Context(), target.ID, orgID); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "设置失败")
		return
	}
	s.repo.LogAudit(r.Context(), target.TenantID, acc.ID, acc.Role, "account_org", target.ID,
		"组织 → "+orgID)
	updated, _ := s.repo.GetAccountByID(r.Context(), target.ID)
	ok(w, map[string]any{"ok": true, "account": updated})
}
