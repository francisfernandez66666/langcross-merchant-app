package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

/* ── FIX-10 续：商家主链路（建店 → 草稿 → 确认 → 翻译 → 核销）────
   这一条链路是「注册到门口贴上码」的全部，也是纪律最密的地方：
   未确认不许翻译（纪律 2）、name_zh 永不被覆盖（纪律 13）、
   确认即落库（Q1）、建店归属走 effectiveTenant（纪律 19）。
   这里把它们逐条固化成断言——纯函数直接表驱动，涉库的用假 SQL 驱动。 */

// merchantFake 按 SQL 形态分发结果集并记录查询（复用租户隔离用例的记录类型）。
type merchantFake struct {
	rows func(q string) [][]driver.Value
}

// Open 建一条假连接。
func (d *merchantFake) Open(string) (driver.Conn, error) { return &merchantConn{db: d}, nil }

type merchantConn struct{ db *merchantFake }

// Prepare 恒报错：本假连接实现了 Queryer 快捷路径。
func (c *merchantConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("merchant fake: Prepare not supported")
}

// Close 关闭假连接（无资源可释放）。
func (c *merchantConn) Close() error { return nil }

// Begin 恒报错：本文件用例不涉及事务。
func (c *merchantConn) Begin() (driver.Tx, error) {
	return nil, errors.New("merchant fake: Begin not supported")
}

// QueryContext 按 SQL 形态分发预置结果集，并把 (SQL, 参数) 记进日志供断言。
func (c *merchantConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	rec := &merchantLog
	rec.mu.Lock()
	rec.queries = append(rec.queries, isolationQuery{sql: q, args: vals})
	rec.mu.Unlock()
	return &staticRows{rows: c.db.rows(q)}, nil
}

// ExecContext 恒成功：埋点等写操作不参与本文件断言。
func (c *merchantConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return staticResult{}, nil
}

// merchantLog 是本文件共用的查询日志（假驱动的 QueryContext 需要非闭包的可寻址记录）。
var merchantLog struct {
	mu      sync.Mutex
	queries []isolationQuery
}

var merchantSeq int64 // 驱动名去重计数器

// newMerchantServer 装配一个按 SQL 分发结果集的假存储 Server。
func newMerchantServer(t *testing.T, rows func(q string) [][]driver.Value) *Server {
	t.Helper()
	merchantLog.mu.Lock()
	merchantLog.queries = nil
	merchantLog.mu.Unlock()

	name := fmt.Sprintf("merchantfake-%d", atomic.AddInt64(&merchantSeq, 1))
	sql.Register(name, &merchantFake{rows: rows})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Server{
		repo:   repo.New(db),
		secret: []byte("merchant-secret"),
		tr:     service.NewTranslator("", "", nil),
	}
}

// recordedQueries 取查询日志快照。
func recordedQueries() []isolationQuery {
	merchantLog.mu.Lock()
	defer merchantLog.mu.Unlock()
	out := make([]isolationQuery, len(merchantLog.queries))
	copy(out, merchantLog.queries)
	return out
}

// fp 取 float64 指针（DraftItem.Price 是指针：不传表示「这项没价」）。
func fp(v float64) *float64 { return &v }

// ip 取 int 指针（Dish.Remaining 是指针：nil 表示不设每日剩余）。
func ip(v int) *int { return &v }

// draftRow 构造一行 source_draft（列序见 repo.GetLatestDraft 的 SELECT）。
func draftRow(id, storeID, lang string, payload string, confirmedAt any) []driver.Value {
	return []driver.Value{id, storeID, lang, []byte(payload), confirmedAt, time.Unix(1700000000, 0).UTC()}
}

// callAs 以给定账号直接调 handler（越过 withAuth，断言的是门禁之后的行为）。
func callAs(t *testing.T, s *Server, acc *domain.Account, method, path, body string,
	register func(mux *http.ServeMux)) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	register(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), ctxAccount, acc))
	mux.ServeHTTP(rec, req)
	return rec
}

/* ── 一、纯函数：勾选口径 / 装配继承 / 分类去重合并 ───────────── */

// TestPickedDraftItems_Selection 锁定「一个都没勾视为全选」：
// 前端还没实现勾选时，全选比什么都不翻更符合预期。
// ★ 确认与翻译共用这个函数——两边口径必须一致，否则客人端会在
// 「确认后」与「翻译后」看到两份不同的菜单。
func TestPickedDraftItems_Selection(t *testing.T) {
	items := []domain.DraftItem{
		{Name: "宫保鸡丁", Price: fp(38), Selected: true},
		{Name: "水煮鱼", Price: fp(68), Selected: false},
		{Name: "米饭", Price: fp(3), Selected: true},
	}
	got := pickedDraftItems(items)
	if len(got) != 2 || got[0].Name != "宫保鸡丁" || got[1].Name != "米饭" {
		t.Fatalf("应只留勾选的两道，实际 %+v", got)
	}

	all := []domain.DraftItem{{Name: "a"}, {Name: "b"}}
	if got := pickedDraftItems(all); len(got) != 2 {
		t.Errorf("一个都没勾应视为全选，实际 %d 条", len(got))
	}
}

// TestDishesFromDraft_InheritsStateAndTranslations 锁定「重新确认不毁掉已完成的翻译」：
// 售罄 / 每日剩余 / 属性 / 已有译文都要从同名旧菜继承过来。
// 这条是真实踩过的坑——重新确认一次就把售罄状态和译文清空，
// 商家会以为系统丢了数据。
func TestDishesFromDraft_InheritsStateAndTranslations(t *testing.T) {
	existing := storeRow("st-1", "acc-1", "tenant-A", "老店")
	dishRow := []driver.Value{
		"d-old", "st-1", "宫保鸡丁", "Kung Pao Chicken",
		[]byte(`{"en":"Kung Pao Chicken","ja":"旧日文"}`), "",
		38.0, "热菜", []byte(`{"spicy":2,"allergens":["peanut"]}`),
		int64(3), true, int64(7), "",
	}
	s := newMerchantServer(t, func(q string) [][]driver.Value {
		switch {
		case strings.Contains(q, "FROM store"):
			return [][]driver.Value{existing}
		case strings.Contains(q, "FROM dish"):
			return [][]driver.Value{dishRow}
		default:
			return nil
		}
	})

	got := s.dishesFromDraft(context.Background(), "st-1", []domain.DraftItem{
		{Name: "宫保鸡丁", Price: fp(42), Category: "热菜"}, // 同名：应继承
		{Name: "新菜", Price: fp(10), Category: "凉菜"},   // 新名：应取默认值
	})

	if len(got) != 2 {
		t.Fatalf("应组装 2 道，实际 %d", len(got))
	}
	old := got[0]
	if !old.SoldOut || old.Remaining == nil || *old.Remaining != 7 {
		t.Errorf("同名旧菜应继承售罄与每日剩余，实际 sold_out=%v remaining=%v", old.SoldOut, old.Remaining)
	}
	if old.Names["ja"] != "旧日文" {
		t.Errorf("同名旧菜应继承既有译文，实际 %v", old.Names)
	}
	// 新菜走默认属性：不辣 + 无过敏原（attrs 为空时前端也要能正常渲染）
	if got[1].Attrs["spicy"] != 0 {
		t.Errorf("新菜应有默认属性（不辣 + 无过敏原），实际 %v", got[1].Attrs)
	}
}

// TestDistinctCategories_DedupesKeepsOrderSkipsEmpty 锁定分类去重保序：
// 分类顺序必须等于菜单呈现顺序（前端不做二次排序），空分类不得凭空造出标题区块。
func TestDistinctCategories_DedupesKeepsOrderSkipsEmpty(t *testing.T) {
	got := distinctCategories([]domain.Dish{
		{Category: "热菜"}, {Category: "凉菜"}, {Category: "热菜"}, {Category: "  "}, {Category: "汤"},
	})
	want := []string{"热菜", "凉菜", "汤"}
	if len(got) != len(want) {
		t.Fatalf("应得到 %v，实际 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("顺序应为 %v，实际 %v", want, got)
		}
	}
}

// TestMergeCategoryNames_Incremental 锁定增量合并语义：
// 已确认的旧译文不能因为「这次只翻了几道菜的分类」而丢失（用增量而非整体替换）；
// 同语种内后写覆盖，好让商家的人工校正生效。
func TestMergeCategoryNames_Incremental(t *testing.T) {
	dst := map[string]map[string]string{"en": {"热菜": "Hot dishes", "凉菜": "Cold dishes"}}
	add := map[string]map[string]string{"en": {"热菜": "Hot Dishes(人工改)"}, "ja": {"热菜": "熱菜"}}

	got := mergeCategoryNames(dst, add)
	if got["en"]["热菜"] != "Hot Dishes(人工改)" {
		t.Errorf("同语种内应后写覆盖（人工校正要生效），实际 %q", got["en"]["热菜"])
	}
	if got["en"]["凉菜"] != "Cold dishes" {
		t.Errorf("未涉及的旧译文不应丢失，实际 %v", got["en"])
	}
	if got["ja"]["热菜"] != "熱菜" {
		t.Errorf("新语种应并入，实际 %v", got["ja"])
	}
}

// TestMergeCategoryNames_NilDst 锁定 nil 兜底：首次翻译时 dst 为 nil 也要建出 map，
// 否则直接对 nil map 写会 panic。
func TestMergeCategoryNames_NilDst(t *testing.T) {
	got := mergeCategoryNames(nil, map[string]map[string]string{"en": {"热菜": "Hot dishes"}})
	if got["en"]["热菜"] != "Hot dishes" {
		t.Errorf("dst 为 nil 时应新建，实际 %v", got)
	}
	if got := mergeCategoryNames(map[string]map[string]string{"en": {"a": "b"}}, nil); got["en"]["a"] != "b" {
		t.Errorf("add 为空时应原样返回，实际 %v", got)
	}
}

/* ── 二、建店：角色门禁 + 租户继承 ──────────────────────────── */

// TestHandleCreateStore_ValidationAndRoleGate 锁定建店的两道门：
// 店名空 → 400；店员（staff）→ 403（staff 是来核销券的，不该能开新店）。
func TestHandleCreateStore_ValidationAndRoleGate(t *testing.T) {
	rows := func(string) [][]driver.Value { return nil }
	s := newMerchantServer(t, rows)

	rec := callAs(t, s, &domain.Account{ID: "m1", Role: domain.RoleManager, TenantID: "t1", Status: "active"},
		"POST", "/api/stores", `{"name":"  "}`, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /api/stores", s.handleCreateStore)
		})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("店名为空应 400，实际 %d", rec.Code)
	}

	rec2 := callAs(t, s, &domain.Account{ID: "s1", Role: domain.RoleStaff, TenantID: "t1", Status: "active"},
		"POST", "/api/stores", `{"name":"某店"}`, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /api/stores", s.handleCreateStore)
		})
	if rec2.Code != http.StatusForbidden {
		t.Errorf("店员不应能建店，应 403，实际 %d", rec2.Code)
	}
}

// TestHandleCreateStore_TenantInheritedFromEffectiveTenant 锁定纪律 19：
// 门店归属必须走 effectiveTenant，不能直接拿 acc.TenantID。
// 超管的 tenant_id 是空串（平台级），直接拿来当归属会写出一条「无主门店」——
// 所有按租户的查询都看不见它，而它又占着 slug 和二维码；
// 0007 加了外键后空串还会被数据库拒掉变成 500。
func TestHandleCreateStore_TenantInheritedFromEffectiveTenant(t *testing.T) {
	rows := func(q string) [][]driver.Value {
		if strings.Contains(q, "INSERT INTO store") {
			return [][]driver.Value{storeRow("st-new", "sa", "ignored", "新店")}
		}
		return nil
	}
	s := newMerchantServer(t, rows)
	acc := &domain.Account{ID: "sa", Role: domain.RoleSuperAdmin, TenantID: domain.PlatformTenantID, Status: "active"}

	// 超管不传 tenant_id → 落默认租户（平台自营）
	callAs(t, s, acc, "POST", "/api/stores", `{"name":"平台自营店"}`, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/stores", s.handleCreateStore)
	})

	var insert isolationQuery
	found := false
	for _, q := range recordedQueries() {
		if strings.Contains(q.sql, "INSERT INTO store") {
			insert, found = q, true
			break
		}
	}
	if !found {
		t.Fatal("没有记录到建店 INSERT")
	}
	// VALUES ($1 id, $2 account_id, $3 tenant_id, $4 org_id, $5 name, $6 slug)
	if len(insert.args) < 3 || insert.args[2] != domain.DefaultTenantID {
		t.Fatalf("超管建店应落默认租户，实际 tenant_id=%v", insert.args[2])
	}

	// 普通商家 → 落自己的租户
	s2 := newMerchantServer(t, rows)
	callAs(t, s2, &domain.Account{ID: "m1", Role: domain.RoleManager, TenantID: "tenant-A", Status: "active"},
		"POST", "/api/stores", `{"name":"某店"}`, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /api/stores", s2.handleCreateStore)
		})
	for _, q := range recordedQueries() {
		if strings.Contains(q.sql, "INSERT INTO store") {
			if q.args[2] != "tenant-A" {
				t.Errorf("普通商家建店应落自己的租户，实际 %v", q.args[2])
			}
			return
		}
	}
	t.Fatal("没有记录到建店 INSERT")
}

/* ── 三、翻译闸门：未确认不许翻译（纪律 2）───────────────────── */

// TestHandleTranslate_UnconfirmedDraftRejected 锁死这条硬规则：
// 原文确认稿未确认就翻译，译文必然不可信（真值还没定）。
// 这是全仓最有代表性的一条产品不变量，一旦被绕过，
// 客人端会看到「译得很好但其实是错菜」的菜单。
func TestHandleTranslate_UnconfirmedDraftRejected(t *testing.T) {
	unconfirmed := draftRow("d1", "st-1", "zh",
		`[{"name":"宫保鸡丁","price":38,"category":"热菜","selected":true}]`, nil)
	s := newMerchantServer(t, func(q string) [][]driver.Value {
		switch {
		case strings.Contains(q, "FROM store"):
			return [][]driver.Value{storeRow("st-1", "acc-1", "tenant-A", "某店")}
		case strings.Contains(q, "FROM source_draft"):
			return [][]driver.Value{unconfirmed}
		default:
			return nil
		}
	})
	acc := &domain.Account{ID: "acc-1", Role: domain.RoleManager, TenantID: "tenant-A", Status: "active"}

	rec := callAs(t, s, acc, "POST", "/api/stores/st-1/translate", `{"targets":["en"]}`,
		func(mux *http.ServeMux) {
			mux.HandleFunc("POST /api/stores/{id}/translate", s.handleTranslate)
		})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未确认应 400，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "未确认") {
		t.Errorf("错误提示应说明原因（原文未确认），实际 %s", rec.Body.String())
	}
}

// TestHandleTranslate_NoDraftIs400 区分「没有稿」与「有稿未确认」：
// 两者都是 400，但提示不同——前端靠提示决定跳「上传菜单」还是「去确认」。
func TestHandleTranslate_NoDraftIs400(t *testing.T) {
	s := newMerchantServer(t, func(q string) [][]driver.Value {
		if strings.Contains(q, "FROM store") {
			return [][]driver.Value{storeRow("st-1", "acc-1", "tenant-A", "某店")}
		}
		return nil // 查无确认稿 → ErrNotFound
	})
	acc := &domain.Account{ID: "acc-1", Role: domain.RoleManager, TenantID: "tenant-A", Status: "active"}

	rec := callAs(t, s, acc, "POST", "/api/stores/st-1/translate", `{}`, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/stores/{id}/translate", s.handleTranslate)
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无确认稿应 400，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "请先上传菜单") {
		t.Errorf("提示应为「请先上传菜单」，实际 %s", rec.Body.String())
	}
}

/* ── 四、确认稿与草稿的读写契约 ─────────────────────────────── */

// TestHandleSaveDraft_EmptyItemsRejected 锁定入参校验：
// 空菜单直接 400，不让一条空稿占掉「最新稿」的位置
// （否则后面取最新稿会拿到空稿，商家看到「菜单没了」）。
func TestHandleSaveDraft_EmptyItemsRejected(t *testing.T) {
	s := newMerchantServer(t, func(q string) [][]driver.Value {
		if strings.Contains(q, "FROM store") {
			return [][]driver.Value{storeRow("st-1", "acc-1", "tenant-A", "某店")}
		}
		return nil
	})
	acc := &domain.Account{ID: "acc-1", Role: domain.RoleManager, TenantID: "tenant-A", Status: "active"}

	for _, body := range []string{`{"items":[]}`, `{}`, `not-json`} {
		rec := callAs(t, s, acc, "POST", "/api/stores/st-1/draft", body, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /api/stores/{id}/draft", s.handleSaveDraft)
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s 应 400，实际 %d", body, rec.Code)
		}
	}
}

// TestHandleGetDraft_NoDraftIs404Not200 锁定「没有」与「有但为空」的区分：
// 无稿返回 404 而不是空对象，前端据此区分「还没上传」与「上传了但没内容」。
// 混成 200 + 空对象的话，商家会以为自己上传过。
func TestHandleGetDraft_NoDraftIs404Not200(t *testing.T) {
	s := newMerchantServer(t, func(q string) [][]driver.Value {
		if strings.Contains(q, "FROM store") {
			return [][]driver.Value{storeRow("st-1", "acc-1", "tenant-A", "某店")}
		}
		return nil
	})
	acc := &domain.Account{ID: "acc-1", Role: domain.RoleManager, TenantID: "tenant-A", Status: "active"}

	rec := callAs(t, s, acc, "GET", "/api/stores/st-1/draft", "", func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/stores/{id}/draft", s.handleGetDraft)
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无确认稿应 404，实际 %d body=%s", rec.Code, rec.Body.String())
	}
}

/* ── 五、门店更新：枚举白名单 ──────────────────────────────── */

// TestHandlePatchStore_EnumWhitelist 锁定两个核心分档的白名单：
// service_tier（pure / service）与 order_mode（assist / replace）。
// 写入未知值会让前端渲染与统计一起失准，故宁可在入口挡掉。
func TestHandlePatchStore_EnumWhitelist(t *testing.T) {
	acc := &domain.Account{ID: "acc-1", Role: domain.RoleManager, TenantID: "tenant-A", Status: "active"}
	cases := []struct {
		name string
		body string
		msg  string
	}{
		{"档位非法", `{"service_tier":"gold"}`, "档位只能是 pure / service"},
		{"点单模式非法", `{"order_mode":"menu"}`, "点单模式只能是 assist / replace"},
	}
	for _, c := range cases {
		s := newMerchantServer(t, func(q string) [][]driver.Value {
			if strings.Contains(q, "FROM store") {
				return [][]driver.Value{storeRow("st-1", "acc-1", "tenant-A", "某店")}
			}
			return nil
		})
		rec := callAs(t, s, acc, "PATCH", "/api/stores/st-1", c.body, func(mux *http.ServeMux) {
			mux.HandleFunc("PATCH /api/stores/{id}", s.handlePatchStore)
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 应 400，实际 %d", c.name, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), c.msg) {
			t.Errorf("%s 提示应含「%s」，实际 %s", c.name, c.msg, rec.Body.String())
		}
	}
}

// TestHandlePatchStore_ValidEnumsAccepted 划定上一条的边界：
// 白名单不能拧太紧把合法值也挡了（反向断言，防「一刀切」）。
func TestHandlePatchStore_ValidEnumsAccepted(t *testing.T) {
	// 更新后的行：档位与点单模式都取新值（假存储不会真的算，直接回这行）
	updated := []driver.Value{
		"st-1", "acc-1", "tenant-A", "", "某店", "",
		nil, nil, "", "", "",
		true, "service", "replace", "iso-slug", nil, time.Unix(1700000000, 0).UTC(),
	}
	// UpdateStore 是「Exec + 回读」，两次读走同一条 SQL，只能按顺序区分：
	// 第 1 次是 requireStore 读到的原值，第 2 次起是更新后回读的新值。
	reads := 0
	s := newMerchantServer(t, func(q string) [][]driver.Value {
		if strings.Contains(q, "FROM store") {
			reads++
			if reads == 1 {
				return [][]driver.Value{storeRow("st-1", "acc-1", "tenant-A", "某店")}
			}
			return [][]driver.Value{updated}
		}
		return nil
	})
	acc := &domain.Account{ID: "acc-1", Role: domain.RoleManager, TenantID: "tenant-A", Status: "active"}

	rec := callAs(t, s, acc, "PATCH", "/api/stores/st-1",
		`{"service_tier":"service","order_mode":"replace"}`, func(mux *http.ServeMux) {
			mux.HandleFunc("PATCH /api/stores/{id}", s.handlePatchStore)
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("合法枚举值应放行，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Store struct {
			ServiceTier string `json:"service_tier"`
			OrderMode   string `json:"order_mode"`
		} `json:"store"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if resp.Store.ServiceTier != "service" || resp.Store.OrderMode != "replace" {
		t.Errorf("应回写新档位，实际 %+v", resp.Store)
	}
}
