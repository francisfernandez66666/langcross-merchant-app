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
	"errors"
	"fmt"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// dishCols 是 dish 表的固定列清单，供各查询 SELECT 复用，避免列序与 Scan 不一致。
// 可空列用 COALESCE(...,”) 折叠成空串；names_json / attrs_json 保持 JSONB 原样读回。
// image_url 也在列清单里：客人端要拿到图片 URL，凡是查菜的地方都得带上它。
const dishCols = `id, store_id, name_zh, COALESCE(name_en,''), names_json, COALESCE(description,''),
	price, COALESCE(category,''), attrs_json, sort_order, sold_out, remaining, COALESCE(image_url,'')`

// ReplaceDishes 用给定清单整体替换某店菜品（单事务，先清后插）。
//
// 为什么先清后插：菜单是一次性翻译的成品，整体覆盖最简单也最不容易出现脏数据；
// 放在同一事务里，保证「清空」与「写入」要么都成功要么都回滚，中途失败不会留下
// 空菜单。sort_order 直接取切片下标 i，即「传入顺序 = 客人端展示顺序」。
// 传入项的 ID 为空时自动生成，非空则沿用（便于保留既有菜品 ID）。
// 空字符串用 NULLIF 转成 NULL，与可空列语义保持一致。
//
// ★ 图片必须在重翻后活下来：dish 行是先 DELETE 再 INSERT 新建的，图片列存在
// dish 行上，若不设法保留，商家每次「补充语种 / 重新翻译」都会丢掉全部菜品图。
// 因此事务开启后先把旧菜品按 name_zh（稳定身份）的图片地址快照进内存，
// 插入新行时按 name_zh 回填。name_zh 相同的多道菜取最后一张图的地址。
func (r *Repo) ReplaceDishes(ctx context.Context, storeID string, dishes []domain.Dish) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 1) 快照旧图片（按 name_zh 索引），随后插入新行时回填。
	legacy, err := snapshotImageURLs(ctx, tx, storeID)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM dish WHERE store_id = $1`, storeID); err != nil {
		return fmt.Errorf("clear dishes: %w", err)
	}
	for i, d := range dishes {
		if d.ImageURL == "" {
			d.ImageURL = legacy[d.NameZh] // 回填旧图；没有则保持空串
		}
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
		// ★ 各列默认值语义要跟迁移对齐，不能一律 NULLIF：
		//   - name_en / description / price / category 是可空列 → NULLIF('', NULL) 是对的，
		//     避免把「没填」存成空串，前端靠 omitempty 区分；
		//   - image_url 是 NOT NULL DEFAULT ''（迁移 0004）→ 必须原样传空串，
		//     NULLIF 会产出 NULL 直接违反约束。这类错只在运行时炸，
		//     且报错信息与根因离得很远，所以在这里显式说明。
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dish(id, store_id, name_zh, name_en, names_json, description, price, category,
			                  attrs_json, sort_order, sold_out, remaining, image_url)
			 VALUES ($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),$7,NULLIF($8,''),$9,$10,$11,$12,$13)`,
			id, storeID, d.NameZh, d.NameEn, names, d.Description, d.Price, d.Category,
			attrs, i, d.SoldOut, d.Remaining, d.ImageURL); err != nil {
			return fmt.Errorf("insert dish %q: %w", d.NameZh, err)
		}
	}
	return tx.Commit()
}

// snapshotImageURLs 在某事务内读取该店现有菜品的「name_zh → image_url」映射。
// 只取 image_url 非空的行；空图不进映射，避免用空串覆盖掉本来就没图的事实。
func snapshotImageURLs(ctx context.Context, tx *sql.Tx, storeID string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT name_zh, COALESCE(image_url,'') FROM dish WHERE store_id = $1`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, url string
		if err := rows.Scan(&name, &url); err != nil {
			return nil, err
		}
		if url != "" {
			out[name] = url
		}
	}
	return out, rows.Err()
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

// GetDishByID 按 ID 取菜品（必须属于该门店）。
//
// 用于「加菜请求」的校验：若拿别家门店的菜品 ID 也能给本店塞请求，
// 服务员就会看到一个本店根本没有的菜，纯噪音。
// 不存在或不属于该店都返回 ErrNotFound，调用方无需区分两种原因。
func (r *Repo) GetDishByID(ctx context.Context, storeID, dishID string) (*domain.Dish, error) {
	return scanDish(r.DB.QueryRowContext(ctx,
		`SELECT `+dishCols+` FROM dish WHERE id = $1 AND store_id = $2`, dishID, storeID))
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

// SetDishImage 设置 / 清空单道菜的图片 URL（空串 = 清空）。
//
// WHERE 同样带 store_id：即便调用方传了别店的 dishID 也改不动，天然防越权。
// 返回 ErrNotFound 表示菜品不存在或不属于该店，接口层据此报 404。
//
// ★ image_url 是 NOT NULL DEFAULT ”（迁移 0004），因此「清空图片」是写空串、不是写 NULL。
// 早先这里写成 NULLIF($3,”)，删除图片时会产出 NULL 直接违反约束——
// 与 ReplaceDishes 曾经踩的是同一个坑，故在两处都留下说明。
func (r *Repo) SetDishImage(ctx context.Context, storeID, dishID, imageURL string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE dish SET image_url = $3, updated_at = now() WHERE id = $1 AND store_id = $2`,
		dishID, storeID, imageURL)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDishImage 读取单道菜的图片 URL（空串表示无图）。
//
// 只服务「换图时删旧文件」这一件事：上传新图成功后要先把旧文件删掉，
// 否则磁盘上会留下一堆没人引用的孤儿图片。WHERE 同样带 store_id 防越权，
// 菜品不存在或不属于该店时返回 ErrNotFound。
func (r *Repo) GetDishImage(ctx context.Context, storeID, dishID string) (string, error) {
	var url string
	err := r.DB.QueryRowContext(ctx,
		`SELECT COALESCE(image_url,'') FROM dish WHERE id = $1 AND store_id = $2`, dishID, storeID).
		Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return url, err
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
		&price, &d.Category, &attrs, &d.SortOrder, &d.SoldOut, &remain, &d.ImageURL); err != nil {
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
