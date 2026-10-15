/**
 * 客人端门店页（路由 `/s/:slug`）。
 *
 * ─────────────────────────────────────────────────────────────
 * 业务定位
 * ─────────────────────────────────────────────────────────────
 * 这是 LangCross 的「门面」：外国客人扫桌码（`?ref=t01`）或门口码（`?ref=door`）
 * 打开本页，页面必须**用客人自己的语言**呈现菜单。核心闭环：
 *   看菜单 → 提交三维度评价（菜品/服务/会再来吗）→ 自动领券 → 到店核销。
 * 发券是免费的、不卡付费墙——评价完成即发，这是产品获客的钩子。
 *
 * ─────────────────────────────────────────────────────────────
 * ★ 渲染纪律 L1–L6（本文件所有渲染决策的最高优先级，逐条对应下方代码）
 * ─────────────────────────────────────────────────────────────
 *   L1 主标题永远是目标语言：绝不能让中文冒充英文大字——这是用户明确提过的痛点。
 *      落地：`title(d)` 命中译文用译文、否则回落原文，但一定同时走 L3 标注。
 *   L2 原文只作小字辅助（`.gx-dish-orig`），不能和译文抢视觉层级。
 *   L3 译文缺失时三处样式同时变化：显示原文 + 标注「未翻译」（`.gx-tag--pending`）
 *      + 弱化斜体（`.gx-pending-text`）+ 原文小字位置换成提示语（`.gx-dish-orig--hint`）。
 *      这三处全部由 `title(d).ok === false` 驱动，是 `{text, ok}` 返回值的唯一用途。
 *   L4 绝不编造：没有译文就留空、回落原文并标注，不许塞占位符或拿原文冒充译文。
 *   L5 非中文时必须标注「机器翻译」（`.gx-note`），见下方 `machineNote` 那行。
 *   L6 不用 `text-transform: uppercase`（很多语种会破坏字形）——样式约束，勿加。
 *
 * 另外必须同步两处浏览器级元信息：
 *   - `document.documentElement.lang`：影响浏览器字体回退、CJK/泰文断行、读屏发音。
 *   - `document.title`：浏览器标签页 / 分享预览标题。
 * 二者刻意拆成两个独立 effect（详见下方注释），切语言时只改标题、不重拉门店数据。
 *
 * ─────────────────────────────────────────────────────────────
 * 数据来源与不变量
 * ─────────────────────────────────────────────────────────────
 *   - 语种清单**一律来自** `GET /api/translation/langs`，前端绝不硬编码语种数组
 *     （新增语种 = 后端注册表登记一行，前端零改动）。
 *   - 菜品接口返回的是**扁平数组**，按 `category` 分组在**前端**做（见 `groups`）。
 *   - 原文语种固定 `zh`，`name_zh` 始终是原文真值，任何情况下都保留。
 */

import { useEffect, useMemo, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { api, type Coupon, type Dish, type Lang, type Store } from '../api/client'
import { PHRASES, langLabel, phraseText, t } from './i18n'
import GuestAssistant from './GuestAssistant'

/**
 * 浏览器语言 → 受支持语种（en-US → en）。
 *
 * 入参 `codes` 是后端注册表给出的受支持语种短码列表。
 * 逐个拿 `navigator.languages`（用户偏好顺序）去匹配，命中即返回。
 * 全不命中时回落 `en`；连 `en` 都没有（理论上不会）才退回注册表首项。
 * 之所以不用 `navigator.language` 单值：多语用户的语言列表顺序才是真实偏好。
 */
function detectLang(codes: string[]): string {
  const list = navigator.languages?.length ? navigator.languages : [navigator.language]
  for (const raw of list) {
    const base = String(raw || '').toLowerCase().split('-')[0]
    if (codes.includes(base)) return base
  }
  return codes.includes('en') ? 'en' : (codes[0] ?? 'en')
}

/** localStorage 里记住的「上次选择语言」，用于回访时免选。与 URL `?lang=` 分属不同优先级档。 */
const LANG_KEY = 'lc_guest_lang'

/**
 * 客人端门店页根组件（无 props，全靠路由参数与查询串驱动）。
 *
 * 页面自上而下：
 *   1. Hero 头部：店名（原文，不翻译）+ 语言切换按钮 + WiFi 行（可选兜底）。
 *   2. 招牌推荐位（仅当菜品 ≥3 时出现）。
 *   3. 按分类分组的菜单列表。
 *   4. 机器翻译声明（L5，非中文时才出现）。
 *   5. 底部固定操作条：「需要帮忙？」+「评价领券」。
 *   6. 四个弹层：评价（RateSheet）/ 问店员（HelpSheet）/ 选语言（LangSheet）/ 券卡（CouponSheet）。
 *
 * 语言切换是**整页文案的开关**：`lang` 一变，`t()` 取词、`title()` 取菜名、
 * `document.title` 与 `<html lang>` 全部跟着变，无需重新请求门店数据。
 */
export default function StorePage() {
  const { slug = '' } = useParams() // 门店短链标识；物料已印死，不可变
  const [sp] = useSearchParams()
  const ref = sp.get('ref') ?? 'door' // 来源码：桌码 t01… / 门口码 door，用于归因与券绑定

  const [store, setStore] = useState<Store | null>(null)
  const [dishes, setDishes] = useState<Dish[]>([])
  // 券模板的多语字段（后端下发的原始模板内容），作为券面文案的最后兜底
  const [couponContent, setCouponContent] = useState<Record<string, unknown>>({})
  const [langs, setLangs] = useState<Lang[]>([]) // 后端注册表返回的受支持语种清单
  // 语种优先级：URL ?lang= > 上次选择 > 浏览器语言（浏览器语言在拿到注册表后由 detectLang 决定）
  const [lang, setLang] = useState<string>(() => sp.get('lang') || localStorage.getItem(LANG_KEY) || 'en')
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [rateOpen, setRateOpen] = useState(false) // 三维度评价弹层
  const [helpOpen, setHelpOpen] = useState(false) // 问店员短语卡
  const [assistOpen, setAssistOpen] = useState(false) // AI 点菜助手面板
  const [langOpen, setLangOpen] = useState(false) // 语言选择弹层
  const [coupon, setCoupon] = useState<Coupon | null>(null) // 评价成功后后端发的券
  const [couponGift, setCouponGift] = useState('') // 券面文案（后端已本地化）
  const [couponChecked, setCouponChecked] = useState(false) // 是否展示券卡弹层

  // 语种注册表（后端是唯一真值，前端不硬编码语种清单）。
  // 拉到清单后：① 存进 langs 供选择器渲染；② 校验当前 lang 是否受支持，
  // 不支持（如 URL 带了野语种）则用浏览器语言兜底，避免页面出现空词典。
  useEffect(() => {
    api
      .langs()
      .then((d) => {
        setLangs(d.langs ?? [])
        const codes = (d.langs ?? []).map((l) => l.code)
        setLang((cur) => (codes.includes(cur) ? cur : detectLang(codes)))
      })
      .catch(() => undefined) // 注册表拉取失败不阻塞页面：沿用已有 lang，词典走 en 回落
  }, [])

  // 语言副作用：持久化选择 + 同步 <html lang>。
  useEffect(() => {
    localStorage.setItem(LANG_KEY, lang)
    // ★ 同步 <html lang>：影响浏览器字体回退、CJK/泰文断行、读屏发音。
    // 不更新的话，日语页面会按 zh-CN 选字体，泰文断行也可能出错。
    document.documentElement.lang = lang
  }, [lang])

  // 拉门店公开数据（无需登录）。依赖 slug + ref：换门店或换来源码才重拉。
  useEffect(() => {
    setLoading(true)
    api
      .publicStore(slug, ref)
      .then((d) => {
        setStore(d.store)
        setDishes(d.dishes ?? [])
        setCouponContent((d.coupon_template?.content as Record<string, unknown>) ?? {})
      })
      .catch((e: Error) => setErr(e.message))
      .finally(() => setLoading(false))
  }, [slug, ref])

  // 浏览器标签页 / 分享预览标题 = 店名 + 目标语言的「菜单」。
  // 单独一个 effect：切语言时只改标题，不重新拉门店数据。
  // 若与上面的数据 effect 合并，切语言会触发整页重新请求，白白多一次网络往返。
  useEffect(() => {
    if (store?.name) document.title = `${store.name} · ${t('menu', lang)}`
  }, [lang, store?.name])

  const current = langs.find((l) => l.code === lang) // 当前语种的元信息（母语名等），供头部按钮显示
  const catNames = store?.category_names?.[lang] ?? {} // 当前语言的「原文分类 → 译文」映射

  // 菜品分组：后端返回扁平数组，分组放到前端做。
  // 原因：后端只管数据真值，不关心展示顺序；前端按 category 原序聚合，保留后端给的排序语义。
  // 用 Map 而非对象，保证插入顺序稳定且 key 可含任意字符。
  const groups = useMemo(() => {
    const m = new Map<string, Dish[]>()
    for (const d of dishes) {
      const k = d.category || ''
      const arr = m.get(k) ?? []
      arr.push(d)
      m.set(k, arr)
    }
    return Array.from(m.entries())
  }, [dishes])

  /**
   * 菜名渲染：返回主标题 + 是否命中译文。
   *
   * ★ L1/L3 的核心函数。返回 `{text, ok}` 的设计意图：
   *   - `ok === true`：text 是目标语言译文（或 lang 为 zh 时的原文），正常大字渲染。
   *   - `ok === false`：没有译文，text 回落成 `name_zh`（原文），同时由调用方
   *     触发 L3 的三处样式变化（未翻译标 + 弱化斜体 + 原文小字换提示语）。
   *   调用方**必须**同时消费 `text` 和 `ok`，只取 `text` 会退化成「中文冒充英文大字」。
   */
  const title = (d: Dish): { text: string; ok: boolean } => {
    if (lang === 'zh') return { text: d.name_zh, ok: true } // 客人选了中文：原文即目标语言，算命中
    const hit = d.names?.[lang]
    return hit ? { text: hit, ok: true } : { text: d.name_zh, ok: false } // L4：不编造，回落原文并交由 ok=false 标注
  }

  // 加载态：整屏居中 spinner + 目标语言的「加载中…」
  if (loading) {
    return (
      <div className="guest warm gx-center" data-lang={lang}>
        <div className="gx-spinner" />
        <div className="lc-muted" style={{ marginTop: 14 }}>
          {t('loading', lang)}
        </div>
      </div>
    )
  }
  // 错误 / 门店不存在：目标语言的「门店不可用」+ 具体错误信息（含 404）
  if (err || !store) {
    return (
      <div className="guest warm gx-center" data-lang={lang}>
        <h2>{t('unavailable', lang)}</h2>
        <p className="lc-muted">{err || '404'}</p>
      </div>
    )
  }

  // 招牌位取前 3 道菜（后端已按商家意图排序）。
  // 刻意只叫「本店招牌」，不叫「外国人常点」——后者需要订单数据支撑，当前没有，不冒充。
  const signature = dishes.slice(0, 3)

  return (
    <div className="guest warm gx" data-lang={lang}>
      {/* 顶部 Hero：店名（原文，店名不翻译）+ 语言切换 + WiFi（可选） */}
      <header className="gx-hero">
        <div className="gx-hero-row">
          <div className="gx-brand">
            {/* 店名是专有名词，按原文展示，不进翻译链 */}
            <h1 className="gx-store">{store.name}</h1>
            {/* 只有开了券的店才提「评价领券」，避免客人做了评价却拿不到券 */}
            {store.coupon_enabled && <div className="gx-sub">{t('rate', lang)}</div>}
          </div>
          {/* 语言切换入口：显示当前语言的母语写法，未知语种时退回大写短码 */}
          <button className="gx-langbtn" onClick={() => setLangOpen(true)} aria-label={t('chooseLang', lang)}>
            <span className="gx-globe" aria-hidden>
              ◍
            </span>
            <span className="gx-langbtn-txt">{current ? langLabel(current) : lang.toUpperCase()}</span>
            <span className="gx-caret" aria-hidden>
              ⌄
            </span>
          </button>
        </div>

        {/* ★ 兜底不依赖 WiFi：未填 WiFi 时整行不渲染。
            注意这是「告知客人 WiFi 密码」的行，不是页面自身的联网依赖。 */}
        {store.wifi_ssid && (
          <div className="gx-wifi">
            <span className="gx-wifi-k">{t('wifi', lang)}</span>
            <span className="gx-wifi-v">
              {store.wifi_ssid}
              {store.wifi_pwd ? ` · ${store.wifi_pwd}` : ''}
            </span>
          </div>
        )}
      </header>

      {/* 招牌推荐位（只做「本店招牌」，不用订单数据冒充「外国人常点」） */}
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

      {/* L5：非中文且确有菜单内容时，明确告知「机器翻译，疑问问店员」 */}
      {lang !== 'zh' && dishes.length > 0 && <p className="gx-note">{t('machineNote', lang)}</p>}

      {/* 底部固定操作条：AI 点菜助手（首选） / 问店员（无网兜底） / 评价领券（主转化）。
          三个按钮在窄屏下各占一份宽度，文案已尽量短，必要时可换行。 */}
      <div className="gx-bar">
        <button className="gx-btn gx-btn--primary" onClick={() => setAssistOpen(true)}>
          🤖 {t('aiTitle', lang)}
        </button>
        <button className="gx-btn gx-btn--ghost" onClick={() => setHelpOpen(true)}>
          {t('ask', lang)}
        </button>
        <button className="gx-btn gx-btn--ghost" onClick={() => setRateOpen(true)}>
          {t('rate', lang)}
        </button>
      </div>

      {/* 评价层：完成评价后由 onDone 触发领券。★ 传 refCode 而非 ref，见 RateSheet 说明 */}
      {rateOpen && (
        <RateSheet
          lang={lang}
          slug={slug}
          refCode={ref} // ★ 属性名不能叫 ref（React 保留），故用 refCode
          onClose={() => setRateOpen(false)}
          onDone={(c, gift) => {
            setCoupon(c)
            setCouponGift(gift ?? '')
            setRateOpen(false) // 先关评价层，再弹券层，避免两层叠加
            setCouponChecked(true)
          }}
        />
      )}

      {/* 问店员短语卡（无网兜底） */}
      {helpOpen && <HelpSheet lang={lang} onClose={() => setHelpOpen(false)} />}

      {/* AI 点菜助手面板：语音 + 文字问答 + 加菜/服务铃动作。
          转真人店员时先关助手、再开短语卡（两者互斥一层，避免叠加）。 */}
      {assistOpen && (
        <GuestAssistant
          slug={slug}
          lang={lang}
          refCode={ref}
          onClose={() => setAssistOpen(false)}
          onTalkToStaff={() => {
            setAssistOpen(false)
            setHelpOpen(true)
          }}
        />
      )}

      {/* 语言选择层：选中即写回 lang 并关闭 */}
      {langOpen && (
        <LangSheet
          langs={langs}
          current={lang}
          onPick={(code) => {
            setLang(code)
            setLangOpen(false)
          }}
          onClose={() => setLangOpen(false)}
        />
      )}

      {/* 券卡层：评价成功后展示；coupon 可能为 null（如该店未开券） */}
      {couponChecked && (
        <CouponSheet
          lang={lang}
          coupon={coupon}
          gift={couponGift}
          content={couponContent}
          onClose={() => setCouponChecked(false)}
        />
      )}
    </div>
  )
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
function SectionHead({ label, pending, accent }: { label: string; pending?: string; accent?: boolean }) {
  return (
    <div className={`gx-sec-head ${accent ? 'is-accent' : ''}`}>
      <h2 className="gx-cat">{label}</h2>
      {pending && <span className="gx-tag gx-tag--pending">{pending}</span>}
      <span className="gx-rule" />
    </div>
  )
}

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
function LangSheet({
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
function RateSheet(props: {
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
function HelpSheet({ lang, onClose }: { lang: string; onClose: () => void }) {
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
function CouponSheet({
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
