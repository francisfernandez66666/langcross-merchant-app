/**
 * 应用入口：把 <App /> 挂到 index.html 的 #root 上。
 *
 * 三层包裹各有职责：
 *   - React.StrictMode：开发期双重调用 effect/渲染，帮我们暴露副作用没清理干净的问题
 *     （生产构建不生效，不影响线上行为）。
 *   - BrowserRouter：提供路由上下文；客人端与商家端共用同一 Router，靠路径前缀分端。
 *   - global.css：全局样式（手写，类名以 gx-/lc- 开头），无 UI 组件库。
 *
 * `getElementById('root')!` 的断言依赖 index.html 里确实有 <div id="root">。
 */
import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import './styles/global.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
)
