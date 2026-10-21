/**
 * 平台管理 · 租户分区（仅超管）。
 *
 * 租户是平台的商家分组：每个租户有独立的菜单、账号与翻译 Key。
 * 前端显隐只是体验优化，真准入以后端 requireRole(super_admin) 为准。
 */
import { useCallback, useEffect, useState } from 'react'
import { api, type Tenant } from '../api/client'
import { dateOf, StatusBadge } from './platformHelpers'

type TenantForm = { code: string; name: string }

/** 租户管理：列表 + 新建 + 启停 + 展开查看门店。 */
export function Tenants({ msg, setMsg }: { msg: string; setMsg: (s: string) => void }) {
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [form, setForm] = useState<TenantForm>({ code: '', name: '' })
  const [formOpen, setFormOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [expanded, setExpanded] = useState<string>('') // 正在查看门店的租户 ID

  /** 拉取租户列表（写操作后刷新）。 */
  const load = useCallback(() => {
    api
      .listTenants()
      .then((d) => setTenants(d.tenants ?? []))
      .catch((e: Error) => setMsg(e.message))
  }, [setMsg])

  useEffect(() => {
    load()
  }, [load])

  /** 新建租户。 */
  const create = async () => {
    setBusy(true)
    setMsg('')
    try {
      await api.createTenant({ code: form.code.trim().toLowerCase(), name: form.name.trim() })
      setMsg(`已创建租户「${form.name.trim()}」。`)
      setForm({ code: '', name: '' })
      setFormOpen(false)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 停用 / 启用租户（disabled 的租户下成员无法再登录写操作）。 */
  const toggle = async (t: Tenant) => {
    const next = t.status === 'active' ? 'disabled' : 'active'
    setBusy(true)
    setMsg('')
    try {
      await api.patchTenant(t.id, { status: next })
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
          租户是平台的商家分组：每个租户有独立的菜单、账号与翻译 Key。
        </p>
        <button className="lc-btn lc-btn--sm" onClick={() => setFormOpen((v) => !v)}>
          {formOpen ? '收起' : '新建租户'}
        </button>
      </div>

      {formOpen && (
        <div className="lc-card lc-stack" style={{ gap: 12, maxWidth: 480 }}>
          <div className="lc-label">新建租户</div>
          <div className="lc-row" style={{ gap: 10 }}>
            <input
              className="lc-input"
              placeholder="编码（3-32 位小写字母/数字/中划线，如 hualian）"
              value={form.code}
              onChange={(e) => setForm({ ...form, code: e.target.value })}
            />
            <input
              className="lc-input"
              placeholder="租户名称（如 华联餐饮集团）"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </div>
          <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={create}>
            {busy ? '创建中…' : '创建'}
          </button>
        </div>
      )}

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>编码</th>
              <th>门店</th>
              <th>账号</th>
              <th>到期</th>
              <th>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {tenants.map((t) => (
              <tr key={t.id}>
                <td>{t.name}</td>
                <td>
                  <code>{t.code}</code>
                </td>
                <td>{t.stores ?? 0}</td>
                <td>{t.accounts ?? 0}</td>
                <td className="lc-dim">{dateOf(t.expires_at)}</td>
                <td>
                  <StatusBadge status={t.status} />
                </td>
                <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                  <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => setExpanded(expanded === t.id ? '' : t.id)}>
                    {expanded === t.id ? '收起门店' : '查看门店'}
                  </button>
                  <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 6 }} onClick={() => toggle(t)}>
                    {t.status === 'active' ? '停用' : '启用'}
                  </button>
                </td>
              </tr>
            ))}
            {tenants.length === 0 && (
              <tr>
                <td colSpan={7} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  还没有租户。新建后即可把账号与门店划归该租户。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {msg && <div className="lc-alert lc-alert--err">{msg}</div>}
    </div>
  )
}
