/**
 * 平台管理 · 账号分区（超管 / 租户管理员共用）。
 *
 * 这是租户管理员唯一真正用到的分区——他的日常就是「维护自己人」。
 * 能看到哪些人、能改成什么角色，由后端 effectiveTenant + 角色等级收口，
 * 前端只做体验优化（不列高于自己的角色、标出「我」这一行）。
 */
import { useCallback, useEffect, useState } from 'react'
import { api, type Account } from '../api/client'
import { ROLE_LABEL } from './platformHelpers'

type AccountForm = { phone: string; role: string; display_name: string }

/** 账号管理：列表 + 新建 + 改角色 + 启停。 */
export function Accounts({ me, msg, setMsg }: { me: Account; msg: string; setMsg: (s: string) => void }) {
  const [accounts, setAccounts] = useState<Account[]>([])
  const [form, setForm] = useState<AccountForm>({ phone: '', role: 'manager', display_name: '' })
  const [formOpen, setFormOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  /** 拉账号列表（超管看全部、租户管理员看本租户，后端已收口）。 */
  const load = useCallback(() => {
    api
      .listAccounts()
      .then((d) => setAccounts(d.accounts ?? []))
      .catch((e: Error) => setMsg(e.message))
  }, [setMsg])

  useEffect(() => {
    load()
  }, [load])

  /** 新建账号；手机号已存在时后端会就地改角色（响应 created=false）。 */
  const create = async () => {
    setBusy(true)
    setMsg('')
    try {
      const r = await api.createAccount({
        phone: form.phone.trim(),
        role: form.role,
        display_name: form.display_name.trim(),
      })
      setMsg(r.created ? `已创建账号 ${form.phone.trim()}。` : `该手机号已存在，已更新其角色。`)
      setForm({ phone: '', role: 'manager', display_name: '' })
      setFormOpen(false)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 就地改角色（不能改自己，后端强制）。 */
  const setRole = async (a: Account, role: string) => {
    setBusy(true)
    setMsg('')
    try {
      await api.patchAccountRole(a.id, role)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 停用 / 启用账号（不能停用同级或更高级成员，后端强制）。 */
  const toggleStatus = async (a: Account) => {
    const next = a.status === 'active' ? 'disabled' : 'active'
    setBusy(true)
    setMsg('')
    try {
      await api.patchAccountStatus(a.id, next)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // 可选角色清单：低于自己等级的才列出（前端体验优化；后端再兜底）。
  const levelOf = (r: string) => ({ super_admin: 4, tenant_admin: 3, org_admin: 2, manager: 1, staff: 0 }[r] ?? 0)
  const myLevel = levelOf(me.role)
  const roleOptions = ['tenant_admin', 'org_admin', 'manager', 'staff'].filter((r) => levelOf(r) < myLevel)

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <p className="lc-muted" style={{ margin: 0 }}>
          账号即登录手机号。角色决定他能做什么：超管管平台，租户管理员管自己人，店长管门店。
        </p>
        <button className="lc-btn lc-btn--sm" onClick={() => setFormOpen((v) => !v)}>
          {formOpen ? '收起' : '新建账号'}
        </button>
      </div>

      {formOpen && (
        <div className="lc-card lc-stack" style={{ gap: 12, maxWidth: 480 }}>
          <div className="lc-label">新建账号</div>
          <div className="lc-row" style={{ gap: 10 }}>
            <input
              className="lc-input"
              placeholder="手机号"
              value={form.phone}
              onChange={(e) => setForm({ ...form, phone: e.target.value })}
            />
            <select
              className="lc-input"
              value={form.role}
              onChange={(e) => setForm({ ...form, role: e.target.value })}
            >
              {roleOptions.map((r) => (
                <option key={r} value={r}>
                  {ROLE_LABEL[r]}
                </option>
              ))}
            </select>
          </div>
          <input
            className="lc-input"
            placeholder="姓名（可选，便于团队辨认）"
            value={form.display_name}
            onChange={(e) => setForm({ ...form, display_name: e.target.value })}
          />
          <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={create}>
            {busy ? '创建中…' : '创建'}
          </button>
        </div>
      )}

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>手机号</th>
              <th>姓名</th>
              <th>角色</th>
              <th>租户</th>
              <th>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {accounts.map((a) => (
              <tr key={a.id}>
                <td>
                  {a.phone}
                  {/* 自己这一行标出来：不能给自己改角色 / 停用自己 */}
                  {a.id === me.id && <span className="lc-dim">（我）</span>}
                </td>
                <td>{a.display_name || <span className="lc-dim">—</span>}</td>
                <td>
                  {a.id === me.id ? (
                    <span>{ROLE_LABEL[a.role] ?? a.role}</span>
                  ) : (
                    <select
                      className="lc-input lc-input--sm"
                      value={a.role}
                      onChange={(e) => setRole(a, e.target.value)}
                    >
                      {/* 当前角色可能不低于自己，也列出来避免「看不见」 */}
                      <option value={a.role}>{ROLE_LABEL[a.role] ?? a.role}</option>
                      {roleOptions
                        .filter((r) => r !== a.role)
                        .map((r) => (
                          <option key={r} value={r}>
                            {ROLE_LABEL[r]}
                          </option>
                        ))}
                    </select>
                  )}
                </td>
                <td className="lc-dim">{a.tenant_id || '平台级'}</td>
                <td>
                  {a.status === 'active' ? (
                    <span className="lc-badge lc-badge--ok">启用</span>
                  ) : (
                    <span className="lc-badge">停用</span>
                  )}
                </td>
                <td style={{ textAlign: 'right' }}>
                  {a.id !== me.id && (
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => toggleStatus(a)}>
                      {a.status === 'active' ? '停用' : '启用'}
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {accounts.length === 0 && (
              <tr>
                <td colSpan={6} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无账号。
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
