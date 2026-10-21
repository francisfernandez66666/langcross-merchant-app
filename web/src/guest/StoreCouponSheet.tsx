/**
 * 券卡弹层。从 StorePage.tsx 拆出，行段逐字照搬。
 */
import type { Coupon } from '../api/client'
import { t } from './i18n'

/**
 * 券卡：白底例外家族——「截图即券」，必须在任何背景下可读可扫，故不做主题化弱化。
 *
 * 券面文案优先级（`text`）：
 *   后端本地化结果（`gift` / coupon_gift）→ 模板多语字段 `content[lang]`
 *   → `content.en` → `content.zh` → 空串（此时不渲染文案行）。
 *   与 `t()` 的回落链同源：目标语言优先，其次英语，最后中文。
 *
 * props：
 *   - `lang`：目标语言，用于界面文案与模板取词。
 *   - `coupon`：券对象；可能为 null（如该店未开券），此时只显示标题不显示券面。
 *   - `gift`：后端已本地化的券面赠言（优先级最高）。
 *   - `content`：券模板原始多语内容，作为兜底。
 *   - `onClose`：关闭本层。
 * 位置：评价提交成功后弹出（由父组件在 onDone 里切换）。
 */
export function CouponSheet({
  lang,
  coupon,
  gift,
  content,
  onClose,
}: {
  lang: string
  coupon: Coupon | null
  gift: string
  content: Record<string, unknown>
  onClose: () => void
}) {
  // 券面赠言：按「后端本地化 → 模板目标语言 → 英文 → 中文」逐级回落，全无则空串
  const text = gift || String(content[lang] ?? content.en ?? content.zh ?? '')
  return (
    <div className="gx-mask" onClick={onClose}>
      <div className="gx-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="gx-sheet-grip" />
        {/* coupon 为 null 时只显示标题（例如该店未开券），不渲染券面细节 */}
        {coupon ? (
          <>
            <h2 className="gx-coupon-title">{t('couponTitle', lang)}</h2>
            <div className="gx-coupon">
              {/* 赠言可能为空串（回落链全落空），此时整行不渲染 */}
              {text && <div className="gx-coupon-gift">{text}</div>}
              <div className="gx-coupon-tip">{t('couponTip', lang)}</div>
              {/* 核销码：店员凭此码核销，不可翻译 */}
              <div className="gx-coupon-code">{coupon.code}</div>
              {/* 有效期：用浏览器本地化格式展示日期，避免各语言日期顺序混乱 */}
              <div className="gx-coupon-date">
                {t('validUntil', lang)} {new Date(coupon.expire_at).toLocaleDateString()}
              </div>
            </div>
          </>
        ) : (
          <h2 className="gx-coupon-title">{t('couponTitle', lang)}</h2>
        )}
        <button className="gx-btn gx-btn--primary gx-btn--block" style={{ marginTop: 16 }} onClick={onClose}>
          {t('done', lang)}
        </button>
      </div>
    </div>
  )
}
