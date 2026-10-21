/**
 * 平台管理 · 概览分区（仅超管）。
 *
 * 只读四个平台级总数，不含任何明细——明细在下面各分区，
 * 这样概览永远是「一眼看完」而不至于又变成一张大表。
 */
import type { PlatformStats } from '../api/client'

/** 平台概览：四个总数卡片 + 一句说明。 */
export function Overview({ stats }: { stats: PlatformStats | null }) {
  const items: { label: string; value: number }[] = [
    { label: '租户', value: stats?.tenants ?? 0 },
    { label: '账号', value: stats?.accounts ?? 0 },
    { label: '门店', value: stats?.stores ?? 0 },
    { label: '翻译 Key', value: stats?.translation_keys ?? 0 },
  ]
  return (
    <div className="lc-stack">
      <div className="lc-row" style={{ gap: 12, flexWrap: 'wrap' }}>
        {items.map((it) => (
          <div key={it.label} className="lc-card" style={{ minWidth: 140, flex: '0 0 auto' }}>
            <div className="lc-dim">{it.label}</div>
            <div style={{ fontSize: 28, fontWeight: 700 }}>{stats ? it.value : '—'}</div>
          </div>
        ))}
      </div>
      <p className="lc-muted">平台级总数。租户、账号、翻译 Key 的明细在下面各分区。</p>
    </div>
  )
}
