package repo

import (
	"context"
	"strconv"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// 本文件：AI 经营助手的对话历史存取。
//
// 落库的三个理由：
//  1. 商家刷新 / 换设备后要能看到之前的问答（会话是产品的一部分，不是日志）；
//  2. 排障时要能复盘「助手当时基于什么事实答的」；
//  3. 只保留最近若干条（AssistHistoryLimit），不追求完整审计——
//     真正要审计的是管理操作（admin_audit），不是商家和助手的闲聊。
//
// 归属口径：全部按 account_id 过滤。账号只能看自己的对话，
// 门店维度只是筛选条件（同一账号可切换门店提问）。

// AssistHistoryLimit 是单个账号保留的最大对话条数。
// 取 100 条：足够覆盖「今天问过的所有问题」，又不至于把表撑大。
const AssistHistoryLimit = 100

// AssistMessage 是一条对话记录（不落库的展示形态）。
type AssistMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"` // user / assistant
	Content   string `json:"content"`
	Source    string `json:"source,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// 角色常量（与 domain 分开：这是对话侧的概念）。
const (
	AssistRoleUser      = "user"
	AssistRoleAssistant = "assistant"
)

// AppendAssistMessage 追加一条对话。
// storeID 允许为空串（平台级问答，不绑定门店）。
func (r *Repo) AppendAssistMessage(ctx context.Context, accountID, storeID, role, content, source string) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO assist_message (id, account_id, store_id, role, content, source)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		idgen.New(), accountID, storeID, role, content, source)
	return err
}

// ListAssistMessages 按时间正序列出某账号的对话（最新的在后，前端直接 append 渲染）。
//
// 为什么正序：前端聊天窗是「旧在上、新在下」，正序返回省一次 reverse。
// storeID 为空串时不按门店过滤（看全部）。
func (r *Repo) ListAssistMessages(ctx context.Context, accountID, storeID string) ([]AssistMessage, error) {
	q := `SELECT id, role, content, COALESCE(source,''), extract(epoch FROM created_at)::bigint
	        FROM assist_message WHERE account_id = $1`
	args := []any{accountID}
	if storeID != "" {
		q += ` AND (store_id = $2 OR store_id = '')`
		args = append(args, storeID)
	}
	q += ` ORDER BY created_at ASC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, AssistHistoryLimit)

	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssistMessage{}
	for rows.Next() {
		var m AssistMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Source, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ClearAssistMessages 清空某账号的对话（「重新开始」按钮）。
func (r *Repo) ClearAssistMessages(ctx context.Context, accountID, storeID string) error {
	if storeID == "" {
		_, err := r.DB.ExecContext(ctx,
			`DELETE FROM assist_message WHERE account_id = $1`, accountID)
		return err
	}
	_, err := r.DB.ExecContext(ctx,
		`DELETE FROM assist_message WHERE account_id = $1 AND (store_id = $2 OR store_id = '')`,
		accountID, storeID)
	return err
}

// TrimAssistMessages 裁剪到只保留最近 limit 条。
// 写入后顺手调用，避免单账号历史无限增长（不依赖定时任务，零运维）。
func (r *Repo) TrimAssistMessages(ctx context.Context, accountID string, limit int) error {
	_, err := r.DB.ExecContext(ctx,
		`DELETE FROM assist_message WHERE account_id = $1 AND id NOT IN (
		   SELECT id FROM assist_message WHERE account_id = $1
		    ORDER BY created_at DESC LIMIT $2)`,
		accountID, limit)
	return err
}

// CountAssistMessages 统计对话数（后台概览用：助手活跃度是产品质量信号）。
func (r *Repo) CountAssistMessages(ctx context.Context) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM assist_message`).Scan(&n)
	return n, err
}
