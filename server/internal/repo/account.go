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
var ErrNotFound = errors.New("not found")

// CreateAccount 新建账号（手机号唯一）。
func (r *Repo) CreateAccount(ctx context.Context, phone string) (*domain.Account, error) {
	a := &domain.Account{ID: idgen.New(), Phone: phone}
	err := r.DB.QueryRowContext(ctx,
		`INSERT INTO account(id, phone) VALUES ($1, $2)
		 RETURNING created_at`, a.ID, a.Phone).Scan(&a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	return a, nil
}

// GetAccountByPhone 按手机号查账号；不存在返回 ErrNotFound。
func (r *Repo) GetAccountByPhone(ctx context.Context, phone string) (*domain.Account, error) {
	return r.scanAccount(r.DB.QueryRowContext(ctx,
		`SELECT id, phone, created_at FROM account WHERE phone = $1`, phone))
}

// GetAccountByID 按 ID 查账号。
func (r *Repo) GetAccountByID(ctx context.Context, id string) (*domain.Account, error) {
	return r.scanAccount(r.DB.QueryRowContext(ctx,
		`SELECT id, phone, created_at FROM account WHERE id = $1`, id))
}

func (r *Repo) scanAccount(row *sql.Row) (*domain.Account, error) {
	var a domain.Account
	if err := row.Scan(&a.ID, &a.Phone, &a.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// ── 验证码 ──────────────────────────────────────────────────────

// UpsertOTP 写入/覆盖某手机号的验证码。
func (r *Repo) UpsertOTP(ctx context.Context, phone, code string, expiresUnix int64) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO otp(phone, code, expires_at) VALUES ($1, $2, to_timestamp($3))
		 ON CONFLICT (phone) DO UPDATE SET code = EXCLUDED.code, expires_at = EXCLUDED.expires_at,
		   created_at = now()`, phone, code, expiresUnix)
	return err
}

// GetOTP 读取验证码与其过期时间（Unix 秒）。
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
func (r *Repo) DeleteOTP(ctx context.Context, phone string) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM otp WHERE phone = $1`, phone)
	return err
}
