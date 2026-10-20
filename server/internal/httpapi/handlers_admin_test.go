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
	err error // 非 nil 时任何查询都返回它
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

// QueryContext 返回预置错误（若有），否则零行——让 repo 归一为 ErrNotFound。
func (c *adminConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.db.err != nil {
		return nil, c.db.err
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
