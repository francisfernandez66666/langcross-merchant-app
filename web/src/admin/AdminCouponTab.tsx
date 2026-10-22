/**
 * 券与核销 Tab：核销客人券码 + 已发券列表。从 AdminApp.tsx 拆出，行段逐字照搬。
 */
import { useCallback, useEffect, useState } from 'react'
import {
  api,
  type Coupon,
  type Store,
} from '../api/client'

/* ── 券与核销 ───────────────────────────────────────────── */
/**
 * 券与核销 Tab：店员在客人到店出示券码时输入核销，下方是已发券列表。
 *
 * @param store 当前选中的门店
 *
 * 核销是「不可逆」的动作（核销后券作废），所以：
 *   - 成功与失败分两条独立的提示（okMsg / msg），避免商家把报错误读成成功；
 *   - 成功后清空输入框并刷新列表，方便连着核销下一张；
 *   - 券码长度 < 4 时禁用按钮，挡掉明显的误触。
 */
export function CouponTab({ store }: { store: Store }) {
  const [coupons, setCoupons] = useState<Coupon[]>([]) // 已发出的券
  const [code, setCode] = useState('') // 待核销的券码（受控输入）
  const [msg, setMsg] = useState('') // 核销失败信息
  const [okMsg, setOkMsg] = useState('') // 核销成功信息

  /** 拉取券列表；失败静默（列表刷不出来不影响核销本身）。 */
  const load = useCallback(() => {
    api.listCoupons(store.id).then((d) => setCoupons(d.coupons ?? [])).catch(() => undefined)
  }, [store.id])

  useEffect(() => {
    load()
  }, [load])

  /** 核销券码：先清掉上一次的两条提示，成功则清空输入并刷新列表。 */
  const redeem = async () => {
    setMsg('')
    setOkMsg('')
    try {
      // 券码两端可能被误输入空格，trim 后再提交
      const r = await api.redeem(store.id, code.trim())
      setOkMsg(`核销成功：${r.coupon.code}`)
      setCode('')
      load()
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  return (
    <div className="lc-stack">
      <h1>券与核销</h1>
      <div className="lc-card">
        <label className="lc-label">输入客人券码核销</label>
        <div className="lc-row" style={{ gap: 10, maxWidth: 420 }}>
          <input
            className="lc-input"
            value={code}
            placeholder="8 位券码"
            onChange={(e) => setCode(e.target.value)}
            // 支持回车直接核销，方便店员用扫码枪（扫码枪末尾通常带回车）
            onKeyDown={(e) => e.key === 'Enter' && void redeem()}
          />
          <button className="lc-btn lc-btn--primary" disabled={code.trim().length < 4} onClick={redeem}>
            核销
          </button>
        </div>
        {/* 失败与成功用不同颜色的提示条，避免误读 */}
        {msg && <div className="lc-alert lc-alert--err" style={{ marginTop: 10 }}>{msg}</div>}
        {okMsg && <div className="lc-alert lc-alert--info" style={{ marginTop: 10 }}>{okMsg}</div>}
      </div>

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>券码</th>
              <th>状态</th>
              <th>有效期至</th>
            </tr>
          </thead>
          <tbody>
            {coupons.map((c) => (
              <tr key={c.id}>
                {/* 券码用等宽数字 + 加粗，便于和客人手机上的码逐位比对 */}
                <td style={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{c.code}</td>
                <td>
                  {/* 三种状态：已核销（绿）/ 已过期（默认）/ 待核销（黄） */}
                  {c.status === 'redeemed' ? (
                    <span className="lc-badge lc-badge--ok">已核销</span>
                  ) : c.status === 'expired' ? (
                    <span className="lc-badge">已过期</span>
                  ) : (
                    <span className="lc-badge lc-badge--warn">待核销</span>
                  )}
                </td>
                <td>{new Date(c.expire_at).toLocaleDateString()}</td>
              </tr>
            ))}
            {coupons.length === 0 && (
              <tr>
                <td colSpan={3} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无券
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
