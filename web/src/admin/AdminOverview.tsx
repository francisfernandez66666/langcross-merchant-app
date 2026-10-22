/**
 * 概览 Tab：门店状态 + 对外物料（门口码 / 桌码 / 门店页链接）。从 AdminApp.tsx 拆出，行段逐字照搬。
 */
import { useEffect, useState } from 'react'
import {
  api,
  type Dish,
  type Store,
  type StoreLinks,
} from '../api/client'
import { QrImage } from './QrImage'

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
export function Overview({ store }: { store: Store }) {
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
