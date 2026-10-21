/**
 * 招牌推荐位（仅当菜品 >=3 时出现）。
 * 刻意只叫「本店招牌」，不叫「外国人常点」——后者需要订单数据支撑，当前没有，不冒充。
 * 从 StorePage.tsx 拆出，行段逐字照搬。
 */
import type { Dish } from '../api/client'
import { t } from './i18n'
import { SectionHead } from './storeShared'

/** 招牌位：取前 3 道菜（后端已按商家意图排序）。 */
export function Signature({
  dishes,
  lang,
  title,
}: {
  dishes: Dish[]
  lang: string
  title: (d: Dish) => { text: string; ok: boolean }
}) {
  const signature = dishes.slice(0, 3)
  return (
    <>
      {signature.length >= 3 && (
        <section className="gx-sec">
          <SectionHead label={t('signature', lang)} accent />
          <div className="gx-signature">
            {signature.map((d) => {
              const ti = title(d)
              return (
                <div className={`gx-scard ${d.sold_out ? 'is-out' : ''}`} key={d.id}>
                  {/* 菜品图片：有图用图，无图用「首字占位块」——占位块比留白更整齐，
                      也避免未上传图的店出现参差不齐的空洞 */}
                  {d.image_url ? (
                    <img className="gx-scard-img" src={d.image_url} alt={ti.text} loading="lazy" />
                  ) : (
                    <div className="gx-scard-img gx-scard-img--ph" aria-hidden>
                      {(ti.text || d.name_zh).slice(0, 1)}
                    </div>
                  )}
                  <div className="gx-scard-name">
                    {/* L1：招牌位同样以目标语言为主标题 */}
                    {ti.text}
                    {/* L3：缺译文才挂「未翻译」标 */}
                    {!ti.ok && <span className="gx-tag gx-tag--pending">{t('pending', lang)}</span>}
                  </div>
                  {/* L2：非中文时原文以小字辅助；zh 客人无需重复 */}
                  {lang !== 'zh' && <div className="gx-scard-orig">{d.name_zh}</div>}
                  {/* 价格是数字，不随语言变；null/undefined 表示未定价，整块不渲染 */}
                  {d.price != null && <div className="gx-scard-price">¥{d.price}</div>}
                </div>
              )
            })}
          </div>
        </section>
      )}
    </>
  )
}
