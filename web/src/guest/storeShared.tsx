/**
 * 客人端门店页的共用零件（从 StorePage.tsx 拆出）。
 *
 * 为什么单独一个文件：detectLang 与 LANG_KEY 只有 StorePage 用，
 * 而 dishTitle 与 SectionHead 会被招牌位、菜单区等多个子组件用到——
 * 放在各自的组件文件里会互相 import，抽出来才不用循环依赖。
 *
 * ── 拆分背景（2026-10-21 FIX-13）─────────────────────────
 * StorePage.tsx 719 行，客人端整页（Hero / 招牌 / 菜单 / 四个弹层）全在里面。
 * 现在按「共用零件 + 主体各块 + 四个弹层」拆开，根组件只保留状态机与组装。
 */
import type { Dish } from '../api/client'

/**
 * 浏览器语言 → 受支持语种（en-US → en）。
 *
 * 入参 `codes` 是后端注册表给出的受支持语种短码列表。
 * 逐个拿 `navigator.languages`（用户偏好顺序）去匹配，命中即返回。
 * 全不命中时回落 `en`；连 `en` 都没有（理论上不会）才退回注册表首项。
 * 之所以不用 `navigator.language` 单值：多语用户的语言列表顺序才是真实偏好。
 */
export function detectLang(codes: string[]): string {
  const list = navigator.languages?.length ? navigator.languages : [navigator.language]
  for (const raw of list) {
    const base = String(raw || '').toLowerCase().split('-')[0]
    if (codes.includes(base)) return base
  }
  return codes.includes('en') ? 'en' : (codes[0] ?? 'en')
}

/** localStorage 里记住的「上次选择语言」，用于回访时免选。与 URL `?lang=` 分属不同优先级档。 */
export const LANG_KEY = 'lc_guest_lang'

/**
 * 菜名渲染：返回主标题 + 是否命中译文。
 *
 * ★ L1/L3 的核心函数。返回 `{text, ok}` 的设计意图：
 *   - `ok === true`：text 是目标语言译文（或 lang 为 zh 时的原文），正常大字渲染。
 *   - `ok === false`：没有译文，text 回落成 `name_zh`（原文），同时由调用方
 *     触发 L3 的三处样式变化（未翻译标 + 弱化斜体 + 原文小字换提示语）。
 *   调用方**必须**同时消费 `text` 和 `ok`，只取 `text` 会退化成「中文冒充英文大字」。
 */
export function dishTitle(d: Dish, lang: string): { text: string; ok: boolean } {
  if (lang === 'zh') return { text: d.name_zh, ok: true } // 客人选了中文：原文即目标语言，算命中
  const hit = d.names?.[lang]
  return hit ? { text: hit, ok: true } : { text: d.name_zh, ok: false } // L4：不编造，回落原文并交由 ok=false 标注
}

/**
 * 区块标题：目标语言大字 + 细分隔线；缺译文时挂「未翻译」标。
 *
 * props：
 *   - `label`：标题文字，**必须是目标语言**（L1）；调用方已保证要么是译文、
 *     要么是原文（此时由 `pending` 负责标注，见 L3）。
 *   - `pending`：非空时渲染「未翻译」标；空串表示命中译文，不渲染。
 *   - `accent`：招牌位用强调色分隔线，与普通分类区隔开。
 * 位置：每个 `gx-sec` 区块（招牌位、各分类区）的第一行。
 */
export function SectionHead({ label, pending, accent }: { label: string; pending?: string; accent?: boolean }) {
  return (
    <div className={`gx-sec-head ${accent ? 'is-accent' : ''}`}>
      <h2 className="gx-cat">{label}</h2>
      {pending && <span className="gx-tag gx-tag--pending">{pending}</span>}
      <span className="gx-rule" />
    </div>
  )
}
