package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
)

/* ── CanAccessStore：商家端越权防线的唯一判据 ──────────────────
   httpapi.requireStore 全靠它，改错一行就是「列表里没有、接口却能打开」。
   这里把五种角色的判定一次列全——不依赖数据库：除 org_admin 子树分支外
   全是纯内存判断，可直接表驱动。 */

// TestCanAccessStore_RoleMatrix 锁定角色 × 归属的判定矩阵。
//
// 两条最容易写错、也最危险的边界单独点名：
//   - owner 优先：门店的 account_id 是自己 → 无论租户归属一律放行。
//     有人把店转手、或归属还没同步时，owner 不能被自己挡在门外。
//   - 超管不等于「看全部」：CanAccessStore 只放行默认租户（平台自营），
//     跨租户可见性的出口在后台列表接口，不在这里。把它放宽成 true
//     等于让超管账号能直接读任意租户的门店详情。
func TestCanAccessStore_RoleMatrix(t *testing.T) {
	const (
		myAcc    = "acc-me"
		otherAcc = "acc-other"
	)
	store := func(accountID, tenantID string) *domain.Store {
		return &domain.Store{ID: "st-1", AccountID: accountID, TenantID: tenantID}
	}

	cases := []struct {
		name string
		acc  *domain.Account
		st   *domain.Store
		want bool
	}{
		{
			name: "owner 永远能碰自己的店（哪怕归属还没同步）",
			acc:  &domain.Account{ID: myAcc, Role: domain.RoleStaff, TenantID: "tenant-A"},
			st:   store(myAcc, "tenant-B"),
			want: true,
		},
		{
			name: "超管能碰平台自营店",
			acc:  &domain.Account{ID: myAcc, Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID},
			st:   store(otherAcc, domain.DefaultTenantID),
			want: true,
		},
		{
			name: "超管不能碰他租户的店（可见性出口在后台列表）",
			acc:  &domain.Account{ID: myAcc, Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID},
			st:   store(otherAcc, "tenant-B"),
			want: false,
		},
		{
			name: "集团管理员能碰本租户的店",
			acc:  &domain.Account{ID: myAcc, Role: domain.RoleTenantAdmin, TenantID: "tenant-A"},
			st:   store(otherAcc, "tenant-A"),
			want: true,
		},
		{
			name: "集团管理员不能碰他租户的店",
			acc:  &domain.Account{ID: myAcc, Role: domain.RoleTenantAdmin, TenantID: "tenant-A"},
			st:   store(otherAcc, "tenant-B"),
			want: false,
		},
		{
			name: "门店管理员不是 owner 则无权",
			acc:  &domain.Account{ID: myAcc, Role: domain.RoleManager, TenantID: "tenant-A"},
			st:   store(otherAcc, "tenant-A"),
			want: false,
		},
		{
			name: "店员不是 owner 则无权",
			acc:  &domain.Account{ID: myAcc, Role: domain.RoleStaff, TenantID: "tenant-A"},
			st:   store(otherAcc, "tenant-A"),
			want: false,
		},
	}

	r := New(nil) // 上述分支全是内存判断，不触达 DB
	for _, c := range cases {
		if got := r.CanAccessStore(context.Background(), c.acc, c.st); got != c.want {
			t.Errorf("%s：期望 %v，实际 %v", c.name, c.want, got)
		}
	}
}

// TestCanAccessStore_OrgAdminWithoutOrgFallsBackToDeny 锁定 org_admin 的
// 「没分配到组织」分支：既不放大到全集团，也不因为报错被当成放行。
// 查不出子树时按无权处理——拒绝优于放过，这是安全判据的默认方向。
func TestCanAccessStore_OrgAdminWithoutOrgFallsBackToDeny(t *testing.T) {
	// 用一条会报错的连接：OrgDescendantIDs 失败时也必须返回 false
	r := New(newErrDB(t))
	acc := &domain.Account{ID: "acc-me", Role: domain.RoleOrgAdmin, TenantID: "tenant-A", OrgID: "org-1"}
	st := &domain.Store{ID: "st-1", AccountID: "acc-other", TenantID: "tenant-A", OrgID: "org-1"}

	if r.CanAccessStore(context.Background(), acc, st) {
		t.Error("子树查询失败时按无权处理，不应放行")
	}

	// 没分配组织（OrgID 为空）也直接拒绝，绝不退化成「全集团可见」
	acc2 := &domain.Account{ID: "acc-me", Role: domain.RoleOrgAdmin, TenantID: "tenant-A", OrgID: ""}
	if r.CanAccessStore(context.Background(), acc2, st) {
		t.Error("未分配组织的 org_admin 不应获得集团级可见性")
	}
}

/* ── 测试辅助：一条任何查询都报错的 DB ───────────────────────── */

// errDriver 是任何查询都返回固定错误的假驱动，用来验证「存储失败时的降级方向」。
type errDriver struct{}

// Open 建一条恒报错的连接。
func (errDriver) Open(string) (driver.Conn, error) { return errConn{}, nil }

type errConn struct{}

// Prepare 恒报错：本驱动只用于验证失败路径。
func (errConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("err driver: Prepare not supported")
}

// Close 关闭连接（无资源可释放）。
func (errConn) Close() error { return nil }

// Begin 恒报错：本驱动不涉及事务。
func (errConn) Begin() (driver.Tx, error) {
	return nil, errors.New("err driver: Begin not supported")
}

// QueryContext 恒报错，模拟存储不可用。
func (errConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, errors.New("err driver: storage unavailable")
}

// ExecContext 恒报错，模拟存储不可用。
func (errConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, errors.New("err driver: storage unavailable")
}

// newErrDB 起一个任何操作都失败的 *sql.DB，供失败路径用例使用。
func newErrDB(t *testing.T) *sql.DB {
	t.Helper()
	// 驱动已在本文件 init 里注册，这里按名字取（sql.Open 第一参是驱动名）
	db, err := sql.Open("errdriver-store-test", "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func init() { sql.Register("errdriver-store-test", errDriver{}) }
