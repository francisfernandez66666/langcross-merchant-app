package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

const storeCols = `id, account_id, name, COALESCE(addr,''), geo_lat, geo_lng,
	COALESCE(wifi_ssid,''), COALESCE(wifi_pwd,''), COALESCE(business_hours,''),
	coupon_enabled, service_tier, order_mode, slug, category_names, created_at`

// CreateStore 新建门店。slug 冲突时自动重试。
func (r *Repo) CreateStore(ctx context.Context, accountID, name, slug string) (*domain.Store, error) {
	for attempt := 0; attempt < 5; attempt++ {
		row := r.DB.QueryRowContext(ctx,
			`INSERT INTO store(id, account_id, name, slug)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (slug) DO NOTHING
			 RETURNING `+storeCols,
			idgen.New(), accountID, name, slug)

		s, err := scanStore(row)
		if err == nil {
			return s, nil
		}
		if errors.Is(err, ErrNotFound) { // slug 撞了，换一个再来
			slug = idgen.Slug()
			continue
		}
		return nil, fmt.Errorf("create store: %w", err)
	}
	return nil, fmt.Errorf("create store: slug collision retries exhausted")
}

// GetStoreBySlug 按 slug 查门店（客人端入口）。
func (r *Repo) GetStoreBySlug(ctx context.Context, slug string) (*domain.Store, error) {
	return scanStore(r.DB.QueryRowContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE slug = $1`, slug))
}

// GetStoreByID 按 ID 查门店。
func (r *Repo) GetStoreByID(ctx context.Context, id string) (*domain.Store, error) {
	return scanStore(r.DB.QueryRowContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE id = $1`, id))
}

// ListStoresByAccount 列出账号下所有门店。
func (r *Repo) ListStoresByAccount(ctx context.Context, accountID string) ([]domain.Store, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE account_id = $1 ORDER BY created_at`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Store{}
	for rows.Next() {
		s, err := scanStore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// StorePatch 是门店可更新字段（nil 表示不改）。
type StorePatch struct {
	Name          *string
	Addr          *string
	WifiSSID      *string
	WifiPwd       *string
	BusinessHours *string
	CouponEnabled *bool
	ServiceTier   *string
	OrderMode     *string
}

// UpdateStore 局部更新门店。
func (r *Repo) UpdateStore(ctx context.Context, id string, p StorePatch) (*domain.Store, error) {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE store SET
		   name           = COALESCE($2, name),
		   addr           = COALESCE($3, addr),
		   wifi_ssid      = COALESCE($4, wifi_ssid),
		   wifi_pwd       = COALESCE($5, wifi_pwd),
		   business_hours = COALESCE($6, business_hours),
		   coupon_enabled = COALESCE($7, coupon_enabled),
		   service_tier   = COALESCE($8, service_tier),
		   order_mode     = COALESCE($9, order_mode)
		 WHERE id = $1`,
		id, p.Name, p.Addr, p.WifiSSID, p.WifiPwd, p.BusinessHours,
		p.CouponEnabled, p.ServiceTier, p.OrderMode)
	if err != nil {
		return nil, fmt.Errorf("update store: %w", err)
	}
	return r.GetStoreByID(ctx, id)
}

// UpdateStoreCategoryNames 整体写入门店的分类名多语种表（{lang: {原文: 译文}}）。
func (r *Repo) UpdateStoreCategoryNames(ctx context.Context, id string, names map[string]map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	raw, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("marshal category names: %w", err)
	}
	if _, err := r.DB.ExecContext(ctx,
		`UPDATE store SET category_names = $2 WHERE id = $1`, id, raw); err != nil {
		return fmt.Errorf("update category names: %w", err)
	}
	return nil
}

func scanStore(sc rowScanner) (*domain.Store, error) {
	var (
		s       domain.Store
		catJSON []byte
	)
	if err := sc.Scan(&s.ID, &s.AccountID, &s.Name, &s.Addr, &s.GeoLat, &s.GeoLng,
		&s.WifiSSID, &s.WifiPwd, &s.BusinessHours,
		&s.CouponEnabled, &s.ServiceTier, &s.OrderMode, &s.Slug, &catJSON, &s.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(catJSON) > 0 {
		_ = json.Unmarshal(catJSON, &s.CategoryNames)
	}
	return &s, nil
}
