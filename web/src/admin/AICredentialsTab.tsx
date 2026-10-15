/**
 * 平台 AI 凭据管理（超管）。
 *
 * 权限模型与后端 handlers_assist_admin.go 一致：
 *   - 列表接口对租户管理员只读（脱敏），写操作（新增 / 启停 / 轮换 / 查看明文 / 删除 / 试译）仅超管。
 *   - 后端在列表响应里给出 can_write 字段（超管 = true），本页据此显隐写按钮，
 *     但真正的准入仍以服务端为准——前端显隐只是体验优化。
 *
 * 为什么放在商家后台（/admin）：超管也是用同一套登录进来，
 * 区别只在账号 role；按 role 显隐入口即可，不另开一套后台。
 *
 * 涉及的接口（全部经 ../api/client 调用）：
 *   - GET  /api/admin/ai-credentials            列凭据（脱敏）+ can_write
 *   - POST /api/admin/ai-credentials            新增（明文仅本次返回）
 *   - POST /api/admin/ai-credentials/{id}/status  启停
 *   - POST /api/admin/ai-credentials/{id}/rotate  轮换 Key
 *   - POST /api/admin/ai-credentials/{id}/reveal  查看明文（一次性）
 *   - POST /api/admin/ai-credentials/{id}/test    试译验证
 *   - DELETE /api/admin/ai-credentials/{id}       删除
 */
import { useCallback, useEffect, useState } from 'react'
import { api, type AICredential } from '../api/client'

/** 新建凭据表单的受控状态。 */
type NewForm = { name: string; tenant_id: string; base_url: string; model: string; api_key: string }

/** 某条凭据的「就地展开」状态：明文 / 试译结果 / 轮换输入，互不干扰。 */
type RowUI = {
  revealed?: string // reveal 返回的明文（一次性，刷新即丢）
  test?: { source: string; model: string; content: string; note: string } // 试译结果
  rotateOpen?: boolean // 是否展开轮换输入
  rotateKey?: string // 轮换输入的新 Key
}

const emptyForm: NewForm = { name: '', tenant_id: '', base_url: '', model: '', api_key: '' }

/**
 * AI 凭据管理 Tab。
 *
 * @param account 当前登录账号（仅用于展示手机号占位，真正权限由后端 can_write 决定）
 */
export default function AICredentialsTab({ account }: { account: { phone: string } }) {
  const [creds, setCreds] = useState<AICredential[]>([]) // 凭据列表（脱敏）
  const [canWrite, setCanWrite] = useState(false) // 是否可写（超管）
  const [ui, setUI] = useState<Record<string, RowUI>>({}) // 逐行展开态
  const [form, setForm] = useState<NewForm>(emptyForm) // 新建表单
  const [formOpen, setFormOpen] = useState(false) // 新建表单是否展开
  const [busy, setBusy] = useState(false) // 任意写操作进行中
  const [msg, setMsg] = useState('') // 统一提示条（错误 / 成功）

  /** 重新拉列表（任何写操作后都刷新，以拿到服务端真值）。 */
  const load = useCallback(() => {
    api
      .listAICredentials()
      .then((d) => {
        setCreds(d.credentials ?? [])
        setCanWrite(d.can_write)
      })
      .catch((e: Error) => setMsg(e.message))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  /** 更新某一行的就地展开态（合并式，不动其它行）。 */
  const patchRow = (id: string, patch: Partial<RowUI>) =>
    setUI((cur) => ({ ...cur, [id]: { ...cur[id], ...patch } }))

  /** 新增凭据。明文只在本次响应里返回，落库后无法再查，故提示用户「已一次性展示」。 */
  const create = async () => {
    setBusy(true)
    setMsg('')
    try {
      const r = await api.createAICredential(form)
      setMsg(`已保存「${r.credential.name}」；Key 已加密，此后只能「查看明文」复制。`)
      setForm(emptyForm)
      setFormOpen(false)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 启停凭据。 */
  const toggleStatus = async (c: AICredential) => {
    const next = c.status === 'active' ? 'disabled' : 'active'
    setBusy(true)
    setMsg('')
    try {
      await api.setAICredentialStatus(c.id, next)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 查看明文（一次性）。明文只存在内存，刷新即丢，符合「明文不外泄」的纪律。 */
  const reveal = async (c: AICredential) => {
    setMsg('')
    try {
      const r = await api.revealAICredential(c.id)
      patchRow(c.id, { revealed: r.api_key })
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

  /** 轮换 Key：提交新 Key，成功后收起输入并刷新脱敏串。 */
  const rotate = async (c: AICredential) => {
    const key = (ui[c.id]?.rotateKey ?? '').trim()
    if (key.length < 8) {
      setMsg('新 Key 长度看起来不完整，请重新复制')
      return
    }
    setBusy(true)
    setMsg('')
    try {
      await api.rotateAICredential(c.id, key)
      patchRow(c.id, { rotateOpen: false, rotateKey: '', revealed: undefined })
      setMsg(`已轮换「${c.name}」的 Key，旧 Key 立即失效。`)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 试译：发一句固定话，验证「地址 / 模型 / Key」三件事是否都对。 */
  const test = async (c: AICredential) => {
    setMsg('')
    try {
      const r = await api.testAICredential(c.id)
      patchRow(c.id, { test: { source: r.source, model: r.model, content: r.content, note: r.note } })
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  /** 删除凭据（前端二次确认，避免误删线上凭据）。 */
  const remove = async (c: AICredential) => {
    if (!confirm(`确定删除凭据「${c.name}」？删除后该模型的助手将回落到其它凭据或规则模式。`)) return
    setBusy(true)
    setMsg('')
    try {
      await api.deleteAICredential(c.id)
      setMsg(`已删除「${c.name}」。`)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 租户作用域文案：空 = 平台级默认（所有租户共用）。 */
  const scopeOf = (c: AICredential) => (c.tenant_id ? `租户 ${c.tenant_id}` : '平台级默认')

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <h1 style={{ margin: 0 }}>AI 凭据管理</h1>
        {canWrite && (
          <button className="lc-btn lc-btn--sm" onClick={() => setFormOpen((v) => !v)}>
            {formOpen ? '收起' : '新增凭据'}
          </button>
        )}
      </div>

      <div className="lc-alert lc-alert--info">
        调用大模型是真金白银的成本，凭据由平台超管统一维护、密文落库；商家端只关心「助手是模型模式还是规则模式」。
        {!canWrite && ' 当前账号为只读，仅可查看脱敏状态。'}
      </div>

      {msg && <div className="lc-alert lc-alert--info">{msg}</div>}

      {/* 新建凭据表单：仅超管可见 */}
      {canWrite && formOpen && (
        <div className="lc-card lc-stack" style={{ gap: 12, maxWidth: 560 }}>
          <div className="lc-label">新增 LLM 凭据</div>
          <div className="lc-row" style={{ gap: 10 }}>
            <input
              className="lc-input"
              placeholder="别名（如 平台默认 DeepSeek）"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
            <input
              className="lc-input"
              placeholder="租户 ID（留空=平台级）"
              value={form.tenant_id}
              onChange={(e) => setForm({ ...form, tenant_id: e.target.value })}
            />
          </div>
          <div className="lc-row" style={{ gap: 10 }}>
            <input
              className="lc-input"
              placeholder="Base URL（https://api.deepseek.com/v1）"
              value={form.base_url}
              onChange={(e) => setForm({ ...form, base_url: e.target.value })}
            />
            <input
              className="lc-input"
              placeholder="模型名（deepseek-chat）"
              value={form.model}
              onChange={(e) => setForm({ ...form, model: e.target.value })}
            />
          </div>
          <input
            className="lc-input"
            placeholder="API Key"
            value={form.api_key}
            onChange={(e) => setForm({ ...form, api_key: e.target.value })}
          />
          <button className="lc-btn lc-btn--primary lc-btn--sm" disabled={busy} onClick={create}>
            {busy ? '保存中…' : '保存（Key 加密落库）'}
          </button>
        </div>
      )}

      {/* 凭据列表 */}
      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>别名</th>
              <th>作用域</th>
              <th>Base URL</th>
              <th>模型</th>
              <th>Key</th>
              <th>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {creds.map((c) => {
              const row = ui[c.id] ?? {}
              return (
                <tr key={c.id}>
                  <td>{c.name}</td>
                  <td className="lc-dim">{scopeOf(c)}</td>
                  <td style={{ wordBreak: 'break-all' }}>{c.base_url}</td>
                  <td>{c.model}</td>
                  <td>
                    <code>{c.key_masked}</code>
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
                  <td>
                    {c.status === 'active' ? (
                      <span className="lc-badge lc-badge--ok">启用</span>
                    ) : (
                      <span className="lc-badge">停用</span>
                    )}
                  </td>
                  <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                    {canWrite ? (
                      <div className="lc-row" style={{ gap: 6, justifyContent: 'flex-end' }}>
                        <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => toggleStatus(c)}>
                          {c.status === 'active' ? '停用' : '启用'}
                        </button>
                        <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => reveal(c)}>
                          明文
                        </button>
                        <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => test(c)}>
                          试译
                        </button>
                        <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => patchRow(c.id, { rotateOpen: !row.rotateOpen })}>
                          轮换
                        </button>
                        <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => remove(c)}>
                          删除
                        </button>
                      </div>
                    ) : (
                      <span className="lc-dim">只读</span>
                    )}
                  </td>
                </tr>
              )
            })}
            {creds.length === 0 && (
              <tr>
                <td colSpan={7} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无凭据；助手将在「规则模式」下回答（话术直配 / 知识库）。
                </td>
              </tr>
            )}
          </tbody>
        </table>

        {/* 逐行就地展开：轮换输入 / 试译结果 */}
        {creds.map((c) => {
          const row = ui[c.id] ?? {}
          if (!row.rotateOpen && !row.test) return null
          return (
            <div key={`${c.id}-x`} style={{ padding: '12px 16px', borderTop: '1px solid var(--lc-border)' }}>
              {row.rotateOpen && canWrite && (
                <div className="lc-row" style={{ gap: 8 }}>
                  <input
                    className="lc-input"
                    placeholder="粘贴新的 API Key"
                    value={row.rotateKey ?? ''}
                    onChange={(e) => patchRow(c.id, { rotateKey: e.target.value })}
                  />
                  <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={() => rotate(c)}>
                    提交轮换
                  </button>
                </div>
              )}
              {row.test && (
                <div className="lc-dim">
                  试译：source=<b>{row.test.source}</b> · 模型={row.test.model}
                  <div style={{ marginTop: 4 }}>模型回执：{row.test.content}</div>
                  <div style={{ marginTop: 4 }}>{row.test.note}</div>
                </div>
              )}
            </div>
          )
        })}
      </div>

      <p className="lc-muted">当前账号：{account.phone}</p>
    </div>
  )
}
