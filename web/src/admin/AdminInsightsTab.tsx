/**
 * 经营洞察 Tab：KPI + 最近评价（口径只覆盖用我们工具的外国客）。从 AdminApp.tsx 拆出，行段逐字照搬。
 */
import { useEffect, useState } from 'react'
import {
  api,
  type Insights,
  type Store,
} from '../api/client'

/* ── 经营洞察（口径钉死：只算用我们工具的外国客数据） ──────── */
/**
 * 经营洞察 Tab：扫码 / 评价 / 复购意愿等 KPI + 最近评价列表。
 *
 * @param store 当前选中的门店
 *
 * 口径说明（重要）：这里的统计只覆盖「通过我们的二维码 / 工具进来的外国客」，
 * 不代表门店的全量客流。后端会在 scope_note 里给出这段口径文案，前端原样展示，
 * 不做任何二次计算或换算，避免商家误以为这是全店经营数据。
 */
export function InsightsTab({ store }: { store: Store }) {
  const [data, setData] = useState<Insights | null>(null) // 洞察数据，加载前为 null
  const [err, setErr] = useState('')

  useEffect(() => {
    api.insights(store.id).then(setData).catch((e: Error) => setErr(e.message))
  }, [store.id])

  if (err) return <div className="lc-alert lc-alert--err">{err}</div>
  if (!data) return <div className="lc-muted">加载中…</div>

  /** 把 0~1 的比率格式化成百分比整数串。 */
  const pct = (v: number) => `${Math.round(v * 100)}%`

  return (
    <div className="lc-stack">
      <h1>经营洞察</h1>
      {/* 口径提示由后端下发（scope_note），前端不写死，便于后端调整统计范围 */}
      <div className="lc-alert lc-alert--info">{data.scope_note}</div>

      <div className="kpi">
        <div className="lc-card">
          <div className="lc-dim">扫码次数</div>
          <div className="kpi-num">{data.scans}</div>
        </div>
        <div className="lc-card">
          <div className="lc-dim">评价数</div>
          <div className="kpi-num">{data.rating.count}</div>
        </div>
        <div className="lc-card">
          {/* 「会再来」= 复购意愿打分 ≥ 4 的比例，衡量服务是否真的打动人 */}
          <div className="lc-dim">复购意愿（会再来 ≥4）</div>
          <div className="kpi-num">{pct(data.rating.would_return)}</div>
        </div>
        <div className="lc-card">
          {/* 低分率 = 综合打分 ≤ 2 的比例，是预警指标 */}
          <div className="lc-dim">低分率（≤2）</div>
          <div className="kpi-num">{pct(data.rating.low_rate)}</div>
        </div>
        <div className="lc-card">
          {/* 发券数 / 核销数：核销率高低反映券的吸引力与店员执行度 */}
          <div className="lc-dim">发券 / 核销</div>
          <div className="kpi-num">
            {data.coupons_issued} / {data.coupons_redeemed}
          </div>
        </div>
      </div>

      <div className="lc-card">
        <h2>最近评价</h2>
        {data.recent_ratings.length === 0 && <p className="lc-dim">还没有评价。</p>}
        {data.recent_ratings.map((r) => (
          <div key={r.id} style={{ padding: '10px 0', borderBottom: '1px solid var(--lc-border)' }}>
            {/* 三个维度分：菜品 / 服务 / 是否再来 */}
            <div className="lc-row" style={{ gap: 12 }}>
              <span className="lc-badge">菜品 {r.score_dish}</span>
              <span className="lc-badge">服务 {r.score_service}</span>
              <span className="lc-badge">再来 {r.score_return}</span>
              {/* ref 是客人来源标记（门口码 / 桌码），用于区分渠道 */}
              {r.ref && <span className="lc-dim">{r.ref}</span>}
            </div>
            {/* 评价原文（客人语言）与后端给出的中文译文；译文缺失时只显示原文 */}
            {r.comment_native && <div style={{ marginTop: 6 }}>{r.comment_native}</div>}
            {r.comment_zh && <div className="lc-dim">译：{r.comment_zh}</div>}
          </div>
        ))}
      </div>
    </div>
  )
}
