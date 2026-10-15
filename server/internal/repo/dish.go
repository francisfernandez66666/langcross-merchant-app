// Package repo 的数据访问实现之一：菜品（dish）。
//
// 菜品是「多语化后的成品」：name_zh 永远是原文真值，其它语种统一放在
// names_json（JSONB，形如 {"en":"...","ja":"..."}）。翻译流程是「整体替换」——
// 确认原文后由上层重新生成整张菜单并调用 ReplaceDishes，故本包没有逐条增删改。
// 唯一允许单独改动的动态字段是售罄 / 每日剩余（见 UpdateDishStatus）。
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// dishCols 是 dish 表的固定列清单，供各查询 SELECT 复用，避免列序与 Scan 不一致。
// 可空列用 COALESCE(...,”) 折叠成空串；names_json / attrs_json 保持 JSONB 原样读回。
const dishCols = `id, store_id, name_zh, COALESCE(name_en,''), names_json, COALESCE(description,''),
	price, COALESCE(category,''), attrs_json, sort_order, sold_out, remaining`

// ReplaceDishes 用给定清单整体替换某店菜品（单事务，先清后插）。
//
// 为什么先清后插：菜单是一次性翻译的成品，整体覆盖最简单也最不容易出现脏数据；
// 放在同一事务里，保证「清空」与「写入」要么都成功要么都回滚，中途失败不会留下
// 空菜单。sort_order 直接取切片下标 i，即「传入顺序 = 客人端展示顺序」。
// 传入项的 ID 为空时自动生成，非空则沿用（便于保留既有菜品 ID）。
// 空字符串用 NULLIF 转成 NULL，与可空列语义保持一致。
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
//
// 排序键为 (sort_order, name_zh)：前者是商家在菜单里排定的顺序，后者是兜底，
// 保证同序菜品有稳定次序。name_zh 恒为原文真值，多语种名在 Names 字段里。
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
//
// WHERE 同时带 id 与 store_id：即便调用方传了别店的 dishID 也改不动，天然防越权。
// 没有命中任何行时返回 ErrNotFound（菜品不存在或不属于该店），调用方据此报错。
// remaining 为 nil 表示不限制每日份数（列置 NULL）。
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

// rowScanner 抽象出 *sql.Row 与 *sql.Rows 的公共方法 Scan。
//
// 有了它，scanDish / scanStore / scanCouponRow 等辅助函数既能接单行查询结果，
// 也能接遍历结果集，避免为两种场景各写一份映射逻辑。定义在本文件，包内共用。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanDish 把单行结果映射成 domain.Dish。
//
// price / remaining 是可为 NULL 的列，用 sql.Null* 接收后再按 Valid 决定是否
// 赋成指针，从而在领域对象里表达「未设置」而非「零值」。
// names_json / attrs_json 反序列化失败按留空处理，不阻断读取（展示可降级）。
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
