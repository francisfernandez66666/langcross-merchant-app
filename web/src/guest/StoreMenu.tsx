/**
 * 按分类分组的菜单列表（含「菜单为空」的安抚文案）。
 *
 * 菜品分组在前端做：后端返回扁平数组，只管数据真值、不关心展示顺序；
 * 前端按 category 原序聚合，保留后端给的排序语义（见根组件的 groups）。
 * 从 StorePage.tsx 拆出，行段逐字照搬。
 */
import type { Dish } from '../api/client'
import { t } from './i18n'
import { SectionHead } from './storeShared'

/** 分组菜单 + 空菜单兜底文案。 */
export function Menu({
  groups,
  catNames,
  dishes,
  lang,
  title,
}: {
  groups: [string, Dish[]][]
  catNames: Record<string, string>
  dishes: Dish[]
  lang: string
  title: (d: Dish) => { text: string; ok: boolean }
}) {
  return (
    <>
      {groups.map(([cat, list]) => {
        // 分类名回落到原文；空分类归到「菜单」区。
        const catName = cat ? (catNames[cat] ?? cat) : t('menu', lang)
        // 分类译文是否命中：空分类 / 中文 / 有译文 都算 ok，否则挂「未翻译」标
        const catOk = !cat || lang === 'zh' || Boolean(catNames[cat])
        return (
          <section className="gx-sec" key={cat || '_'}>
            <SectionHead label={catName} pending={catOk ? '' : t('pending', lang)} />
            <ul className="gx-dishes">
              {list.map((d) => {
                const ti = title(d)
                return (
                  <li className={`gx-dish ${d.sold_out ? 'is-out' : ''}`} key={d.id}>
                    {/* 菜品缩略图：有图用图（左侧固定方图），无图用首字占位块。
                        aria-hidden 的占位不含信息，图片 alt 才承担可访问性 */}
                    {d.image_url ? (
                      <img className="gx-dish-img" src={d.image_url} alt={ti.text} loading="lazy" />
                    ) : (
                      <div className="gx-dish-img gx-dish-img--ph" aria-hidden>
                        {(ti.text || d.name_zh).slice(0, 1)}
                      </div>
                    )}
                    <div className="gx-dish-body">
                      <div className="gx-dish-name">
                        {/* L3：缺译文时加 `.gx-pending-text` 弱化斜体，区别于正常译文 */}
                        <span className={ti.ok ? '' : 'gx-pending-text'}>{ti.text}</span>
                        {!ti.ok && <span className="gx-tag gx-tag--pending">{t('pending', lang)}</span>}
                        {/* 售罄与「余 N 份」互斥：售罄优先 */}
                        {d.sold_out && <span className="gx-tag gx-tag--out">{t('soldOut', lang)}</span>}
                        {!d.sold_out && d.remaining != null && (
                          <span className="gx-tag gx-tag--warn">
                            {t('left', lang)} {d.remaining}
                          </span>
                        )}
                      </div>
                      {/* L2 + L3：非中文时给原文小字行。
                          命中译文 → 显示原文（辅助）；未命中 → 换成提示语（此时主标题已是原文，
                          再放一遍原文是冗余，改为「暂无译文，以下为原文」更诚实）。 */}
                      {lang !== 'zh' && (
                        <div className={`gx-dish-orig ${ti.ok ? '' : 'gx-dish-orig--hint'}`}>
                          {ti.ok ? d.name_zh : t('pendingHint', lang)}
                        </div>
                      )}
                    </div>
                    {d.price != null && <div className="gx-dish-price">¥{d.price}</div>}
                  </li>
                )
              })}
            </ul>
          </section>
        )
      })}

      {/* 菜单为空：给一句目标语言的安抚文案，而不是白屏 */}
      {dishes.length === 0 && (
        <section className="gx-sec">
          <p className="lc-muted">{t('emptyMenu', lang)}</p>
        </section>
      )}
    </>
  )
}
