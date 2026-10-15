package repo

import (
	"context"
	"fmt"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// CreateRating 落一条三维度评价。
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
type RatingSummary struct {
	Count        int     `json:"count"`
	AvgDish      float64 `json:"avg_dish"`
	AvgService   float64 `json:"avg_service"`
	AvgReturn    float64 `json:"avg_return"`
	LowRate      float64 `json:"low_rate"`      // ≤2 分占比
	WouldReturn  float64 `json:"would_return"`  // 第③问 ≥4 占比（复购意愿）
}

// RatingSummaryOf 计算某店评价聚合。
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
