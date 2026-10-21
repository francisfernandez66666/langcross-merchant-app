/**
 * 三维度评价弹层 + 五星评分控件。从 StorePage.tsx 拆出，行段逐字照搬。
 * Stars 只被 RateSheet 用，跟着一起搬过来，不单独成文件。
 */
import { useState } from 'react'
import { api, type Coupon } from '../api/client'
import { t } from './i18n'

/**
 * 三维度评价弹层：菜品 / 服务 / 会再来吗。
 *
 * ★★ 关键坑（真实发生过的 P0 事故）：
 *   入口参数**必须叫 `refCode`，绝不能叫 `ref`**。`ref` 是 React 的保留属性，
 *   当父组件以属性形式传一个**字符串**给函数组件时，React 会当作 legacy 字符串 ref 处理，
 *   抛出 "Function components cannot have string refs" 并让**整个弹层崩溃**（白屏）。
 *   所以这里属性名用 `refCode`，仅在调用接口时映射回后端的 `ref` 字段。
 *
 * props：
 *   - `lang`：当前目标语言，用于取评价表单的界面文案。
 *   - `slug`：门店短链，提交接口路径需要。
 *   - `refCode`：来源码（桌码/门口码），随评价一起上报用于归因。
 *   - `onClose`：关闭弹层（不提交）。
 *   - `onDone(coupon, gift)`：提交成功回调，把后端发的券与券面文案交回父组件。
 *     父组件在回调里**先关评价层再弹券层**，避免两层叠加。
 *
 * 位置：底部「评价领券」按钮点击后弹出。
 */
export function RateSheet(props: {
  lang: string
  slug: string
  refCode: string
  onClose: () => void
  onDone: (c: Coupon | null, gift?: string) => void
}) {
  const { lang, slug, refCode, onClose, onDone } = props
  const [dish, setDish] = useState(0) // 菜品评分 0=未评，1–5 星
  const [service, setService] = useState(0) // 服务评分
  const [ret, setRet] = useState(0) // 「会再来吗」评分
  const [comment, setComment] = useState('') // 可选文字评论（客人母语）
  const [busy, setBusy] = useState(false) // 提交中，防重复提交
  const [err, setErr] = useState('') // 提交失败信息

  // 三维度都必须评过才允许提交（后端也校验，前端先卡住给即时反馈）
  const ready = dish > 0 && service > 0 && ret > 0

  /**
   * 提交评价并领券。
   * 成功后调 `onDone(res.coupon, res.coupon_gift)` 把券交回父组件。
   * `comment` 去空白后上报；`lang` 一并上报，便于后端按语言做本地化/统计。
   */
  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    setErr('')
    try {
      const res = await api.submitRating(slug, {
        ref: refCode, // ★ 后端字段就叫 ref，这里把 refCode 映射回去
        score_dish: dish,
        score_service: service,
        score_return: ret,
        comment: comment.trim(),
        lang,
      })
      onDone(res.coupon, res.coupon_gift)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="gx-mask" onClick={onClose}>
      <div className="gx-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="gx-sheet-grip" />
        <div className="gx-sheet-head">
          <h2>{t('rateTitle', lang)}</h2>
          <button className="gx-x" onClick={onClose} aria-label={t('close', lang)}>
            ✕
          </button>
        </div>
        <div className="gx-stack">
          {/* 三维度评分：顺序固定为菜品 → 服务 → 会再来吗，对应后端三个字段 */}
          <Stars label={t('q1', lang)} value={dish} onChange={setDish} />
          <Stars label={t('q2', lang)} value={service} onChange={setService} />
          <Stars label={t('q3', lang)} value={ret} onChange={setRet} />
          {/* 文字评论可选，占位符随语言切换 */}
          <textarea
            className="gx-textarea"
            placeholder={t('commentPh', lang)}
            value={comment}
            onChange={(e) => setComment(e.target.value)}
          />
          {err && <div className="gx-alert">{err}</div>}
          <div className="gx-sheet-actions">
            <button className="gx-btn gx-btn--ghost" onClick={onClose}>
              {t('close', lang)}
            </button>
            {/* 未评满三维度或提交中均禁用；提交中显示「提交中…」 */}
            <button className="gx-btn gx-btn--primary" disabled={!ready || busy} onClick={submit}>
              {busy ? t('submitting', lang) : t('submit', lang)}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

/**
 * 五星评分控件（受控组件）。
 *
 * props：
 *   - `label`：该维度的问题文案（目标语言），如「菜品」。
 *   - `value`：当前分数 0–5；0 表示未评。
 *   - `onChange(v)`：点第 i 颗星回传 i。
 * 位置：RateSheet 内，三个维度各用一个。
 * 细节：`type="button"` 防止在表单内误触发提交；`aria-label` 给出星值供读屏。
 */
function Stars({ label, value, onChange }: { label: string; value: number; onChange: (v: number) => void }) {
  return (
    <div>
      <div className="gx-label">{label}</div>
      <div className="gx-stars">
        {[1, 2, 3, 4, 5].map((i) => (
          <button
            key={i}
            type="button"
            className={`gx-star ${i <= value ? 'is-on' : ''}`}
            aria-label={`${i}`}
            onClick={() => onChange(i)}
          >
            ★
          </button>
        ))}
      </div>
    </div>
  )
}
