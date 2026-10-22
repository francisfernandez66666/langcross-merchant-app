/**
 * 商家端管理台 · 入口（挂在路由 /admin/* 下，见 App.tsx）。
 *
 * 本文件现在只做一件事：**有 token 就给控制台，没 token 就给登录页**。
 *
 * ─────────────────────────────────────────────────────────────
 * 拆分结构（2026-10-21 FIX-13：本文件原 910 行）
 * ─────────────────────────────────────────────────────────────
 * 原文件把登录、控制台外壳、五个业务 Tab 全塞在一起，改一个 Tab 要在
 * 九百行里找位置。现在按「一个 Tab 一个文件」拆开：
 *   adminShared       Tab 类型 + TABS 清单（数据驱动导航的源头）
 *   AdminLogin        登录 / 注册页
 *   AdminConsole      控制台外壳（装载数据 / 切换门店 / 切换 Tab）
 *   AdminOverview     概览：门店状态 + 门口码 / 桌码 / 门店页链接
 *   AdminMenuTab      菜单：语种覆盖 + 补充语种补翻 + 售罄 + 菜品图片
 *   AdminCouponTab    券与核销
 *   AdminInsightsTab  经营洞察
 *   AdminSettingsTab  设置：档位 / 复购券开关 / WiFi
 * 另外三个 Tab 此前已各自成文件：RequestsTab / AICredentialsTab / PlatformTab。
 *
 * ─────────────────────────────────────────────────────────────
 * 页面结构（左侧导航切换的 Tab）
 * ─────────────────────────────────────────────────────────────
 *   - 概览 Overview：门店状态徽标 + 门口码 / 桌码 / 门店页链接（物料）
 *   - 菜单 MenuTab：语种覆盖率一览、「补充语种」批量补翻、售罄开关
 *   - 券与核销 CouponTab：输入客人券码核销 + 已发券列表
 *   - 经营洞察 InsightsTab：扫码 / 评价 / 复券 KPI 与最近评价
 *   - 服务请求 RequestsTab：客人发起的服务铃 / 加水 / 加菜 / 买单待办（轮询 + 完成/取消）
 *   - 平台管理 PlatformTab：超管控制台（概览/租户）+ 租户管理员后台（账号/翻译 Key/审计）
 *   - AI 凭据 AICredentialsTab：平台超管维护 LLM 凭据（tenant_admin 只读，后端同款门禁）
 *   - 设置 SettingsTab：交付档位、复购券开关、店内 WiFi
 *
 * 涉及的接口（全部经 ../api/client 调用；响应统一为 {ok:true,...}
 * 或 {ok:false,error:{code,msg}}，错误由 client 抛成 Error 供各 Tab catch）：
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
 * Authorization: Bearer <token>；token 只在登录 / 登出时读写。
 */
import { useState } from 'react'
import { tokenStore } from '../api/client'
import { Login } from './AdminLogin'
import { Console } from './AdminConsole'

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
