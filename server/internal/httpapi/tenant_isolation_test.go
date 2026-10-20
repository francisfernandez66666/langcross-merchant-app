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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
)

/* ── FIX-10：把「多租户隔离」的手工结论固化成回归 ──────────────
   独立 UAT 的 E 段（15 条）每次都要起真实服务 + 真实 Postgres 才跑得动，
   结论上不了 CI。这里用 httptest + 假 SQL 驱动在单测里复现同一组判断：
     - 他租户的门店：点进去 403；
     - 列表：SQL 里必须带着调用方租户（不是取回来再在 Go 里筛）；
     - 超管边界：跨租户门店详情是 403，这是 CanAccessStore 的设计，不是 bug。
   ★ 为什么要断言 SQL 文本而不是只断言响应：只断言响应时，「先把全表捞回来
   再在 Go 里过滤」这种实现照样能绿，却在数据量上来后变成慢查询 + 内存峰值，
   而且一旦有人给列表加了新分支就会漏筛。口径写在 WHERE 里才是真的锁住。 */

// isolationQuery 记录一次查询，供断言「作用域条件进了 SQL」。
type isolationQuery struct {
	sql  string
	args []driver.Value
}

// isolationFake 按 SQL 形态分发结果集，并记录全部查询。
type isolationFake struct {
	accountRow []driver.Value // FROM account 的返回行（nil = 查无此人）
	storeRow   []driver.Value // 按 ID 查门店的返回行（nil = 不存在）
	listRows   [][]driver.Value
	mu         sync.Mutex
	queries    []isolationQuery
}

// Open 建一条共享同一份夹具数据的假连接。
func (d *isolationFake) Open(string) (driver.Conn, error) {
	return &isolationConn{db: d}, nil
}

type isolationConn struct{ db *isolationFake }

// Prepare 恒报错：本假连接实现了 Queryer 快捷路径，不会用到预编译。
func (c *isolationConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("isolation fake: Prepare not supported")
}

// Close 关闭假连接（无资源可释放，恒成功）。
func (c *isolationConn) Close() error { return nil }

// Begin 恒报错：本文件的用例都不涉及事务。
func (c *isolationConn) Begin() (driver.Tx, error) {
	return nil, errors.New("isolation fake: Begin not supported")
}

// QueryContext 按 SQL 形态分发，并把 (SQL, 参数) 记进日志供断言。
func (c *isolationConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	c.db.mu.Lock()
	c.db.queries = append(c.db.queries, isolationQuery{sql: q, args: vals})
	c.db.mu.Unlock()

	switch {
	case strings.Contains(q, "FROM account"):
		return &staticRows{rows: oneRowOrNone(c.db.accountRow)}, nil
	case strings.Contains(q, "FROM dish"):
		return &staticRows{}, nil // 门店详情会带菜品，本用例不关心
	case strings.Contains(q, "FROM store") && strings.Contains(q, "WHERE id ="):
		return &staticRows{rows: oneRowOrNone(c.db.storeRow)}, nil
	default: // 门店列表
		return &staticRows{rows: c.db.listRows}, nil
	}
}

// ExecContext 恒返回「影响一行」：本用例的写路径不参与断言。
func (c *isolationConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return staticResult{}, nil
}

// oneRowOrNone 把单行夹具包成结果集；nil 表示零行（repo 归一为 ErrNotFound）。
func oneRowOrNone(row []driver.Value) [][]driver.Value {
	if row == nil {
		return nil
	}
	return [][]driver.Value{row}
}

// queries 取已记录的查询日志快照。
func (d *isolationFake) recorded() []isolationQuery {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]isolationQuery, len(d.queries))
	copy(out, d.queries)
	return out
}

var isolationSeq int64 // 驱动名去重计数器

// newIsolationServer 装配一个挂假存储的 Server，返回它和背后的假库（供查日志）。
func newIsolationServer(t *testing.T, f *isolationFake) *Server {
	t.Helper()
	name := fmt.Sprintf("isolation-%d", atomic.AddInt64(&isolationSeq, 1))
	sql.Register(name, f)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Server{repo: repo.New(db), secret: []byte("isolation-secret")}
}

// storeRow 构造一行 store 记录（列序见 repo.storeCols）。
func storeRow(id, accountID, tenantID, name string) []driver.Value {
	return []driver.Value{
		id, accountID, tenantID, "", name, "",
		nil, nil, "", "", "",
		true, "service", "assist", "iso-slug", nil, time.Unix(1700000000, 0).UTC(),
	}
}

// getStoreAs 以 accID 的身份请求门店详情，返回状态码。
func getStoreAs(t *testing.T, s *Server, accID, storeID string) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stores/{id}", s.withAuth(s.handleGetStore))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/stores/"+storeID, nil)
	req.Header.Set("Authorization", "Bearer "+signToken(s.secret, accID, time.Hour))
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// listStoresAs 以 accID 的身份请求门店列表，返回响应体。
func listStoresAs(t *testing.T, s *Server, accID string) []byte {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stores", s.withAuth(s.handleListStores))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/stores", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(s.secret, accID, time.Hour))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("门店列表应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

/* ── 一、他租户的门店：点进去必须 403 ───────────────────────── */

// TestTenantIsolation_OtherTenantStoreForbidden 是隔离的主断言：
// 门店挂在别的租户、也不是我建的 → 403。放行就是「列表里没有、接口却能打开」
// 那种信息泄露。
func TestTenantIsolation_OtherTenantStoreForbidden(t *testing.T) {
	f := &isolationFake{
		accountRow: accountRow("acc-A", "13800000000", "tenant-A", domain.RoleManager, "active"),
		storeRow:   storeRow("store-B", "acc-B", "tenant-B", "别家的店"),
	}
	s := newIsolationServer(t, f)

	if code := getStoreAs(t, s, "acc-A", "store-B"); code != http.StatusForbidden {
		t.Fatalf("他租户门店应 403，实际 %d", code)
	}
}

// TestTenantIsolation_OwnStoreAllowed 锁定反向边界：
// 我建的店永远能进（owner 判定优先于租户判定）——有人把店转给了别人，
// 或者门店归属还没来得及同步时，owner 不能被自己挡在门外。
func TestTenantIsolation_OwnStoreAllowed(t *testing.T) {
	f := &isolationFake{
		accountRow: accountRow("acc-A", "13800000000", "tenant-A", domain.RoleManager, "active"),
		storeRow:   storeRow("store-mine", "acc-A", "tenant-A", "我的店"),
	}
	s := newIsolationServer(t, f)

	if code := getStoreAs(t, s, "acc-A", "store-mine"); code != http.StatusOK {
		t.Fatalf("自己的店应 200，实际 %d", code)
	}
}

// TestTenantIsolation_TenantAdminCrossTenantForbidden 锁定集团管理员的边界：
// 本租户内可见，跨租户不可见——集团管理员不是平台超管。
func TestTenantIsolation_TenantAdminCrossTenantForbidden(t *testing.T) {
	acc := accountRow("acc-TA", "13800000000", "tenant-A", domain.RoleTenantAdmin, "active")

	f1 := &isolationFake{accountRow: acc, storeRow: storeRow("store-A", "acc-other", "tenant-A", "本租户的店")}
	if code := getStoreAs(t, newIsolationServer(t, f1), "acc-TA", "store-A"); code != http.StatusOK {
		t.Errorf("集团管理员应能进本租户的店，实际 %d", code)
	}

	f2 := &isolationFake{accountRow: acc, storeRow: storeRow("store-B", "acc-other", "tenant-B", "别家租户的店")}
	if code := getStoreAs(t, newIsolationServer(t, f2), "acc-TA", "store-B"); code != http.StatusForbidden {
		t.Errorf("集团管理员不应进他租户的店，实际 %d", code)
	}
}

/* ── 二、超管边界：跨租户门店详情 403 是设计，不是 bug ────────── */

// TestTenantIsolation_SuperAdminCrossTenantBlockedByDesign 固化一条容易被
// 误报成缺陷的行为：平台超管也**不能**直接打开他租户的门店详情。
// 跨租户可见性走 /api/admin/tenants/{id}/stores（列表），管理动作不开放。
// 不写这条的话，下一个人看到 403 会当成越权 bug 去「修」，反而把
// 设计边界拆了。
func TestTenantIsolation_SuperAdminCrossTenantBlockedByDesign(t *testing.T) {
	f := &isolationFake{
		accountRow: accountRow("acc-SA", "13800000000", domain.PlatformTenantID, domain.RoleSuperAdmin, "active"),
		storeRow:   storeRow("store-B", "acc-B", "tenant-B", "别家租户的店"),
	}
	s := newIsolationServer(t, f)

	if code := getStoreAs(t, s, "acc-SA", "store-B"); code != http.StatusForbidden {
		t.Fatalf("超管跨租户门店详情应 403（设计边界，可见性出口在 admin 列表），实际 %d", code)
	}
}

// TestTenantIsolation_SuperAdminSeesDefaultTenantStore 锁定超管的可见范围：
// 平台自营（默认租户）的店能直接打开——否则超管连日常运维都做不了。
func TestTenantIsolation_SuperAdminSeesDefaultTenantStore(t *testing.T) {
	f := &isolationFake{
		accountRow: accountRow("acc-SA", "13800000000", domain.PlatformTenantID, domain.RoleSuperAdmin, "active"),
		storeRow:   storeRow("store-def", "acc-other", domain.DefaultTenantID, "平台自营店"),
	}
	s := newIsolationServer(t, f)

	if code := getStoreAs(t, s, "acc-SA", "store-def"); code != http.StatusOK {
		t.Fatalf("超管应能进默认租户的店，实际 %d", code)
	}
}

/* ── 三、列表：作用域必须写进 SQL，而不是取回来再筛 ───────────── */

// TestTenantIsolation_ListQueryScopedToCallerTenant 锁住列表的隔离口径：
// 集团管理员列门店时，SQL 里必须带 tenant_id 条件且参数就是他自己的租户。
// 只在 Go 里过滤的实现能通过「响应不含他店」的断言，却过不了这一条。
func TestTenantIsolation_ListQueryScopedToCallerTenant(t *testing.T) {
	f := &isolationFake{
		accountRow: accountRow("acc-TA", "13800000000", "tenant-A", domain.RoleTenantAdmin, "active"),
		listRows: [][]driver.Value{
			storeRow("store-A1", "acc-TA", "tenant-A", "本租户 A1"),
		},
	}
	s := newIsolationServer(t, f)

	body := listStoresAs(t, s, "acc-TA")

	var resp struct {
		Stores []struct {
			ID string `json:"id"`
		} `json:"stores"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("列表响应不是合法 JSON: %v", err)
	}
	if len(resp.Stores) != 1 || resp.Stores[0].ID != "store-A1" {
		t.Fatalf("应返回本租户的门店，实际 %+v", resp.Stores)
	}

	var listSQL isolationQuery
	found := false
	for _, q := range f.recorded() {
		if strings.Contains(q.sql, "FROM store") && !strings.Contains(q.sql, "WHERE id =") {
			listSQL = q
			found = true
			break
		}
	}
	if !found {
		t.Fatal("没有记录到门店列表查询")
	}
	if !strings.Contains(listSQL.sql, "tenant_id = $1") {
		t.Errorf("列表 SQL 必须按租户过滤（不能取全表回来再筛），实际 %q", listSQL.sql)
	}
	if len(listSQL.args) != 1 || listSQL.args[0] != "tenant-A" {
		t.Errorf("列表参数应是调用方租户 tenant-A，实际 %v", listSQL.args)
	}
}

// TestTenantIsolation_SuperAdminListScopedToDefaultTenant 锁定超管列表的落点：
// 不传 tenant_id 时只看平台自营那份数据，不会「顺手看到所有租户」。
func TestTenantIsolation_SuperAdminListScopedToDefaultTenant(t *testing.T) {
	f := &isolationFake{
		accountRow: accountRow("acc-SA", "13800000000", domain.PlatformTenantID, domain.RoleSuperAdmin, "active"),
		listRows:   [][]driver.Value{storeRow("store-def", "acc-x", domain.DefaultTenantID, "平台自营店")},
	}
	s := newIsolationServer(t, f)

	_ = listStoresAs(t, s, "acc-SA")

	for _, q := range f.recorded() {
		if strings.Contains(q.sql, "FROM store") && !strings.Contains(q.sql, "WHERE id =") {
			if len(q.args) != 1 || q.args[0] != domain.DefaultTenantID {
				t.Errorf("超管列表应落在默认租户，实际参数 %v（SQL=%q）", q.args, q.sql)
			}
			return
		}
	}
	t.Fatal("没有记录到门店列表查询")
}

/* ── 四、门店不存在：不能因为隔离逻辑吞掉 404 ─────────────────── */

// TestTenantIsolation_MissingStoreIs404Not403 锁住错误码的诚实性：
// 门店压根不存在时报 404，而不是被权限分支吞成 403。
// 混在一起的话，前端「无权访问」的提示会出现在「链接过期」的场景上，
// 客人以为自己被针对了，实际只是码贴错了。
func TestTenantIsolation_MissingStoreIs404Not403(t *testing.T) {
	f := &isolationFake{
		accountRow: accountRow("acc-A", "13800000000", "tenant-A", domain.RoleManager, "active"),
		storeRow:   nil, // 查无此店
	}
	s := newIsolationServer(t, f)

	if code := getStoreAs(t, s, "acc-A", "ghost-store"); code != http.StatusNotFound {
		t.Fatalf("不存在的门店应 404，实际 %d（被权限分支吞成 403 会误导前端）", code)
	}
}

// 保证 io 被用到（fakeRows.Next 返回 io.EOF 的路径在本文件经由 staticRows 复用）。
var _ = io.EOF
