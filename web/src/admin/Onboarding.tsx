/**
 * 商家上线引导流 · P1–P5（路由 /admin/onboarding，见 App.tsx）。
 *
 * 目标：让一个新商家在几分钟内、零思考地完成「把纸质菜单变成多语菜单并上线」。
 * 五个步骤是「不可跳过的顺序依赖」——每一步的产物是下一步的输入：
 *   P1 算损失   ：本地算一笔账（纯说服用，不落任何数据、不联网）
 *   P2 上传菜单 ：OCR 出菜品草稿（需联网，识别不可用时退回示例菜单）
 *   P3 挑要翻译的菜 + 改价（决定后面翻译的输入）
 *   P4 选目标语种 → 多语预览（全程唯一一次「真生成」，需联网）
 *   P5 注册/登录 → 建店 → 存草稿 → 确认原文稿 → 批量翻译 → 出双码
 *
 * 关键不变量（改代码时务必守住）：
 *   - 原文语种固定为 zh；item.name / Dish.name_zh 是原文真值，任何时候都不能被译文覆盖。
 *   - 必须先「确认原文稿」才能翻译：P5 里 confirmDraft 一定要排在 translate 之前。
 *   - 目标语种清单来自后端注册表 GET /api/translation/langs，前端不硬编码，
 *     这样后端新增语种时前端零改动。
 *
 * 涉及的接口：
 *   - POST /api/ocr/extract            ：菜单图片 OCR
 *   - GET  /api/translation/langs      ：语种注册表
 *   - POST /api/public/translate-preview：注册前的多语预览（免登录）
 *   - POST /api/auth/otp、/api/auth/login：注册 / 登录
 *   - POST /api/stores                 ：建店
 *   - POST /api/stores/:id/draft、/draft/confirm、/translate：落草稿并生成多语
 */
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  api,
  tokenStore,
  type CouponTemplate,
  type DraftItem,
  type Lang,
  type Store,
  type StoreLinks,
} from '../api/client'

/**
 * 示例菜单：用于本地无 OCR 服务时快速演示后续步骤。
 * 选的都是最常见的中餐菜品，方便商家一眼看懂「这就是我的菜单」，
 * 从而愿意继续走完 P4/P5；所有项默认 selected: true，进 P3 时已是「全选」状态。
 */
const SAMPLE: DraftItem[] = [
  { name: '宫保鸡丁', price: 38, category: '热菜', selected: true },
  { name: '麻婆豆腐', price: 32, category: '热菜', selected: true },
  { name: '水煮鱼', price: 68, category: '热菜', selected: true },
  { name: '糖醋里脊', price: 42, category: '热菜', selected: true },
  { name: '番茄炒蛋', price: 26, category: '热菜', selected: true },
  { name: '清炒时蔬', price: 22, category: '素菜', selected: true },
  { name: '蛋炒饭', price: 18, category: '主食', selected: true },
  { name: '米饭', price: 3, category: '主食', selected: true },
  { name: '酸辣汤', price: 22, category: '汤', selected: true },
  { name: '可乐', price: 8, category: '饮品', selected: true },
  { name: '啤酒', price: 12, category: '饮品', selected: true },
]

/**
 * P4 默认勾选的语种：覆盖最大的三拨客人（欧美 / 日 / 韩）。
 * 这三个是来华客人里占比最高的语种，默认勾上能让商家零思考走完流程；
 * 其余语种（th/fr/es/de/pt/vi）不默认勾选，由商家按需自行加选。
 * 注意这里是「默认值」而非「语种清单」——真正的可选项一律来自后端注册表，
 * 所以即便后端新增/删除语种，这个默认值也不需要跟着改（不存在的 code 会被忽略）。
 */
const DEFAULT_TARGETS = ['en', 'ja', 'ko']

// 五个步骤的标题，下标 = step - 1。顺序即流程顺序，不要随意调整。
const STEP_TITLES = ['算损失', '上传菜单', '挑要翻译的菜', '核心多语言', '注册解锁']

/**
 * 引导流根组件：只负责「步骤状态机」——记录当前第几步、以及跨步骤共享的数据。
 * 各步骤的具体 UI 都拆到 Step1~Step5 子组件，由本组件按 step 挂载。
 *
 * 跨步骤共享的三份状态（因为要在多个步骤间传递，所以提升到这里）：
 *   - items  ：菜品草稿（P2 产出 → P3 编辑 → P4 预览 → P5 落库）
 *   - targets：目标语种（P4 选择 → P5 翻译）
 *   - result ：上线结果（P5 产出后用于展示成功页，非空即代表已完成上线）
 */
export default function Onboarding() {
  const nav = useNavigate()
  const [step, setStep] = useState(1) // 当前步骤 1~5
  const [items, setItems] = useState<DraftItem[]>([]) // 跨步骤共享的菜品草稿
  const [targets, setTargets] = useState<string[]>(DEFAULT_TARGETS) // 跨步骤共享的目标语种
  // 上线结果：一旦有值，P5 就切换成「上线成功」页；同时它也用来隐藏「上一步」按钮。
  const [result, setResult] = useState<{ store: Store; links: StoreLinks; coupon?: CouponTemplate } | null>(null)

  /** 步骤跳转：把目标步数夹在 1~5 之间，防止越界。 */
  const go = (n: number) => setStep(Math.min(5, Math.max(1, n)))

  return (
    <div className="warm" style={{ minHeight: '100%', background: 'var(--lc-bg)', color: 'var(--lc-text)' }}>
      <div className="lc-wrap" style={{ maxWidth: 720, paddingTop: 34 }}>
        <div className="lc-row lc-row--between" style={{ marginBottom: 14 }}>
          <div className="lc-dim">能言翻译助手 · 商家上线（{step}/5）</div>
          {/* 「跳过」直接去管理台：商家可能已建过店，不强制走引导 */}
          <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => nav('/admin')}>
            跳过
          </button>
        </div>

        {/* 顶部进度条：i <= step 的格子上色，直观显示当前进度 */}
        <div className="lc-steps">
          {[1, 2, 3, 4, 5].map((i) => (
            <div key={i} className={`lc-step ${i <= step ? 'is-on' : ''}`} />
          ))}
        </div>

        <h1>{STEP_TITLES[step - 1]}</h1>

        {/* 按 step 挂载对应步骤组件。每一步的「下一步」回调里带上必要的状态写入 */}
        {step === 1 && <Step1 onNext={() => go(2)} />}
        {step === 2 && (
          <Step2
            items={items}
            onLoaded={(its) => {
              setItems(its)
              go(3)
            }}
          />
        )}
        {/* P3 需要回写 items（勾选/改价），所以把 setItems 直接透传下去 */}
        {step === 3 && <Step3 items={items} setItems={setItems} onNext={() => go(4)} />}
        {step === 4 && <Step4 items={items} targets={targets} setTargets={setTargets} onNext={() => go(5)} />}
        {step === 5 && <Step5 items={items} targets={targets} result={result} setResult={setResult} />}

        {/* 上一步按钮：第 1 步没有上一步；已上线（result 有值）后也不再显示，避免误退 */}
        {step > 1 && !result && (
          <div style={{ marginTop: 22 }}>
            <button className="lc-btn lc-btn--ghost lc-btn--sm" onClick={() => go(step - 1)}>
              ← 上一步
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

/**
 * P1 · 算损失（本地算账，离线可用，零等待）。
 * 目的不是给出精确数字，而是让商家自己「感觉」到语言不通带来的损失，愿意往下走。
 * 全部输入只在本地参与计算，不发起任何请求、不落库。
 *
 * @param onNext 点击「看看怎么挽回」后进入 P2
 */
function Step1({ onNext }: { onNext: () => void }) {
  // 三个受控输入：每天外国客桌数、人均消费、因语言不通流失的比例（%）。
  const [guests, setGuests] = useState(6)
  const [avg, setAvg] = useState(85)
  const [lost, setLost] = useState(30)
  // 月度流失估算 = 每天桌数 × 30 天 × 流失比例 × 人均消费。
  // 纯本地算术，不涉及任何接口。
  const monthly = Math.round(guests * 30 * (lost / 100) * avg)

  return (
    <div className="lc-stack">
      {/* 明说「本地计算、不上传」，打消商家对数据隐私的顾虑 */}
      <p className="lc-muted">
        先算一笔账：语言不通，最贵的一桌客人可能就在门口走了。以下都在你的手机上本地计算，不上传任何数据。
      </p>
      <div className="lc-card">
        <label className="lc-label">每天进店的外国客人（桌）</label>
        {/* Number(...) || 0：把空串/NaN 兜成 0，避免输入被清空时算出 NaN */}
        <input
          className="lc-input"
          type="number"
          min={0}
          value={guests}
          onChange={(e) => setGuests(Number(e.target.value) || 0)}
        />
        <div style={{ height: 12 }} />
        <label className="lc-label">人均消费（元）</label>
        <input
          className="lc-input"
          type="number"
          min={0}
          value={avg}
          onChange={(e) => setAvg(Number(e.target.value) || 0)}
        />
        <div style={{ height: 12 }} />
        <label className="lc-label">其中因语言不通而流失的比例（%）</label>
        <input
          className="lc-input"
          type="number"
          min={0}
          max={100}
          value={lost}
          onChange={(e) => setLost(Number(e.target.value) || 0)}
        />
      </div>
      {/* 损失金额用大号数字突出，制造「痛点」推动下一步 */}
      <div className="lc-card lc-card--accent">
        <div className="lc-dim">按此估算，每月可能流失</div>
        <div className="kpi-num" style={{ fontSize: 36 }}>
          ¥{monthly.toLocaleString()}
        </div>
      </div>
      <button className="lc-btn lc-btn--primary lc-btn--block" onClick={onNext}>
        看看怎么挽回 →
      </button>
    </div>
  )
}

/**
 * P2 · 上传一页菜单（服务端识别 · 需联网；无服务时走手动保底）。
 *
 * @param items    已识别的菜品草稿（用于显示「已识别 N 道菜」）
 * @param onLoaded 识别成功后的回调：把草稿写回父组件并自动进入 P3
 *
 * 容错设计：OCR 未接入（need_manual 或识别为空）时不报错阻断，而是提示商家
 * 用「示例菜单」继续，保证任何环境下都能走完整个引导流。
 */
function Step2({ items, onLoaded }: { items: DraftItem[]; onLoaded: (its: DraftItem[]) => void }) {
  const [busy, setBusy] = useState(false) // OCR 请求进行中
  const [msg, setMsg] = useState('') // 提示信息
  // 隐藏的 <input type="file">，用 ref 触发点击——因为原生文件选择框无法自定义样式
  const fileRef = useRef<HTMLInputElement>(null)

  /** 读取用户选的图片 → 转 base64 → 调 OCR → 把识别结果交给 onLoaded。 */
  const onFile = async (f: File) => {
    setBusy(true)
    setMsg('')
    try {
      const b64 = await toBase64(f)
      const r = await api.ocr(b64)
      // 识别服务没接好（本地 stub）或没识别出菜：不阻断，提示改用示例菜单
      if (r.result.need_manual || r.result.items.length === 0) {
        setMsg('识别服务当前为本地 stub（未接 PaddleOCR）。可先用「示例菜单」继续，正式环境会走服务端识别。')
      } else {
        // 识别出的每一项默认勾选（selected: true），进 P3 后商家只需「取消不要的」
        onLoaded(r.result.items.map((i) => ({ name: i.name, price: i.price, selected: true })))
      }
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="lc-stack">
      <p className="lc-muted">
        拍一页菜单就行。<span className="lc-badge lc-badge--warn">需联网</span> 识别在服务端完成；识别不准时可直接手动录入。
      </p>
      {/* 真正承载文件选择的原生 input，隐藏起来由下面的按钮代为触发 */}
      <input
        ref={fileRef}
        type="file"
        accept="image/*"
        style={{ display: 'none' }}
        onChange={(e) => {
          const f = e.target.files?.[0]
          if (f) void onFile(f)
        }}
      />
      <button className="lc-btn lc-btn--block" disabled={busy} onClick={() => fileRef.current?.click()}>
        {busy ? '识别中…' : '上传菜单照片'}
      </button>
      {/* 保底路径：直接用示例菜单继续。注意 map 里做了浅拷贝，避免改动 SAMPLE 常量本身 */}
      <button className="lc-btn lc-btn--ghost lc-btn--block" onClick={() => onLoaded(SAMPLE.map((s) => ({ ...s })))}>
        用示例菜单继续（{SAMPLE.length} 道）
      </button>
      {msg && <div className="lc-alert lc-alert--info">{msg}</div>}
      {items.length > 0 && (
        <div className="lc-alert lc-alert--info">已识别 {items.length} 道菜，点「上传菜单照片」可重新识别。</div>
      )}
    </div>
  )
}

/**
 * P3 · 挑要翻译的菜（只勾选 + 改价，零等待）。
 * 这一步决定后面翻译的输入集合：只有勾选的菜才会进入 P4 的翻译预览与 P5 的正式翻译。
 * 全部改动只发生在本地内存里，不发请求。
 *
 * @param items    当前菜品草稿
 * @param setItems 回写草稿（勾选 / 改名 / 改价都走它）
 * @param onNext   进入 P4
 */
function Step3({
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

/**
 * P4 · 核心多语言（全程唯一一次真生成 · 需联网）。
 * 商家在这里选目标语种，并即时看到「多语菜单长什么样」——这一步用的是免登录的
 * 预览接口 previewTranslate，产出只用于展示，真正的翻译在 P5 注册后才落到门店数据里。
 *
 * @param items      菜品草稿（只有 selected 的会被送去翻译）
 * @param targets    目标语种 code 列表
 * @param setTargets 回写目标语种（勾选/取消语种）
 * @param onNext     进入 P5
 */
function Step4({
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

/**
 * P5 · 引导注册（免费；注册后自动配券 + 生成双码）。
 * 这是整条流程的「落地」步骤，把前面几步攒下的东西一次性写进后端：
 *   登录/注册 → 建店 → 保存草稿 → 确认原文稿 → 按所选语种批量翻译。
 * 顺序不能乱：必须 confirmDraft（确认原文稿）之后才能 translate，否则没有可翻译的原文。
 *
 * @param items      菜品草稿（要落库的内容）
 * @param targets    目标语种
 * @param result     上线结果；非空即表示已完成，本组件切换成「上线成功」页
 * @param setResult  写入上线结果（触发成功页）
 */
function Step5({
  items,
  targets,
  result,
  setResult,
}: {
  items: DraftItem[]
  targets: string[]
  result: { store: Store; links: StoreLinks; coupon?: CouponTemplate } | null
  setResult: (v: { store: Store; links: StoreLinks; coupon?: CouponTemplate }) => void
}) {
  const [phone, setPhone] = useState('') // 手机号
  const [code, setCode] = useState('') // 短信验证码
  const [storeName, setStoreName] = useState('') // 店名（可留空，提交时兜底为「我的门店」）
  const [busy, setBusy] = useState(false) // 上线请求链进行中
  const [msg, setMsg] = useState('') // 提示信息
  const [sent, setSent] = useState(false) // 是否已发过验证码

  /** 发送验证码；开发模式回显 dev_code 便于联调。 */
  const sendOtp = async () => {
    setMsg('')
    try {
      const r = await api.sendOtp(phone)
      setSent(true)
      setMsg(r.dev_code ? `验证码已发送（开发模式：${r.dev_code}）` : '验证码已发送')
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  /**
   * 一键上线：登录 → 建店 → 落草稿 → 确认原文稿 → 翻译。
   * 这几步是严格的顺序依赖，任一步失败都会中断并提示，不会留下半成品门店的假成功。
   */
  const submit = async () => {
    setBusy(true)
    setMsg('')
    try {
      const { token } = await api.login(phone, code)
      tokenStore.set(token)
      // 店名允许留空，用「我的门店」兜底，避免建店被空名挡住
      const created = await api.createStore(storeName || '我的门店')
      // 只有确实有菜品时才走「落草稿 → 确认 → 翻译」三步
      if (items.length > 0) {
        await api.saveDraft(created.store.id, items)
        await api.confirmDraft(created.store.id)
        await api.translate(created.store.id, targets)
      }
      // 建店时后端已自动配好券模板并生成双码，这里一并带到结果页展示
      setResult({ store: created.store, links: created.links, coupon: created.coupon_template })
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // 已上线：渲染成功页，展示双码与门店页链接，引导进入经营后台
  if (result) {
    return (
      <div className="lc-stack">
        <div className="lc-card lc-card--accent">
          <h2>上线成功 🎉</h2>
          <p className="lc-muted" style={{ marginBottom: 0 }}>
            菜单已多语化（{targets.length} 个语种），复购券模板已自动配好（发券免费）。把下面两张码打印出来贴好即可。
          </p>
        </div>
        <div className="lc-card">
          <div className="lc-row" style={{ gap: 20, flexWrap: 'wrap' }}>
            <div style={{ textAlign: 'center' }}>
              <div className="qr-box">
                <QrImage url={result.links.qr_door} />
              </div>
              <div className="lc-dim" style={{ marginTop: 6 }}>门口码</div>
            </div>
            <div style={{ textAlign: 'center' }}>
              <div className="qr-box">
                <QrImage url={result.links.qr_table} />
              </div>
              <div className="lc-dim" style={{ marginTop: 6 }}>桌码</div>
            </div>
          </div>
          <div style={{ marginTop: 14 }}>
            {/* 再次强调物料链接不可变：印好后改链接要重印 */}
            <div className="lc-label">门店页链接（物料印好后不可变）</div>
            <a href={result.links.store_page} target="_blank" rel="noreferrer" style={{ wordBreak: 'break-all' }}>
              {result.links.store_page}
            </a>
          </div>
        </div>
        <a className="lc-btn lc-btn--primary lc-btn--block" href="/admin">
          进入经营后台 →
        </a>
      </div>
    )
  }

  return (
    <div className="lc-stack">
      <p className="lc-muted">
        注册只收手机号。注册后解锁：全量多语菜单、双码物料、复购券、评价数据。
        <span className="lc-badge lc-badge--ok" style={{ marginLeft: 6 }}>本期免费</span>
      </p>
      <div className="lc-card lc-stack" style={{ gap: 10 }}>
        <div>
          <label className="lc-label">店名</label>
          <input className="lc-input" value={storeName} placeholder="老张川菜馆" onChange={(e) => setStoreName(e.target.value)} />
        </div>
        <div>
          <label className="lc-label">手机号</label>
          <div className="lc-row" style={{ gap: 8 }}>
            <input className="lc-input" value={phone} placeholder="13800000000" onChange={(e) => setPhone(e.target.value)} />
            {/* 手机号至少 6 位才允许发码（前端粗校验） */}
            <button className="lc-btn" style={{ flexShrink: 0 }} disabled={phone.length < 6} onClick={sendOtp}>
              {sent ? '重发' : '获取验证码'}
            </button>
          </div>
        </div>
        <div>
          <label className="lc-label">验证码</label>
          <input className="lc-input" value={code} placeholder="6 位数字" onChange={(e) => setCode(e.target.value)} />
        </div>
      </div>
      {msg && <div className="lc-alert lc-alert--info">{msg}</div>}
      {/* 上线请求链较长，busy 期间禁用避免重复建店 */}
      <button
        className="lc-btn lc-btn--primary lc-btn--block"
        disabled={busy || phone.length < 6 || code.length < 4}
        onClick={submit}
      >
        {busy ? '正在上线…' : '注册并上线'}
      </button>
    </div>
  )
}

/**
 * 二维码图片。二维码接口需要鉴权，普通 <img src> 带不上 Authorization 头，
 * 故用 fetch 带 token 取回 blob，再转 objectURL 交给 <img> 渲染。
 *
 * @param url 二维码接口地址（StoreLinks.qr_door / qr_table）
 */
function QrImage({ url }: { url: string }) {
  const [src, setSrc] = useState('')
  useEffect(() => {
    // alive 标记：组件卸载后不再 setState，避免 React 告警
    let alive = true
    const token = tokenStore.get()
    fetch(url, { headers: token ? { Authorization: `Bearer ${token}` } : {} })
      .then((r) => r.blob())
      .then((b) => {
        if (alive) setSrc(URL.createObjectURL(b))
      })
      .catch(() => undefined)
    return () => {
      alive = false
    }
  }, [url])
  return src ? <img src={src} alt="二维码" /> : <div className="lc-dim" style={{ padding: 60 }}>生成中…</div>
}

/**
 * 把本地图片文件读成 base64 字符串（去掉 dataURL 前缀，只留纯数据段）。
 * OCR 接口 POST /api/ocr/extract 的 image_base64 字段要的就是纯 base64。
 */
function toBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => {
      const s = String(r.result)
      // readAsDataURL 得到的是 "data:image/png;base64,xxxx"，逗号后才是纯 base64
      resolve(s.includes(',') ? s.split(',')[1] : s)
    }
    r.onerror = () => reject(new Error('读取图片失败'))
    r.readAsDataURL(file)
  })
}
