import { Navigate, Route, Routes } from 'react-router-dom'
import StorePage from './guest/StorePage'
import AdminApp from './admin/AdminApp'
import Onboarding from './admin/Onboarding'

/** 单一 React 应用承载两个端：
 *  - 客人端：/s/{slug}（短链，扫码进入，物料定死不可变）
 *  - 商家端：/admin（管理台）、/admin/onboarding（引导流 P1–P5）
 */
export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/admin" replace />} />
      <Route path="/s/:slug" element={<StorePage />} />
      <Route path="/admin/onboarding" element={<Onboarding />} />
      <Route path="/admin/*" element={<AdminApp />} />
      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

function NotFound() {
  return (
    <div className="lc-center">
      <h1>页面不存在</h1>
      <p className="lc-muted">请检查链接，或扫码重新进入。</p>
    </div>
  )
}
