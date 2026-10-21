/**
 * 平台管理 · 审计分区（超管看全量，租户管理员后端已按租户过滤）。
 *
 * 后台关键操作（建租户、改角色、维护 Key、查看明文）都会在这里留痕。
 * 前端不做任何筛选 / 分页：后端已按角色收口并限制最近 200 条，
 * 再在前端加一份过滤只会出现「两边口径不一致」的怪象。
 */
import { useEffect, useState } from 'react'
import { api, type AuditEntry } from '../api/client'
import { relTime, ROLE_LABEL } from './platformHelpers'

/** 审计日志：谁在何时做了什么。超管看全量，租户管理员后端已按租户过滤。 */
export function Audit({ msg, setMsg }: { msg: string; setMsg: (s: string) => void }) {
  const [entries, setEntries] = useState<AuditEntry[]>([])

  useEffect(() => {
    api
      .listAudit()
      .then((d) => setEntries(d.audit ?? []))
      .catch((e: Error) => setMsg(e.message))
  }, [setMsg])

  return (
    <div className="lc-stack">
      <p className="lc-muted" style={{ margin: 0 }}>
        后台关键操作（建租户、改角色、维护 Key、查看明文等）都会留痕。最近 200 条。
      </p>
      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>操作</th>
              <th>操作者</th>
              <th>目标</th>
              <th>详情</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => (
              <tr key={e.id}>
                <td className="lc-dim" style={{ whiteSpace: 'nowrap' }}>
                  {relTime(e.created_at)}
                </td>
                <td>
                  <code>{e.action}</code>
                </td>
                <td className="lc-dim">{ROLE_LABEL[e.actor_role] ?? e.actor_role}</td>
                <td className="lc-dim" style={{ wordBreak: 'break-all' }}>
                  {e.target}
                </td>
                <td>{e.detail}</td>
              </tr>
            ))}
            {entries.length === 0 && (
              <tr>
                <td colSpan={5} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无审计记录。
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
