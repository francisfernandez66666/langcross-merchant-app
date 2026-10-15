// Package repo 的数据访问实现之一：埋点事件（event）。
//
// 埋点用于经营看板与转化漏斗（如 g_scan 扫码、t_rate_submit 提交评价、
// t_coupon_issue 发券等），特点是高写入、且不应拖慢主链路——因此 event 表
// 特意不设外键，写入失败也不致命（上层普遍用 `_ = TrackEvent(...)` 忽略错误）。
//
// 坑：正因为没有外键，删除门店时不会级联清理 event 里的历史埋点，需要显式清理，
// 否则会残留孤儿数据（store_id 指向已不存在的店）。
package repo

import (
	"context"
	"encoding/json"
)

// TrackEvent 落一条埋点（高写入，不阻塞主链路；失败不致命）。
//
// payload 为空时不写 JSON（存 NULL），避免写入无意义的空对象。storeID 为空串时
// 用 NULLIF 转 NULL，用于承载注册前等「尚无门店」的事件（如注册动作本身）。
// 调用方通常忽略返回错误，以免埋点故障影响主流程。
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
//
// 典型用法：CountEvents(storeID, "g_scan") 得扫码量、("t_rate_submit") 得评价量，
// 二者相除即得转化率。注意口径只覆盖「用了我们工具的外国客」。
func (r *Repo) CountEvents(ctx context.Context, storeID, name string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM event WHERE store_id = $1 AND name = $2`, storeID, name).Scan(&n)
	return n, err
}
