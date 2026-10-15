// Package repo 的数据访问实现之一：评价（rating）。
//
// 外国客人扫桌码看菜单后提交三维度评价（菜品 / 服务 / 会再来吗），提交成功即
// 自动领券（发券免费、不卡付费墙，见 handlers_public.go 与 coupon.go）。评价也是
// 经营洞察的数据来源，但口径只统计「用我们工具的外国客」，因此聚合只做趋势 /
// 占比 / 排序，不编造精确百分比（见 RatingSummary 的说明）。
package repo

import (
	"context"
	"fmt"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// CreateRating 落一条三维度评价。
//
// ID 在写入前生成；created_at 由数据库默认值给出并 RETURNING 回读，直接写回入参
// 结构体（因此调用方传入的 *domain.Rating 会被就地补全 ID 与时间）。
// ref 记录入口来源（如 t07 / door / coupon），空串用 NULLIF 转 NULL；
// comment_native 是客人原话、comment_zh 是中文译文，二者都可能为空。
func (r *Repo) CreateRating(ctx context.Context, rt *domain.Rating) error {
	rt.ID = idgen.New()
	return r.DB.QueryRowContext(ctx,
		`INSERT INTO rating(id, store_id, ref, score_dish, score_service, score_return,
		                     comment_native, comment_zh)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,NULLIF($7,''),NULLIF($8,''))
		 RETURNING created_at`,
		rt.ID, rt.StoreID, rt.Ref, rt.ScoreDish, rt.ScoreService, rt.ScoreReturn,
		rt.CommentNative, rt.CommentZh).Scan(&rt.CreatedAt)
}

// ListRatings 按时间倒序列出评价。
//
// limit 做夹逼：<=0 或 >200 一律回落到 50，避免前端传 0 / 超大值导致全表扫描。
// 结果按 created_at 倒序，最新的评价排在最前，符合商家查看习惯。
func (r *Repo) ListRatings(ctx context.Context, storeID string, limit int) ([]domain.Rating, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, store_id, COALESCE(ref,''), score_dish, score_service, score_return,
		        COALESCE(comment_native,''), COALESCE(comment_zh,''), created_at
		 FROM rating WHERE store_id = $1 ORDER BY created_at DESC LIMIT $2`, storeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Rating{}
	for rows.Next() {
		var rt domain.Rating
		if err := rows.Scan(&rt.ID, &rt.StoreID, &rt.Ref, &rt.ScoreDish, &rt.ScoreService,
			&rt.ScoreReturn, &rt.CommentNative, &rt.CommentZh, &rt.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

// RatingSummary 是经营洞察用的聚合口径（只做趋势·占比·排序，不编精确百分比）。
//
// 各字段都是「可对外解释」的稳健指标：均值与占比都在样本足够时才有意义，
// 故界面应据此呈现趋势而非绝对结论。LowRate 是任一维度 ≤2 分的占比（差评面），
// WouldReturn 是第③问 ≥4 分的占比（复购意愿），二者是最关键的两个经营信号。
type RatingSummary struct {
	Count       int     `json:"count"`
	AvgDish     float64 `json:"avg_dish"`
	AvgService  float64 `json:"avg_service"`
	AvgReturn   float64 `json:"avg_return"`
	LowRate     float64 `json:"low_rate"`     // ≤2 分占比
	WouldReturn float64 `json:"would_return"` // 第③问 ≥4 占比（复购意愿）
}

// RatingSummaryOf 计算某店评价聚合。
//
// 用单条 SQL 一次算出计数、三维均值、差评数与高复购数，避免多次往返数据库。
// 所有 avg/sum 都套 COALESCE(...,0)：门店零评价时聚合函数返回 NULL，折叠成 0
// 才能安全 Scan。占比在 Go 侧按 Count 归一（Count 为 0 时保持 0，避免除零）。
func (r *Repo) RatingSummaryOf(ctx context.Context, storeID string) (*RatingSummary, error) {
	var (
		s        RatingSummary
		lowCount int
		hiReturn int
	)
	err := r.DB.QueryRowContext(ctx,
		`SELECT count(*),
		        COALESCE(avg(score_dish),0), COALESCE(avg(score_service),0), COALESCE(avg(score_return),0),
		        COALESCE(sum(CASE WHEN score_dish <= 2 OR score_service <= 2 OR score_return <= 2 THEN 1 ELSE 0 END),0),
		        COALESCE(sum(CASE WHEN score_return >= 4 THEN 1 ELSE 0 END),0)
		 FROM rating WHERE store_id = $1`, storeID).
		Scan(&s.Count, &s.AvgDish, &s.AvgService, &s.AvgReturn, &lowCount, &hiReturn)
	if err != nil {
		return nil, fmt.Errorf("rating summary: %w", err)
	}
	if s.Count > 0 {
		s.LowRate = float64(lowCount) / float64(s.Count)
		s.WouldReturn = float64(hiReturn) / float64(s.Count)
	}
	return &s, nil
}
