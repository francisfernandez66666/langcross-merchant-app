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
 * ★ 渲染纪律 L1–L6（本文件与所有子组件的渲染决策最高优先级）
 * ─────────────────────────────────────────────────────────────
 *   L1 主标题永远是目标语言：绝不能让中文冒充英文大字——这是用户明确提过的痛点。
 *      落地：`dishTitle()` 命中译文用译文、否则回落原文，但一定同时走 L3 标注。
 *   L2 原文只作小字辅助（`.gx-dish-orig`），不能和译文抢视觉层级。
 *   L3 译文缺失时三处样式同时变化：显示原文 + 标注「未翻译」（`.gx-tag--pending`）
 *      + 弱化斜体（`.gx-pending-text`）+ 原文小字位置换成提示语（`.gx-dish-orig--hint`）。
 *      这三处全部由 `dishTitle().ok === false` 驱动，是 `{text, ok}` 返回值的唯一用途。
 *   L4 绝不编造：没有译文就留空、回落原文并标注，不许塞占位符或拿原文冒充译文。
 *   L5 非中文时必须标注「机器翻译」（`.gx-note`）；一道译文都没有时改标
 *      「尚未翻译，展示原文」（untranslatedNote）——没翻说成翻了是不诚实。
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
 *
 * ─────────────────────────────────────────────────────────────
 * 拆分结构（2026-10-21 FIX-13：本文件原 719 行）
 * ─────────────────────────────────────────────────────────────
 * 根组件只留「数据状态机 + 组装」，展示块与弹层各自成文件：
 *   storeShared.ts   检测语种 / 语种持久化键 / dishTitle / SectionHead
 *   StoreHero        店名 + 语言入口 + WiFi
 *   StoreSignature   招牌位（前 3 道菜）
 *   StoreMenu        分组菜单 + 空菜单兜底
 *   StoreLangSheet   选语言弹层
 *   StoreRateSheet   三维度评价弹层（含 Stars）
 *   StoreHelpSheet   问店员短语卡
 *   StoreCouponSheet 券卡
 */
import { useEffect, useMemo, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { api, type Coupon, type Dish, type Lang, type Store } from '../api/client'
import { t } from './i18n'
import GuestAssistant from './GuestAssistant'
import { LANG_KEY, detectLang, dishTitle } from './storeShared'
import { Hero } from './StoreHero'
import { Signature } from './StoreSignature'
import { Menu } from './StoreMenu'
import { LangSheet } from './StoreLangSheet'
import { RateSheet } from './StoreRateSheet'
import { HelpSheet } from './StoreHelpSheet'
import { CouponSheet } from './StoreCouponSheet'

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
 * 语言切换是**整页文案的开关**：`lang` 一变，`t()` 取词、`dishTitle()` 取菜名、
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
  // 菜名渲染交给共用函数 dishTitle（已抽到 storeShared）。
  // ★ 这里刻意保留局部名 `title` 且签名不变：子组件里几百处调用点仍是 title(d)，
  //   切过去的 JSX 才能逐字照搬、不做任何改名，从而把拆分风险压到最低。
  const title = (d: Dish) => dishTitle(d, lang)

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
  return (
    <div className="guest warm gx" data-lang={lang}>
      {/* 顶部 Hero：店名（原文，店名不翻译）+ 语言切换 + WiFi（可选） */}
      <Hero store={store} lang={lang} current={current} onLangOpen={() => setLangOpen(true)} />

      {/* 招牌推荐位（只做「本店招牌」，不用订单数据冒充「外国人常点」） */}
      <Signature dishes={dishes} lang={lang} title={title} />

      {/* 分组菜单（空菜单的安抚文案也在这个组件里） */}
      <Menu groups={groups} catNames={catNames} dishes={dishes} lang={lang} title={title} />

      {/* L5：非中文且确有菜单内容时，明确告知「机器翻译，疑问问店员」。
          ★ 2026-10-19 Q1：一道译文都没有时不能说「机器翻译」——如实告知
          「尚未翻译，展示的是原文」。没翻说成翻了，是另一种不诚实。 */}
      {lang !== 'zh' && dishes.length > 0 && (
        <p className="gx-note">
          {dishes.some((d) => Object.values(d.names ?? {}).some((v) => v))
            ? t('machineNote', lang)
            : t('untranslatedNote', lang)}
        </p>
      )}

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
      )}    </div>
  )
}
