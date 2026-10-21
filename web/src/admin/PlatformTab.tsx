/**
 * 平台管理 Tab（外壳：按角色决定能看到哪些分区）。
 *
 * 与后端 handlers_admin.go 的门禁一一对应（前端显隐只是体验优化，真准入以服务端为准）：
 *   - 概览 / 租户：仅 super_admin（平台级数据，跨租户可见）；
 *   - 账号：super_admin 与 tenant_admin 都可（后者只能管本租户成员，由后端 effectiveTenant 收口）；
 *   - 翻译 Key：★ 超管专属增删改查（Key 是平台成本凭据，租户一律不接触，连列表也不给）；
 *   - 审计：super_admin 全量、tenant_admin 只看本租户（后端已过滤）。
 *
 * 为什么租户管理员也进这个 Tab：他的日常就是「维护自己人」，
 * 这恰恰是账号区；超管才多出概览、租户、翻译 Key 三个平台级分区。
 *
 * ★ 2026-10-21 FIX-13：原先这个文件 860 行、五个分区全塞在里面。
 *   现在外壳只保留「分区清单 + 二级 Tab 切换 + 一条提示消息」，
 *   各分区独立成文件（PlatformOverview / PlatformTenants / PlatformAccounts /
 *   PlatformTranslateKeys / PlatformAudit），共用小工具在 platformHelpers.tsx。
 *   拆分的收益不是行数，而是**改一个分区不必在千行里上下翻**，
 *   且「新增分区」从「往大文件里塞 200 行」变成「加一个文件 + 一行清单」。
 */
import { useEffect, useState } from 'react'
import { api, type Account, type PlatformStats } from '../api/client'
import { ROLE_LABEL } from './platformHelpers'
import { Overview } from './PlatformOverview'
import { Tenants } from './PlatformTenants'
import { Accounts } from './PlatformAccounts'
import { TranslateKeys } from './PlatformTranslateKeys'
import { Audit } from './PlatformAudit'

type Section = 'overview' | 'tenants' | 'accounts' | 'tkeys' | 'audit'

/**
 * 平台管理 Tab。
 *
 * @param account 当前登录账号（role 决定能看到哪些分区；后端还会再校验一次）
 */
export default function PlatformTab({ account }: { account: Account }) {
  const isSuper = account.role === 'super_admin'
  const [section, setSection] = useState<Section>(isSuper ? 'overview' : 'accounts')
  const [stats, setStats] = useState<PlatformStats | null>(null)
  const [msg, setMsg] = useState('')
  const setMsgOnce = (s: string) => setMsg(s)

  // 分区清单：超管全见；租户管理员只有「账号 / 审计」两个区
  // （翻译 Key 是平台成本凭据，超管专属增删改查，租户一律不接触）。
  const sections: { id: Section; label: string }[] = [
    ...(isSuper ? [{ id: 'overview' as Section, label: '概览' }] : []),
    ...(isSuper ? [{ id: 'tenants' as Section, label: '租户' }] : []),
    { id: 'accounts', label: '账号' },
    ...(isSuper ? [{ id: 'tkeys' as Section, label: '翻译 Key' }] : []),
    { id: 'audit', label: '审计' },
  ]

  // 概览数据只在超管视图拉一次；切走再切回也不重复打扰后端。
  useEffect(() => {
    if (isSuper && section === 'overview' && !stats) {
      api
        .platformStats()
        .then((d) => setStats(d))
        .catch((e: Error) => setMsg(e.message))
    }
  }, [isSuper, section, stats])

  // 切换分区时清掉上一条提示，避免张冠李戴。
  useEffect(() => {
    setMsg('')
  }, [section])

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <h1 style={{ margin: 0 }}>平台管理</h1>
        <span className="lc-dim">
          {account.phone} · {ROLE_LABEL[account.role] ?? account.role}
        </span>
      </div>

      {/* 分区切换（类似左侧导航的二级 Tab） */}
      <div className="lc-row" style={{ gap: 8, flexWrap: 'wrap' }}>
        {sections.map((s) => (
          <button
            key={s.id}
            className="lc-btn lc-btn--sm"
            style={
              section === s.id
                ? { borderColor: 'var(--lc-accent)', background: 'var(--lc-accent)', color: '#fff' }
                : undefined
            }
            onClick={() => setSection(s.id)}
          >
            {s.label}
          </button>
        ))}
      </div>

      {section === 'overview' && isSuper && <Overview stats={stats} />}
      {section === 'tenants' && isSuper && <Tenants msg={msg} setMsg={setMsgOnce} />}
      {section === 'accounts' && <Accounts me={account} msg={msg} setMsg={setMsgOnce} />}
      {section === 'tkeys' && isSuper && <TranslateKeys msg={msg} setMsg={setMsgOnce} />}
      {section === 'audit' && <Audit msg={msg} setMsg={setMsgOnce} />}
    </div>
  )
}
