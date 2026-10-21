/**
 * P4 · 核心多语言（全程唯一一次真生成 · 需联网）。
 * 商家在这里选目标语种，并即时看到「多语菜单长什么样」——这一步用的是免登录的
 * 预览接口 previewTranslate，产出只用于展示，真正的翻译在 P5 注册后才落到门店数据里。
 */
import { useEffect, useState } from 'react'
import { api, type DraftItem, type Lang } from '../api/client'

/** 选目标语种并即时预览多语菜单（走免登录预览接口，产出只用于展示）。
 * @param items      菜品草稿（只有 selected 的会被送去翻译）
 * @param targets    目标语种 code 列表
 * @param setTargets 回写目标语种（勾选/取消语种）
 * @param onNext     进入 P5
 */
export function Step4({
  items,
  targets,
  setTargets,
  onNext,
}: {
  items: DraftItem[]
  targets: string[]
  setTargets: (v: string[]) => void
  onNext: () => void
}) {
  const [langs, setLangs] = useState<Lang[]>([]) // 语种注册表（来自后端，前端不硬编码）
  const [out, setOut] = useState<DraftItem[]>([]) // 预览结果：带 names 的菜品
  const [cats, setCats] = useState<Record<string, Record<string, string>>>({}) // 分类译文：{lang: {原文分类: 译文}}
  const [busy, setBusy] = useState(true) // 初始为 true，避免首屏闪一下「还没结果」
  const [err, setErr] = useState('')

  useEffect(() => {
    // 语种清单必须来自后端注册表：新增语种时前端零改动。加载失败静默。
    api.langs().then((d) => setLangs(d.langs ?? [])).catch(() => undefined)
  }, [])

  const picked = items.filter((i) => i.selected)
  // 用 join 做依赖：避免 targets 数组引用变化导致重复请求
  const key = targets.join(',')

  useEffect(() => {
    // 没有菜或没选语种就没什么可预览的，直接结束 loading
    if (picked.length === 0 || targets.length === 0) {
      setBusy(false)
      return
    }
    let alive = true
    setBusy(true)
    setErr('')
    api
      .previewTranslate(picked, targets)
      .then((r) => {
        if (!alive) return
        setOut(r.items)
        setCats(r.categories ?? {})
      })
      .catch((e: Error) => alive && setErr(e.message))
      .finally(() => alive && setBusy(false))
    // alive 标记：语种快速切换时，丢弃已过期的旧请求结果，避免用旧数据覆盖新数据
    return () => {
      alive = false
    }
    // 依赖用 key（targets 的字符串快照）与 items，刻意不写 targets 本身
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, items])

  /** 切换某个目标语种：已选则移除，未选则追加。 */
  const toggle = (code: string) =>
    setTargets(targets.includes(code) ? targets.filter((c) => c !== code) : [...targets, code])

  // 展示用的语种 = 注册表里除 zh 之外的全部（zh 是原文，不是翻译目标）
  const shown = langs.filter((l) => l.code !== 'zh')
  /** 取某道菜在指定语种下的分类译文；原文分类为空时返回 undefined。 */
  const catOf = (c: string | undefined, code: string) => (c ? cats[code]?.[c] : undefined)

  return (
    <div className="lc-stack">
      <p className="lc-muted">
        这是你店里的菜单，翻成客人看得懂的样子。<span className="lc-badge lc-badge--warn">需联网</span>
        勾选要覆盖的语种，一次全生成。
      </p>

      <div className="lc-card" style={{ padding: 14 }}>
        <div className="lc-label" style={{ marginBottom: 8 }}>
          覆盖语种（已选 {targets.length} 种）
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 7 }}>
          {shown.map((l) => {
            const on = targets.includes(l.code)
            return (
              // 语种多选标签：选中时主题色填充。文案用 native（各语种的自称）
              <button
                key={l.code}
                onClick={() => toggle(l.code)}
                className="lc-btn lc-btn--sm"
                style={{
                  borderColor: on ? 'var(--lc-accent)' : undefined,
                  background: on ? 'var(--lc-accent)' : undefined,
                  color: on ? '#fff' : undefined,
                }}
              >
                {l.native}
              </button>
            )
          })}
        </div>
      </div>

      {/* 生成中提示：逐语种调用，约十几秒，给个预期避免商家以为卡死 */}
      {busy && <div className="lc-card lc-dim">正在生成 {targets.length} 个语种…（逐语种调用，约十几秒）</div>}
      {err && <div className="lc-alert lc-alert--err">{err}</div>}
      {targets.length === 0 && <div className="lc-alert lc-alert--info">至少勾选一个语种。</div>}

      {/* 预览网格：每个语种一列卡片，逐条列出「译文 · 中文原文」 */}
      {!busy && targets.length > 0 && (
        <div className="ml-grid">
          {targets.map((code) => {
            // meta 取注册表里的语种元信息；找不到时退回 code 大写，保证永远有标题
            const meta = langs.find((l) => l.code === code)
            return (
              <div className="ml-card" key={code}>
                <div className="ml-card-h">
                  <b>{meta?.native ?? code.toUpperCase()}</b>
                  <span className="lc-dim">{meta?.name_zh}</span>
                </div>
                {out.map((it, i) => {
                  // v = 该菜在该语种下的译文；c = 该菜分类的译文
                  const v = it.names?.[code]
                  const c = catOf(it.category, code)
                  return (
                    <div className="ml-row" key={i}>
                      <span className="ml-name">
                        {/* 有分类译文就带上分类前缀；译文缺失时标「未收录」，而不是显示空白 */}
                        {c ? `${c} · ` : ''}
                        {v || <span className="ml-miss">未收录</span>}
                      </span>
                      {/* 右侧永远显示中文原文（name），作为对照的真值 */}
                      <span className="ml-zh">{it.name}</span>
                    </div>
                  )
                })}
              </div>
            )
          })}
        </div>
      )}

      {/* 生成中或没选语种时不允许继续，保证 P5 有 targets 可翻 */}
      <button className="lc-btn lc-btn--primary lc-btn--block" disabled={busy || targets.length === 0} onClick={onNext}>
        注册后即可对外使用 →
      </button>
    </div>
  )
}
