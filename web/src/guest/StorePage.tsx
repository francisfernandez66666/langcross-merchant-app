import { useEffect, useMemo, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { api, type Coupon, type Dish, type Lang, type Store } from '../api/client'
import { PHRASES, langLabel, phraseText, t } from './i18n'

/** 浏览器语言 → 受支持语种（en-US → en）。 */
function detectLang(codes: string[]): string {
  const list = navigator.languages?.length ? navigator.languages : [navigator.language]
  for (const raw of list) {
    const base = String(raw || '').toLowerCase().split('-')[0]
    if (codes.includes(base)) return base
  }
  return codes.includes('en') ? 'en' : (codes[0] ?? 'en')
}

const LANG_KEY = 'lc_guest_lang'

/**
 * 客人端门店页。
 *
 * ★ 语言纪律（本轮修复的核心）：
 *   页面主标题永远是**目标语言**；原文只作为小字辅助。
 *   译文缺失时显示原文并明确标注「未翻译」，绝不让中文冒充英文大字。
 */
export default function StorePage() {
  const { slug = '' } = useParams()
  const [sp] = useSearchParams()
  const ref = sp.get('ref') ?? 'door'

  const [store, setStore] = useState<Store | null>(null)
  const [dishes, setDishes] = useState<Dish[]>([])
  const [couponContent, setCouponContent] = useState<Record<string, unknown>>({})
  const [langs, setLangs] = useState<Lang[]>([])
  // 语种优先级：URL ?lang= > 上次选择 > 浏览器语言
  const [lang, setLang] = useState<string>(() => sp.get('lang') || localStorage.getItem(LANG_KEY) || 'en')
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [rateOpen, setRateOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const [langOpen, setLangOpen] = useState(false)
  const [coupon, setCoupon] = useState<Coupon | null>(null)
  const [couponGift, setCouponGift] = useState('')
  const [couponChecked, setCouponChecked] = useState(false)

  // 语种注册表（后端是唯一真值，前端不硬编码语种清单）
  useEffect(() => {
    api
      .langs()
      .then((d) => {
        setLangs(d.langs ?? [])
        const codes = (d.langs ?? []).map((l) => l.code)
        setLang((cur) => (codes.includes(cur) ? cur : detectLang(codes)))
      })
      .catch(() => undefined)
  }, [])

  useEffect(() => {
    localStorage.setItem(LANG_KEY, lang)
  }, [lang])

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

  const current = langs.find((l) => l.code === lang)
  const catNames = store?.category_names?.[lang] ?? {}

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

  /** 菜名渲染：返回主标题 + 是否命中译文。 */
  const title = (d: Dish): { text: string; ok: boolean } => {
    if (lang === 'zh') return { text: d.name_zh, ok: true }
    const hit = d.names?.[lang]
    return hit ? { text: hit, ok: true } : { text: d.name_zh, ok: false }
  }

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
  if (err || !store) {
    return (
      <div className="guest warm gx-center" data-lang={lang}>
        <h2>{t('unavailable', lang)}</h2>
        <p className="lc-muted">{err || '404'}</p>
      </div>
    )
  }

  const signature = dishes.slice(0, 3)

  return (
    <div className="guest warm gx" data-lang={lang}>
      <header className="gx-hero">
        <div className="gx-hero-row">
          <div className="gx-brand">
            <h1 className="gx-store">{store.name}</h1>
            {store.coupon_enabled && <div className="gx-sub">{t('rate', lang)}</div>}
          </div>
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

        {/* ★ 兜底不依赖 WiFi：未填 WiFi 时整行不渲染 */}
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
                  <div className="gx-scard-name">
                    {ti.text}
                    {!ti.ok && <span className="gx-tag gx-tag--pending">{t('pending', lang)}</span>}
                  </div>
                  {lang !== 'zh' && <div className="gx-scard-orig">{d.name_zh}</div>}
                  {d.price != null && <div className="gx-scard-price">¥{d.price}</div>}
                </div>
              )
            })}
          </div>
        </section>
      )}

      {groups.map(([cat, list]) => {
        const catName = cat ? (catNames[cat] ?? cat) : t('menu', lang)
        const catOk = !cat || lang === 'zh' || Boolean(catNames[cat])
        return (
          <section className="gx-sec" key={cat || '_'}>
            <SectionHead label={catName} pending={catOk ? '' : t('pending', lang)} />
            <ul className="gx-dishes">
              {list.map((d) => {
                const ti = title(d)
                return (
                  <li className={`gx-dish ${d.sold_out ? 'is-out' : ''}`} key={d.id}>
                    <div className="gx-dish-body">
                      <div className="gx-dish-name">
                        <span className={ti.ok ? '' : 'gx-pending-text'}>{ti.text}</span>
                        {!ti.ok && <span className="gx-tag gx-tag--pending">{t('pending', lang)}</span>}
                        {d.sold_out && <span className="gx-tag gx-tag--out">{t('soldOut', lang)}</span>}
                        {!d.sold_out && d.remaining != null && (
                          <span className="gx-tag gx-tag--warn">
                            {t('left', lang)} {d.remaining}
                          </span>
                        )}
                      </div>
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

      {dishes.length === 0 && (
        <section className="gx-sec">
          <p className="lc-muted">{t('emptyMenu', lang)}</p>
        </section>
      )}

      {lang !== 'zh' && dishes.length > 0 && <p className="gx-note">{t('machineNote', lang)}</p>}

      <div className="gx-bar">
        <button className="gx-btn gx-btn--ghost" onClick={() => setHelpOpen(true)}>
          {t('ask', lang)}
        </button>
        <button className="gx-btn gx-btn--primary" onClick={() => setRateOpen(true)}>
          {t('rate', lang)}
        </button>
      </div>

      {rateOpen && (
        <RateSheet
          lang={lang}
          slug={slug}
          refCode={ref}
          onClose={() => setRateOpen(false)}
          onDone={(c, gift) => {
            setCoupon(c)
            setCouponGift(gift ?? '')
            setRateOpen(false) // 先关评价层，再弹券层，避免两层叠加
            setCouponChecked(true)
          }}
        />
      )}

      {helpOpen && <HelpSheet lang={lang} onClose={() => setHelpOpen(false)} />}

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

/** 区块标题：目标语言大字 + 细分隔线；缺译文时挂「未翻译」标。 */
function SectionHead({ label, pending, accent }: { label: string; pending?: string; accent?: boolean }) {
  return (
    <div className={`gx-sec-head ${accent ? 'is-accent' : ''}`}>
      <h2 className="gx-cat">{label}</h2>
      {pending && <span className="gx-tag gx-tag--pending">{pending}</span>}
      <span className="gx-rule" />
    </div>
  )
}

/** 语言选择弹层：全部受支持语言，母语写法 + 中文名。 */
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

/** 三维度评价弹层：菜品 / 服务 / 会再来吗。
 *  ★ 注意：入口参数不能叫 `ref`——那是 React 保留属性，传字符串会触发
 *  "Function components cannot have string refs" 并让整个弹层崩掉。故用 refCode。 */
function RateSheet(props: {
  lang: string
  slug: string
  refCode: string
  onClose: () => void
  onDone: (c: Coupon | null, gift?: string) => void
}) {
  const { lang, slug, refCode, onClose, onDone } = props
  const [dish, setDish] = useState(0)
  const [service, setService] = useState(0)
  const [ret, setRet] = useState(0)
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const ready = dish > 0 && service > 0 && ret > 0

  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    setErr('')
    try {
      const res = await api.submitRating(slug, {
        ref: refCode,
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
          <Stars label={t('q1', lang)} value={dish} onChange={setDish} />
          <Stars label={t('q2', lang)} value={service} onChange={setService} />
          <Stars label={t('q3', lang)} value={ret} onChange={setRet} />
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
            <button className="gx-btn gx-btn--primary" disabled={!ready || busy} onClick={submit}>
              {busy ? t('submitting', lang) : t('submit', lang)}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

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

/** 问店员卡：目标语言在上、中文在下，客人店员都能看懂；无网也能用。 */
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
              <div className="gx-phrase-main">{phraseText(p, lang)}</div>
              {lang !== 'zh' && <div className="gx-phrase-zh">{p.zh}</div>}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

/** 券卡：白底例外家族——截图即券，必须在任何背景下可读可扫。
 *  券面文案优先级：后端本地化结果（coupon_gift）→ 模板多语字段 → 英文 → 中文。 */
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
  const text = gift || String(content[lang] ?? content.en ?? content.zh ?? '')
  return (
    <div className="gx-mask" onClick={onClose}>
      <div className="gx-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="gx-sheet-grip" />
        {coupon ? (
          <>
            <h2 className="gx-coupon-title">{t('couponTitle', lang)}</h2>
            <div className="gx-coupon">
              {text && <div className="gx-coupon-gift">{text}</div>}
              <div className="gx-coupon-tip">{t('couponTip', lang)}</div>
              <div className="gx-coupon-code">{coupon.code}</div>
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
