package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/crypt"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

/* ── FIX-10 续：后台（admin）接口的门禁与校验 ─────────────────
   handlers_admin.go 在此之前是 0% 覆盖，而它恰好是**权限最重**的一层：
   建租户、改角色、管翻译 Key、看审计。这里先补三类最容易出错、
   也最不该出错的东西：
     1. 准入门槛（tenantAdmin / superKeyAdmin）：谁能进后台；
     2. 输入校验（租户编码字符集、长度、名称非空）：脏数据入口；
     3. 存储层错误的翻译（唯一冲突 → 409 而不是 500）：给前端的错误码
        决定它跳「换个编码再来」还是「报障」。 */

// adminFake 是可注入查询错误的假存储：用于验证「存储层错误被翻成对的 HTTP 码」。
type adminFake struct {
	err  error          // 非 nil 时任何查询都返回它
	acct []driver.Value // 账号行（withAuth 每请求都要回库取账号）；nil = 查无此人
}

// Open 建一条假连接。
func (d *adminFake) Open(string) (driver.Conn, error) { return &adminConn{db: d}, nil }

type adminConn struct{ db *adminFake }

// Prepare 恒报错：本假连接实现了 Queryer 快捷路径。
func (c *adminConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("admin fake: Prepare not supported")
}

// Close 关闭假连接（无资源可释放）。
func (c *adminConn) Close() error { return nil }

// Begin 恒报错：本文件用例不涉及事务。
func (c *adminConn) Begin() (driver.Tx, error) {
	return nil, errors.New("admin fake: Begin not supported")
}

// QueryContext 返回预置错误（若有）；账号查询走预置账号行，其余零行
// （零行由 repo 归一为 ErrNotFound）。
func (c *adminConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.db.err != nil {
		return nil, c.db.err
	}
	if strings.Contains(q, "FROM account") {
		return &staticRows{rows: oneRowOrNone(c.db.acct)}, nil
	}
	return &staticRows{}, nil
}

// ExecContext 恒成功：审计留痕等写操作不参与本文件断言。
func (c *adminConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return staticResult{}, nil
}

var adminSeq int64 // 驱动名去重计数器

// newAdminServer 装配一个挂假存储的 Server（queryErr 模拟存储层异常）。
func newAdminServer(t *testing.T, queryErr error) *Server {
	t.Helper()
	name := fmt.Sprintf("adminfake-%d", atomic.AddInt64(&adminSeq, 1))
	sql.Register(name, &adminFake{err: queryErr})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Server{repo: repo.New(db), secret: []byte("admin-secret")}
}

// newAdminServerAs 装配一个「withAuth 能查到 acc」的假存储 Server。
//
// 走真实注册的路由时 withAuth 在最外层，它每请求回库取账号——不预置账号行
// 的话请求会在鉴权阶段就 401，根本到不了角色门禁，门禁断言就成了空转。
func newAdminServerAs(t *testing.T, acc *domain.Account) *Server {
	t.Helper()
	name := fmt.Sprintf("adminfake-%d", atomic.AddInt64(&adminSeq, 1))
	sql.Register(name, &adminFake{
		acct: accountRow(acc.ID, "13800000000", acc.TenantID, acc.Role, "active"),
	})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Server{repo: repo.New(db), secret: []byte("admin-secret")}
}

// requestAs 经完整路由（含 withAuth）打一次请求：带真 token，账号由假库提供。
func requestAs(t *testing.T, s *Server, acc *domain.Account, method, path, body string,
	register func(mux *http.ServeMux)) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	register(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+signToken(s.secret, acc.ID, time.Hour))
	mux.ServeHTTP(rec, req)
	return rec
}

// postAs 以给定账号主体发一次 JSON 请求，返回响应记录器。
func postAs(t *testing.T, s *Server, acc *domain.Account, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/tenants", s.withAuth(s.handleCreateTenant))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), ctxAccount, acc))
	// 走 withAuth 需要真 token：绕过鉴权直接注入账号（本文件断言的是门禁之后的行为）
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), ctxAccount, acc))
		s.handleCreateTenant(w, r)
	})
	mux = http.NewServeMux()
	mux.HandleFunc("POST /api/admin/tenants", h)
	mux.ServeHTTP(rec, req)
	return rec
}

/* ── 一、准入门槛 ─────────────────────────────────────────── */

// TestTenantAdmin_Gate 锁定后台准入门槛的角色矩阵：
// 集团管理员及以上放行，门店管理员 / 店员挡在门外。
// ★ 顺带锁住 effectiveTenant 的联动：普通人拿自己的租户，超管可指定。
func TestTenantAdmin_Gate(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		tenantID string
		query    string
		allowed  bool
		wantTid  string
	}{
		{"集团管理员放行", domain.RoleTenantAdmin, "tenant-A", "", true, "tenant-A"},
		{"超管放行且可指定租户", domain.RoleSuperAdmin, domain.PlatformTenantID, "?tenant_id=tenant-B", true, "tenant-B"},
		{"超管不传参落默认租户", domain.RoleSuperAdmin, domain.PlatformTenantID, "", true, domain.DefaultTenantID},
		{"门店管理员被挡", domain.RoleManager, "tenant-A", "", false, ""},
		{"店员被挡", domain.RoleStaff, "tenant-A", "", false, ""},
	}
	for _, c := range cases {
		s := &Server{}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/orgs"+c.query, nil)
		acc := &domain.Account{ID: "a1", Role: c.role, TenantID: c.tenantID, Status: "active"}
		req = req.WithContext(context.WithValue(req.Context(), ctxAccount, acc))

		gotAcc, tid, ok := s.tenantAdmin(rec, req)
		if ok != c.allowed {
			t.Errorf("%s：allowed 应为 %v，实际 %v（HTTP %d）", c.name, c.allowed, ok, rec.Code)
			continue
		}
		if !c.allowed {
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s：应 403，实际 %d", c.name, rec.Code)
			}
			continue
		}
		if gotAcc == nil || gotAcc.ID != "a1" {
			t.Errorf("%s：应回传账号主体，实际 %+v", c.name, gotAcc)
		}
		if tid != c.wantTid {
			t.Errorf("%s：作用域租户应为 %q，实际 %q", c.name, c.wantTid, tid)
		}
	}
}

// TestKeysReady_RequiresCipher 锁定「没配密钥时明确报错」：
// 翻译 Key 管理依赖 crypt，缺 LC_SECRET 时必须 500 + 人话提示，
// 而不是让请求走到解密处才炸——那种错前后端都看不懂。
func TestKeysReady_RequiresCipher(t *testing.T) {
	s := &Server{} // keys 为 nil
	rec := httptest.NewRecorder()
	if s.keysReady(rec) {
		t.Fatal("未装配 KeyVault 时不应报就绪")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("应 500，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "LC_SECRET") {
		t.Errorf("错误信息应点名缺失的 LC_SECRET，实际 %s", rec.Body.String())
	}

	cipher, err := crypt.NewCipher("admin-test-secret-value-123")
	if err != nil {
		t.Fatal(err)
	}
	s2 := &Server{keys: service.NewKeyVault(cipher, nil)}
	rec2 := httptest.NewRecorder()
	if !s2.keysReady(rec2) {
		t.Error("装配了 KeyVault 应报就绪")
	}
}

/* ── 二、输入校验 ─────────────────────────────────────────── */

// TestIsTenantCode_Charset 锁定租户编码字符集：
// 只认小写字母 / 数字 / 中划线。code 会进日志与内部定位，
// 放宽字符集等于给自己埋一个难查的坑。
//
// ★ 两个刻意的边界，别把它们当成 bug 去「修」：
//   - 空串返回 true：本函数只管字符集，长度归调用方（handler 先判 3–32 位）。
//     拆开是为了让「长度」这条业务规则只有一个出处。
//   - 大写不由这里拒绝：handler 先 strings.ToLower 再校验，即**规范化而非拒绝**——
//     商户手填 "LANG" 不该被弹回，落成 "lang" 更符合预期。
func TestIsTenantCode_Charset(t *testing.T) {
	good := []string{"abc", "a-b-c", "tenant2", "x1", "a-b-9"}
	bad := []string{"a_b", "a b", "租户", "a.b", "a/b", "A", "!"}
	for _, s := range good {
		if !isTenantCode(s) {
			t.Errorf("%q 应被接受", s)
		}
	}
	for _, s := range bad {
		if isTenantCode(s) {
			t.Errorf("%q 应被拒绝", s)
		}
	}
}

// TestHandleCreateTenant_Validation 锁定三类脏输入全部 400：
// 编码太短 / 编码含非法字符 / 名称为空。这些是唯一能从外部写进
// 租户表的入口，挡不住就会污染整张表。
//
// ★ 大写**不在**拒绝之列：handler 先 ToLower 再校验，商户填 "LANG"
// 会被规范成 "lang" 而不是弹回——拒绝对转化无益，规范化才有益。
// 这条断言顺带把这个语义钉住，防止有人后来改成「严格小写」把老客户挡在门外。
func TestHandleCreateTenant_Validation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"编码过短", `{"code":"ab","name":"某租户"}`},
		{"编码含下划线", `{"code":"a_b_c","name":"某租户"}`},
		{"编码含空格", `{"code":"a b c","name":"某租户"}`},
		{"名称为空", `{"code":"good-code","name":"  "}`},
	}
	for _, c := range cases {
		s := newAdminServer(t, nil)
		acc := &domain.Account{ID: "sa", Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID, Status: "active"}
		rec := postAs(t, s, acc, "/api/admin/tenants", c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 应 400，实际 %d body=%s", c.name, rec.Code, rec.Body.String())
		}
	}
}

// TestHandleCreateTenant_NonSuperAdminForbidden 锁定最高权限的归属双条件：
// 「role=super_admin 但挂在租户下」的旁路账号必须 403。
// 这条抄自翻译助手的现网事故——那种账号曾被拿来跨租户退款。
func TestHandleCreateTenant_NonSuperAdminForbidden(t *testing.T) {
	s := newAdminServer(t, nil)
	bypass := &domain.Account{ID: "bp", Role: domain.RoleSuperAdmin, TenantID: "tenant-A", Status: "active"}

	rec := postAs(t, s, bypass, "/api/admin/tenants", `{"code":"good-code","name":"某租户"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("租户内的 super_admin 应 403，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "平台级超管") {
		t.Errorf("错误提示应说明仅限平台级超管，实际 %s", rec.Body.String())
	}
}

/* ── 三、存储层错误的翻译 ─────────────────────────────────── */

// TestHandleCreateTenant_DuplicateCodeIs409 锁定冲突码：
// 编码撞了必须 409 CONFLICT 并给出「已存在」的人话，而不是 500。
// 500 会让前端走报障流程，用户只会看到「服务有问题」——
// 而真实原因是「换个编码再来」，完全可自助。
func TestHandleCreateTenant_DuplicateCodeIs409(t *testing.T) {
	s := newAdminServer(t, errors.New(`pq: duplicate key value violates unique constraint "tenant_code_key"`))
	acc := &domain.Account{ID: "sa", Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID, Status: "active"}

	rec := postAs(t, s, acc, "/api/admin/tenants", `{"code":"taken","name":"某租户"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("编码冲突应 409，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已存在") {
		t.Errorf("应提示编码已存在，实际 %s", rec.Body.String())
	}
}

/* ── 四、整个后台路由面的门禁（一次覆盖 20+ 个 handler 的入口）─── */

// TestAdminRoutes_AllGatedByRole 是这一层最关键的一条：
// **每一条**后台路由都必须先过角色门禁。新加一个 admin handler 时忘了挂
// requireRole，是一道没人会立刻发现的口子（功能照常工作，只是谁都能调）。
// 用路由表逐条打过去，等于给「后台接口集合」加了一道装配级门禁。
//
// 断言只锁「没被门禁挡住」而不锁具体业务码：业务码由各 handler 的专属用例负责，
// 这里只关心权限——混在一起会让新增字段动不动就红，反而不敢跑。
func TestAdminRoutes_AllGatedByRole(t *testing.T) {
	routes := []struct{ method, path string }{
		{"GET", "/api/admin/tenants"},
		{"PATCH", "/api/admin/tenants/t-1"},
		{"GET", "/api/admin/tenants/t-1/stores"},
		{"GET", "/api/admin/stats"},
		{"GET", "/api/admin/my-tenant"},
		{"GET", "/api/admin/orgs"},
		{"POST", "/api/admin/orgs"},
		{"PATCH", "/api/admin/orgs/o-1"},
		{"PATCH", "/api/admin/accounts/a-1/org"},
		{"GET", "/api/admin/accounts"},
		{"PATCH", "/api/admin/accounts/a-1/role"},
		{"PATCH", "/api/admin/accounts/a-1/status"},
		{"GET", "/api/admin/translate-keys"},
		{"POST", "/api/admin/translate-keys"},
		{"DELETE", "/api/admin/translate-keys/k-1"},
		{"POST", "/api/admin/translate-keys/k-1/reveal"},
		{"POST", "/api/admin/translate-keys/k-1/rotate"},
		{"POST", "/api/admin/translate-keys/k-1/status"},
		{"POST", "/api/admin/translate-keys/k-1/test"},
		{"GET", "/api/admin/audit"},
	}
	// 低角色一律被挡；平台超管一律不被权限挡（业务成败不在本用例范围内）
	low := []string{domain.RoleStaff, domain.RoleManager}
	for _, r := range routes {
		// 低角色：一律被门禁挡在门外
		for _, role := range low {
			acc := &domain.Account{ID: "a1", Role: role, TenantID: "tenant-A", Status: "active"}
			s := newAdminServerAs(t, acc)
			rec := requestAs(t, s, acc, r.method, r.path, "{}", func(mux *http.ServeMux) {
				s.registerAdmin(mux)
			})
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s：role=%s 应被门禁挡住（403），实际 %d body=%s",
					r.method, r.path, role, rec.Code, rec.Body.String())
			}
		}

		// 平台超管：不该被权限挡住（业务成败不在本用例范围内）
		acc := &domain.Account{ID: "sa", Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID, Status: "active"}
		s := newAdminServerAs(t, acc)
		rec := requestAs(t, s, acc, r.method, r.path, "{}", func(mux *http.ServeMux) {
			s.registerAdmin(mux)
		})
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s：平台超管不应被权限挡住，实际 %d body=%s",
				r.method, r.path, rec.Code, rec.Body.String())
		}
	}
}

// TestSuperKeyAdmin_Gate 锁定翻译 Key 的「超管专属」双条件：
// 集团管理员连列表都看不到（纪律 17：少一个信息暴露面），
// 「role=super_admin 但挂在租户下」的旁路账号同样不放行。
func TestSuperKeyAdmin_Gate(t *testing.T) {
	cases := []struct {
		name    string
		role    string
		tenant  string
		allowed bool
	}{
		{"平台超管放行", domain.RoleSuperAdmin, domain.PlatformTenantID, true},
		{"租户内超管被挡（旁路账号）", domain.RoleSuperAdmin, "tenant-A", false},
		{"集团管理员被挡", domain.RoleTenantAdmin, "tenant-A", false},
		{"门店管理员被挡", domain.RoleManager, "tenant-A", false},
	}
	for _, c := range cases {
		s := &Server{keys: nil}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/admin/translate-keys", nil)
		acc := &domain.Account{ID: "a1", Role: c.role, TenantID: c.tenant, Status: "active"}
		req = req.WithContext(context.WithValue(req.Context(), ctxAccount, acc))

		_, _, ok := s.superKeyAdmin(rec, req)
		if ok != c.allowed {
			t.Errorf("%s：allowed 应为 %v，实际 %v", c.name, c.allowed, ok)
			continue
		}
		if !c.allowed && rec.Code != http.StatusForbidden {
			t.Errorf("%s：应 403，实际 %d", c.name, rec.Code)
		}
	}
}

// TestFriendlyLexicornError_TranslatesProviderJargon 锁定 Key 测试失败时的提示：
// 能言开放平台返回的是英文/技术性错误，直接透给商户等于让他去猜。
// 这里把几种典型失败翻成人话，翻不出来时**原样返回**而不是编一个解释。
func TestFriendlyLexicornError_TranslatesProviderJargon(t *testing.T) {
	// 能言的错误是 *service.APIError（带平台 code），只有它才有人话映射；
	// 普通 error 走「试译失败：+ 原错误」的兜底——原样透出，不编造解释。
	apiErr := func(code, msg string) error {
		return &service.APIError{Code: code, Message: msg}
	}

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"Key 失效给下一步动作", apiErr("invalid_api_key", ""), "请在后台更新"},
		{"余额不足说清是谁的余额", apiErr("insufficient_balance", ""), "积分不足"},
		{"日限用完说明何时能再用", apiErr("daily_quota_exceeded", ""), "明天再试"},
		{"限流", apiErr("rate_limited", ""), "稍后重试"},
		{"未登记的错误码回落到平台文案", apiErr("brand_new_code", "平台原话"), "平台原话"},
		{"未登记且无文案时至少带错误码", apiErr("brand_new_code", ""), "brand_new_code"},
		{"普通 error 原样透出（不编造解释）", errors.New("connection reset"), "connection reset"},
	}
	for _, c := range cases {
		if got := friendlyLexicornError(c.err); !strings.Contains(got, c.want) {
			t.Errorf("%s：提示应含「%s」，实际 %q", c.name, c.want, got)
		}
	}
}

// TestFriendlyLexicornError_NilSafe 锁定一个真实的 panic（本轮修复）：
// 原先 err 为 nil 时直接 err.Error() 会 nil 解引用，把一次「Key 不可用」
// 的提示变成 500 + 一段 goroutine dump。辅助函数不该有能炸掉的入参。
func TestFriendlyLexicornError_NilSafe(t *testing.T) {
	if got := friendlyLexicornError(nil); got != "" {
		t.Errorf("nil 错误应返回空串（交由上层走默认文案），实际 %q", got)
	}
}

// TestHandleCreateAccount_PrivilegeEscalationBlocked 锁定防提权：
// 集团管理员不能建出 tenant_admin / super_admin 账号——
// 否则「给自己提一级」就成了自助操作，整个角色体系形同虚设。
func TestHandleCreateAccount_PrivilegeEscalationBlocked(t *testing.T) {
	for _, role := range []string{domain.RoleTenantAdmin, domain.RoleSuperAdmin} {
		s := newAdminServer(t, nil)
		acc := &domain.Account{ID: "ta", Role: domain.RoleTenantAdmin, TenantID: "tenant-A", Status: "active"}
		body := fmt.Sprintf(`{"phone":"13900000002","role":"%s","display_name":"提权探针"}`, role)

		rec := callAs(t, s, acc, "POST", "/api/admin/accounts", body, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /api/admin/accounts", s.handleCreateAccount)
		})
		if rec.Code != http.StatusForbidden {
			t.Errorf("集团管理员建 %s 应 403，实际 %d body=%s", role, rec.Code, rec.Body.String())
		}
	}
}

// TestHandleCreateTenant_OtherStoreErrorIs500 划定上一条的边界：
// 只有「唯一冲突」才翻 409，别的存储错误仍是 500。
// 不加这条的话，把判断放宽成「出错就 409」也能过上一个用例，
// 于是真正的数据库故障会被谎报成「换个编码再来」。
func TestHandleCreateTenant_OtherStoreErrorIs500(t *testing.T) {
	s := newAdminServer(t, errors.New("connection reset by peer"))
	acc := &domain.Account{ID: "sa", Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID, Status: "active"}

	rec := postAs(t, s, acc, "/api/admin/tenants", `{"code":"good-code","name":"某租户"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("非冲突的存储错误应 500，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "已存在") {
		t.Errorf("不应把连接故障谎报成编码重复，实际 %s", rec.Body.String())
	}
}
