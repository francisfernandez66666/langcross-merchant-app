/**
 * P3 · 挑要翻译的菜 + 改价（决定后面翻译的输入）。
 * 一道都没勾不允许继续，否则 P4 无内容可翻。
 */
import type { DraftItem } from '../api/client'

/** 勾选要翻译的菜并改价，产出后面 P4 预览与 P5 正式翻译的输入集合。
 * @param items    当前菜品草稿
 * @param setItems 回写草稿（勾选 / 改名 / 改价都走它）
 * @param onNext   进入 P4
 */
export function Step3({
  items,
  setItems,
  onNext,
}: {
  items: DraftItem[]
  setItems: (v: DraftItem[]) => void
  onNext: () => void
}) {
  // 已勾选数量，用于按钮文案和「至少选一道」的禁用判断
  const picked = items.filter((i) => i.selected).length
  // 局部更新第 idx 项：用不可变方式生成新数组，避免直接改原对象（React 靠引用变化感知更新）
  const patch = (idx: number, p: Partial<DraftItem>) =>
    setItems(items.map((it, i) => (i === idx ? { ...it, ...p } : it)))

  return (
    <div className="lc-stack">
      {/* 说明「只翻你选的」= 省钱又快，给商家一个明确的取舍动机 */}
      <p className="lc-muted">
        勾选要翻译的菜（只翻你选的，省钱也更快），价格可顺手改。
      </p>
      <div className="lc-card" style={{ padding: 8 }}>
        {/* 这里用数组下标 i 作 key：列表内容可编辑且顺序稳定，可接受；
            若将来支持拖动排序或删除，需要换成稳定 id。 */}
        {items.map((it, i) => (
          <div key={i} className="lc-row" style={{ gap: 10, padding: '8px 6px' }}>
            <input
              type="checkbox"
              checked={it.selected}
              onChange={(e) => patch(i, { selected: e.target.checked })}
              style={{ width: 20, height: 20, accentColor: 'var(--lc-accent)' }}
            />
            {/* 菜名可直接编辑：OCR 常有错别字，让商家当场改 */}
            <input
              className="lc-input"
              style={{ height: 38 }}
              value={it.name}
              onChange={(e) => patch(i, { name: e.target.value })}
            />
            {/* 价格可改；price ?? '' 让缺价时显示为空而不是 undefined */}
            <input
              className="lc-input"
              style={{ height: 38, width: 92, textAlign: 'right' }}
              type="number"
              value={it.price ?? ''}
              onChange={(e) => patch(i, { price: Number(e.target.value) || 0 })}
            />
          </div>
        ))}
      </div>
      {/* 一道都没勾不允许继续，否则 P4 无内容可翻 */}
      <button className="lc-btn lc-btn--primary lc-btn--block" disabled={picked === 0} onClick={onNext}>
        生成多语菜单（{picked} 道）→
      </button>
    </div>
  )
}
