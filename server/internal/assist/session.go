package assist

import (
	"context"
	"sync"
	"time"
)

// 本文件：多轮流程「在途状态」的存储接缝。
//
// 为什么抽成接口而不是让引擎自己抱一张 map：商家端将来是多实例部署，
// 流程状态放进程内存会导致「第一步在 A 实例、第二步在 B 实例」时流程丢，
// 用户体感是「助手失忆」。引擎不感知存储细节，单测里用内存版即可裸跑。
//
// 两种实现：
//   - MemorySessionStore（本文件）：默认，单机开发 / 测试用；
//   - DB 版见 repo.AssistSessionStore，走 migrations/0006 的 assist_session 表。

// SessionState 是某账号当前进行中的多轮流程状态。
type SessionState struct {
	Flow string `json:"flow"`    // 流程 key（对应 seedFlows 的键）
	Step int    `json:"step"`    // 当前走到第几步（从 0 开始）
	At   int64  `json:"updated"` // 最近一次更新时间（Unix 秒，上层展示用）
}

// SessionStore 流程状态存储。
// 读不到（无记录 / 已过期）时 Get 返回 (nil, nil) —— 这是正常状态而非错误。
type SessionStore interface {
	GetSession(ctx context.Context, accountID string) (*SessionState, error)
	SaveSession(ctx context.Context, accountID, flow string, step int) error
	ClearSession(ctx context.Context, accountID string) error
}

// MemorySessionStore 是进程内默认实现：带 TTL 的 map。
//
// 只适合单实例：重启即失忆。生产由 httpapi 注入 repo 版（见 NewWithStore）。
type MemorySessionStore struct {
	mu   sync.Mutex
	data map[string]SessionState
	ttl  time.Duration
}

// NewMemorySessionStore 构造内存版流程状态存储。
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{data: map[string]SessionState{}, ttl: sessionTTL}
}

// GetSession 取在途流程，超时或不存在返回 (nil, nil)。
func (m *MemorySessionStore) GetSession(_ context.Context, accountID string) (*SessionState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.data[accountID]
	if !ok || time.Since(time.Unix(st.At, 0)) > m.ttl {
		delete(m.data, accountID) // 顺手清理，避免 map 无限增长
		return nil, nil
	}
	cp := st
	return &cp, nil
}

// SaveSession 写入 / 更新流程进度。
func (m *MemorySessionStore) SaveSession(_ context.Context, accountID, flow string, step int) error {
	if flow == "" || step < 0 {
		return m.ClearSession(nil, accountID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[accountID] = SessionState{Flow: flow, Step: step, At: time.Now().Unix()}
	return nil
}

// ClearSession 结束流程。
func (m *MemorySessionStore) ClearSession(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, accountID)
	return nil
}

// Count 返回在途流程数（观测用：流程卡住的比例是产品质量信号）。
func (m *MemorySessionStore) Count(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	n := 0
	for _, st := range m.data {
		if now.Sub(time.Unix(st.At, 0)) <= m.ttl {
			n++
		}
	}
	return n, nil
}
