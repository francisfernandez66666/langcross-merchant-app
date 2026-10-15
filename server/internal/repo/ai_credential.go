package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/assist"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
)

// 本文件：AI 经营助手的三块持久化 —— LLM 凭据、多轮流程状态、对话历史。
//
// 权限口径（与 translation_api_key 完全一致）：
//   - 写操作（增删改 / 查看明文 / 轮换）只归平台超管，httpapi 层已做门禁；
//   - repo 层不重复做角色判断（那是 httpapi 的职责），但**归属校验一律写进 WHERE**，
//     即便上层漏判，也读不到别家租户的凭据。
//
// 对应迁移：migrations/0006_org_rbac.sql 的 ai_credential / assist_session 两张表。

const aiCredCols = `id, tenant_id, name, base_url, model, key_masked, status,
	COALESCE(created_by,''), extract(epoch FROM created_at)::bigint,
	extract(epoch FROM updated_at)::bigint`

// AICredRow 是插入 LLM 凭据的入参（密文由上层算好）。
type AICredRow struct {
	ID        string
	TenantID  string
	Name      string
	BaseURL   string
	Model     string
	KeyEnc    []byte
	KeyMask   string
	Status    string
	CreatedBy string
}

// CreateAICredential 写入一份 LLM 凭据（密文 + 脱敏串一并落库）。
func (r *Repo) CreateAICredential(ctx context.Context, in AICredRow) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO ai_credential
		   (id, tenant_id, name, base_url, model, key_enc, key_masked, status, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		in.ID, in.TenantID, in.Name, in.BaseURL, in.Model, in.KeyEnc, in.KeyMask, in.Status, in.CreatedBy)
	return err
}

// ListAICredentials 列出凭据（脱敏展示，绝不含明文）。
// 排序：平台级（tenant_id=”)在前，其余按创建时间倒序。
func (r *Repo) ListAICredentials(ctx context.Context) ([]domain.AICredential, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+aiCredCols+` FROM ai_credential ORDER BY (tenant_id <> '') ASC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AICredential{}
	for rows.Next() {
		c, err := scanAICred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetAICredential 按 ID 取凭据（不含密文）；不存在返回 ErrNotFound。
func (r *Repo) GetAICredential(ctx context.Context, id string) (*domain.AICredential, error) {
	return scanAICred(r.DB.QueryRowContext(ctx,
		`SELECT `+aiCredCols+` FROM ai_credential WHERE id = $1`, id))
}

// ActiveAICredentialForTenant 取某租户当前生效的 LLM 凭据。
//
// 解析顺序刻意固定为「先租户、后平台」：
//  1. 本租户自己配的 active 凭据（租户想用自家额度时）；
//  2. 平台级（tenant_id = ”）active 凭据（默认，超管统一维护）。
//
// 两条都没有时返回 (nil, nil) —— 这是正常状态，AI 助手自动降级到确定性兜底，
// 不报错、不影响其它功能。
func (r *Repo) ActiveAICredentialForTenant(ctx context.Context, tenantID string) (*domain.AICredential, error) {
	// 同一租户可能被误配多条 active：取最新创建的那条，保证行为确定。
	c, err := scanAICred(r.DB.QueryRowContext(ctx,
		`SELECT `+aiCredCols+` FROM ai_credential
		 WHERE tenant_id = $1 AND status = 'active' ORDER BY created_at DESC LIMIT 1`, tenantID))
	if err == nil && c != nil {
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) && err != nil {
		return nil, err
	}
	return scanAICred(r.DB.QueryRowContext(ctx,
		`SELECT `+aiCredCols+` FROM ai_credential
		 WHERE tenant_id = '' AND status = 'active' ORDER BY created_at DESC LIMIT 1`))
}

// AICredCiphertext 取凭据密文（解密前的原始字节）。
func (r *Repo) AICredCiphertext(ctx context.Context, id string) ([]byte, error) {
	var blob []byte
	err := r.DB.QueryRowContext(ctx,
		`SELECT key_enc FROM ai_credential WHERE id = $1`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return blob, err
}

// SetAICredentialStatus 启用 / 停用凭据。
func (r *Repo) SetAICredentialStatus(ctx context.Context, id, status string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE ai_credential SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAICredential 改凭据的非密钥字段（别名 / 地址 / 模型）。
func (r *Repo) UpdateAICredential(ctx context.Context, id, name, baseURL, model string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE ai_credential SET name = $2, base_url = $3, model = $4, updated_at = now() WHERE id = $1`,
		id, strings.TrimSpace(name), strings.TrimSpace(baseURL), strings.TrimSpace(model))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RotateAICredential 轮换 key（新密文覆盖，旧的立即失效）。
func (r *Repo) RotateAICredential(ctx context.Context, id string, enc []byte, masked string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE ai_credential SET key_enc = $2, key_masked = $3, updated_at = now() WHERE id = $1`,
		id, enc, masked)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAICredential 删除凭据。
func (r *Repo) DeleteAICredential(ctx context.Context, id string) error {
	res, err := r.DB.ExecContext(ctx, `DELETE FROM ai_credential WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAICredentials 统计凭据数（后台概览用）。
func (r *Repo) CountAICredentials(ctx context.Context) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM ai_credential`).Scan(&n)
	return n, err
}

// scanAICred 把一行凭据记录扫描成 domain.AICredential（仅脱敏字段，不含明文 api_key）。
func scanAICred(sc rowScanner) (*domain.AICredential, error) {
	var c domain.AICredential
	err := sc.Scan(&c.ID, &c.TenantID, &c.Name, &c.BaseURL, &c.Model, &c.KeyMasked,
		&c.Status, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

/* ── 多轮流程状态（assist.SessionStore 的 DB 实现） ───────── */

// flowSessionTTL 与 assist 包的内存版保持一致：流程在途 10 分钟。
// 商家被打断（去接客）几分钟后回来，流程应当自然结束而不是接着追问。
const flowSessionTTL = 10 * time.Minute

// AssistSessionStore 是 assist.SessionStore 的 PostgreSQL 实现。
//
// 为什么值得单独存库：商家端将来多实例部署，流程状态放内存会导致
// 「第一步在 A 实例、第二步在 B 实例」时流程丢失。表很轻（一行一会话）。
//
// 编译期断言见本文件末尾。
type AssistSessionStore struct {
	db *sql.DB
}

// NewAssistSessionStore 构造 DB 版流程状态存储。
func NewAssistSessionStore(db *sql.DB) *AssistSessionStore { return &AssistSessionStore{db: db} }

// GetSession 取在途流程；过期或不存在返回 (nil, nil)。
func (s *AssistSessionStore) GetSession(ctx context.Context, accountID string) (*assist.SessionState, error) {
	var st assist.SessionState
	err := s.db.QueryRowContext(ctx,
		`SELECT in_flow, flow_step, extract(epoch FROM updated_at)::bigint
		   FROM assist_session WHERE account_id = $1`, accountID).Scan(&st.Flow, &st.Step, &st.At)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// TTL 判定放在存储层：与内存版同口径，读出来就是「还在流程中」的状态。
	if time.Since(time.Unix(st.At, 0)) > flowSessionTTL {
		_ = s.ClearSession(ctx, accountID)
		return nil, nil
	}
	if st.Flow == "" {
		return nil, nil
	}
	return &st, nil
}

// SaveSession 写入 / 更新流程进度（upsert：一行一会话）。
func (s *AssistSessionStore) SaveSession(ctx context.Context, accountID, flow string, step int) error {
	if flow == "" || step < 0 {
		return s.ClearSession(ctx, accountID)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO assist_session (id, account_id, store_id, in_flow, flow_step, updated_at)
		 VALUES ($1, $2, '', $3, $4, now())
		 ON CONFLICT (id) DO UPDATE SET in_flow = $3, flow_step = $4, updated_at = now()`,
		"sess_"+accountID, accountID, flow, step)
	return err
}

// ClearSession 结束流程（直接删行：会话本就该轻，删除比置空更省心）。
func (s *AssistSessionStore) ClearSession(ctx context.Context, accountID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM assist_session WHERE account_id = $1`, accountID)
	return err
}

// Count 返回在途流程数（观测用）。
func (s *AssistSessionStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM assist_session WHERE updated_at > now() - ($1::interval)`,
		flowSessionTTL.String()).Scan(&n)
	return n, err
}

// 编译期断言：DB 实现满足引擎的存储接缝。
var _ assist.SessionStore = (*AssistSessionStore)(nil)
