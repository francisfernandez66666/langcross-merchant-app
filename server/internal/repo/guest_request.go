package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// 本文件：客人服务请求的读写（服务铃 / 加水 / 加菜 / 买单）。
//
// 归属口径：写侧是公开接口（客人不登录），只能靠 store_id 圈定；
// 读侧走 httpapi 的 requireStore 校验（门店存在 + 当前账号有权访问）。
// 因此这里对「读」不再重复做账号判断，但**写**必须校验门店真实存在——
// 否则任何人都能往别人的门店里塞请求。

const guestReqCols = `id, store_id, COALESCE(table_ref,''), kind, COALESCE(payload,'{}'),
	COALESCE(lang,''), COALESCE(note,''), status,
	extract(epoch FROM created_at)::bigint`

// CreateGuestRequest 写入一条服务请求。
//
// ★ 门店存在性在这里校验：写侧是公开接口，没有登录态可依，
// 唯一的防线就是「门店得是真的」。表上没有外键（历史包袱），所以显式查一次。
func (r *Repo) CreateGuestRequest(ctx context.Context, req *domain.GuestRequest) error {
	if _, err := r.GetStoreByID(ctx, req.StoreID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	payload := req.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// 先生成 id 并回写到 req.ID：否则调用方（handler）拿不到刚插入的请求 ID，
	// 响应里的 request_id 会是空串，前端/排障都无从引用这条记录。
	id := idgen.New()
	req.ID = id
	_, err = r.DB.ExecContext(ctx,
		`INSERT INTO guest_request (id, store_id, table_ref, kind, payload, lang, note)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		id, req.StoreID, req.TableRef, req.Kind, blob, req.Lang, req.Note)
	return err
}

// ListGuestRequests 列出门店的服务请求（待办优先，新的在前）。
//
// status 传空串 = 只要待办（open）——这是商家待办页最常用的视图。
// 之所以不默认给全部：已完成的历史请求会淹没有待处理的，
// 而服务员真正需要「一眼看到有没有人叫我」。
func (r *Repo) ListGuestRequests(ctx context.Context, storeID, status string, limit int) ([]domain.GuestRequest, error) {
	q := `SELECT ` + guestReqCols + ` FROM guest_request WHERE store_id = $1`
	args := []any{storeID}
	if status != "" {
		q += ` AND status = $2`
		args = append(args, status)
	} else {
		q += ` AND status = 'open'`
	}
	q += ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.GuestRequest{}
	for rows.Next() {
		g, err := scanGuestRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// CountOpenGuestRequests 统计待办数（商家端角标）。
func (r *Repo) CountOpenGuestRequests(ctx context.Context, storeID string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM guest_request WHERE store_id = $1 AND status = 'open'`,
		storeID).Scan(&n)
	return n, err
}

// SetGuestRequestStatus 更新请求状态（完成 / 取消）。
// 只允许操作本店请求：WHERE 里带 store_id，避免越权处理别家门店的请求。
func (r *Repo) SetGuestRequestStatus(ctx context.Context, storeID, id, status string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE guest_request SET status = $3, done_at = CASE WHEN $3 = 'done' THEN now() ELSE NULL END
		 WHERE id = $1 AND store_id = $2`, id, storeID, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListGuestRequestsWithDish 列请求并补上菜名（商家端待办页专用）。
//
// 批量补名而不是逐条查：待办列表可能一次有好几条加菜，
// N+1 查询在高峰期会放大成明显的卡顿。
func (r *Repo) ListGuestRequestsWithDish(ctx context.Context, storeID, status string, limit int) ([]domain.GuestRequest, error) {
	reqs, err := r.ListGuestRequests(ctx, storeID, status, limit)
	if err != nil {
		return nil, err
	}
	needName := false
	for _, g := range reqs {
		if g.Kind == domain.ReqAddDish {
			needName = true
			break
		}
	}
	if !needName {
		return reqs, nil
	}
	dishes, derr := r.ListDishes(ctx, storeID)
	if derr != nil {
		return reqs, nil // 菜名拿不到不阻断：请求本身才是待办主体
	}
	byID := make(map[string]string, len(dishes))
	for _, d := range dishes {
		byID[d.ID] = d.NameZh
	}
	for i := range reqs {
		if reqs[i].Kind != domain.ReqAddDish {
			continue
		}
		if id, _ := reqs[i].Payload["dish_id"].(string); id != "" {
			reqs[i].DishName = byID[id]
		}
	}
	return reqs, nil
}

// scanGuestRequest 把一行服务请求扫描成 domain.GuestRequest（payload 的 JSONB 解回 map）。
func scanGuestRequest(sc rowScanner) (*domain.GuestRequest, error) {
	var (
		g       domain.GuestRequest
		payload []byte
	)
	if err := sc.Scan(&g.ID, &g.StoreID, &g.TableRef, &g.Kind, &payload,
		&g.Lang, &g.Note, &g.Status, &g.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// payload 解析失败不阻断：这条请求的 kind 与状态才是商家最需要的信息，
	// 少了参数顶多少看一个菜名。与 dish 的 attrs 解析保持同一口径。
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &g.Payload)
	}
	return &g, nil
}
