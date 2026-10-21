/**
 * 语言选择弹层。从 StorePage.tsx 拆出，行段逐字照搬。
 */
import type { Lang } from '../api/client'
import { langLabel, t } from './i18n'

/**
 * 语言选择弹层：列出全部受支持语言，每项显示「母语写法 + 中文名」。
 *
 * props：
 *   - `langs`：后端注册表返回的清单（**不硬编码**，新增语种后端登记即可）。
 *   - `current`：当前语种短码，用于高亮选中项；也作为本层界面文案的取词语言。
 *   - `onPick(code)`：选中某语种，由父组件写回 `lang` 并关闭本层。
 *   - `onClose`：关闭本层。
 * 位置：头部语言按钮点击后弹出。
 * 交互细节：遮罩点击关闭，面板点击 `stopPropagation` 防止穿透关闭。
 */
export function LangSheet({
  langs,
  current,
  onPick,
  onClose,
}: {
  langs: Lang[]
  current: string
  onPick: (code: string) => void
  onClose: () => void
}) {
  return (
    <div className="gx-mask" onClick={onClose}>
      <div className="gx-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="gx-sheet-grip" />
        <div className="gx-sheet-head">
          <h2>{t('chooseLang', current)}</h2>
          <button className="gx-x" onClick={onClose} aria-label={t('close', current)}>
            ✕
          </button>
        </div>
        <div className="gx-langgrid">
          {langs.map((l) => (
            <button
              key={l.code}
              className={`gx-langitem ${l.code === current ? 'is-on' : ''}`}
              onClick={() => onPick(l.code)}
            >
              <span className="gx-langitem-native">{langLabel(l)}</span>
              <span className="gx-langitem-zh">{l.name_zh}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
