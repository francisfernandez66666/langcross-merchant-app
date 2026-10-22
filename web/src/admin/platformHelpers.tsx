/**
 * 平台管理各分区共用的小工具。
 *
 * 为什么单独一个文件：这几个函数原先散在 PlatformTab.tsx 顶部，而五个分区
 * （概览 / 租户 / 账号 / 翻译 Key / 审计）里有四个要用它们。文件一拆，
 * 共享面就成了一个明确的模块，而不是「从一个大文件里剪出来」的悬空引用。
 *
 * ── 拆分背景（2026-10-21 FIX-13）─────────────────────────
 * PlatformTab.tsx 860 行、塞了 5 个分区 + 1 个外壳，改任何一处都要在千行里
 * 上下翻。现在外壳只留「按角色决定能看到哪些分区」与二级 Tab 切换，
 * 每个分区独立成一个文件。
 */

/** 五级角色的中文名，用于账号列表与角色标签；命中不到按原值回显，避免新增角色后直接显示空白。 */
export const ROLE_LABEL: Record<string, string> = {
  super_admin: '超管',
  tenant_admin: '租户管理员',
  org_admin: '区域管理员',
  manager: '店长',
  staff: '店员',
}

// 把 Unix 秒格式化成「刚刚 / N 分钟前 / HH:MM」。
export function relTime(ts: number): string {
  const diff = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  const d = new Date(ts * 1000)
  return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

// 把 Unix 秒格式化成「YYYY-MM-DD」（到期时间展示用）。
export function dateOf(ts?: number): string {
  if (!ts) return '永久'
  const d = new Date(ts * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

// 状态徽标：active=绿、disabled=灰、expired=红。
export function StatusBadge({ status }: { status: string }) {
  if (status === 'active') return <span className="lc-badge lc-badge--ok">启用</span>
  if (status === 'expired') return <span className="lc-badge lc-badge--err">已过期</span>
  return <span className="lc-badge">停用</span>
}
