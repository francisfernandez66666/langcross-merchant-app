/**
 * 商家端管理台 · 入口与各功能页（挂在路由 /admin/* 下，见 App.tsx）。
 *
 * 本文件负责「门店已上线之后」的全部日常经营操作，是引导流（Onboarding.tsx）的下游：
 * 引导流只负责把门店「生出来」，之后每天的菜单维护、发券核销、看数据都在这里。
 *
 * 页面结构（左侧导航切换的 Tab，均为本文件内的子组件）：
 *   - 概览 Overview：门店状态徽标 + 门口码 / 桌码 / 门店页链接（物料）
 *   - 菜单 MenuTab：语种覆盖率一览、「补充语种」批量补翻、售罄开关
 *   - 券与核销 CouponTab：输入客人券码核销 + 已发券列表
 *   - 经营洞察 InsightsTab：扫码 / 评价 / 复券 KPI 与最近评价
 *   - 服务请求 RequestsTab：客人发起的服务铃 / 加水 / 加菜 / 买单待办（轮询 + 完成/取消）
 *   - AI 凭据 AICredentialsTab：平台超管维护 LLM 凭据（仅超管可见；tenant_admin 只读）
 *   - 设置 SettingsTab：交付档位、复购券开关、店内 WiFi
 *
 * 涉及的接口（全部经 ../api/client 调用；响应统一为 {ok:true,...}
 * 或 {ok:false,error:{code,msg}}，错误由 client 抛成 Error 供这里 catch）：
 *   - POST /api/auth/otp、POST /api/auth/login：手机号验证码登录
 *   - GET  /api/me：取当前账号 + 名下门店列表
 *   - GET  /api/stores/:id：门店详情（含 links 物料与 dishes 菜单）
 *   - GET  /api/stores/:id/dishes、PATCH .../dishes/:dishId：菜单读取与售罄切换
 *   - GET  /api/stores/:id/coupons、POST .../coupon/redeem：券列表与核销
 *   - GET  /api/stores/:id/insights：经营洞察
 *   - PATCH /api/stores/:id：保存门店设置
 *   - GET  /api/translation/langs：语种注册表（前端语种清单的唯一来源）
 *   - POST /api/stores/:id/translate：按语种批量补翻
 *
 * 登录态：token 存 localStorage（tokenStore），client.ts 会自动给每个请求带上
 * Authorization: Bearer <token>；本文件只在登录 / 登出时读写这个 token。
 */
import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  api,
  tokenStore,
  type Account,
  type Coupon,
  type Dish,
  type Insights,
  type Lang,
  type Store,
  type StoreLinks,
} from '../api/client'
import RequestsTab from './RequestsTab'
import AICredentialsTab from './AICredentialsTab'

/**
 * 管理台根组件：只做一件事——「有 token 就给控制台，没 token 就给登录页」。
 * 刻意不做路由级守卫：登录态完全由 localStorage 里的 token 决定，
 * 所以刷新页面仍保持登录，退出登录也只是清 token 并把 authed 置回 false。
 */
export default function AdminApp() {
  // 初始值直接读 localStorage：刷新后能凭已存 token 免登录进入控制台。
  const [authed, setAuthed] = useState(Boolean(tokenStore.get()))
  if (!authed) return <Login onDone={() => setAuthed(true)} />
  // 退出登录 = 清 token + 回到登录页；两件事必须一起做，否则会留下「有 UI 无凭证」的假登录态。
  return <Console onLogout={() => { tokenStore.clear(); setAuthed(false) }} />
}

/* ── 登录（手机号 + 验证码） ─────────────────────────────── */
/**
 * 登录 / 注册页（手机号 + 短信验证码）。
 * 同一入口既登录也注册：后端对未注册过的手机号会自动建账号，所以商家不需要单独的注册流程。
 * 成功后把 token 落盘（tokenStore）并回调 onDone，由父组件切到控制台。
 *
 * @param onDone 登录成功回调，父组件据此把 authed 置 true 进入控制台
 */
function Login({ onDone }: { onDone: () => void }) {
  // 全部为受控输入：值来自 state，输入变化只改 state，不读 DOM。
  const [phone, setPhone] = useState('') // 手机号
  const [code, setCode] = useState('') // 短信验证码
  const [sent, setSent] = useState(false) // 是否已发过码，用于把按钮文案从「获取验证码」切成「重发」
  const [busy, setBusy] = useState(false) // 登录请求进行中，用于禁用按钮防重复提交
  const [msg, setMsg] = useState('') // 统一提示条：错误信息或「已发送」文案

  /** 发送验证码。开发模式下后端会把验证码回显在 dev_code 里，直接展示便于联调。 */
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

  /** 校验验证码换 token。token 落盘后立刻通知父组件切页。 */
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
              {/* 受控输入：value 绑 phone，onChange 只写回 state */}
              <input className="lc-input" value={phone} placeholder="13800000000" onChange={(e) => setPhone(e.target.value)} />
              {/* 手机号至少 6 位才允许发码，纯粹是前端体验上的粗校验，真正校验在后端 */}
              <button className="lc-btn" style={{ flexShrink: 0 }} disabled={phone.length < 6} onClick={sendOtp}>
                {sent ? '重发' : '获取验证码'}
              </button>
            </div>
          </div>
          <div>
            <label className="lc-label">验证码</label>
            <input className="lc-input" value={code} onChange={(e) => setCode(e.target.value)} placeholder="6 位数字" />
          </div>
          {/* 提示条：错误与成功共用同一条 msg */}
          {msg && <div className="lc-alert lc-alert--info">{msg}</div>}
          {/* busy 期间禁用，避免重复提交；码长度 < 4 视为明显没填完 */}
          <button className="lc-btn lc-btn--primary lc-btn--block" disabled={busy || phone.length < 6 || code.length < 4} onClick={login}>
            {busy ? '登录中…' : '登录 / 注册'}
          </button>
          {/* 给还没建店的商家一个入口，直接去走引导流 */}
          <Link className="lc-btn lc-btn--ghost lc-btn--block" to="/admin/onboarding">
            还没上线？先看引导流
          </Link>
        </div>
      </div>
    </div>
  )
}

/* ── 控制台外壳 ─────────────────────────────────────────── */
// Tab 用字符串联合类型而非枚举，配合下面的 TABS 常量做「数据驱动」渲染，
// 新增一个 Tab 只需在联合类型和 TABS 各加一处，导航栏自动出现。
type Tab = 'overview' | 'menu' | 'coupon' | 'insights' | 'requests' | 'ai' | 'settings'

// 左侧导航项：id 用于匹配当前 Tab，label 是展示文案。
// 顺序即商家日常使用的优先级：先看概览、再维护菜单，券/洞察/服务请求靠后。
// 「AI 凭据」是平台超管专属入口，普通商家（manager/staff）看不到这一项。
const TABS: { id: Tab; label: string }[] = [
  { id: 'overview', label: '概览' },
  { id: 'menu', label: '菜单' },
  { id: 'coupon', label: '券与核销' },
  { id: 'insights', label: '经营洞察' },
  { id: 'requests', label: '服务请求' },
  { id: 'ai', label: 'AI 凭据' },
  { id: 'settings', label: '设置' },
]

/**
 * 控制台外壳：登录后的数据装载、门店切换、Tab 切换都在这里，各 Tab 的具体业务拆到子组件。
 *
 * @param onLogout 退出登录回调（清 token 并回到登录页）
 */
function Console({ onLogout }: { onLogout: () => void }) {
  const [tab, setTab] = useState<Tab>('overview') // 当前选中的 Tab，默认落在概览
  const [account, setAccount] = useState<Account | null>(null) // 当前登录账号（用于显示手机号）
  const [stores, setStores] = useState<Store[]>([]) // 该账号名下的门店列表
  const [storeId, setStoreId] = useState('') // 当前选中的门店 id
  const [err, setErr] = useState('') // 装载失败信息（多为 token 失效 / 网络问题）

  /** 拉取账号与门店列表。登出后重新登录、或设置页保存后都会调用它刷新。 */
  const reload = useCallback(() => {
    api
      .me()
      .then((d) => {
        setAccount(d.account)
        setStores(d.stores ?? [])
        // 关键：只在 storeId 还是空串时才用「第一家门店」兜底，
        // 否则刷新会把商家手动切到的门店重置掉（cur || first 的短路顺序不能反）。
        setStoreId((cur) => cur || d.stores?.[0]?.id || '')
      })
      .catch((e: Error) => setErr(e.message))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  // 当前门店对象：可能是 undefined（加载中 / 该账号一家店都没有）。
  const store = stores.find((s) => s.id === storeId)

  // 装载失败优先展示（最常见是 token 过期），并给出退出登录的出路。
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

  // 账号名下没有任何门店：引导去走引导流建店。
  // 注意：门店列表尚未加载完时 stores 也是空的，这一分支会短暂闪现（见文末说明）。
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
        {/* 数据驱动渲染导航：当前 Tab 加 is-on 高亮。
            「AI 凭据」仅超管可见（权限以服务端为准，这里只是体验优化）。 */}
        {TABS.filter((t) => t.id !== 'ai' || account?.role === 'super_admin').map((t) => (
          <button key={t.id} className={tab === t.id ? 'is-on' : ''} onClick={() => setTab(t.id)}>
            {t.label}
          </button>
        ))}
        <div style={{ flex: 1 }} />
        <div className="lc-dim" style={{ padding: '0 12px 8px' }}>{account?.phone}</div>
        <button onClick={onLogout}>退出登录</button>
      </nav>

      <main className="admin-main">
        {/* 多门店账号才显示切换器；单店商家不打扰 */}
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

        {/* 按当前 Tab 挂载对应子组件；store 一定非空（上面已提前 return） */}
        {tab === 'overview' && <Overview store={store} />}
        {tab === 'menu' && <MenuTab store={store} />}
        {tab === 'coupon' && <CouponTab store={store} />}
        {tab === 'insights' && <InsightsTab store={store} />}
        {tab === 'requests' && <RequestsTab store={store} />}
        {tab === 'ai' && account && <AICredentialsTab account={account} />}
        {tab === 'settings' && <SettingsTab store={store} onSaved={reload} />}
      </main>
    </div>
  )
}

/* ── 概览：门店页 + 双码 ─────────────────────────────────── */
/**
 * 概览 Tab：门店当前状态 + 对外物料（门口码 / 桌码 / 门店页链接）。
 *
 * @param store 当前选中的门店
 *
 * 两张码的分工（物料话术也写死在页面上，别改语义）：
 *   - 门口码：贴在门外，给「进店前做决策」的客人扫；
 *   - 桌码：贴在桌面，给「已入座、要评价领券」的客人扫。
 * 门店页链接印到物料上后就不可变，改了要重印，所以这里只读展示、不给编辑入口。
 */
function Overview({ store }: { store: Store }) {
  const [links, setLinks] = useState<StoreLinks | null>(null) // 物料链接与二维码地址，加载前为 null
  const [dishes, setDishes] = useState<Dish[]>([]) // 仅用于统计「菜品 N 道」

  useEffect(() => {
    // 概览只需要 links 和菜数；失败静默（概览不是关键路径，不打断商家）。
    api.getStore(store.id).then((d) => {
      setLinks(d.links)
      setDishes(d.dishes ?? [])
    }).catch(() => undefined)
  }, [store.id])

  return (
    <div className="lc-stack">
      <h1>{store.name}</h1>
      {/* 状态徽标行：档位 / 券开关 / 菜品数，一眼看全门店现状 */}
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
                {/* QrImage 需要鉴权，内部用 fetch+blob 渲染 */}
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
              {/* 提醒商家链接不可变：物料一旦印好，改链接就要全部重印 */}
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

/* ── 菜单：多语种覆盖 / 补翻 / 售罄 ──────────────────────── */
/**
 * 菜单 Tab：语种覆盖一览 + 「补充语种」批量补翻 + 售罄开关。
 *
 * @param store 当前选中的门店
 *
 * 业务要点：
 *   - 语种清单来自后端注册表 GET /api/translation/langs，前端绝不硬编码：
 *     后端新增一个语种只需登记一行，这里会自动多出一个语种标签，前端零改动。
 *   - 覆盖率 = 该语种已有译文的菜数 / 总菜数（百分比），用颜色一眼看出哪门语言还没翻。
 *   - 「补充语种」是增量补翻：后端对已有译文不重复翻，所以可以放心重复点，省翻译配额。
 *   - 售罄是唯一需要天天维护的字段；不维护就不显示，也不会报错。
 */
function MenuTab({ store }: { store: Store }) {
  const [dishes, setDishes] = useState<Dish[]>([]) // 门店全部菜品（含多语名 names 与售罄状态）
  const [langs, setLangs] = useState<Lang[]>([]) // 后端注册表返回的受支持语种
  const [msg, setMsg] = useState('') // 统一提示条
  const [busy, setBusy] = useState(false) // 补翻请求进行中
  const [pickOpen, setPickOpen] = useState(false) // 「补充语种」面板是否展开
  const [picked, setPicked] = useState<string[]>([]) // 面板里勾选待补翻的语种 code

  /** 重新拉取菜品列表（补翻 / 切售罄后都要刷新）。 */
  const load = useCallback(() => {
    api.listDishes(store.id).then((d) => setDishes(d.dishes ?? [])).catch((e: Error) => setMsg(e.message))
  }, [store.id])

  useEffect(() => {
    load()
    // 语种注册表加载失败静默：覆盖一览会空着，但不影响菜单本身的操作。
    api.langs().then((d) => setLangs(d.langs ?? [])).catch(() => undefined)
  }, [load])

  // 目标语种 = 注册表里除 zh 之外的全部。zh 是原文语种，永远不作为翻译目标。
  const targets = langs.filter((l) => l.code !== 'zh')

  /** 某语种的覆盖率：有译文的菜 / 总菜数。空菜单返回 0 以避免除零。 */
  const coverage = (code: string) => {
    if (dishes.length === 0) return 0
    const hit = dishes.filter((d) => d.names?.[code]).length
    return Math.round((hit / dishes.length) * 100)
  }

  /** 切换单个菜品的售罄状态，成功后重新拉列表以拿到服务端的真值。 */
  const toggle = async (d: Dish) => {
    try {
      await api.patchDish(store.id, d.id, { sold_out: !d.sold_out })
      load()
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  /** 对勾选的语种执行批量补翻。成功后收起面板、清空选择并刷新覆盖率。 */
  const runTranslate = async () => {
    if (picked.length === 0) return
    setBusy(true)
    setMsg('')
    try {
      const r = await api.translate(store.id, picked)
      setMsg(`已生成 ${picked.length} 个语种，新增译文 ${r.translated_count} 条。`)
      setPickOpen(false)
      setPicked([])
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <h1 style={{ margin: 0 }}>菜单</h1>
        {/* 展开 / 收起「补充语种」面板 */}
        <button className="lc-btn lc-btn--sm" onClick={() => setPickOpen((v) => !v)}>
          补充语种
        </button>
      </div>
      {/* 说明「每日剩余」是可选字段，降低商家的维护心理负担 */}
      <p className="lc-muted">「每日剩余」是唯一需要每天维护的字段；不维护就不显示，不会报错。</p>

      {/* 语种覆盖一览：一眼看出哪些语言还没翻 */}
      <div className="lc-card">
        <div className="lc-label">语种覆盖（{dishes.length} 道菜）</div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 7 }}>
          {targets.map((l) => {
            const pct = coverage(l.code)
            // 100% 绿色、0% 默认色、其余黄色，让缺口一眼可见
            return (
              <span
                key={l.code}
                className={`lc-badge ${pct === 100 ? 'lc-badge--ok' : pct === 0 ? '' : 'lc-badge--warn'}`}
                title={`${l.native} · ${l.name_zh}`}
              >
                {l.native} {pct}%
              </span>
            )
          })}
        </div>
      </div>

      {pickOpen && (
        <div className="lc-card">
          {/* 说明补翻是增量的，打消「会不会重复花钱」的顾虑 */}
          <div className="lc-label">选择要补充生成的语种（已有译文不会重复翻，省配额）</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 7, marginBottom: 12 }}>
            {targets.map((l) => {
              const on = picked.includes(l.code)
              return (
                // 多选标签：选中时用主题色填充；点击即在 picked 里增删该 code
                <button
                  key={l.code}
                  className="lc-btn lc-btn--sm"
                  style={{
                    borderColor: on ? 'var(--lc-accent)' : undefined,
                    background: on ? 'var(--lc-accent)' : undefined,
                    color: on ? '#fff' : undefined,
                  }}
                  onClick={() => setPicked(on ? picked.filter((c) => c !== l.code) : [...picked, l.code])}
                >
                  {l.native}
                </button>
              )
            })}
          </div>
          {/* 一个都没选时禁用，避免发空请求 */}
          <button className="lc-btn lc-btn--primary lc-btn--sm" disabled={busy || picked.length === 0} onClick={runTranslate}>
            {busy ? '生成中…' : `生成所选 ${picked.length} 个语种`}
          </button>
        </div>
      )}

      {msg && <div className="lc-alert lc-alert--info">{msg}</div>}

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>中文原文</th>
              <th>English</th>
              <th>分类</th>
              <th style={{ textAlign: 'right' }}>价格</th>
              <th style={{ textAlign: 'right' }}>已译语种</th>
              <th style={{ textAlign: 'right' }}>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {dishes.map((d) => {
              // have：该菜已有译文的语种条目（过滤掉空字符串，兼容旧数据里的空值）
              const have = Object.entries(d.names ?? {}).filter(([, v]) => v)
              return (
                <tr key={d.id}>
                  {/* name_zh 是原文真值，只读展示，任何时候都不被译文覆盖 */}
                  <td>{d.name_zh}</td>
                  {/* English 列直接取 names.en（即兼容字段 name_en 的来源），没有则显示「未命中」 */}
                  <td>{d.names?.en || <span className="lc-dim">未命中</span>}</td>
                  <td>{d.category || '-'}</td>
                  <td style={{ textAlign: 'right' }}>{d.price != null ? `¥${d.price}` : '-'}</td>
                  <td style={{ textAlign: 'right' }}>
                    {/* 覆盖率胶囊：已译语种数 / 目标语种总数；hover 显示具体是哪几个语种 */}
                    <span className="lc-dim" title={have.map(([k]) => k).join(', ')}>
                      {have.length}/{targets.length}
                    </span>
                  </td>
                  <td style={{ textAlign: 'right' }}>
                    {d.sold_out ? <span className="lc-badge">售罄</span> : <span className="lc-badge lc-badge--ok">在售</span>}
                  </td>
                  <td style={{ textAlign: 'right' }}>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => toggle(d)}>
                      {d.sold_out ? '恢复' : '售罄'}
                    </button>
                  </td>
                </tr>
              )
            })}
            {/* 空态：引导商家去引导流上传菜单 */}
            {dishes.length === 0 && (
              <tr>
                <td colSpan={7} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
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
/**
 * 券与核销 Tab：店员在客人到店出示券码时输入核销，下方是已发券列表。
 *
 * @param store 当前选中的门店
 *
 * 核销是「不可逆」的动作（核销后券作废），所以：
 *   - 成功与失败分两条独立的提示（okMsg / msg），避免商家把报错误读成成功；
 *   - 成功后清空输入框并刷新列表，方便连着核销下一张；
 *   - 券码长度 < 4 时禁用按钮，挡掉明显的误触。
 */
function CouponTab({ store }: { store: Store }) {
  const [coupons, setCoupons] = useState<Coupon[]>([]) // 已发出的券
  const [code, setCode] = useState('') // 待核销的券码（受控输入）
  const [msg, setMsg] = useState('') // 核销失败信息
  const [okMsg, setOkMsg] = useState('') // 核销成功信息

  /** 拉取券列表；失败静默（列表刷不出来不影响核销本身）。 */
  const load = useCallback(() => {
    api.listCoupons(store.id).then((d) => setCoupons(d.coupons ?? [])).catch(() => undefined)
  }, [store.id])

  useEffect(() => {
    load()
  }, [load])

  /** 核销券码：先清掉上一次的两条提示，成功则清空输入并刷新列表。 */
  const redeem = async () => {
    setMsg('')
    setOkMsg('')
    try {
      // 券码两端可能被误输入空格，trim 后再提交
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
            // 支持回车直接核销，方便店员用扫码枪（扫码枪末尾通常带回车）
            onKeyDown={(e) => e.key === 'Enter' && void redeem()}
          />
          <button className="lc-btn lc-btn--primary" disabled={code.trim().length < 4} onClick={redeem}>
            核销
          </button>
        </div>
        {/* 失败与成功用不同颜色的提示条，避免误读 */}
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
                {/* 券码用等宽数字 + 加粗，便于和客人手机上的码逐位比对 */}
                <td style={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{c.code}</td>
                <td>
                  {/* 三种状态：已核销（绿）/ 已过期（默认）/ 待核销（黄） */}
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
/**
 * 经营洞察 Tab：扫码 / 评价 / 复购意愿等 KPI + 最近评价列表。
 *
 * @param store 当前选中的门店
 *
 * 口径说明（重要）：这里的统计只覆盖「通过我们的二维码 / 工具进来的外国客」，
 * 不代表门店的全量客流。后端会在 scope_note 里给出这段口径文案，前端原样展示，
 * 不做任何二次计算或换算，避免商家误以为这是全店经营数据。
 */
function InsightsTab({ store }: { store: Store }) {
  const [data, setData] = useState<Insights | null>(null) // 洞察数据，加载前为 null
  const [err, setErr] = useState('')

  useEffect(() => {
    api.insights(store.id).then(setData).catch((e: Error) => setErr(e.message))
  }, [store.id])

  if (err) return <div className="lc-alert lc-alert--err">{err}</div>
  if (!data) return <div className="lc-muted">加载中…</div>

  /** 把 0~1 的比率格式化成百分比整数串。 */
  const pct = (v: number) => `${Math.round(v * 100)}%`

  return (
    <div className="lc-stack">
      <h1>经营洞察</h1>
      {/* 口径提示由后端下发（scope_note），前端不写死，便于后端调整统计范围 */}
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
          {/* 「会再来」= 复购意愿打分 ≥ 4 的比例，衡量服务是否真的打动人 */}
          <div className="lc-dim">复购意愿（会再来 ≥4）</div>
          <div className="kpi-num">{pct(data.rating.would_return)}</div>
        </div>
        <div className="lc-card">
          {/* 低分率 = 综合打分 ≤ 2 的比例，是预警指标 */}
          <div className="lc-dim">低分率（≤2）</div>
          <div className="kpi-num">{pct(data.rating.low_rate)}</div>
        </div>
        <div className="lc-card">
          {/* 发券数 / 核销数：核销率高低反映券的吸引力与店员执行度 */}
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
            {/* 三个维度分：菜品 / 服务 / 是否再来 */}
            <div className="lc-row" style={{ gap: 12 }}>
              <span className="lc-badge">菜品 {r.score_dish}</span>
              <span className="lc-badge">服务 {r.score_service}</span>
              <span className="lc-badge">再来 {r.score_return}</span>
              {/* ref 是客人来源标记（门口码 / 桌码），用于区分渠道 */}
              {r.ref && <span className="lc-dim">{r.ref}</span>}
            </div>
            {/* 评价原文（客人语言）与后端给出的中文译文；译文缺失时只显示原文 */}
            {r.comment_native && <div style={{ marginTop: 6 }}>{r.comment_native}</div>}
            {r.comment_zh && <div className="lc-dim">译：{r.comment_zh}</div>}
          </div>
        ))}
      </div>
    </div>
  )
}

/* ── 设置：档位开关 / WiFi / 券开关 ───────────────────────── */
/**
 * 设置 Tab：交付档位、复购券开关、店内 WiFi。
 *
 * @param store  当前选中的门店（各输入框的初始值都来自它）
 * @param onSaved 保存成功回调，父组件用它重新拉 /api/me 刷新门店数据
 *
 * 档位（service_tier）说明：两档共用同一条链路，升级不会让商家重新上传菜单，
 * 所以这里切换是「解锁更多功能」而不是「换一套系统」。
 * WiFi 是可选信息，不填也不影响任何主流程。
 */
function SettingsTab({ store, onSaved }: { store: Store; onSaved: () => void }) {
  // 受控输入的初始值直接取自传入的 store；wifi 字段可能是 undefined，用 ?? '' 兜成空串，
  // 否则受控 input 会从 undefined 变成非受控，触发 React 告警。
  const [tier, setTier] = useState(store.service_tier)
  const [couponEnabled, setCouponEnabled] = useState(store.coupon_enabled)
  const [wifiSsid, setWifiSsid] = useState(store.wifi_ssid ?? '')
  const [wifiPwd, setWifiPwd] = useState(store.wifi_pwd ?? '')
  const [msg, setMsg] = useState('')

  /** 一次性提交四个字段（后端 PATCH 支持部分字段，但这里全量提交更简单可靠）。 */
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
      // 通知父组件刷新，让概览里的徽标等同步更新
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
          {/* 切换档位用 select；断言成 Store['service_tier'] 收窄类型 */}
          <select className="lc-select" value={tier} onChange={(e) => setTier(e.target.value as Store['service_tier'])}>
            <option value="pure">纯翻译档（多语菜单 + 评价领券 + 问店员）</option>
            <option value="service">外客服务档（再开点菜 / 服务铃 / 每日剩余）</option>
          </select>
        </div>
        <div>
          <label className="lc-label">复购券（发券免费）</label>
          <label className="lc-row" style={{ gap: 8 }}>
            {/* 复选框受控：checked 绑 couponEnabled */}
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

/**
 * 二维码图片。二维码接口需要鉴权（Bearer token），普通 <img src> 带不上请求头，
 * 所以这里用 fetch 手动带 token 取回 blob，再转成 objectURL 交给 <img> 渲染。
 *
 * @param url 二维码接口地址（来自 StoreLinks.qr_door / qr_table）
 *
 * 实现细节：用 alive 标记防止组件已卸载后仍 setState（避免 React 告警）。
 */
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
  // 未拿到 blob 前显示占位文案；注意这里没有在卸载时 revoke 掉 objectURL。
  return src ? <img src={src} alt="二维码" /> : <div className="lc-dim" style={{ padding: 60 }}>生成中…</div>
}
