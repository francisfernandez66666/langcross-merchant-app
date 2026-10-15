// Package repo 的数据访问实现之一：租户（tenant）、翻译 API Key 与管理审计。
//
// 这三张表是「平台化」的地基（迁移 0005）：
//   - tenant            隔离与计费单元；超管在这里增删改；
//   - translation_api_key 租户级能言 Key，密文存储，商家在后台自行维护；
//   - admin_audit       超管与租户管理员的每一次敏感操作留痕。
//
// ★ 安全口径：Key 明文只在创建 / 轮换 / 查看这三个一次性响应里出现，
// 本包只提供「按 ID 取密文并解密」的能力，是否放行由上层鉴权决定。
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

/* ── 租户 ──────────────────────────────────────────────── */

// tenantCols 是 tenant 表的固定列清单（时间转 Unix 秒；perms 以文本形式返回，
// 再在 scanTenant 里反序列化成 map——database/sql 不会替我们把 JSONB 变成 map）。
const tenantCols = `id, code, name, status,
	COALESCE(extract(epoch FROM expires_at)::bigint, 0), perms::text,
	extract(epoch FROM created_at)::bigint`

// EnsureDefaultTenant 保证平台默认租户存在（幂等）。
//
// 启动时调用一次即可：新库由迁移 0005 建好，历史库在 0005 执行时也已回填。
// 这里再兜一层，是为了「迁移被跳过 / 手工建库」时仍能得到可用的默认租户。
func (r *Repo) EnsureDefaultTenant(ctx context.Context) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO tenant(id, code, name) VALUES ($1, 'langcross', 'LangCross 平台租户')
		 ON CONFLICT (id) DO NOTHING`, domain.DefaultTenantID)
	return err
}

// GetTenant 按 ID 查租户；不存在返回 ErrNotFound。
func (r *Repo) GetTenant(ctx context.Context, id string) (*domain.Tenant, error) {
	return scanTenant(r.DB.QueryRowContext(ctx,
		`SELECT `+tenantCols+` FROM tenant WHERE id = $1`, id))
}

// ListTenants 列出租户（后台租户管理页）。
// 按创建时间倒序：新租户在前，符合「最近接了谁」的关注顺序。
func (r *Repo) ListTenants(ctx context.Context) ([]domain.Tenant, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+tenantCols+` FROM tenant ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Tenant{}
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// TenantPatch 是租户可更新字段（nil 表示不改）；语义同 StorePatch。
type TenantPatch struct {
	Name      *string // 租户名
	Status    *string // active / disabled / expired
	ExpiresAt *int64  // 到期时间（Unix 秒）；0 表示清除到期时间
	Perms     *map[string]any
}

// CreateTenant 新建租户（超管操作）。
// code 是租户编码，命中唯一约束时返回错误，由上层转成「编码已存在」。
func (r *Repo) CreateTenant(ctx context.Context, code, name string, expiresAt *int64, perms map[string]any) (*domain.Tenant, error) {
	t := &domain.Tenant{ID: idgen.New(), Code: strings.TrimSpace(code), Name: strings.TrimSpace(name)}
	raw := []byte("{}")
	if len(perms) > 0 {
		var err error
		if raw, err = json.Marshal(perms); err != nil {
			return nil, fmt.Errorf("marshal tenant perms: %w", err)
		}
	}
	var exp any
	if expiresAt != nil && *expiresAt > 0 {
		exp = time.Unix(*expiresAt, 0)
	}
	err := r.DB.QueryRowContext(ctx,
		`INSERT INTO tenant(id, code, name, expires_at, perms) VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, code, name, status,
		   COALESCE(extract(epoch FROM expires_at)::bigint, 0),
		   perms::text, extract(epoch FROM created_at)::bigint`,
		t.ID, t.Code, t.Name, exp, raw).Scan(
		&t.ID, &t.Code, &t.Name, &t.Status, &t.ExpiresAt, new(string), &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}
	return t, nil
}

// UpdateTenant 局部更新租户；更新后回读整行返回。
func (r *Repo) UpdateTenant(ctx context.Context, id string, p TenantPatch) (*domain.Tenant, error) {
	raw := []byte("{}")
	if p.Perms != nil {
		var err error
		if raw, err = json.Marshal(*p.Perms); err != nil {
			return nil, fmt.Errorf("marshal tenant perms: %w", err)
		}
	}
	var exp any
	if p.ExpiresAt != nil {
		if *p.ExpiresAt > 0 {
			exp = time.Unix(*p.ExpiresAt, 0)
		} else {
			exp = nil // 0 = 清除到期时间
		}
	}
	if _, err := r.DB.ExecContext(ctx,
		`UPDATE tenant SET
		   name       = COALESCE($2, name),
		   status     = COALESCE($3, status),
		   expires_at = COALESCE($4, expires_at),
		   perms      = COALESCE($5, perms),
		   updated_at = now()
		 WHERE id = $1`,
		id, p.Name, p.Status, exp, raw); err != nil {
		return nil, fmt.Errorf("update tenant: %w", err)
	}
	return r.GetTenant(ctx, id)
}

// CountTenants 统计租户总数（平台总览）。
func (r *Repo) CountTenants(ctx context.Context) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM tenant`).Scan(&n)
	return n, err
}

// scanTenant 把一行租户记录扫描成 domain.Tenant。
func scanTenant(sc rowScanner) (*domain.Tenant, error) {
	var (
		t   domain.Tenant
		raw string
	)
	if err := sc.Scan(&t.ID, &t.Code, &t.Name, &t.Status, &t.ExpiresAt, &raw, &t.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if t.ExpiresAt != nil && *t.ExpiresAt == 0 {
		t.ExpiresAt = nil
	}
	if raw != "" && raw != "null" {
		_ = json.Unmarshal([]byte(raw), &t.Perms)
	}
	return &t, nil
}

/* ── 翻译 API Key ──────────────────────────────────────── */

// keyCols 是 translation_api_key 的固定列清单（时间转 Unix 秒）。
const keyCols = `id, tenant_id, name, provider, key_masked, mode, status, daily_limit,
	COALESCE(extract(epoch FROM last_used_at)::bigint, 0),
	COALESCE(created_by,''), extract(epoch FROM created_at)::bigint,
	extract(epoch FROM updated_at)::bigint`

// CreateTranslationKey 写入一把新 Key（密文 + 脱敏串一并落库）。
// enc 是 crypt.Cipher.Encrypt 的产物；masked 由上层算好（见 crypt.Mask）。
func (r *Repo) CreateTranslationKey(ctx context.Context, k *domain.TranslationKey, enc []byte, masked string) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO translation_api_key
		   (id, tenant_id, name, provider, key_enc, key_masked, mode, status, daily_limit, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		k.ID, k.TenantID, k.Name, k.Provider, enc, masked, k.Mode, k.Status, k.DailyLimit, k.CreatedBy)
	return err
}

// ListTranslationKeys 列出某租户的 Key（脱敏展示，绝不含明文）。
// 按创建时间倒序：后加的在前，便于找到刚轮换的新 Key。
func (r *Repo) ListTranslationKeys(ctx context.Context, tenantID string) ([]domain.TranslationKey, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+keyCols+` FROM translation_api_key WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.TranslationKey{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// GetTranslationKey 按 ID 取 Key（不含密文）；不存在或不属于该租户返回 ErrNotFound。
//
// ★ 归属校验写在 WHERE 里：即便上层漏判租户，也读不到别家的 Key。
func (r *Repo) GetTranslationKey(ctx context.Context, tenantID, id string) (*domain.TranslationKey, error) {
	return scanKey(r.DB.QueryRowContext(ctx,
		`SELECT `+keyCols+` FROM translation_api_key WHERE id = $1 AND tenant_id = $2`, id, tenantID))
}

// KeyCiphertext 取 Key 的密文（解密前的原始字节）。
// 只服务「查看明文」与「翻译时取凭据」两个场景，都是已鉴权路径。
func (r *Repo) KeyCiphertext(ctx context.Context, tenantID, id string) ([]byte, error) {
	var blob []byte
	err := r.DB.QueryRowContext(ctx,
		`SELECT key_enc FROM translation_api_key WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return blob, err
}

// ActiveTranslationKey 取该租户第一把启用中的 Key（翻译时的取用口径）。
// 返回 nil, nil 表示「该租户没有可用 Key」——这不是错误，调用方据此降级到别的引擎。
func (r *Repo) ActiveTranslationKey(ctx context.Context, tenantID string) (*domain.TranslationKey, error) {
	k, err := scanKey(r.DB.QueryRowContext(ctx,
		`SELECT `+keyCols+` FROM translation_api_key
		 WHERE tenant_id = $1 AND status = 'active' ORDER BY updated_at DESC, created_at DESC LIMIT 1`, tenantID))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return k, err
}

// SetTranslationKeyStatus 启用 / 停用 Key（不影响名称与模式）。
// 商家发现某把 Key 出问题时第一反应就是「先停掉」，因此单独开这个方法，
// 不必把名称 / 模式一起传（少传一个字段就少一处误改）。
func (r *Repo) SetTranslationKeyStatus(ctx context.Context, tenantID, id, status string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE translation_api_key SET status = $3, updated_at = now()
		 WHERE id = $1 AND tenant_id = $2`, id, tenantID, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateTranslationKeyMeta 更新 Key 的名称与模式（两者都可选，空串表示不改）。
// 明文不可改：换 Key 走 RotateTranslationKey，避免「密文换了但脱敏串没换」。
func (r *Repo) UpdateTranslationKeyMeta(ctx context.Context, tenantID, id, name, mode string, dailyLimit *int) error {
	var limit any
	if dailyLimit != nil {
		limit = *dailyLimit
	}
	res, err := r.DB.ExecContext(ctx,
		`UPDATE translation_api_key SET
		   name = COALESCE(NULLIF($3,''), name),
		   mode = COALESCE(NULLIF($4,''), mode),
		   daily_limit = COALESCE($5, daily_limit),
		   updated_at = now()
		 WHERE id = $1 AND tenant_id = $2`, id, tenantID, name, mode, limit)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RotateTranslationKey 轮换 Key：写新密文 + 新脱敏串，旧 Key 立即失效。
// 在同一事务外单条 UPDATE 完成——轮换是「替换语义」，不存在两边共存的中间态。
func (r *Repo) RotateTranslationKey(ctx context.Context, tenantID, id string, enc []byte, masked string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE translation_api_key SET key_enc = $3, key_masked = $4, updated_at = now()
		 WHERE id = $1 AND tenant_id = $2`, id, tenantID, enc, masked)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTranslationKey 删除 Key（密文一并消失，不可恢复）。
func (r *Repo) DeleteTranslationKey(ctx context.Context, tenantID, id string) error {
	res, err := r.DB.ExecContext(ctx,
		`DELETE FROM translation_api_key WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchTranslationKey 记录 Key 最近一次成功使用的时间（试译 / 实际翻译都会更新）。
// 失败不影响主流程，调用方忽略错误即可。
func (r *Repo) TouchTranslationKey(ctx context.Context, id string) error {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE translation_api_key SET last_used_at = now() WHERE id = $1`, id)
	return err
}

// CountTranslationKeys 统计翻译 Key 数（tenantID 为空串时统计全部，供平台总览）。
func (r *Repo) CountTranslationKeys(ctx context.Context, tenantID string) (int, error) {
	var n int
	if tenantID == "" {
		err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM translation_api_key`).Scan(&n)
		return n, err
	}
	err := r.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM translation_api_key WHERE tenant_id = $1`, tenantID).Scan(&n)
	return n, err
}

// scanKey 把一行翻译 Key 记录扫描成 domain.TranslationKey（仅脱敏，不含明文 key）。
func scanKey(sc rowScanner) (*domain.TranslationKey, error) {
	var k domain.TranslationKey
	if err := sc.Scan(&k.ID, &k.TenantID, &k.Name, &k.Provider, &k.KeyMasked,
		&k.Mode, &k.Status, &k.DailyLimit, &k.LastUsedAt, &k.CreatedBy, &k.CreatedAt, &k.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if k.LastUsedAt != nil && *k.LastUsedAt == 0 {
		k.LastUsedAt = nil
	}
	return &k, nil
}

/* ── 管理审计 ──────────────────────────────────────────── */

// LogAudit 写一条管理审计。
// ★ detail 里绝不出现 Key 明文、token 等秘密（调用方负责，这里再兜一层过滤不了语义）。
func (r *Repo) LogAudit(ctx context.Context, tenantID, actorID, actorRole, action, target, detail string) {
	_, _ = r.DB.ExecContext(ctx,
		`INSERT INTO admin_audit(tenant_id, actor_id, actor_role, action, target, detail)
		 VALUES ($1,$2,$3,$4,$5,$6)`, tenantID, actorID, actorRole, action, target, detail)
}

// AuditEntry 是一条审计记录（后台「操作日志」页展示）。
type AuditEntry struct {
	ID        int64  `json:"id"`
	TenantID  string `json:"tenant_id"`
	ActorID   string `json:"actor_id"`
	ActorRole string `json:"actor_role"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	CreatedAt int64  `json:"created_at"`
}

// ListAudit 列出最近的审计记录（默认 200 条，新的在前）。
func (r *Repo) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, tenant_id, actor_id, actor_role, action, target, detail,
		        extract(epoch FROM created_at)::bigint
		 FROM admin_audit ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.TenantID, &a.ActorID, &a.ActorRole,
			&a.Action, &a.Target, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
