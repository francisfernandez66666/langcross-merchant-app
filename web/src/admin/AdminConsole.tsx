/**
 * 控制台外壳：登录后装载数据、切换门店、切换 Tab。从 AdminApp.tsx 拆出，行段逐字照搬。
 */
import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  api,
  type Account,
  type Store,
} from '../api/client'
import RequestsTab from './RequestsTab'
import AICredentialsTab from './AICredentialsTab'
import PlatformTab from './PlatformTab'
import { type Tab, TABS } from './adminShared'
import { Overview } from './AdminOverview'
import { MenuTab } from './AdminMenuTab'
import { CouponTab } from './AdminCouponTab'
import { InsightsTab } from './AdminInsightsTab'
import { SettingsTab } from './AdminSettingsTab'

/**
 * 控制台外壳：登录后的数据装载、门店切换、Tab 切换都在这里，各 Tab 的具体业务拆到子组件。
 *
 * @param onLogout 退出登录回调（清 token 并回到登录页）
 */
export function Console({ onLogout }: { onLogout: () => void }) {
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

  // 平台角色（超管 / 租户管理员）可能一家门店都没有——他们的舞台是平台管理 Tab，
  // 因此「没有门店」对他们不是死路，只是看不到门店级 Tab。
  const isPlatformRole = account?.role === 'super_admin' || account?.role === 'tenant_admin'

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

  // 账号名下没有任何门店（且不是平台角色）：引导去走引导流建店。
  // 注意：门店列表尚未加载完时 stores 也是空的，这一分支会短暂闪现（见文末说明）。
  if (!store && !isPlatformRole) {
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
            可见性两层规则：
            1) 平台角色（超管/租户管理员）若一家门店都没有，只展示平台级 Tab
               （平台管理 / AI 凭据）——他们没有门店可管，别拿空页面烦他们；
            2) 有门店（或非平台角色）时按 roles 过滤：未标注 = 所有人可见，
               标注了 = 仅列出的角色可见（真准入仍以服务端为准）。 */}
        {TABS.filter((t) => {
          if (!store) return t.id === 'platform' || t.id === 'ai'
          return !t.roles || (account?.role != null && t.roles.includes(account.role))
        }).map((t) => (
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
        {store && stores.length > 1 && (
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

        {/* 按当前 Tab 挂载对应子组件。门店级 Tab 都要求 store 非空（平台角色无门店时
            这些 Tab 不会出现在导航里，这里的 && 兜底防止误渲染空组件）。 */}
        {tab === 'overview' && store && <Overview store={store} />}
        {tab === 'menu' && store && <MenuTab store={store} />}
        {tab === 'coupon' && store && <CouponTab store={store} />}
        {tab === 'insights' && store && <InsightsTab store={store} />}
        {tab === 'requests' && store && <RequestsTab store={store} />}
        {tab === 'platform' && account && <PlatformTab account={account} />}
        {tab === 'ai' && account && <AICredentialsTab account={account} />}
        {tab === 'settings' && store && <SettingsTab store={store} onSaved={reload} />}
      </main>
    </div>
  )
}
