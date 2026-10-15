package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// DefaultCouponContent 是注册后自动配好的标准券（小额、可截图、14 天有效）。
func DefaultCouponContent() map[string]any {
	return map[string]any{
		"type":     "gift", // 赠饮 / 小食
		"zh":       "凭券到店赠小食一份",
		"en":       "Free side dish on your next visit",
		"valid_days": 14,
	}
}

// CreateTemplate 新建券模板（注册后自动调用；发券免费）。
func (r *Repo) CreateTemplate(ctx context.Context, storeID string, content map[string]any, validDays int) (*domain.CouponTemplate, error) {
	if content == nil {
		content = DefaultCouponContent()
	}
	if validDays <= 0 {
		validDays = 14
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	t := &domain.CouponTemplate{
		ID: idgen.New(), StoreID: storeID, Content: content, ValidDays: validDays, Enabled: true,
	}
	if err := r.DB.QueryRowContext(ctx,
		`INSERT INTO coupon_template(id, store_id, content_json, valid_days)
		 VALUES ($1,$2,$3,$4) RETURNING created_at`,
		t.ID, t.StoreID, raw, t.ValidDays).Scan(&t.CreatedAt); err != nil {
		return nil, fmt.Errorf("create coupon template: %w", err)
	}
	return t, nil
}

// GetTemplateByStore 取某店最新券模板。
func (r *Repo) GetTemplateByStore(ctx context.Context, storeID string) (*domain.CouponTemplate, error) {
	var (
		t   domain.CouponTemplate
		raw []byte
	)
	err := r.DB.QueryRowContext(ctx,
		`SELECT id, store_id, content_json, valid_days, enabled, created_at
		 FROM coupon_template WHERE store_id = $1 ORDER BY created_at DESC LIMIT 1`, storeID).
		Scan(&t.ID, &t.StoreID, &raw, &t.ValidDays, &t.Enabled, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &t.Content); err != nil {
		return nil, fmt.Errorf("unmarshal coupon content: %w", err)
	}
	return &t, nil
}

// IssueCoupon 基于模板发一张券（评价领券时调用）。
func (r *Repo) IssueCoupon(ctx context.Context, storeID, templateID, lang string) (*domain.Coupon, error) {
	tpl, err := r.GetTemplateByStore(ctx, storeID)
	if err != nil {
		return nil, err
	}
	if templateID != "" {
		tpl.ID = templateID
	}
	c := &domain.Coupon{
		ID:         idgen.New(),
		TemplateID: tpl.ID,
		StoreID:    storeID,
		Code:       idgen.Code(8),
		Lang:       lang,
		ExpireAt:   time.Now().AddDate(0, 0, tpl.ValidDays),
		Status:     "issued",
	}
	if err := r.DB.QueryRowContext(ctx,
		`INSERT INTO coupon(id, template_id, store_id, code, lang, expire_at)
		 VALUES ($1,$2,$3,$4,NULLIF($5,''),$6) RETURNING issued_at`,
		c.ID, c.TemplateID, c.StoreID, c.Code, c.Lang, c.ExpireAt).Scan(&c.IssuedAt); err != nil {
		return nil, fmt.Errorf("issue coupon: %w", err)
	}
	return c, nil
}

// GetCouponByCode 按券码查券（核销页用）。
func (r *Repo) GetCouponByCode(ctx context.Context, code string) (*domain.Coupon, error) {
	return r.scanCoupon(r.DB.QueryRowContext(ctx,
		`SELECT c.id, c.template_id, c.store_id, c.code, COALESCE(c.lang,''),
		        c.issued_at, c.expire_at, c.status, r.redeemed_at
		 FROM coupon c LEFT JOIN coupon_redeem r ON r.coupon_id = c.id
		 WHERE c.code = $1`, code))
}

// RedeemCoupon 核销：事务内校验状态与有效期，写核销记录并置状态。
func (r *Repo) RedeemCoupon(ctx context.Context, code, by string) (*domain.Coupon, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		c          domain.Coupon
		redeemedAt sql.NullTime
	)
	err = tx.QueryRowContext(ctx,
		`SELECT id, template_id, store_id, code, COALESCE(lang,''), issued_at, expire_at, status
		 FROM coupon WHERE code = $1 FOR UPDATE`, code).
		Scan(&c.ID, &c.TemplateID, &c.StoreID, &c.Code, &c.Lang, &c.IssuedAt, &c.ExpireAt, &c.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if c.Status == "redeemed" {
		return nil, fmt.Errorf("coupon already redeemed")
	}
	if time.Now().After(c.ExpireAt) {
		_, _ = tx.ExecContext(ctx, `UPDATE coupon SET status='expired' WHERE id=$1`, c.ID)
		return nil, fmt.Errorf("coupon expired")
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO coupon_redeem(id, coupon_id, redeemed_by) VALUES ($1,$2,NULLIF($3,''))`,
		idgen.New(), c.ID, by); err != nil {
		return nil, fmt.Errorf("insert redeem: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE coupon SET status='redeemed' WHERE id=$1`, c.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	now := time.Now()
	c.Status = "redeemed"
	c.RedeemedAt = &now
	_ = redeemedAt
	return &c, nil
}

// ListCoupons 列出某店券（核销台用）。
func (r *Repo) ListCoupons(ctx context.Context, storeID string, limit int) ([]domain.Coupon, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.DB.QueryContext(ctx,
		`SELECT c.id, c.template_id, c.store_id, c.code, COALESCE(c.lang,''),
		        c.issued_at, c.expire_at, c.status, r.redeemed_at
		 FROM coupon c LEFT JOIN coupon_redeem r ON r.coupon_id = c.id
		 WHERE c.store_id = $1 ORDER BY c.issued_at DESC LIMIT $2`, storeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Coupon{}
	for rows.Next() {
		c, err := scanCouponRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (r *Repo) scanCoupon(row *sql.Row) (*domain.Coupon, error) {
	var (
		c  domain.Coupon
		ra sql.NullTime
	)
	if err := row.Scan(&c.ID, &c.TemplateID, &c.StoreID, &c.Code, &c.Lang,
		&c.IssuedAt, &c.ExpireAt, &c.Status, &ra); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ra.Valid {
		c.RedeemedAt = &ra.Time
	}
	return &c, nil
}

func scanCouponRow(sc rowScanner) (*domain.Coupon, error) {
	var (
		c  domain.Coupon
		ra sql.NullTime
	)
	if err := sc.Scan(&c.ID, &c.TemplateID, &c.StoreID, &c.Code, &c.Lang,
		&c.IssuedAt, &c.ExpireAt, &c.Status, &ra); err != nil {
		return nil, err
	}
	if ra.Valid {
		c.RedeemedAt = &ra.Time
	}
	return &c, nil
}
