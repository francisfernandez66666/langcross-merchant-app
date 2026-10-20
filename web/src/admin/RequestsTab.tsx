/**
 * 商家端 · 服务请求看板（服务铃 / 加水 / 加菜 / 买单）。
 *
 * 这是客人端 AI 助手与「问店员」动作的最终落点：客人点一下，请求就进这里，
 * 服务员在这里一眼看到「谁在叫我」并标记完成。
 *
 * 设计要点：
 *   - 默认只看待办（open），已完成/取消的历史不淹没待办——服务员真正需要的是
 *     「现在有没有人叫我」。顶部提供「待办 / 全部」切换。
 *   - 每 8 秒轮询一次（GET /api/stores/{id}/requests），新请求自动冒出来，
 *     不用服务员手动刷新。轮询是进程内简单方案，量级小、够用。
 *   - 加菜请求后端已批量补好菜名（dish_name），这里直接展示，服务员不用回菜单查 ID。
 *   - 「完成 / 取消」调用 POST .../requests/{rid}/done，成功后即时从待办移除。
 *
 * @param store 当前选中的门店
 */

import { useCallback, useEffect, useState } from 'react'
import { api, type GuestRequest, type Store } from '../api/client'

// 请求类型 → 中文标签（商家端界面固定中文，无需多语）。
const KIND_LABEL: Record<string, string> = {
  bell: '服务铃',
  water: '加水',
  add_dish: '加菜',
  bill: '买单',
  other: '其它',
}

// 把 Unix 秒格式化成「刚刚 / N 分钟前 / HH:MM」。
function relTime(ts: number): string {
  const diff = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  const d = new Date(ts * 1000)
  return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

/** 把一条请求渲染成一行卡片。 */
function RequestRow({
  r,
  onDone,
}: {
  r: GuestRequest
  onDone: (rid: string, status: 'done' | 'cancelled') => void
}) {
  const isAdd = r.kind === 'add_dish'
  return (
    <div className="lc-req" data-kind={r.kind}>
      <div className="lc-req-main">
        <div className="lc-req-top">
          <span className="lc-badge lc-badge--warn">{KIND_LABEL[r.kind] ?? r.kind}</span>
          {r.table_ref && <span className="lc-dim">桌 {r.table_ref}</span>}
          <span className="lc-dim" style={{ marginLeft: 'auto' }}>{relTime(r.created_at)}</span>
        </div>
        {/* 加菜：展示菜名 + 数量 */}
        {isAdd && (
          <div className="lc-req-line">
            菜品：<b>{r.dish_name || r.payload?.dish_id || '未知'}</b>
            {r.payload?.qty ? ` ×${r.payload.qty}` : ''}
          </div>
        )}
        {/* 客人原话（语音转写）：服务员理解上下文用 */}
        {r.note && <div className="lc-req-note">“{r.note}”</div>}
        <div className={`lc-req-status lc-req-status--${r.status}`}>
          {r.status === 'open' ? '待处理' : r.status === 'done' ? '已完成' : '已取消'}
        </div>
      </div>
      {r.status === 'open' && (
        <div className="lc-req-actions">
          <button className="lc-btn lc-btn--sm lc-btn--primary" onClick={() => onDone(r.id, 'done')}>
            完成
          </button>
          <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => onDone(r.id, 'cancelled')}>
            取消
          </button>
        </div>
      )}
    </div>
  )
}

/**
 * 商家端服务请求看板组件（挂在本店控制台的「服务」Tab 下）。
 * 定位、轮询策略与交互细节见文件头；本组件只负责呈现与状态切换，
 * 数据全部来自 GET /api/stores/{id}/requests。
 */
export default function RequestsTab({ store }: { store: Store }) {
  const [status, setStatus] = useState<'open' | ''>('open') // 默认待办
  const [reqs, setReqs] = useState<GuestRequest[]>([])
  const [openCount, setOpenCount] = useState(0)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    api
      .listGuestRequests(store.id, status, 50)
      .then((d) => {
        setReqs(d.requests ?? [])
        setOpenCount(d.open_count ?? 0)
      })
      .catch((e: Error) => setMsg(e.message))
  }, [store.id, status])

  useEffect(() => {
    load()
  }, [load])

  // 轮询：每 8 秒刷新一次，新请求自动出现（服务员无需手动刷新）。
  useEffect(() => {
    const id = setInterval(load, 8000)
    return () => clearInterval(id)
  }, [load])

  const done = async (rid: string, s: 'done' | 'cancelled') => {
    setBusy(true)
    try {
      await api.doneGuestRequest(store.id, rid, s)
      load() // 重新拉取，已处理的从待办列表消失
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <h1 style={{ margin: 0 }}>服务请求</h1>
        {/* 待办数角标：一眼看到有没有人等待 */}
        <span className="lc-badge lc-badge--warn">待办 {openCount}</span>
      </div>
      <p className="lc-muted">客人在店里点「加菜 / 加水 / 服务铃 / 买单」都会到这里。列表每 8 秒自动刷新。</p>

      {/* 待办 / 全部 切换 */}
      <div className="lc-row" style={{ gap: 8 }}>
        <button
          className="lc-btn lc-btn--sm"
          style={{ borderColor: status === 'open' ? 'var(--lc-accent)' : undefined, background: status === 'open' ? 'var(--lc-accent)' : undefined, color: status === 'open' ? '#fff' : undefined }}
          onClick={() => setStatus('open')}
        >
          待办
        </button>
        <button
          className="lc-btn lc-btn--sm"
          style={{ borderColor: status === '' ? 'var(--lc-accent)' : undefined, background: status === '' ? 'var(--lc-accent)' : undefined, color: status === '' ? '#fff' : undefined }}
          onClick={() => setStatus('')}
        >
          全部
        </button>
      </div>

      {msg && <div className="lc-alert lc-alert--err">{msg}</div>}

      <div className="lc-card" style={{ padding: 0 }}>
        {reqs.length === 0 ? (
          <div className="lc-dim" style={{ padding: 24, textAlign: 'center' }}>
            {status === 'open' ? '暂无待办请求' : '还没有任何请求'}
          </div>
        ) : (
          <div>
            {reqs.map((r) => (
              <RequestRow key={r.id} r={r} onDone={done} />
            ))}
          </div>
        )}
      </div>
      {busy && <div className="lc-dim">处理中…</div>}
    </div>
  )
}
