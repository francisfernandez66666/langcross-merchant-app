// Package repo 的数据访问实现之一：商家账号（account）与手机验证码（otp）。
//
// 账号是注册链路的主体：商家用手机号注册（phone 全局唯一），一个账号可开多店
// （见 store.go）。验证码采用「覆盖式写入」——同一手机号在 otp 表只有一行，
// 重新发送即覆盖 code 与过期时间；登录消费后立即删除，防止一码多用。
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// ErrNotFound 表示目标记录不存在。
//
// 这是包级哨兵错误：所有「查不到」的路径都归一到这里，调用方用
// errors.Is(err, repo.ErrNotFound) 判断并映射成 404 / 业务提示。因此不要
// 用 fmt.Errorf 把它转成别的错误类型；若要附加语境请用 %w 包裹以保留哨兵。
var ErrNotFound = errors.New("not found")

// ErrConflict 表示业务冲突（如删除还有子组织的组织、改已被占用的编码）。
// 与 ErrNotFound 分开：404 是「没有」，409 是「有但不允许这么干」。
var ErrConflict = errors.New("conflict")

// CreateAccount 新建账号（手机号唯一）。
//
// ID 由 idgen 生成（32 位十六进制随机值），created_at 交给数据库默认值并用
// RETURNING 回读，避免应用服务器与数据库时钟不一致。若 phone 命中唯一约束，
// 会返回底层 pq 错误（而非 ErrNotFound），调用方需自行识别「已注册」场景。
//
// tenantID 为空串表示平台级账号（超管）；role 见 domain.Role* 常量。
func (r *Repo) CreateAccount(ctx context.Context, tenantID, orgID, role, phone, displayName string) (*domain.Account, error) {
	a := &domain.Account{ID: idgen.New(), Phone: phone, TenantID: tenantID, OrgID: orgID,
		Role: role, DisplayName: displayName, Status: "active"}
	err := r.DB.QueryRowContext(ctx,
		`INSERT INTO account(id, tenant_id, org_id, role, status, display_name, phone)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING created_at`, a.ID, a.TenantID, a.OrgID, a.Role, a.Status, a.DisplayName, a.Phone).Scan(&a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	return a, nil
}

// accountCols 是 account 表的固定列清单。
const accountCols = `id, phone, tenant_id, role, COALESCE(org_id,''), status, COALESCE(display_name,''), created_at`

// GetAccountByPhone 按手机号查账号；不存在返回 ErrNotFound。
// 用于登录 / 注册前的存在性判断（决定是新建还是复用）。
func (r *Repo) GetAccountByPhone(ctx context.Context, phone string) (*domain.Account, error) {
	return r.scanAccount(r.DB.QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM account WHERE phone = $1`, phone))
}

// GetAccountByID 按 ID 查账号。
// 用于已登录请求：从会话里取到 account_id 后回捞主体信息（含角色与租户）。
func (r *Repo) GetAccountByID(ctx context.Context, id string) (*domain.Account, error) {
	return r.scanAccount(r.DB.QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM account WHERE id = $1`, id))
}

// ListAccountsByTenant 列出某租户下的账号（后台成员管理）。
// 按角色等级倒序 + 创建时间升序：重要的账号排前面，同等级先来的排前面。
func (r *Repo) ListAccountsByTenant(ctx context.Context, tenantID string) ([]domain.Account, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+accountCols+` FROM account WHERE tenant_id = $1
		 ORDER BY CASE role WHEN 'super_admin' THEN 4 WHEN 'tenant_admin' THEN 3
		                   WHEN 'manager' THEN 2 ELSE 1 END DESC, created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Account{}
	for rows.Next() {
		a, err := r.scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// SetAccountOrg 调整账号的组织归属（品牌区域管理员的分组依据）。
// 影响 0 行（账号不存在）时返回 ErrNotFound。
func (r *Repo) SetAccountOrg(ctx context.Context, accountID, orgID string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE account SET org_id = NULLIF($2,'') WHERE id = $1`, accountID, orgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAccountRole 修改账号角色（超管 / 租户管理员提权或降权）。
// 影响 0 行（账号不存在）时返回 ErrNotFound。
func (r *Repo) UpdateAccountRole(ctx context.Context, accountID, role string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE account SET role = $2 WHERE id = $1`, accountID, role)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateAccountStatus 启用 / 停用账号（数据保留，只是不能登录）。
// 影响 0 行时返回 ErrNotFound。
func (r *Repo) UpdateAccountStatus(ctx context.Context, accountID, status string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE account SET status = $2 WHERE id = $1`, accountID, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAccounts 统计账号总数（平台总览用；tenantID 为空串时统计全平台）。
func (r *Repo) CountAccountsByTenant(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM account WHERE tenant_id = $1`, tenantID).Scan(&n)
	return n, err
}

// CountAccounts 统计账号总数（平台总览用）。
func (r *Repo) CountAccounts(ctx context.Context) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM account`).Scan(&n)
	return n, err
}

// scanAccount 把单行结果映射成 domain.Account，并把 sql.ErrNoRows 归一为 ErrNotFound。
// 私有辅助：GetAccountByPhone / GetAccountByID / ListAccountsByTenant 共用，保证列序一致。
// 形参用 rowScanner 接口（*sql.Row 与 *sql.Rows 都能传），两种查询路径复用同一份映射。
func (r *Repo) scanAccount(sc rowScanner) (*domain.Account, error) {
	var a domain.Account
	if err := sc.Scan(&a.ID, &a.Phone, &a.TenantID, &a.Role, &a.OrgID, &a.Status, &a.DisplayName, &a.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// ── 验证码 ──────────────────────────────────────────────────────

// UpsertOTP 写入/覆盖某手机号的验证码。
//
// 用 ON CONFLICT (phone) 做 upsert：同一手机号只保留最新一条，重发即覆盖，
// 并把 created_at 刷新为当前时间。expiresUnix 为 Unix 秒，落库前用
// to_timestamp 转成 timestamptz，避免调用方关心时区。
func (r *Repo) UpsertOTP(ctx context.Context, phone, code string, expiresUnix int64) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO otp(phone, code, expires_at) VALUES ($1, $2, to_timestamp($3))
		 ON CONFLICT (phone) DO UPDATE SET code = EXCLUDED.code, expires_at = EXCLUDED.expires_at,
		   created_at = now()`, phone, code, expiresUnix)
	return err
}

// GetOTP 读取验证码与其过期时间（Unix 秒）。
//
// 过期时间用 extract(epoch ...)::bigint 直接在数据库侧转成 Unix 秒返回，
// 让「是否过期」的判断留给调用方（便于统一用服务器时钟做比较）。
// 不存在时返回 ErrNotFound。
func (r *Repo) GetOTP(ctx context.Context, phone string) (code string, expiresUnix int64, err error) {
	err = r.DB.QueryRowContext(ctx,
		`SELECT code, extract(epoch FROM expires_at)::bigint FROM otp WHERE phone = $1`, phone).
		Scan(&code, &expiresUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	return code, expiresUnix, err
}

// DeleteOTP 消费后清除验证码。
//
// 登录/注册成功后立即调用，保证验证码一次性；删除不存在的记录不视为错误。
func (r *Repo) DeleteOTP(ctx context.Context, phone string) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM otp WHERE phone = $1`, phone)
	return err
}
