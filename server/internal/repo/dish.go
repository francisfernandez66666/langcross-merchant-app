package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

const dishCols = `id, store_id, name_zh, COALESCE(name_en,''), names_json, COALESCE(description,''),
	price, COALESCE(category,''), attrs_json, sort_order, sold_out, remaining`

// ReplaceDishes 用给定清单整体替换某店菜品（单事务，先清后插）。
func (r *Repo) ReplaceDishes(ctx context.Context, storeID string, dishes []domain.Dish) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM dish WHERE store_id = $1`, storeID); err != nil {
		return fmt.Errorf("clear dishes: %w", err)
	}
	for i, d := range dishes {
		attrs := []byte("{}")
		if len(d.Attrs) > 0 {
			if attrs, err = json.Marshal(d.Attrs); err != nil {
				return fmt.Errorf("marshal attrs: %w", err)
			}
		}
		names := []byte("{}")
		if len(d.Names) > 0 {
			if names, err = json.Marshal(d.Names); err != nil {
				return fmt.Errorf("marshal names: %w", err)
			}
		}
		id := d.ID
		if id == "" {
			id = idgen.New()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dish(id, store_id, name_zh, name_en, names_json, description, price, category,
			                  attrs_json, sort_order, sold_out, remaining)
			 VALUES ($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),$7,NULLIF($8,''),$9,$10,$11,$12)`,
			id, storeID, d.NameZh, d.NameEn, names, d.Description, d.Price, d.Category,
			attrs, i, d.SoldOut, d.Remaining); err != nil {
			return fmt.Errorf("insert dish %q: %w", d.NameZh, err)
		}
	}
	return tx.Commit()
}

// ListDishes 按排序列出某店全部菜品。
func (r *Repo) ListDishes(ctx context.Context, storeID string) ([]domain.Dish, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+dishCols+` FROM dish WHERE store_id = $1 ORDER BY sort_order, name_zh`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Dish{}
	for rows.Next() {
		d, err := scanDish(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// UpdateDishStatus 更新单道菜的售罄 / 每日剩余（唯一动态字段）。
func (r *Repo) UpdateDishStatus(ctx context.Context, storeID, dishID string, soldOut bool, remaining *int) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE dish SET sold_out = $3, remaining = $4, updated_at = now()
		 WHERE id = $1 AND store_id = $2`, dishID, storeID, soldOut, remaining)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDish(sc rowScanner) (*domain.Dish, error) {
	var (
		d      domain.Dish
		price  sql.NullFloat64
		names  []byte
		attrs  []byte
		remain sql.NullInt64
	)
	if err := sc.Scan(&d.ID, &d.StoreID, &d.NameZh, &d.NameEn, &names, &d.Description,
		&price, &d.Category, &attrs, &d.SortOrder, &d.SoldOut, &remain); err != nil {
		return nil, err
	}
	if price.Valid {
		d.Price = &price.Float64
	}
	if remain.Valid {
		v := int(remain.Int64)
		d.Remaining = &v
	}
	if len(names) > 0 {
		_ = json.Unmarshal(names, &d.Names)
	}
	if len(attrs) > 0 {
		_ = json.Unmarshal(attrs, &d.Attrs)
	}
	return &d, nil
}
