package repo

import (
	"context"
	"encoding/json"
)

// TrackEvent 落一条埋点（高写入，不阻塞主链路；失败不致命）。
func (r *Repo) TrackEvent(ctx context.Context, storeID, name string, payload map[string]any) error {
	var raw []byte
	if len(payload) > 0 {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = b
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO event(store_id, name, payload) VALUES (NULLIF($1,''), $2, $3)`,
		storeID, name, raw)
	return err
}

// CountEvents 统计某店某事件次数（看板用）。
func (r *Repo) CountEvents(ctx context.Context, storeID, name string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM event WHERE store_id = $1 AND name = $2`, storeID, name).Scan(&n)
	return n, err
}
