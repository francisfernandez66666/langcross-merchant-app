import { useEffect, useMemo, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { api, type Coupon, type Dish, type Store } from '../api/client'

type Lang = 'zh' | 'en'

const T: Record<Lang, Record<string, string>> = {
  zh: {
    signature: '本店招牌',
    menu: '菜单',
    rate: '评价本次体验 · 领优惠券',
    ask: '需要帮忙？',
    soldOut: '售罄',
    left: '余',
    loading: '加载中…',
    wifi: 'WiFi',
    close: '关闭',
    couponTitle: '感谢你的评价！',
    couponTip: '截图保存，到店出示即可核销',
    validUntil: '有效期至',
    noCoupon: '已收到你的评价，谢谢！',
    done: '完成',
    rateTitle: '这次体验怎么样？',
    q1: '菜品',
    q2: '服务',
    q3: '还会再来吗',
    commentPh: '想说点什么？（可选）',
    submit: '提交并领取优惠券',
    submitting: '提交中…',
    helpTitle: '请向店员出示',
  },
  en: {
    signature: 'House Favorites',
    menu: 'Menu',
    rate: 'Rate your visit · Get a coupon',
    ask: 'Need help?',
    soldOut: 'Sold out',
    left: 'left',
    loading: 'Loading…',
    wifi: 'WiFi',
    close: 'Close',
    couponTitle: 'Thanks for your review!',
    couponTip: 'Screenshot & show it at the store',
    validUntil: 'Valid until',
    noCoupon: 'Your review has been received. Thank you!',
    done: 'Done',
    rateTitle: 'How was your visit?',
    q1: 'Food',
    q2: 'Service',
    q3: 'Would you come back?',
    commentPh: 'Anything to add? (optional)',
    submit: 'Submit & get coupon',
    submitting: 'Submitting…',
    helpTitle: 'Please show this to our staff',
  },
}

/** 问店员常用短语（无网时的兜底也是这张卡）。 */
const PHRASES: { zh: string; en: string }[] = [
  { zh: '请给我菜单', en: 'Could I see the menu?' },
  { zh: '请不要放辣', en: 'Not spicy, please.' },
  { zh: '我对花生过敏', en: 'I am allergic to peanuts.' },
  { zh: '请不要放猪肉', en: 'No pork, please.' },
  { zh: '请帮我结账', en: 'The bill, please.' },
  { zh: '请打包', en: 'Could you pack this to go?' },
  { zh: '洗手间在哪里？', en: 'Where is the restroom?' },
  { zh: '可以刷卡吗？', en: 'Do you take credit cards?' },
]

export default function StorePage() {
  const { slug = '' } = useParams()
  const [sp] = useSearchParams()
  const ref = sp.get('ref') ?? 'door'

  const [store, setStore] = useState<Store | null>(null)
  const [dishes, setDishes] = useState<Dish[]>([])
  const [couponContent, setCouponContent] = useState<Record<string, unknown>>({})
  const [lang, setLang] = useState<Lang>('en')
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [rateOpen, setRateOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const [coupon, setCoupon] = useState<Coupon | null>(null)
  const [couponChecked, setCouponChecked] = useState(false)

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

  const groups = useMemo(() => {
    const m = new Map<string, Dish[]>()
    for (const d of dishes) {
      const k = d.category || '其他'
      const arr = m.get(k) ?? []
      arr.push(d)
      m.set(k, arr)
    }
    return Array.from(m.entries())
  }, [dishes])

  const t = (k: string) => T[lang][k] ?? k
  const name = (d: Dish) => (lang === 'en' && d.name_en ? d.name_en : d.name_zh)
  const secondary = (d: Dish) => (lang === 'en' ? d.name_zh : d.name_en ?? '')

  if (loading) return <div className="guest warm lc-center">{T[lang].loading}</div>
  if (err || !store) {
    return (
      <div className="guest warm lc-center">
        <h2>门店不可用</h2>
        <p className="lc-muted">{err || '未找到该门店'}</p>
      </div>
    )
  }

  return (
    <div className="guest warm">
      <header className="guest-hero">
        <div className="lc-row lc-row--between">
          <div>
            <h1 style={{ marginBottom: 2 }}>{store.name}</h1>
            <div className="lc-dim">
              {store.coupon_enabled ? (lang === 'en' ? 'Rate & get a coupon' : '评价即领券') : ''}
            </div>
          </div>
          <div className="guest-lang">
            <button className={lang === 'en' ? 'is-on' : ''} onClick={() => setLang('en')}>
              EN
            </button>
            <button className={lang === 'zh' ? 'is-on' : ''} onClick={() => setLang('zh')}>
              中文
            </button>
          </div>
        </div>

        {/* ★ 兜底不依赖 WiFi：未填 WiFi 时整行不渲染 */}
        {store.wifi_ssid && (
          <div className="lc-dim" style={{ marginTop: 10 }}>
            {t('wifi')}: {store.wifi_ssid}
            {store.wifi_pwd ? ` · ${store.wifi_pwd}` : ''}
          </div>
        )}
      </header>

      {/* 招牌推荐位（不用订单数据冒充"外国人常点"，只做本店招牌） */}
      {dishes.length >= 3 && (
        <section className="guest-sec">
          <h3>{t('signature')}</h3>
          <div className="lc-stack" style={{ gap: 8, marginTop: 8 }}>
            {dishes.slice(0, 2).map((d) => (
              <div key={d.id} className="lc-card lc-card--flat" style={{ padding: 12 }}>
                <div className="lc-row lc-row--between">
                  <div>
                    <div className="dish-name">{name(d)}</div>
                    <div className="dish-zh">{secondary(d)}</div>
                  </div>
                  {d.price != null && <div className="dish-price">¥{d.price}</div>}
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {groups.map(([cat, list]) => (
        <section className="guest-sec" key={cat}>
          <h3>{cat === '其他' ? t('menu') : cat}</h3>
          {list.map((d) => (
            <div className={`dish ${d.sold_out ? 'is-out' : ''}`} key={d.id}>
              <div className="dish-main">
                <div className="dish-name">
                  {name(d)}{' '}
                  {d.sold_out && <span className="lc-badge">{t('soldOut')}</span>}
                  {!d.sold_out && d.remaining != null && (
                    <span className="lc-badge lc-badge--warn">
                      {t('left')} {d.remaining}
                    </span>
                  )}
                </div>
                {secondary(d) && <div className="dish-zh">{secondary(d)}</div>}
              </div>
              {d.price != null && <div className="dish-price">¥{d.price}</div>}
            </div>
          ))}
        </section>
      ))}

      {dishes.length === 0 && (
        <div className="guest-sec">
          <p className="lc-muted">
            {lang === 'en' ? 'The menu is being prepared.' : '菜单正在准备中。'}
          </p>
        </div>
      )}

      <div className="guest-bar">
        <button className="lc-btn lc-btn--ghost" onClick={() => setHelpOpen(true)}>
          {t('ask')}
        </button>
        <button className="lc-btn lc-btn--primary lc-btn--block" onClick={() => setRateOpen(true)}>
          {t('rate')}
        </button>
      </div>

      {rateOpen && (
        <RateSheet
          lang={lang}
          slug={slug}
          ref={ref}
          couponContent={couponContent}
          onClose={() => setRateOpen(false)}
          onDone={(c) => {
            setCoupon(c)
            setCouponChecked(true)
          }}
        />
      )}

      {helpOpen && (
        <div className="modal-mask" onClick={() => setHelpOpen(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <div className="lc-row lc-row--between" style={{ marginBottom: 12 }}>
              <h2 style={{ margin: 0 }}>{t('ask')}</h2>
              <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => setHelpOpen(false)}>
                {t('close')}
              </button>
            </div>
            <div className="lc-stack" style={{ gap: 8 }}>
              {PHRASES.map((p) => (
                <div key={p.zh} className="lc-card lc-card--flat" style={{ padding: '10px 12px' }}>
                  <div style={{ fontWeight: 700 }}>{p.en}</div>
                  <div className="lc-dim">{p.zh}</div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {couponChecked && (
        <CouponSheet lang={lang} coupon={coupon} onClose={() => setCouponChecked(false)} />
      )}
    </div>
  )
}

/** 三维度评价弹层：菜品 / 服务 / 会再来吗。 */
function RateSheet(props: {
  lang: Lang
  slug: string
  ref: string
  couponContent: Record<string, unknown>
  onClose: () => void
  onDone: (c: Coupon | null) => void
}) {
  const { lang, slug, ref, onClose, onDone } = props
  const t = (k: string) => T[lang][k] ?? k

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
        ref,
        score_dish: dish,
        score_service: service,
        score_return: ret,
        comment: comment.trim(),
        lang,
      })
      onDone(res.coupon)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-mask" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <h2>{t('rateTitle')}</h2>
        <div className="lc-stack" style={{ gap: 16, marginTop: 6 }}>
          <Stars label={t('q1')} value={dish} onChange={setDish} />
          <Stars label={t('q2')} value={service} onChange={setService} />
          <Stars label={t('q3')} value={ret} onChange={setRet} />
          <textarea
            className="lc-textarea"
            placeholder={t('commentPh')}
            value={comment}
            onChange={(e) => setComment(e.target.value)}
          />
          {err && <div className="lc-alert lc-alert--err">{err}</div>}
          <div className="lc-row" style={{ gap: 10 }}>
            <button className="lc-btn lc-btn--ghost" onClick={onClose}>
              {t('close')}
            </button>
            <button className="lc-btn lc-btn--primary lc-btn--block" disabled={!ready || busy} onClick={submit}>
              {busy ? t('submitting') : t('submit')}
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
      <div className="lc-label" style={{ marginBottom: 2 }}>
        {label}
      </div>
      <div className="lc-stars">
        {[1, 2, 3, 4, 5].map((i) => (
          <button
            key={i}
            type="button"
            className={`lc-star ${i <= value ? 'is-on' : ''}`}
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

/** 券卡：白底例外家族——截图即券，必须在任何背景下可读可扫。 */
function CouponSheet({ lang, coupon, onClose }: { lang: Lang; coupon: Coupon | null; onClose: () => void }) {
  const t = (k: string) => T[lang][k] ?? k
  return (
    <div className="modal-mask" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        {coupon ? (
          <>
            <h2>{t('couponTitle')}</h2>
            <div className="coupon" style={{ marginTop: 10 }}>
              <div style={{ fontSize: 13, color: '#5c5c63' }}>{t('couponTip')}</div>
              <div className="coupon-code">{coupon.code}</div>
              <div style={{ fontSize: 12.5, color: '#8e8e96' }}>
                {t('validUntil')} {new Date(coupon.expire_at).toLocaleDateString()}
              </div>
            </div>
          </>
        ) : (
          <h2>{t('noCoupon')}</h2>
        )}
        <button className="lc-btn lc-btn--primary lc-btn--block" style={{ marginTop: 16 }} onClick={onClose}>
          {t('done')}
        </button>
      </div>
    </div>
  )
}
