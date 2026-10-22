/**
 * 设置 Tab：交付档位 / 复购券开关 / 店内 WiFi。从 AdminApp.tsx 拆出，行段逐字照搬。
 */
import { useState } from 'react'
import { api, type Store } from '../api/client'

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
export function SettingsTab({ store, onSaved }: { store: Store; onSaved: () => void }) {
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
// QrImage 已抽到 ./QrImage（原先在 AdminApp 与 Onboarding 里各有一份逐字重复
// 的副本，且都漏了 URL.revokeObjectURL——换门店时旧 blob 一直被浏览器持有）。
// 现在两处共用同一个实现，漏一次只修一处。
