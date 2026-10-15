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

// CreateDraft 写入一份原文确认稿（P2 上传 / OCR 之后）。
func (r *Repo) CreateDraft(ctx context.Context, storeID, lang string, items []domain.DraftItem) (*domain.SourceDraft, error) {
	payload, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	d := &domain.SourceDraft{ID: idgen.New(), StoreID: storeID, Lang: lang, Items: items}
	if err := r.DB.QueryRowContext(ctx,
		`INSERT INTO source_draft(id, store_id, lang, payload_json) VALUES ($1,$2,$3,$4)
		 RETURNING created_at`, d.ID, d.StoreID, d.Lang, payload).Scan(&d.CreatedAt); err != nil {
		return nil, fmt.Errorf("create draft: %w", err)
	}
	return d, nil
}

// GetLatestDraft 取某店最近一份确认稿。
func (r *Repo) GetLatestDraft(ctx context.Context, storeID string) (*domain.SourceDraft, error) {
	var (
		d       domain.SourceDraft
		payload []byte
		conf    sql.NullTime
	)
	err := r.DB.QueryRowContext(ctx,
		`SELECT id, store_id, lang, payload_json, confirmed_at, created_at
		 FROM source_draft WHERE store_id = $1 ORDER BY created_at DESC LIMIT 1`, storeID).
		Scan(&d.ID, &d.StoreID, &d.Lang, &payload, &conf, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(payload, &d.Items); err != nil {
		return nil, fmt.Errorf("unmarshal draft payload: %w", err)
	}
	if conf.Valid {
		d.ConfirmedAt = &conf.Time
	}
	return &d, nil
}

// ConfirmDraft 标记确认稿已确认（唯一真值生效，之后才允许翻译）。
func (r *Repo) ConfirmDraft(ctx context.Context, storeID, draftID string) error {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE source_draft SET confirmed_at = now() WHERE id = $1 AND store_id = $2`,
		draftID, storeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
