// Package repo 的数据访问实现之一：原文确认稿（source_draft）。
//
// 这是整条翻译链的「唯一真值」入口：商家上传 / OCR 识别菜单后先落成一份草稿
// （items 为原始语言的菜品清单），必须由商家确认（confirmed_at 非空）之后才允许
// 翻译。「未确认不允许翻译」是硬规则，由上层在调用翻译前检查 ConfirmedAt 保证；
// 本包只负责存取与置位。同一门店可有多份草稿，取用时按 created_at 取最新一份。
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
//
// items 序列化进 payload_json（JSONB）。新建时 confirmed_at 为空，表示尚未确认；
// created_at 由数据库默认值生成并 RETURNING 回读。每次上传 / OCR 都新建一份，
// 不做覆盖，历史草稿得以保留（取用时只认最新）。
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
//
// 用 ORDER BY created_at DESC LIMIT 1 取最新；若门店尚无草稿返回 ErrNotFound。
// confirmed_at 为 NULL 时 ConfirmedAt 保持 nil——调用方据此判断「是否已确认」，
// 未确认则不得进入翻译。payload_json 反序列化失败会显式报错（区别于其它表
// 的「留空降级」），因为草稿是翻译的唯一输入源，读坏了不能装作没事。
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

// GetDraftByID 按 ID 取一份确认稿（必须属于该店，否则 ErrNotFound）。
//
// 与 GetLatestDraft 的分工：确认接口允许显式指定 draft_id（前端可传版本号），
// 而「确认即落库菜品」（2026-10-19 Q1）必须读**被确认那一稿**的条目——
// 落错稿的后果是「商家确认的菜单」与「客人看到的菜单」不是同一份。
func (r *Repo) GetDraftByID(ctx context.Context, storeID, draftID string) (*domain.SourceDraft, error) {
	var (
		d       domain.SourceDraft
		payload []byte
		conf    sql.NullTime
	)
	err := r.DB.QueryRowContext(ctx,
		`SELECT id, store_id, lang, payload_json, confirmed_at, created_at
		 FROM source_draft WHERE id = $1 AND store_id = $2`, draftID, storeID).
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
//
// 只置 confirmed_at = now()，不改动 payload。WHERE 同时带 id 与 store_id，
// 防止跨店确认他店草稿；未命中任何行（不存在或不属于该店）返回 ErrNotFound。
// 幂等：重复确认只会刷新时间戳，不影响后续流程。
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
