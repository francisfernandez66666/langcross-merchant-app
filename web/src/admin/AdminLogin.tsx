/**
 * 登录 / 注册页（手机号 + 短信验证码）。从 AdminApp.tsx 拆出，行段逐字照搬。
 */
import { useState } from 'react'
import { Link } from 'react-router-dom'
import {
  api,
  tokenStore,
} from '../api/client'

/* ── 登录（手机号 + 验证码） ─────────────────────────────── */
/**
 * 登录 / 注册页（手机号 + 短信验证码）。
 * 同一入口既登录也注册：后端对未注册过的手机号会自动建账号，所以商家不需要单独的注册流程。
 * 成功后把 token 落盘（tokenStore）并回调 onDone，由父组件切到控制台。
 *
 * @param onDone 登录成功回调，父组件据此把 authed 置 true 进入控制台
 */
export function Login({ onDone }: { onDone: () => void }) {
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
