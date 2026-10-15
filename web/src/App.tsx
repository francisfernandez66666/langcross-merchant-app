import { Navigate, Route, Routes } from 'react-router-dom'
import StorePage from './guest/StorePage'
import AdminApp from './admin/AdminApp'
import Onboarding from './admin/Onboarding'

/**
 * 路由表：单一 React 应用承载两个端，**按路由分端**。
 *  - 客人端：`/s/{slug}`（短链，扫码进入；物料已印死，路径不可变）。
 *  - 商家端：`/admin`（管理台）、`/admin/onboarding`（引导流 P1–P5）。
 *
 * 无状态管理库、无 UI 组件库：路由即「端」的边界，页面各自管理自己的状态。
 * 注意路由顺序：`/admin/onboarding` 必须排在 `/admin/*` 之前，否则会被通配吞掉。
 */
export default function App() {
  return (
    <Routes>
      {/* 根路径统一跳商家端；客人只从扫码短链进入，不会主动访问 / */}
      <Route path="/" element={<Navigate to="/admin" replace />} />
      {/* 客人端门店页：slug 是门店短链标识 */}
      <Route path="/s/:slug" element={<StorePage />} />
      {/* 商家端引导流（注册后逐步建店），需先于通配匹配 */}
      <Route path="/admin/onboarding" element={<Onboarding />} />
      {/* 商家端其余所有子路由交给 AdminApp 内部再分发 */}
      <Route path="/admin/*" element={<AdminApp />} />
      {/* 其余一律 404 */}
      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

/** 404 兜底页：提示检查链接或重新扫码（物料二维码失效时的出口）。 */
function NotFound() {
  return (
    <div className="lc-center">
      <h1>页面不存在</h1>
      <p className="lc-muted">请检查链接，或扫码重新进入。</p>
    </div>
  )
}
