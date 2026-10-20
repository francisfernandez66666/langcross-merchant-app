package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

/* ── 记录型假 SQL 驱动 ───────────────────────────────────────
   repo 包第一个测试文件。AI 凭据的租户归属校验（FIX-8）是「SQL 里的 WHERE」
   而不是 Go 里的 if——要锁住它，就必须断言发出去的 SQL 文本与参数。
   这里用一个记录型假驱动：每次查询/执行都把 (SQL, args) 记下来，
   结果行可编程（返回一行 / 返回零行 / 影响行数），从而不依赖真实 PostgreSQL。 */

// recEntry 是一条被记录下来的 SQL 调用。
type recEntry struct {
	query string
	args  []driver.NamedValue
}

// recLog 是并发安全的调用记录（database/sql 可能池化连接，宁弱勿漏）。
type recLog struct {
	mu      sync.Mutex
	entries []recEntry
}

func (l *recLog) add(q string, args []driver.NamedValue) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, recEntry{query: q, args: args})
}

// last 取最后一条记录（测试断言用）。
func (l *recLog) last() recEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return recEntry{}
	}
	return l.entries[len(l.entries)-1]
}

// argString 取第 i 个位置参数的字符串值（从 0 计）。
func (e recEntry) argString(i int) string {
	if i >= len(e.args) {
		return "<缺失>"
	}
	s, _ := e.args[i].Value.(string)
	return s
}

// recDriver 把每个连接绑定到同一份记录日志；rowsFn / execN 控制返回结果。
type recDriver struct {
	log    *recLog
	rowsFn func() [][]driver.Value // 查询返回的数据行（每行一列或多列）
	execN  int64                   // 执行类语句的影响行数
}

// Open 实现 driver.Driver：每个连接共享同一份记录日志与可编程返回。
func (d *recDriver) Open(string) (driver.Conn, error) {
	return &recConn{d: d}, nil
}

type recConn struct{ d *recDriver }

// Prepare 实现 driver.Conn：本假驱动不预备语句，走 Queryer/Execer 快捷路径。
func (c *recConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("rec driver: Prepare not supported")
}

// Close 实现 driver.Conn：无资源可释放。
func (c *recConn) Close() error { return nil }

// Begin 实现 driver.Conn：测试不涉及事务，明确报错比静默返回 nil 更难被误用。
func (c *recConn) Begin() (driver.Tx, error) {
	return nil, errors.New("rec driver: Begin not supported")
}

// QueryContext 实现 driver.QueryerContext：记录 (SQL, 参数) 后返回可编程数据行——
// 测试靠「记录下来的 SQL」断言归属口径，靠「返回的行」走正常扫描路径。
func (c *recConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.d.log.add(q, args)
	rows := c.d.rowsFn()
	return &recRows{rows: rows}, nil
}

// ExecContext 实现 driver.ExecerContext：记录 (SQL, 参数) 后返回可编程影响行数——
// 行数设为 0 即模拟「WHERE 没命中」，用来锁 ErrNotFound 语义。
func (c *recConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.d.log.add(q, args)
	return recResult{n: c.d.execN}, nil
}

type recRows struct {
	rows [][]driver.Value
	i    int
}

// Columns 实现 driver.Rows：列数随数据行给（凭据行 10 列、密文行 1 列），
// 与 aiCredCols / 单列 SELECT 的扫描目标一一对应。
func (r *recRows) Columns() []string {
	// 列数随数据行走：GetAICredential 走 10 列，AICredCiphertext 走 1 列
	if len(r.rows) > 0 {
		cols := make([]string, len(r.rows[0]))
		for i := range cols {
			cols[i] = "c"
		}
		return cols
	}
	return []string{"c"}
}

// Close 实现 driver.Rows：无游标资源。
func (r *recRows) Close() error { return nil }

// Next 实现 driver.Rows：逐行拷贝给 database/sql，行尽返回 io.EOF
// （QueryRow 路径下 Scan 会把它转成 sql.ErrNoRows → repo 归一为 ErrNotFound）。
func (r *recRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

type recResult struct{ n int64 }

// LastInsertId 实现 driver.Result：插入 ID 由调用方用 idgen 生成，不走数据库序列。
func (r recResult) LastInsertId() (int64, error) { return 0, nil }

// RowsAffected 实现 driver.Result：返回可编程的影响行数，是写路径测试的旋钮。
func (r recResult) RowsAffected() (int64, error) { return r.n, nil }

// newRecRepo 起一个只挂假驱动的 Repo，并返回它使用的记录日志。
func newRecRepo(t *testing.T, d *recDriver) *Repo {
	t.Helper()
	name := "rec-ai-cred"
	// 同一进程内多次注册同名 driver 会 panic：用测试名去重
	sql.Register(name+"-"+t.Name(), d)
	db, err := sql.Open(name+"-"+t.Name(), "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db)
}

// fullAICredRow 是一行完整的凭据记录（列序同 repo.aiCredCols）。
func fullAICredRow() [][]driver.Value {
	return [][]driver.Value{{
		"cred-1", "t-owner", "生产凭据", "https://api.example.com/v1", "test-model",
		"sk-****1234", "active", "admin-1", int64(1700000000), int64(1700000000),
	}}
}

// scopeClause 是 FIX-8 的归属口径三段式，任何按 ID 的读写都必须带上。
const scopeClause = "tenant_id = $2 OR tenant_id = '' OR $2 = ''"

/* ── 归属口径：SQL 形状与参数 ─────────────────────────────── */

// TestAICredScope_EveryByIDCallCarriesScope 锁定：六个按 ID 的方法，
// 每一条 SQL 都必须带归属口径，且第二个参数就是调用方租户。
// 这是 P1-3 的回归——修订前全是裸 WHERE id=$1，上层漏判就能读别家凭据。
func TestAICredScope_EveryByIDCallCarriesScope(t *testing.T) {
	blobRow := [][]driver.Value{{[]byte("enc-blob")}}

	cases := []struct {
		name string
		call func(r *Repo) error
	}{
		{"GetAICredential", func(r *Repo) error {
			_, err := r.GetAICredential(context.Background(), "cred-1", "t-caller")
			return err
		}},
		{"AICredCiphertext", func(r *Repo) error {
			_, err := r.AICredCiphertext(context.Background(), "cred-1", "t-caller")
			return err
		}},
		{"SetAICredentialStatus", func(r *Repo) error {
			return r.SetAICredentialStatus(context.Background(), "cred-1", "t-caller", "disabled")
		}},
		{"UpdateAICredential", func(r *Repo) error {
			return r.UpdateAICredential(context.Background(), "cred-1", "t-caller", "n", "https://x/v1", "m")
		}},
		{"RotateAICredential", func(r *Repo) error {
			return r.RotateAICredential(context.Background(), "cred-1", "t-caller", []byte("enc"), "sk-****")
		}},
		{"DeleteAICredential", func(r *Repo) error {
			return r.DeleteAICredential(context.Background(), "cred-1", "t-caller")
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := &recLog{}
			rowsFn := func() [][]driver.Value {
				if strings.Contains(c.name, "Ciphertext") {
					return blobRow
				}
				return fullAICredRow()
			}
			r := newRecRepo(t, &recDriver{log: log, rowsFn: rowsFn, execN: 1})
			if err := c.call(r); err != nil {
				t.Fatalf("%s 调用失败: %v", c.name, err)
			}
			e := log.last()
			if !strings.Contains(e.query, scopeClause) {
				t.Errorf("%s 的 SQL 缺少归属口径：\n%s", c.name, e.query)
			}
			if got := e.argString(1); got != "t-caller" {
				t.Errorf("%s 的第二个参数应为调用方租户 t-caller，实际 %q", c.name, got)
			}
			if got := e.argString(0); got != "cred-1" {
				t.Errorf("%s 的第一个参数应为凭据 ID，实际 %q", c.name, got)
			}
		})
	}
}

/* ── 空串放行：平台级账号与平台默认凭据 ───────────────────── */

// TestAICredScope_PlatformCallerPassesEmpty 锁定空串放行条款的两个方向：
// 平台超管（tenantID=”）发起的调用必须把空串作为第二个参数传下去——
// repo 的 `$2 = ”` 条款靠它见到全部行；把空串吃掉或改写就会把超管挡在门外。
func TestAICredScope_PlatformCallerPassesEmpty(t *testing.T) {
	log := &recLog{}
	r := newRecRepo(t, &recDriver{log: log, rowsFn: fullAICredRow, execN: 1})

	c, err := r.GetAICredential(context.Background(), "cred-1", "")
	if err != nil {
		t.Fatalf("平台级调用应成功: %v", err)
	}
	if c == nil || c.ID != "cred-1" {
		t.Fatalf("应读到凭据行，实际 %+v", c)
	}
	if got := log.last().argString(1); got != "" {
		t.Errorf("平台级账号的租户参数应为空串，实际 %q", got)
	}
}

// TestGetAICredential_ScansFullRow 锁定正常路径的字段装配：
// 十个列按 aiCredCols 顺序Scan，含 created_by 的 COALESCE 与两个 Unix 秒时间。
func TestGetAICredential_ScansFullRow(t *testing.T) {
	log := &recLog{}
	r := newRecRepo(t, &recDriver{log: log, rowsFn: fullAICredRow, execN: 1})

	c, err := r.GetAICredential(context.Background(), "cred-1", "t-owner")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if c.ID != "cred-1" || c.TenantID != "t-owner" || c.Name != "生产凭据" ||
		c.BaseURL != "https://api.example.com/v1" || c.Model != "test-model" ||
		c.KeyMasked != "sk-****1234" || c.Status != "active" || c.CreatedBy != "admin-1" {
		t.Errorf("字段装配错位: %+v", c)
	}
	if c.CreatedAt != 1700000000 || c.UpdatedAt != 1700000000 {
		t.Errorf("时间字段应按 Unix 秒装配: %+v", c)
	}
}

/* ── 写路径：影响 0 行即不存在 ────────────────────────────── */

// TestAICredWrites_ZeroRowsMeansNotFound 锁定三个写方法与删除的
// 「影响 0 行 → ErrNotFound」语义：调用方靠这个返回 404，
// 若把 0 行当成功，超管会对一个不存在的 ID 收到「成功」。
func TestAICredWrites_ZeroRowsMeansNotFound(t *testing.T) {
	calls := []struct {
		name string
		fn   func(r *Repo) error
	}{
		{"SetAICredentialStatus", func(r *Repo) error {
			return r.SetAICredentialStatus(context.Background(), "ghost", "t1", "active")
		}},
		{"UpdateAICredential", func(r *Repo) error {
			return r.UpdateAICredential(context.Background(), "ghost", "t1", "n", "u", "m")
		}},
		{"RotateAICredential", func(r *Repo) error {
			return r.RotateAICredential(context.Background(), "ghost", "t1", []byte("e"), "m")
		}},
		{"DeleteAICredential", func(r *Repo) error {
			return r.DeleteAICredential(context.Background(), "ghost", "t1")
		}},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			log := &recLog{}
			r := newRecRepo(t, &recDriver{log: log, rowsFn: fullAICredRow, execN: 0})
			if err := c.fn(r); !errors.Is(err, ErrNotFound) {
				t.Errorf("影响 0 行应返回 ErrNotFound，实际 %v", err)
			}
		})
	}
}
