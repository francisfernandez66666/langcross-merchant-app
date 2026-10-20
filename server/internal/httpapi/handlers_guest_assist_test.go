package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/assist"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/config"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/crypt"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

/* ── FIX-7：客人助手按门店租户接线 LLM ───────────────────────
   三条 acceptance 各有回归：
     A. 配了凭据 → 客人提问真的走模型（reply.source=llm），mock LLM 服务器收到请求；
     B. 没配凭据 → 现有行为不变（规则兜底，source=fallback）；
     C. 按店日限超限 → LLM 被收回（不再打模型），但客人照常得到回答。
   另锁 reserveGuestLLM 本身的计数与跨日归零语义。 */

/* ── 假 repo 驱动：按 SQL 内容分发结果集 ───────────────────── */

// guestFakeStore 是客人助手链路需要的假存储：GetStoreBySlug 返回一行门店，
// ListDishes 返回一道菜，GetTemplateByStore 返回零行（无券模板）。
type guestFakeStore struct {
	storeRow []driver.Value
	dishRows [][]driver.Value
}

// Open 建一条假连接（不碰任何真实 IO，数据源名直接丢弃）。
func (s *guestFakeStore) Open(string) (driver.Conn, error) {
	return &guestFakeConn{store: s}, nil
}

type guestFakeConn struct{ store *guestFakeStore }

// Prepare 恒返回 io.EOF：本假连接走 Queryer/Execer 快捷路径，不会用到预编译。
func (c *guestFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, io.EOF
}

// Close 关闭假连接（无资源可释放，恒成功）。
func (c *guestFakeConn) Close() error { return nil }

// Begin 恒返回 io.EOF：本用例的读路径都不需要事务。
func (c *guestFakeConn) Begin() (driver.Tx, error) {
	return nil, io.EOF
}

// QueryContext 按 SQL 里的表名分发预置结果集，等价于一个只会三句查询的假库。
func (c *guestFakeConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(q, "FROM store"):
		return &staticRows{rows: [][]driver.Value{c.store.storeRow}}, nil
	case strings.Contains(q, "FROM dish"):
		return &staticRows{rows: c.store.dishRows}, nil
	default:
		// GetTemplateByStore 等：零行 → repo 归一为 ErrNotFound
		return &staticRows{}, nil
	}
}

// ExecContext 恒返回「影响一行」：埋点等写操作只要不报错，链路就继续。
func (c *guestFakeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return staticResult{}, nil
}

// staticRows 是预置数据行的 driver.Rows 实现（列数随行走）。
type staticRows struct {
	rows [][]driver.Value
	i    int
}

// Columns 给出与首行等宽的占位列名：repo 按列序 Scan，列名本身不参与匹配。
func (r *staticRows) Columns() []string {
	if len(r.rows) > 0 {
		cols := make([]string, len(r.rows[0]))
		for i := range cols {
			cols[i] = "c"
		}
		return cols
	}
	return []string{"c"}
}

// Close 结束结果集读取（无资源可释放，恒成功）。
func (r *staticRows) Close() error { return nil }

// Next 把下一行拷进 dest；读完返回 io.EOF，由 database/sql 归一成「无更多行」。
func (r *staticRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

type staticResult struct{}

// LastInsertId 恒返回 0：本用例的写路径都不读自增 ID。
func (staticResult) LastInsertId() (int64, error) { return 0, nil }

// RowsAffected 恒返回 1，让调用方按「写成功」处理。
func (staticResult) RowsAffected() (int64, error) { return 1, nil }

// newGuestTestRepo 起一个挂假存储的 Repo（门店/菜品/券模板三路分发）。
//
// storeID 由调用方指定：日限计数按 storeID 走，用例之间必须各用各的，
// 否则上一个用例消耗的名额会算到下一个头上，测试互相污染。
func newGuestTestRepo(t *testing.T, tenantID, storeID string) *repo.Repo {
	t.Helper()
	storeRow := []driver.Value{
		storeID, "acc-1", tenantID, "", "LLM联调店", "",
		nil, nil, "", "", "",
		true, "service", "assist", "llm-shop", nil, time.Unix(1700000000, 0).UTC(),
	}
	dishRows := [][]driver.Value{{
		"d-1", storeID, "宫保鸡丁", "",
		[]byte(`{"en":"Kung Pao Chicken"}`), "",
		38.0, "热菜", []byte(`{"spicy":1,"allergens":[]}`), int64(0), false, nil, "",
	}}
	name := "guest-fake-" + t.Name()
	sql.Register(name, &guestFakeStore{storeRow: storeRow, dishRows: dishRows})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return repo.New(db)
}

/* ── 假 AICredStore + mock LLM 服务器 ──────────────────────── */

// guestFakeAICredStore 按租户返回 LLM 凭据；密文按调用计数，供断言「打了几次模型」。
type guestFakeAICredStore struct {
	byTenant map[string]*domain.AICredential
	blob     []byte
	calls    int32
}

// ActiveAICredentialForTenant 按租户返回预置凭据；没预置就是 nil（= 该店没配凭据）。
func (f *guestFakeAICredStore) ActiveAICredentialForTenant(_ context.Context, tenantID string) (*domain.AICredential, error) {
	return f.byTenant[tenantID], nil
}

// AICredCiphertext 返回固定密文并累加调用次数，供断言「vault 解了几次密」。
func (f *guestFakeAICredStore) AICredCiphertext(_ context.Context, _, _ string) ([]byte, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.blob, nil
}

// newMockLLM 起一个假 OpenAI 兼容端点，返回固定译文，并统计被调用次数。
func newMockLLM(t *testing.T, content string) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

// newGuestTestServer 装配一个跑着真引擎 + 真 vault（假存储）的 Server。
func newGuestTestServer(t *testing.T, tenantID, storeID string, cred *domain.AICredential) (*Server, *guestFakeAICredStore) {
	t.Helper()
	cipher, err := crypt.NewCipher("unit-test-secret-value-123456")
	if err != nil {
		t.Fatal(err)
	}
	store := &guestFakeAICredStore{byTenant: map[string]*domain.AICredential{}}
	if cred != nil {
		store.byTenant[tenantID] = cred
		blob, err := cipher.Encrypt("lx-live-abcdef123456")
		if err != nil {
			t.Fatal(err)
		}
		store.blob = blob
	}
	vault := service.NewAICredVault(cipher, store)
	s := NewServer(config.Config{DevMode: true, Currency: "¥"}, newGuestTestRepo(t, tenantID, storeID),
		service.StubOCR{}, service.NewTranslator("", "", nil), nil, nil)
	s.WithAssist(assist.New(), vault)
	return s, store
}

// askGuest 打一次客人助手接口，返回解包后的 reply。
func askGuest(t *testing.T, s *Server, slug, question, lang string) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/public/store/{slug}/assist", s.handleGuestAssist)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/public/store/"+slug+"/assist",
		strings.NewReader(`{"question":"`+question+`","lang":"`+lang+`"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("助手接口应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ok    bool `json:"ok"`
		Reply struct {
			Content string `json:"content"`
			Source  string `json:"source"`
			Model   string `json:"model"`
		} `json:"reply"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	return map[string]any{"content": resp.Reply.Content, "source": resp.Reply.Source, "model": resp.Reply.Model}
}

/* ── A：配了凭据 → 真的走模型 ─────────────────────────────── */

// TestGuestAssist_LLMWiredWhenCredentialExists 是 FIX-7 的核心验收：
// 门店租户有 active 凭据时，客人提问必须真的打到模型端点（source=llm、
// 带回模型名），且守卫链不失效（译文里带 [[菜名]]，价格与菜单一致）。
// 修订前这里恒为 fallback——handler 传 nil，凭据建了也用不上。
func TestGuestAssist_LLMWiredWhenCredentialExists(t *testing.T) {
	mock, hits := newMockLLM(t, "I recommend [[Kung Pao Chicken]], it is ¥38.")
	cred := &domain.AICredential{
		ID: "cred-1", TenantID: "t-guest", Name: "测试凭据",
		BaseURL: mock.URL, Model: "mock-model", Status: domain.AICredActive,
	}
	s, _ := newGuestTestServer(t, "t-guest", "st-llm-wired", cred)

	rep := askGuest(t, s, "llm-shop", "recommend a chicken dish", "en")

	if rep["source"] != "llm" {
		t.Fatalf("配了凭据应走模型（source=llm），实际 %v content=%v", rep["source"], rep["content"])
	}
	if rep["model"] != "mock-model" {
		t.Errorf("应带回模型名 mock-model，实际 %v", rep["model"])
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Errorf("mock LLM 应被调用 1 次，实际 %d", n)
	}
	if content, _ := rep["content"].(string); !strings.Contains(content, "Kung Pao Chicken") {
		t.Errorf("译文应经过守卫链原样带回，实际 %q", content)
	}
}

/* ── B：没配凭据 → 现有行为不变 ───────────────────────────── */

// TestGuestAssist_NoCredentialKeepsFallback 锁定降级路径：
// 店里没有可用凭据（既无租户凭据也无平台默认）时，客人照常得到
// 基于菜单事实的回答，且不打模型——「没配 Key」与「配了 Key」对客人
// 是无缝的，对平台是零成本的。
func TestGuestAssist_NoCredentialKeepsFallback(t *testing.T) {
	_, hits := newMockLLM(t, "不该被调用")
	s, _ := newGuestTestServer(t, "t-guest", "st-no-cred", nil)

	rep := askGuest(t, s, "llm-shop", "有没有鸡", "zh")

	if rep["source"] == "llm" {
		t.Fatalf("无凭据不应走模型，实际 source=%v", rep["source"])
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("无凭据时 mock LLM 不应被调用，实际 %d 次", n)
	}
	if content, _ := rep["content"].(string); content == "" {
		t.Error("规则兜底也应给出非空回答")
	}
}

/* ── C：按店日限 → 超限收回 LLM ───────────────────────────── */

// TestGuestAssist_DailyLimitWithdrawsLLM 锁定成本护栏：
// 超过单店当日名额后，助手收回 LLM 配置（不再打模型），但客人照常
// 得到回答——护栏的目的是「不再花钱」，不是「让客人干等」。
func TestGuestAssist_DailyLimitWithdrawsLLM(t *testing.T) {
	old := guestLLMDailyLimit
	guestLLMDailyLimit = 2
	t.Cleanup(func() { guestLLMDailyLimit = old })

	mock, hits := newMockLLM(t, "I recommend [[Kung Pao Chicken]], it is ¥38.")
	cred := &domain.AICredential{
		ID: "cred-1", TenantID: "t-guest", Name: "测试凭据",
		BaseURL: mock.URL, Model: "mock-model", Status: domain.AICredActive,
	}
	// 门店 ID 与日限计数都按 storeID 走：用一个独立的租户/门店组合，
	// 避免与其它用例共享 guestLLMUsage 计数（否则上一个用例消耗的名额
	// 会让本用例提前超限，绿红全凭执行顺序）。
	s, _ := newGuestTestServer(t, "t-limit", "st-daily-limit", cred)

	for i := 0; i < 2; i++ {
		if rep := askGuest(t, s, "llm-shop", "recommend a chicken dish", "en"); rep["source"] != "llm" {
			t.Fatalf("第 %d 次应在限额内走模型，实际 %v", i+1, rep["source"])
		}
	}
	// 第三次：超限 → 撤回 LLM，回答照旧
	rep := askGuest(t, s, "llm-shop", "recommend a chicken dish", "en")
	if rep["source"] == "llm" {
		t.Errorf("超日限不应再走模型，实际 %v", rep["source"])
	}
	if content, _ := rep["content"].(string); content == "" {
		t.Error("超限后规则兜底也应给出非空回答")
	}
	if n := atomic.LoadInt32(hits); n != 2 {
		t.Errorf("mock LLM 应只被打 2 次（限额内），实际 %d", n)
	}
}

/* ── 计数器单体语义 ────────────────────────────────────────── */

// TestReserveGuestLLM_CountsAndResets 锁定计数器的三个语义：
// 限额内放行、达限拒绝、跨 UTC 日自动归零（与 TenantPrimary 日限同构）。
func TestReserveGuestLLM_CountsAndResets(t *testing.T) {
	old := guestLLMDailyLimit
	guestLLMDailyLimit = 2
	t.Cleanup(func() { guestLLMDailyLimit = old })

	// 用一个本用例独有的门店 ID，避开其它用例的计数
	storeID := "st-reserve-test"
	guestLLMMu.Lock()
	delete(guestLLMUsage, storeID)
	guestLLMMu.Unlock()

	if !reserveGuestLLM(storeID) || !reserveGuestLLM(storeID) {
		t.Fatal("限额内前两次应放行")
	}
	if reserveGuestLLM(storeID) {
		t.Error("达到限额后应拒绝")
	}

	// 把记账拨到昨天：今天必须重新计数（否则限额一天就把店永久锁死）
	guestLLMMu.Lock()
	guestLLMUsage[storeID] = &llmDailyUsage{
		day:   time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"),
		count: 999,
	}
	guestLLMMu.Unlock()
	if !reserveGuestLLM(storeID) {
		t.Error("跨日后计数应归零重开")
	}
}

// TestReserveGuestLLM_ZeroMeansUnlimited 锁定 0 = 不限的语义：
// 运营要临时放开某店时，改一个数字即可，不需要改代码。
func TestReserveGuestLLM_ZeroMeansUnlimited(t *testing.T) {
	old := guestLLMDailyLimit
	guestLLMDailyLimit = 0
	t.Cleanup(func() { guestLLMDailyLimit = old })

	storeID := "st-unlimited-test"
	guestLLMMu.Lock()
	delete(guestLLMUsage, storeID)
	guestLLMMu.Unlock()
	for i := 0; i < 5; i++ {
		if !reserveGuestLLM(storeID) {
			t.Fatalf("limit=0 时应恒放行，第 %d 次被拒", i+1)
		}
	}
}
