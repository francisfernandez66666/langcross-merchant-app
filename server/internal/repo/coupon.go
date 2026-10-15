// Package repo 的数据访问实现之一：复购券（coupon_template / coupon / coupon_redeem）。
//
// 券链路：商家注册时自动配一张标准模板（小额、可截图、14 天有效）→ 外国客人提交
// 评价后自动发一张券（发券免费，不卡付费墙）→ 店员在核销台输入券码核销。
// 券实例自带 store_id 归属；核销时必须校验「券属于本店」以防跨店核销，该校验由
// 上层（handlers_merchant.handleRedeem）完成——本包的 RedeemCoupon 只认券码、
// 不校验门店归属，调用方务必先比对 GetCouponByCode 结果里的 StoreID。
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
//
// 选「赠饮/小食」这类低门槛、易履约的权益：核销成本低、客人感知强，适合作为
// 冷启动的默认钩子。content 内嵌 zh/en 双语，客人端按自己语言展示。
func DefaultCouponContent() map[string]any {
	return map[string]any{
		"type":       "gift", // 赠饮 / 小食
		"zh":         "凭券到店赠小食一份",
		"en":         "Free side dish on your next visit",
		"valid_days": 14,
	}
}

// CreateTemplate 新建券模板（注册后自动调用；发券免费）。
//
// content 为空时回落到 DefaultCouponContent；validDays <=0 时回落到 14 天，
// 让调用方可以「无脑建模板」。enabled 默认 true（新店立即具备发券能力）。
// content 以 JSONB 存入 content_json，多语内容与有效期一并保留。
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
//
// 一个门店可能先后有多张模板，取 created_at 最新的一张作为当前生效模板。
// 不存在返回 ErrNotFound（此时门店无法发券，发券流程应据此降级）。
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
//
// 先按门店取当前模板拿有效期；若调用方显式传入 templateID 则覆盖模板 ID（保留
// 扩展点）。券码用 8 位纯数字（idgen.Code），便于店员人工念读 / 输入核销；
// 到期时间 = 现在 + 模板有效天数。lang 记录客人语言，空串存 NULL。
// 发券不做任何付费校验：评价即领券，是产品明确的「免费」设计。
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
//
// LEFT JOIN coupon_redeem 带出核销时间，已核销的券 RedeemedAt 非空。
// 关键：返回结果里的 StoreID 是核销前「券归属本店」校验的依据——调用方必须先
// 比对它与当前门店 ID，再调用 RedeemCoupon，否则会造成跨店核销。
// 券码不存在返回 ErrNotFound。
func (r *Repo) GetCouponByCode(ctx context.Context, code string) (*domain.Coupon, error) {
	return r.scanCoupon(r.DB.QueryRowContext(ctx,
		`SELECT c.id, c.template_id, c.store_id, c.code, COALESCE(c.lang,''),
		        c.issued_at, c.expire_at, c.status, r.redeemed_at
		 FROM coupon c LEFT JOIN coupon_redeem r ON r.coupon_id = c.id
		 WHERE c.code = $1`, code))
}

// RedeemCoupon 核销：事务内校验状态与有效期，写核销记录并置状态。
//
// 并发安全：SELECT ... FOR UPDATE 对券行加锁，避免同一张券被两个店员同时核销。
// 校验规则：已 redeemed 直接报错；已过期则顺手把状态置为 expired 再报错。
// 通过校验后先插 coupon_redeem 记录（redeemed_by 记操作者，空串存 NULL），
// 再把券状态改为 redeemed，整体提交。
//
// 注意：本方法只按券码操作，不校验券是否属于当前门店——「券必须属于本店」的
// 防跨店校验由上层在调用前用 GetCouponByCode 的 StoreID 完成，勿依赖本方法。
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
//
// limit 夹逼到 [1,500]，越界回落 100，防止一次拉取过多。按 issued_at 倒序，
// 最新发放的券排在最前，方便店员就近核对。已核销券的核销时间经 LEFT JOIN 带出。
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

// scanCoupon 把单行结果（*sql.Row）映射成 domain.Coupon，ErrNoRows 归一为 ErrNotFound。
// 与 scanCouponRow 列序相同，仅入参类型不同（单行 vs 结果集）。
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

// scanCouponRow 把结果集中的一行映射成 domain.Coupon。
// 核销时间可空，用 sql.NullTime 接收后按 Valid 决定是否赋指针。
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
