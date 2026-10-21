/**
 * 平台管理 · 翻译 Key 分区（★ 超管专属增删改查）。
 *
 * 能言 Key 是真金白银的平台成本凭据，脱敏列表对租户也没有实际用途——
 * 他既不能用也不能改，索性不看，少一个信息暴露面。
 * 明文只在「创建后 / 轮换后 / reveal」三个一次性响应里出现（后端已保证），
 * 这里刻意不缓存明文：试译与轮换都是临时展开，关掉就丢。
 */
import { useCallback, useEffect, useState } from 'react'
import { api, type TranslateKey } from '../api/client'
import { relTime, StatusBadge } from './platformHelpers'

/** 新建 Key 表单。Key 明文只在创建响应里出现一次。 */
type KeyForm = { name: string; key: string; mode: string; daily_limit: string }

/** 某把 Key 的就地展开态：明文 / 试译结果 / 轮换输入。 */
type KeyRowUI = {
  revealed?: string
  test?: { source: string; target: string; to: string; elapsed_ms: number; error?: string }
  rotateOpen?: boolean
  rotateKey?: string
}

export function TranslateKeys({ msg, setMsg }: { msg: string; setMsg: (s: string) => void }) {
  const [keys, setKeys] = useState<TranslateKey[]>([])
  const [activeId, setActiveId] = useState('') // 后端标记的当前生效 Key
  const [ui, setUI] = useState<Record<string, KeyRowUI>>({})
  const [form, setForm] = useState<KeyForm>({ name: '', key: '', mode: 'fast', daily_limit: '' })
  const [formOpen, setFormOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    api
      .listTranslateKeys()
      .then((d) => {
        setKeys(d.keys ?? [])
        setActiveId(d.active_key_id ?? '')
      })
      .catch((e: Error) => setMsg(e.message))
  }, [setMsg])

  useEffect(() => {
    load()
  }, [load])

  /** 合并式更新某一行的展开态。 */
  const patchRow = (id: string, p: Partial<KeyRowUI>) => setUI((cur) => ({ ...cur, [id]: { ...cur[id], ...p } }))

  /** 新建 Key。 */
  const create = async () => {
    setBusy(true)
    setMsg('')
    try {
      await api.createTranslateKey({
        name: form.name.trim(),
        key: form.key.trim(),
        mode: form.mode,
        daily_limit: Number(form.daily_limit) || 0,
      })
      setMsg('已保存；Key 已加密落库，此后只能「查看明文」复制。')
      setForm({ name: '', key: '', mode: 'fast', daily_limit: '' })
      setFormOpen(false)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 查看明文（一次性 + 落审计）。 */
  const reveal = async (k: TranslateKey) => {
    setMsg('')
    try {
      const r = await api.revealTranslateKey(k.id)
      patchRow(k.id, { revealed: r.api_key })
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  /** 复制明文到剪贴板。 */
  const copy = (text: string) => {
    navigator.clipboard?.writeText(text).then(
      () => setMsg('已复制明文到剪贴板，请注意保密'),
      () => setMsg('复制失败，请手动选择文本复制'),
    )
  }

  /** 轮换 Key。 */
  const rotate = async (k: TranslateKey) => {
    const key = (ui[k.id]?.rotateKey ?? '').trim()
    if (key.length < 8) {
      setMsg('新 Key 长度看起来不完整，请重新复制')
      return
    }
    setBusy(true)
    setMsg('')
    try {
      await api.rotateTranslateKey(k.id, key)
      patchRow(k.id, { rotateOpen: false, rotateKey: '', revealed: undefined })
      setMsg(`已轮换「${k.name}」的 Key，旧 Key 立即失效。`)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 启停 Key。 */
  const toggleStatus = async (k: TranslateKey) => {
    const next = k.status === 'active' ? 'disabled' : 'active'
    setBusy(true)
    setMsg('')
    try {
      await api.setTranslateKeyStatus(k.id, next)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 试译：用这把 Key 真翻一句，验证可用性并顺带带回余额。 */
  const test = async (k: TranslateKey) => {
    setMsg('')
    try {
      const r = await api.testTranslateKey(k.id, '麻婆豆腐', 'en')
      patchRow(k.id, { test: { source: r.source, target: r.target, to: r.to, elapsed_ms: r.elapsed_ms, error: r.error } })
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  /** 删除 Key（前端二次确认）。 */
  const remove = async (k: TranslateKey) => {
    if (!confirm(`确定删除翻译 Key「${k.name}」？删除后该租户的翻译将回落到内置词典。`)) return
    setBusy(true)
    setMsg('')
    try {
      await api.deleteTranslateKey(k.id)
      setMsg(`已删除「${k.name}」。`)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <p className="lc-muted" style={{ margin: 0 }}>
          翻译 Key 是能言开放平台的凭据，密文落库。停用一把 Key 后翻译链会回落，最多 30 秒生效。
        </p>
        <button className="lc-btn lc-btn--sm" onClick={() => setFormOpen((v) => !v)}>
          {formOpen ? '收起' : '新增 Key'}
        </button>
      </div>

      {formOpen && (
        <div className="lc-card lc-stack" style={{ gap: 12, maxWidth: 560 }}>
          <div className="lc-label">新增翻译 Key</div>
          <div className="lc-row" style={{ gap: 10 }}>
            <input
              className="lc-input"
              placeholder="别名（如 生产 Key）"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
            <select className="lc-input" value={form.mode} onChange={(e) => setForm({ ...form, mode: e.target.value })}>
              <option value="fast">fast（快）</option>
              <option value="pro">pro（高质量）</option>
            </select>
            <input
              className="lc-input"
              placeholder="日上限（留空=不限）"
              value={form.daily_limit}
              onChange={(e) => setForm({ ...form, daily_limit: e.target.value })}
            />
          </div>
          <input
            className="lc-input"
            placeholder="API Key（lx- 开头）"
            value={form.key}
            onChange={(e) => setForm({ ...form, key: e.target.value })}
          />
          <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={create}>
            {busy ? '保存中…' : '保存（Key 加密落库）'}
          </button>
        </div>
      )}

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>别名</th>
              <th>模式</th>
              <th>Key</th>
              <th>日上限</th>
              <th>最近使用</th>
              <th>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => {
              const row = ui[k.id] ?? {}
              return (
                <tr key={k.id}>
                  <td>
                    {k.name}
                    {/* 后端标记的当前生效 Key 给个角标 */}
                    {k.id === activeId && <span className="lc-badge lc-badge--ok" style={{ marginLeft: 6 }}>生效中</span>}
                  </td>
                  <td>{k.mode}</td>
                  <td>
                    <code>{k.key_masked}</code>
                    {row.revealed && (
                      <div style={{ marginTop: 6 }}>
                        <code style={{ color: 'var(--lc-accent)' }}>{row.revealed}</code>
                        <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 8 }} onClick={() => copy(row.revealed!)}>
                          复制
                        </button>
                        <div className="lc-dim" style={{ marginTop: 4 }}>明文仅本次展示，刷新即丢</div>
                      </div>
                    )}
                  </td>
                  <td>{k.daily_limit > 0 ? k.daily_limit : '不限'}</td>
                  <td className="lc-dim">{k.last_used_at ? relTime(k.last_used_at) : '未使用'}</td>
                  <td>
                    <StatusBadge status={k.status} />
                  </td>
                  <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => reveal(k)}>
                      明文
                    </button>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 6 }} onClick={() => test(k)}>
                      试译
                    </button>
                    <button
                      className="lc-btn lc-btn--sm lc-btn--ghost"
                      style={{ marginLeft: 6 }}
                      onClick={() => patchRow(k.id, { rotateOpen: !row.rotateOpen })}
                    >
                      轮换
                    </button>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 6 }} onClick={() => toggleStatus(k)}>
                      {k.status === 'active' ? '停用' : '启用'}
                    </button>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 6 }} onClick={() => remove(k)}>
                      删除
                    </button>
                  </td>
                </tr>
              )
            })}
            {keys.length === 0 && (
              <tr>
                <td colSpan={7} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无翻译 Key；翻译将使用内置词典（覆盖常见菜名，冷门词会保留原文）。
                </td>
              </tr>
            )}
          </tbody>
        </table>

        {/* 逐行就地展开：轮换输入 / 试译结果 */}
        {keys.map((k) => {
          const row = ui[k.id] ?? {}
          if (!row.rotateOpen && !row.test) return null
          return (
            <div key={`${k.id}-x`} style={{ padding: '12px 16px', borderTop: '1px solid var(--lc-border)' }}>
              {row.rotateOpen && (
                <div className="lc-row" style={{ gap: 8 }}>
                  <input
                    className="lc-input"
                    placeholder="粘贴新的 API Key"
                    value={row.rotateKey ?? ''}
                    onChange={(e) => patchRow(k.id, { rotateKey: e.target.value })}
                  />
                  <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={() => rotate(k)}>
                    提交轮换
                  </button>
                </div>
              )}
              {row.test && (
                <div className="lc-dim">
                  {row.test.error ? (
                    <>试译失败：{row.test.error}</>
                  ) : (
                    <>
                      试译（{row.test.elapsed_ms}ms）：「{row.test.source}」→ {row.test.to} = <b>{row.test.target}</b>
                    </>
                  )}
                </div>
              )}
            </div>
          )
        })}
      </div>
      {msg && <div className="lc-alert lc-alert--err">{msg}</div>}
    </div>
  )
}
