/**
 * P1 · 算损失（引导流第一步 · 本地算账，离线可用，零等待）。
 * 目的不是给出精确数字，而是让商家自己「感觉」到语言不通带来的损失，愿意往下走。
 * 全部输入只在本地参与计算，不发起任何请求、不落库。
 */
import { useState } from 'react'

/** 算一笔「语言不通造成的月度损失」账（纯本地算术，不联网、不落库）。
 * @param onNext 点击「看看怎么挽回」后进入 P2
 */
export function Step1({ onNext }: { onNext: () => void }) {
  // 三个受控输入：每天外国客桌数、人均消费、因语言不通流失的比例（%）。
  const [guests, setGuests] = useState(6)
  const [avg, setAvg] = useState(85)
  const [lost, setLost] = useState(30)
  // 月度流失估算 = 每天桌数 × 30 天 × 流失比例 × 人均消费。
  // 纯本地算术，不涉及任何接口。
  const monthly = Math.round(guests * 30 * (lost / 100) * avg)

  return (
    <div className="lc-stack">
      {/* 明说「本地计算、不上传」，打消商家对数据隐私的顾虑 */}
      <p className="lc-muted">
        先算一笔账：语言不通，最贵的一桌客人可能就在门口走了。以下都在你的手机上本地计算，不上传任何数据。
      </p>
      <div className="lc-card">
        <label className="lc-label">每天进店的外国客人（桌）</label>
        {/* Number(...) || 0：把空串/NaN 兜成 0，避免输入被清空时算出 NaN */}
        <input
          className="lc-input"
          type="number"
          min={0}
          value={guests}
          onChange={(e) => setGuests(Number(e.target.value) || 0)}
        />
        <div style={{ height: 12 }} />
        <label className="lc-label">人均消费（元）</label>
        <input
          className="lc-input"
          type="number"
          min={0}
          value={avg}
          onChange={(e) => setAvg(Number(e.target.value) || 0)}
        />
        <div style={{ height: 12 }} />
        <label className="lc-label">其中因语言不通而流失的比例（%）</label>
        <input
          className="lc-input"
          type="number"
          min={0}
          max={100}
          value={lost}
          onChange={(e) => setLost(Number(e.target.value) || 0)}
        />
      </div>
      {/* 损失金额用大号数字突出，制造「痛点」推动下一步 */}
      <div className="lc-card lc-card--accent">
        <div className="lc-dim">按此估算，每月可能流失</div>
        <div className="kpi-num" style={{ fontSize: 36 }}>
          ¥{monthly.toLocaleString()}
        </div>
      </div>
      <button className="lc-btn lc-btn--primary lc-btn--block" onClick={onNext}>
        看看怎么挽回 →
      </button>
    </div>
  )
}
