import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  api,
  tokenStore,
  type Account,
  type Coupon,
  type Dish,
  type Insights,
  type Store,
  type StoreLinks,
} from '../api/client'

export default function AdminApp() {
  const [authed, setAuthed] = useState(Boolean(tokenStore.get()))
  if (!authed) return <Login onDone={() => setAuthed(true)} />
  return <Console onLogout={() => { tokenStore.clear(); setAuthed(false) }} />
}

/* ── 登录（手机号 + 验证码） ─────────────────────────────── */
function Login({ onDone }: { onDone: () => void }) {
  const [phone, setPhone] = useState('')
  const [code, setCode] = useState('')
  const [sent, setSent] = useState(false)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')

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

  const login = async () => {
    setBusy(true)
    setMsg('')
    try {
      const { token } = await api.login(phone, code)
      tokenStore.set(token)
      onDone()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="lc-center">
      <div className="lc-card" style={{ width: '100%', maxWidth: 380 }}>
        <h1>商家登录</h1>
        <p className="lc-muted">能言翻译助手 · 商家店端</p>
        <div className="lc-stack" style={{ gap: 10, marginTop: 12 }}>
          <div>
            <label className="lc-label">手机号</label>
            <div className="lc-row" style={{ gap: 8 }}>
              <input className="lc-input" value={phone} placeholder="13800000000" onChange={(e) => setPhone(e.target.value)} />
              <button className="lc-btn" style={{ flexShrink: 0 }} disabled={phone.length < 6} onClick={sendOtp}>
                {sent ? '重发' : '获取验证码'}
              </button>
            </div>
          </div>
          <div>
            <label className="lc-label">验证码</label>
            <input className="lc-input" value={code} onChange={(e) => setCode(e.target.value)} placeholder="6 位数字" />
          </div>
          {msg && <div className="lc-alert lc-alert--info">{msg}</div>}
          <button className="lc-btn lc-btn--primary lc-btn--block" disabled={busy || phone.length < 6 || code.length < 4} onClick={login}>
            {busy ? '登录中…' : '登录 / 注册'}
          </button>
          <Link className="lc-btn lc-btn--ghost lc-btn--block" to="/admin/onboarding">
            还没上线？先看引导流
          </Link>
        </div>
      </div>
    </div>
  )
}

/* ── 控制台外壳 ─────────────────────────────────────────── */
type Tab = 'overview' | 'menu' | 'coupon' | 'insights' | 'settings'

const TABS: { id: Tab; label: string }[] = [
  { id: 'overview', label: '概览' },
  { id: 'menu', label: '菜单' },
  { id: 'coupon', label: '券与核销' },
  { id: 'insights', label: '经营洞察' },
  { id: 'settings', label: '设置' },
]

function Console({ onLogout }: { onLogout: () => void }) {
  const [tab, setTab] = useState<Tab>('overview')
  const [account, setAccount] = useState<Account | null>(null)
  const [stores, setStores] = useState<Store[]>([])
  const [storeId, setStoreId] = useState('')
  const [err, setErr] = useState('')

  const reload = useCallback(() => {
    api
      .me()
      .then((d) => {
        setAccount(d.account)
        setStores(d.stores ?? [])
        setStoreId((cur) => cur || d.stores?.[0]?.id || '')
      })
      .catch((e: Error) => setErr(e.message))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  const store = stores.find((s) => s.id === storeId)

  if (err) {
    return (
      <div className="lc-center">
        <div className="lc-alert lc-alert--err">{err}</div>
        <button className="lc-btn" style={{ marginTop: 12 }} onClick={onLogout}>
          退出登录
        </button>
      </div>
    )
  }

  if (!store) {
    return (
      <div className="lc-center">
        <h2>还没有门店</h2>
        <p className="lc-muted">走一遍引导流，几分钟就能上线。</p>
        <Link className="lc-btn lc-btn--primary" to="/admin/onboarding">
          开始上线 →
        </Link>
      </div>
    )
  }

  return (
    <div className="admin-shell">
      <nav className="admin-nav">
        <div className="brand">能言 · 商家店端</div>
        {TABS.map((t) => (
          <button key={t.id} className={tab === t.id ? 'is-on' : ''} onClick={() => setTab(t.id)}>
            {t.label}
          </button>
        ))}
        <div style={{ flex: 1 }} />
        <div className="lc-dim" style={{ padding: '0 12px 8px' }}>{account?.phone}</div>
        <button onClick={onLogout}>退出登录</button>
      </nav>

      <main className="admin-main">
        {stores.length > 1 && (
          <div style={{ marginBottom: 16 }}>
            <label className="lc-label">门店</label>
            <select className="lc-select" style={{ maxWidth: 280 }} value={storeId} onChange={(e) => setStoreId(e.target.value)}>
              {stores.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
        )}

        {tab === 'overview' && <Overview store={store} />}
        {tab === 'menu' && <MenuTab store={store} />}
        {tab === 'coupon' && <CouponTab store={store} />}
        {tab === 'insights' && <InsightsTab store={store} />}
        {tab === 'settings' && <SettingsTab store={store} onSaved={reload} />}
      </main>
    </div>
  )
}

/* ── 概览：门店页 + 双码 ─────────────────────────────────── */
function Overview({ store }: { store: Store }) {
  const [links, setLinks] = useState<StoreLinks | null>(null)
  const [dishes, setDishes] = useState<Dish[]>([])

  useEffect(() => {
    api.getStore(store.id).then((d) => {
      setLinks(d.links)
      setDishes(d.dishes ?? [])
    }).catch(() => undefined)
  }, [store.id])

  return (
    <div className="lc-stack">
      <h1>{store.name}</h1>
      <div className="lc-row" style={{ gap: 8 }}>
        <span className="lc-badge lc-badge--ok">{store.service_tier === 'service' ? '外客服务档' : '纯翻译档'}</span>
        <span className="lc-badge">{store.coupon_enabled ? '发券中' : '未发券'}</span>
        <span className="lc-badge">菜品 {dishes.length} 道</span>
      </div>

      {links && (
        <div className="lc-card">
          <h2>物料与二维码</h2>
          <div className="lc-row" style={{ gap: 20, flexWrap: 'wrap', marginTop: 8 }}>
            <div style={{ textAlign: 'center' }}>
              <div className="qr-box">
                <QrImage url={links.qr_door} />
              </div>
              <div className="lc-dim" style={{ marginTop: 6 }}>门口码</div>
            </div>
            <div style={{ textAlign: 'center' }}>
              <div className="qr-box">
                <QrImage url={links.qr_table} />
              </div>
              <div className="lc-dim" style={{ marginTop: 6 }}>桌码</div>
            </div>
            <div style={{ flex: 1, minWidth: 240 }}>
              <div className="lc-label">门店页链接</div>
              <a href={links.store_page} target="_blank" rel="noreferrer" style={{ wordBreak: 'break-all' }}>
                {links.store_page}
              </a>
              <p className="lc-dim" style={{ marginTop: 10 }}>
                门口码贴在门外（进店前决策），桌码贴桌面（入座后评价领券）。链接印好后不要改，否则物料要重印。
              </p>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

/* ── 菜单：售罄 / 每日剩余 ───────────────────────────────── */
function MenuTab({ store }: { store: Store }) {
  const [dishes, setDishes] = useState<Dish[]>([])
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    api.listDishes(store.id).then((d) => setDishes(d.dishes ?? [])).catch((e: Error) => setMsg(e.message))
  }, [store.id])

  useEffect(() => {
    load()
  }, [load])

  const toggle = async (d: Dish) => {
    try {
      await api.patchDish(store.id, d.id, { sold_out: !d.sold_out })
      load()
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  return (
    <div className="lc-stack">
      <h1>菜单</h1>
      <p className="lc-muted">「每日剩余」是唯一需要每天维护的字段；不维护就不显示，不会报错。</p>
      {msg && <div className="lc-alert lc-alert--err">{msg}</div>}
      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>中文</th>
              <th>English</th>
              <th>分类</th>
              <th style={{ textAlign: 'right' }}>价格</th>
              <th style={{ textAlign: 'right' }}>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {dishes.map((d) => (
              <tr key={d.id}>
                <td>{d.name_zh}</td>
                <td>{d.name_en || <span className="lc-dim">未命中</span>}</td>
                <td>{d.category || '-'}</td>
                <td style={{ textAlign: 'right' }}>{d.price != null ? `¥${d.price}` : '-'}</td>
                <td style={{ textAlign: 'right' }}>
                  {d.sold_out ? <span className="lc-badge">售罄</span> : <span className="lc-badge lc-badge--ok">在售</span>}
                </td>
                <td style={{ textAlign: 'right' }}>
                  <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => toggle(d)}>
                    {d.sold_out ? '恢复' : '售罄'}
                  </button>
                </td>
              </tr>
            ))}
            {dishes.length === 0 && (
              <tr>
                <td colSpan={6} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无菜品，去引导流上传菜单
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

/* ── 券与核销 ───────────────────────────────────────────── */
function CouponTab({ store }: { store: Store }) {
  const [coupons, setCoupons] = useState<Coupon[]>([])
  const [code, setCode] = useState('')
  const [msg, setMsg] = useState('')
  const [okMsg, setOkMsg] = useState('')

  const load = useCallback(() => {
    api.listCoupons(store.id).then((d) => setCoupons(d.coupons ?? [])).catch(() => undefined)
  }, [store.id])

  useEffect(() => {
    load()
  }, [load])

  const redeem = async () => {
    setMsg('')
    setOkMsg('')
    try {
      const r = await api.redeem(store.id, code.trim())
      setOkMsg(`核销成功：${r.coupon.code}`)
      setCode('')
      load()
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  return (
    <div className="lc-stack">
      <h1>券与核销</h1>
      <div className="lc-card">
        <label className="lc-label">输入客人券码核销</label>
        <div className="lc-row" style={{ gap: 10, maxWidth: 420 }}>
          <input
            className="lc-input"
            value={code}
            placeholder="8 位券码"
            onChange={(e) => setCode(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && void redeem()}
          />
          <button className="lc-btn lc-btn--primary" disabled={code.trim().length < 4} onClick={redeem}>
            核销
          </button>
        </div>
        {msg && <div className="lc-alert lc-alert--err" style={{ marginTop: 10 }}>{msg}</div>}
        {okMsg && <div className="lc-alert lc-alert--info" style={{ marginTop: 10 }}>{okMsg}</div>}
      </div>

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>券码</th>
              <th>状态</th>
              <th>有效期至</th>
            </tr>
          </thead>
          <tbody>
            {coupons.map((c) => (
              <tr key={c.id}>
                <td style={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{c.code}</td>
                <td>
                  {c.status === 'redeemed' ? (
                    <span className="lc-badge lc-badge--ok">已核销</span>
                  ) : c.status === 'expired' ? (
                    <span className="lc-badge">已过期</span>
                  ) : (
                    <span className="lc-badge lc-badge--warn">待核销</span>
                  )}
                </td>
                <td>{new Date(c.expire_at).toLocaleDateString()}</td>
              </tr>
            ))}
            {coupons.length === 0 && (
              <tr>
                <td colSpan={3} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无券
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

/* ── 经营洞察（口径钉死：只算用我们工具的外国客数据） ──────── */
function InsightsTab({ store }: { store: Store }) {
  const [data, setData] = useState<Insights | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    api.insights(store.id).then(setData).catch((e: Error) => setErr(e.message))
  }, [store.id])

  if (err) return <div className="lc-alert lc-alert--err">{err}</div>
  if (!data) return <div className="lc-muted">加载中…</div>

  const pct = (v: number) => `${Math.round(v * 100)}%`

  return (
    <div className="lc-stack">
      <h1>经营洞察</h1>
      <div className="lc-alert lc-alert--info">{data.scope_note}</div>

      <div className="kpi">
        <div className="lc-card">
          <div className="lc-dim">扫码次数</div>
          <div className="kpi-num">{data.scans}</div>
        </div>
        <div className="lc-card">
          <div className="lc-dim">评价数</div>
          <div className="kpi-num">{data.rating.count}</div>
        </div>
        <div className="lc-card">
          <div className="lc-dim">复购意愿（会再来 ≥4）</div>
          <div className="kpi-num">{pct(data.rating.would_return)}</div>
        </div>
        <div className="lc-card">
          <div className="lc-dim">低分率（≤2）</div>
          <div className="kpi-num">{pct(data.rating.low_rate)}</div>
        </div>
        <div className="lc-card">
          <div className="lc-dim">发券 / 核销</div>
          <div className="kpi-num">
            {data.coupons_issued} / {data.coupons_redeemed}
          </div>
        </div>
      </div>

      <div className="lc-card">
        <h2>最近评价</h2>
        {data.recent_ratings.length === 0 && <p className="lc-dim">还没有评价。</p>}
        {data.recent_ratings.map((r) => (
          <div key={r.id} style={{ padding: '10px 0', borderBottom: '1px solid var(--lc-border)' }}>
            <div className="lc-row" style={{ gap: 12 }}>
              <span className="lc-badge">菜品 {r.score_dish}</span>
              <span className="lc-badge">服务 {r.score_service}</span>
              <span className="lc-badge">再来 {r.score_return}</span>
              {r.ref && <span className="lc-dim">{r.ref}</span>}
            </div>
            {r.comment_native && <div style={{ marginTop: 6 }}>{r.comment_native}</div>}
            {r.comment_zh && <div className="lc-dim">译：{r.comment_zh}</div>}
          </div>
        ))}
      </div>
    </div>
  )
}

/* ── 设置：档位开关 / WiFi / 券开关 ───────────────────────── */
function SettingsTab({ store, onSaved }: { store: Store; onSaved: () => void }) {
  const [tier, setTier] = useState(store.service_tier)
  const [couponEnabled, setCouponEnabled] = useState(store.coupon_enabled)
  const [wifiSsid, setWifiSsid] = useState(store.wifi_ssid ?? '')
  const [wifiPwd, setWifiPwd] = useState(store.wifi_pwd ?? '')
  const [msg, setMsg] = useState('')

  const save = async () => {
    setMsg('')
    try {
      await api.patchStore(store.id, {
        service_tier: tier,
        coupon_enabled: couponEnabled,
        wifi_ssid: wifiSsid,
        wifi_pwd: wifiPwd,
      })
      setMsg('已保存')
      onSaved()
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  return (
    <div className="lc-stack">
      <h1>设置</h1>
      <div className="lc-card lc-stack" style={{ gap: 14, maxWidth: 520 }}>
        <div>
          <label className="lc-label">交付档位（两档共用同一条链路，升级不重新上传）</label>
          <select className="lc-select" value={tier} onChange={(e) => setTier(e.target.value as Store['service_tier'])}>
            <option value="pure">纯翻译档（多语菜单 + 评价领券 + 问店员）</option>
            <option value="service">外客服务档（再开点菜 / 服务铃 / 每日剩余）</option>
          </select>
        </div>
        <div>
          <label className="lc-label">复购券（发券免费）</label>
          <label className="lc-row" style={{ gap: 8 }}>
            <input type="checkbox" checked={couponEnabled} onChange={(e) => setCouponEnabled(e.target.checked)} />
            <span className="lc-muted">开启后，客人评价即领券（游客店可关掉）</span>
          </label>
        </div>
        <div>
          <label className="lc-label">店内 WiFi（可选，不填也不影响）</label>
          <input className="lc-input" value={wifiSsid} placeholder="WiFi 名称" onChange={(e) => setWifiSsid(e.target.value)} />
          <div style={{ height: 8 }} />
          <input className="lc-input" value={wifiPwd} placeholder="WiFi 密码" onChange={(e) => setWifiPwd(e.target.value)} />
        </div>
        {msg && <div className="lc-alert lc-alert--info">{msg}</div>}
        <button className="lc-btn lc-btn--primary" onClick={save}>
          保存
        </button>
      </div>
    </div>
  )
}

function QrImage({ url }: { url: string }) {
  const [src, setSrc] = useState('')
  useEffect(() => {
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
