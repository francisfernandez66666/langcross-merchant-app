/**
 * 问店员短语卡（无网兜底）。从 StorePage.tsx 拆出，行段逐字照搬。
 */
import { PHRASES, phraseText, t } from './i18n'

/**
 * 问店员卡：常用短语「目标语言在上、中文在下」，客人店员都能看懂。
 *
 * ★ 这是**无网兜底**：短语文案（`PHRASES`）硬编码在前端，不依赖 WiFi、
 *   不依赖翻译服务——客人即使没网/翻译服务挂了，也能靠这张卡与店员沟通。
 *   所以短语不放进后端词典，也绝不走网络请求。
 *
 * props：
 *   - `lang`：目标语言，决定短语取哪种译文（缺列回落 en）。
 *   - `onClose`：关闭本层。
 * 位置：底部「需要帮忙？」按钮点击后弹出。
 */
export function HelpSheet({ lang, onClose }: { lang: string; onClose: () => void }) {
  return (
    <div className="gx-mask" onClick={onClose}>
      <div className="gx-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="gx-sheet-grip" />
        <div className="gx-sheet-head">
          <h2>{t('ask', lang)}</h2>
          <button className="gx-x" onClick={onClose} aria-label={t('close', lang)}>
            ✕
          </button>
        </div>
        <p className="gx-hint">{t('helpTitle', lang)}</p>
        <div className="gx-stack">
          {PHRASES.map((p) => (
            <div key={p.zh} className="gx-phrase">
              {/* 客人侧：目标语言大字 */}
              <div className="gx-phrase-main">{phraseText(p, lang)}</div>
              {/* 店员侧：中文小字；中文客人无需重复 */}
              {lang !== 'zh' && <div className="gx-phrase-zh">{p.zh}</div>}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
