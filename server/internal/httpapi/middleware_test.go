package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

/* ── 内存 SQL 假驱动 ───────────────────────────────────────────
   withAuth 的「正常注入 / 停用账号 / 账号不存在」三条分支都要回库取账号，
   但单元测试不应依赖真实 PostgreSQL。database/sql 允许注册自定义驱动，
   这里用一个只回应单条 SELECT 的假驱动把账号行喂给 repo.GetAccountByID。
   只实现 QueryerContext：database/sql 会优先走它，从而跳过 Prepare。 */

var fakeDriverSeq int64 // 驱动名去重计数器：同一进程内多次注册需不同名字

// fakeAccountDriver 按预设行回应查询；row 为 nil 表示「查无此人」。
type fakeAccountDriver struct{ row []driver.Value }

// Open 实现 driver.Driver：每条连接都持有同一份预设行，互不影响。
func (d *fakeAccountDriver) Open(string) (driver.Conn, error) {
	return &fakeAccountConn{row: d.row}, nil
}

type fakeAccountConn struct{ row []driver.Value }

// Prepare 实现 driver.Conn：本假驱动不预备语句，任何路径都不该走到这里
// （database/sql 发现 Conn 同时实现了 QueryerContext 就会优先走查询接口）。
func (c *fakeAccountConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake driver: Prepare not supported")
}

// Close 实现 driver.Conn：无资源可释放，直接成功。
func (c *fakeAccountConn) Close() error { return nil }

// Begin 实现 driver.Conn：测试不涉及事务，明确报错比静默返回 nil 更难被误用。
func (c *fakeAccountConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fake driver: Begin not supported")
}

// QueryContext 实现 driver.QueryerContext：无论 SQL 是什么都返回预设行，
// 把「repo 查账号」这一步与真实 PostgreSQL 解耦。
func (c *fakeAccountConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return &fakeRows{row: c.row}, nil
}

type fakeRows struct {
	row  []driver.Value
	sent bool
}

// Columns 实现 driver.Rows：列序必须与 repo.accountCols 一致，
// scanAccount 按这个顺序 Scan，错一位就把字段读串了。
func (r *fakeRows) Columns() []string {
	// 与 repo.accountCols 的列序一一对应（scanAccount 按此顺序 Scan）
	return []string{"id", "phone", "tenant_id", "role", "org_id", "status", "display_name", "created_at"}
}

// Close 实现 driver.Rows：无游标资源，直接成功。
func (r *fakeRows) Close() error { return nil }

// Next 实现 driver.Rows：首次调用把预设行拷给 database/sql，
// 之后返回 io.EOF；预设行为 nil（查无此人）时直接 EOF——
// sql.Row.Scan 会把它转成 sql.ErrNoRows，repo 再归一为 ErrNotFound。
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.sent || r.row == nil {
		return io.EOF // sql.Row.Scan 会转成 sql.ErrNoRows，repo 再归一为 ErrNotFound
	}
	r.sent = true
	copy(dest, r.row)
	return nil
}

// accountRow 构造一行 account 记录（列序见 fakeRows.Columns）。
func accountRow(id, phone, tenantID, role, status string) []driver.Value {
	return []driver.Value{id, phone, tenantID, role, "", status, "", time.Unix(1700000000, 0).UTC()}
}

// newFakeAuthServer 构造一个只挂假 repo 的 Server，供 withAuth 系列测试使用。
// 除 repo / secret 外全部依赖留空：这些用例不会触及翻译 / OCR / 图片 / 助手。
func newFakeAuthServer(t *testing.T, row []driver.Value) *Server {
	t.Helper()
	name := fmt.Sprintf("fakeacct-%d", atomic.AddInt64(&fakeDriverSeq, 1))
	sql.Register(name, &fakeAccountDriver{row: row})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Server{repo: repo.New(db), secret: []byte("test-secret")}
}

// errBody 解出失败信封的 code / msg，供断言使用。
func errBody(t *testing.T, body []byte) (int, string, string) {
	t.Helper()
	var v struct {
		Ok    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
			Msg  string `json:"msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（body=%s）", err, body)
	}
	return 0, v.Error.Code, v.Error.Msg
}

/* ── withRecover：panic 必须变成 500 信封 ─────────────────────
   这条链路的契约是「客户端永远收到可解析的 JSON」：没有 recover 时
   panic 会直接断开连接，前端拿到空响应，错误分支全部失效。 */

// 锁定契约：handler panic 必须转成 500 + INTERNAL JSON 信封，
// 且 Content-Type 为 JSON——客户端靠这个走统一错误分支而不是面对空响应。
func TestWithRecover_PanicBecomes500Envelope(t *testing.T) {
	s := &Server{}
	h := s.withRecover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/me", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应转成 500，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("恢复响应应是 JSON，实际 Content-Type=%q", ct)
	}
	_, code, msg := errBody(t, rec.Body.Bytes())
	if code != CodeInternal || msg != "服务内部错误" {
		t.Errorf("应返回 INTERNAL 信封，实际 code=%q msg=%q", code, msg)
	}
}

// 锁定边界：没有 panic 的正常请求，状态码与响应体必须原样透过，
// recover 层不得有任何可见副作用（防「为了兜底把正常响应也包一层」）。
func TestWithRecover_NormalRequestUnaffected(t *testing.T) {
	s := &Server{}
	h := s.withRecover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true, "hit": true})
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("正常请求不应被改写，实际 %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"hit":true`) {
		t.Errorf("正常响应体应原样透过，实际 %s", body)
	}
}

// 锁定边界：响应已开始（写过 200 与部分 body）后才 panic 时，
// 必须保持已写内容与状态码，绝不能补第二个状态码——否则 net/http
// 会拼出畸形响应。这是 recoverWriter.wrote 标记的存在理由。
func TestWithRecover_PanicAfterPartialWriteKeepsOriginalStatus(t *testing.T) {
	// handler 已写出 200 与部分 body 后才 panic：不能补第二个状态码，
	// 否则 net/http 会拼出畸形响应。契约是「保持已写的部分」。
	s := &Server{}
	h := s.withRecover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"partial"`))
		panic("late boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/stores", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("已开始的响应不应被改写成 500，实际 %d", rec.Code)
	}
	if body := rec.Body.String(); body != `{"ok":true,"partial"` {
		t.Errorf("已写出的部分应保持原样，实际 %q", body)
	}
}

/* ── withAuth：token 与账号状态的四条分支 ─────────────────────
   无 token / 坏 token / 过期 token 在验签阶段就该拒（根本不应回库）；
   停用与不存在的账号在回库后分别映射 403 与 401。 */

// 锁定：无 Authorization 头直接 401，且必须早于任何回库
// （夹具行设为 active：若实现跳过验签直接查库会误放行成 200）。
func TestWithAuth_MissingToken(t *testing.T) {
	// row 设为 active：若实现错误地跳过验证直接查库，会拿到 200 而不是 401
	s := newFakeAuthServer(t, accountRow("a1", "13800000000", "t1", domain.RoleManager, "active"))
	h := s.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true})
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/me", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，实际 %d", rec.Code)
	}
	_, code, msg := errBody(t, rec.Body.Bytes())
	if code != CodeUnauth || msg != "请先登录" {
		t.Errorf("应返回 UNAUTH/请先登录，实际 code=%q msg=%q", code, msg)
	}
}

// 锁定：四类畸形 Authorization（非 token / 坏 base64 / 错 scheme /
// 前缀大小写不匹配）全部 401——验签阶段就拒，根本不应触达数据库。
func TestWithAuth_MalformedToken(t *testing.T) {
	cases := []string{
		"Bearer not-a-token",      // 没有两段
		"Bearer aaa.bbb",          // 签名段不是合法 base64
		"Basic dXNlcjpwYXNz",      // 错误的认证 scheme
		"bearer lowercase.scheme", // 前缀大小写不匹配（实现刻意严格）
	}
	for _, hdr := range cases {
		s := newFakeAuthServer(t, accountRow("a1", "13800000000", "t1", domain.RoleManager, "active"))
		h := s.withAuth(func(w http.ResponseWriter, _ *http.Request) {
			ok(w, map[string]any{"ok": true})
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/me", nil)
		req.Header.Set("Authorization", hdr)
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Authorization=%q 应 401，实际 %d", hdr, rec.Code)
		}
	}
}

// 锁定：载荷段被篡改后签名不再匹配，必须 401。
// 这条守住「token 不可伪造」——否则改个账号 ID 就能冒任何身份。
func TestWithAuth_TamperedPayload(t *testing.T) {
	s := newFakeAuthServer(t, accountRow("a1", "13800000000", "t1", domain.RoleManager, "active"))
	valid := signToken(s.secret, "a1", time.Hour)
	// 篡改载荷段第一个字符：签名不再匹配，必须在验签阶段拒绝
	tampered := flipFirstChar(valid)

	h := s.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("篡改载荷应 401，实际 %d", rec.Code)
	}
}

// 锁定：过期 token 在验签阶段就拒（早于回库），且走「请先登录」
// 而不是查库后的分支——保证过期与未登录对前端的提示一致。
func TestWithAuth_ExpiredToken(t *testing.T) {
	// 过期 token 也必须在验签阶段拒绝（早于任何回库）
	s := newFakeAuthServer(t, accountRow("a1", "13800000000", "t1", domain.RoleManager, "active"))
	h := s.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(s.secret, "a1", -time.Hour))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("过期 token 应 401，实际 %d", rec.Code)
	}
	_, code, msg := errBody(t, rec.Body.Bytes())
	if code != CodeUnauth || msg != "请先登录" {
		t.Errorf("过期应走「请先登录」而非查库后的分支，实际 code=%q msg=%q", code, msg)
	}
}

// 锁定：验签通过后账号主体（含角色/租户）与租户 ID 都必须注入
// context——翻译链路靠 context 里的租户取 Key，漏注入会静默用错租户。
func TestWithAuth_ActiveAccountInjectedIntoContext(t *testing.T) {
	const accID, tenantID = "acc-active-01", "tenant-xyz"
	s := newFakeAuthServer(t, accountRow(accID, "13800000000", tenantID, domain.RoleManager, "active"))

	var gotAcc *domain.Account
	var gotTenant string
	h := s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		gotAcc = currentAccount(r)
		gotTenant = service.TenantFromContext(r.Context())
		ok(w, map[string]any{"ok": true})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(s.secret, accID, time.Hour))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("有效 token + 正常账号应 200，实际 %d", rec.Code)
	}
	if gotAcc == nil {
		t.Fatal("账号主体应注入 context，实际为 nil")
	}
	if gotAcc.ID != accID || gotAcc.Role != domain.RoleManager || gotAcc.TenantID != tenantID {
		t.Errorf("注入的账号字段不符：%+v", gotAcc)
	}
	if gotTenant != tenantID {
		t.Errorf("租户应写入 context 供翻译链路使用，实际 %q", gotTenant)
	}
}

// 锁定：token 有效但账号已停用 → 403 FORBIDDEN。
// 与 401 分开：停用是「知道你是谁但不让你进」，前端提示语不同。
func TestWithAuth_DisabledAccountForbidden(t *testing.T) {
	s := newFakeAuthServer(t, accountRow("a1", "13800000000", "t1", domain.RoleManager, "disabled"))
	h := s.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(s.secret, "a1", time.Hour))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("停用账号应 403，实际 %d", rec.Code)
	}
	_, code, msg := errBody(t, rec.Body.Bytes())
	if code != CodeForbidden || msg != "账号已停用，请联系管理员" {
		t.Errorf("应返回 FORBIDDEN，实际 code=%q msg=%q", code, msg)
	}
}

// 锁定：token 有效但账号已不存在（假驱动返回零行 → ErrNotFound）→ 401。
// 对应「账号被删后旧 token 仍能在世上飘」的场景。
func TestWithAuth_UnknownAccount(t *testing.T) {
	// 假驱动返回零行 → repo.ErrNotFound → 401（token 有效但账号没了）
	s := newFakeAuthServer(t, nil)
	h := s.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"ok": true})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(s.secret, "ghost", time.Hour))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("账号不存在应 401，实际 %d", rec.Code)
	}
	_, code, msg := errBody(t, rec.Body.Bytes())
	if code != CodeUnauth || msg != "账号不存在" {
		t.Errorf("应返回「账号不存在」，实际 code=%q msg=%q", code, msg)
	}
}

/* ── requireRole：五级门禁 + 平台级双条件 ──────────────────────
   双条件（role=super_admin 且 tenant_id 为空）直接抄自翻译助手的安全整改：
   任何一条「租户里的超管」旁路账号都必须在这里被 403。 */

// 锁定：context 里没有账号主体（漏包 withAuth 的路由）→ 401 且拒绝。
// 防的是「某个 handler 忘记挂鉴权中间件」这类装配错误。
func TestRequireRole_NoAccountInContext(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/admin/stats", nil)

	if _, ok := s.requireRole(rec, req, domain.MaxRoleLevel); ok {
		t.Error("未登录应被拒绝")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("未登录应 401，实际 %d", rec.Code)
	}
}

// 锁定：店员/门店管理员/集团管理员分别去够更高等级门禁，全部 403。
// 三级各差一级，验证 RoleLevel 比较没有差一错误。
func TestRequireRole_InsufficientLevel(t *testing.T) {
	cases := []struct {
		role string
		min  int
	}{
		{domain.RoleStaff, 2},       // 店员要动门店管理员的能力
		{domain.RoleManager, 4},     // 门店管理员要动集团级能力
		{domain.RoleTenantAdmin, 5}, // 集团管理员要动平台级能力
	}
	for _, c := range cases {
		s := &Server{}
		rec := httptest.NewRecorder()
		req := reqWithAccount(&domain.Account{ID: "a1", Role: c.role, TenantID: "t1", Status: "active"})

		if _, ok := s.requireRole(rec, req, c.min); ok {
			t.Errorf("role=%s 要求等级 %d 应被拒绝", c.role, c.min)
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("role=%s 要求等级 %d 应 403，实际 %d", c.role, c.min, rec.Code)
		}
	}
}

// 锁定（抄自翻译助手安全整改）：同为 super_admin，挂在租户下必须 403，
// 只有平台级（tenant_id 为空）才放行。这条专门堵「租户里的超管」旁路账号。
func TestRequireRole_PlatformSuperAdminDoubleCondition(t *testing.T) {
	// 同样的 super_admin 角色，挂在租户下就必须被拒——这是旁路账号的堵漏点
	s := &Server{}

	rec := httptest.NewRecorder()
	req := reqWithAccount(&domain.Account{ID: "a1", Role: domain.RoleSuperAdmin, TenantID: "t1", Status: "active"})
	if _, ok := s.requireRole(rec, req, domain.MaxRoleLevel); ok {
		t.Error("租户内的 super_admin 不应获得平台级权限")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("租户内的 super_admin 应 403，实际 %d", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := reqWithAccount(&domain.Account{ID: "a2", Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID, Status: "active"})
	acc, ok := s.requireRole(rec2, req2, domain.MaxRoleLevel)
	if !ok || acc == nil || acc.ID != "a2" {
		t.Error("平台级 super_admin 应被放行")
	}
}

// 锁定反向边界：低等级账号过自己等级的门禁必须放行，且不误触发
// 平台级双条件——防止门禁「一刀切拧太紧」把正常操作也挡了。
func TestRequireRole_LowestTierPassesOwnLevel(t *testing.T) {
	// 店员做店员的事：min=1 时放行，且不触发平台级双条件
	s := &Server{}
	rec := httptest.NewRecorder()
	req := reqWithAccount(&domain.Account{ID: "a1", Role: domain.RoleStaff, TenantID: "t1", Status: "active"})

	if _, ok := s.requireRole(rec, req, 1); !ok {
		t.Error("店员应能通过等级 1 的门禁")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("放行时不应写错误状态，实际 %d", rec.Code)
	}
}

/* ── effectiveTenant：租户作用域解析 ─────────────────────────
   普通账号传别人的 tenant_id 必须被忽略，否则一个参数就能越租户。 */

// 锁定：普通账号传 ?tenant_id=别人 必须被忽略，只能作用于自己的租户。
// 一个查询参数就能越租户的话，隔离就白做了。
func TestEffectiveTenant_NonSuperAdminIgnoresQueryParam(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/admin/accounts?tenant_id=other-tenant", nil)
	acc := &domain.Account{ID: "a1", Role: domain.RoleManager, TenantID: "my-tenant"}

	if got := effectiveTenant(req, acc); got != "my-tenant" {
		t.Errorf("普通账号必须作用于自己的租户，实际 %q", got)
	}
}

// 锁定：超管显式传 tenant_id 时作用于指定租户；不传或传纯空白
// 时落到默认租户（平台自营数据）。空白必须被 Trim 掉，不能当有效 ID。
func TestEffectiveTenant_SuperAdmin(t *testing.T) {
	acc := &domain.Account{ID: "a1", Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID}

	req := httptest.NewRequest("GET", "/api/admin/accounts?tenant_id=other-tenant", nil)
	if got := effectiveTenant(req, acc); got != "other-tenant" {
		t.Errorf("超管显式传参应作用于指定租户，实际 %q", got)
	}

	req2 := httptest.NewRequest("GET", "/api/admin/accounts", nil)
	if got := effectiveTenant(req2, acc); got != domain.DefaultTenantID {
		t.Errorf("超管不传参应落到默认租户，实际 %q", got)
	}

	req3 := httptest.NewRequest("GET", "/api/admin/accounts?tenant_id=%20%20", nil) // 纯空白
	if got := effectiveTenant(req3, acc); got != domain.DefaultTenantID {
		t.Errorf("空白 tenant_id 应被忽略并落到默认租户，实际 %q", got)
	}
}

/* ── 测试辅助 ─────────────────────────────────────────────── */

// reqWithAccount 把账号主体塞进 context，模拟「已过 withAuth」的请求。
func reqWithAccount(acc *domain.Account) *http.Request {
	r := httptest.NewRequest("GET", "/api/x", nil)
	return r.WithContext(context.WithValue(r.Context(), ctxAccount, acc))
}

// flipFirstChar 把字符串首字符替换成另一个字符（用于构造「签名不匹配」的 token）。
func flipFirstChar(s string) string {
	if s == "" {
		return s
	}
	c := s[0]
	if c == 'a' {
		c = 'b'
	} else {
		c = 'a'
	}
	return string(c) + s[1:]
}
