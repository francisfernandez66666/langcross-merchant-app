/**
 * P5 · 引导注册（免费；注册后自动配券 + 生成双码）。
 * 这是整条流程的「落地」步骤，把前面几步攒下的东西一次性写进后端：
 *   登录/注册 → 建店 → 保存草稿 → 确认原文稿 → 按所选语种批量翻译。
 * 顺序不能乱：必须 confirmDraft（确认原文稿）之后才能 translate，否则没有可翻译的原文。
 */
import { useState } from 'react'
import { api, tokenStore, type CouponTemplate, type DraftItem, type Store, type StoreLinks } from '../api/client'
import { QrImage } from './QrImage'

/** 注册 / 登录后一把梭：建店 → 落草稿 → 确认原文稿 → 批量翻译 → 出双码。
 * @param items      菜品草稿（要落库的内容）
 * @param targets    目标语种
 * @param result     上线结果；非空即表示已完成，本组件切换成「上线成功」页
 * @param setResult  写入上线结果（触发成功页）
 */
export function Step5({
  items,
  targets,
  result,
  setResult,
}: {
  items: DraftItem[]
  targets: string[]
  result: { store: Store; links: StoreLinks; coupon?: CouponTemplate } | null
  setResult: (v: { store: Store; links: StoreLinks; coupon?: CouponTemplate }) => void
}) {
  const [phone, setPhone] = useState('') // 手机号
  const [code, setCode] = useState('') // 短信验证码
  const [storeName, setStoreName] = useState('') // 店名（可留空，提交时兜底为「我的门店」）
  const [busy, setBusy] = useState(false) // 上线请求链进行中
  const [msg, setMsg] = useState('') // 提示信息
  const [sent, setSent] = useState(false) // 是否已发过验证码

  /** 发送验证码；开发模式回显 dev_code 便于联调。 */
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

  /**
   * 一键上线：登录 → 建店 → 落草稿 → 确认原文稿 → 翻译。
   * 这几步是严格的顺序依赖，任一步失败都会中断并提示，不会留下半成品门店的假成功。
   */
  const submit = async () => {
    setBusy(true)
    setMsg('')
    try {
      const { token } = await api.login(phone, code)
      tokenStore.set(token)
      // 店名允许留空，用「我的门店」兜底，避免建店被空名挡住
      const created = await api.createStore(storeName || '我的门店')
      // 只有确实有菜品时才走「落草稿 → 确认 → 翻译」三步
      if (items.length > 0) {
        await api.saveDraft(created.store.id, items)
        await api.confirmDraft(created.store.id)
        await api.translate(created.store.id, targets)
      }
      // 建店时后端已自动配好券模板并生成双码，这里一并带到结果页展示
      setResult({ store: created.store, links: created.links, coupon: created.coupon_template })
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // 已上线：渲染成功页，展示双码与门店页链接，引导进入经营后台
  if (result) {
    return (
      <div className="lc-stack">
        <div className="lc-card lc-card--accent">
          <h2>上线成功 🎉</h2>
          <p className="lc-muted" style={{ marginBottom: 0 }}>
            菜单已多语化（{targets.length} 个语种），复购券模板已自动配好（发券免费）。把下面两张码打印出来贴好即可。
          </p>
        </div>
        <div className="lc-card">
          <div className="lc-row" style={{ gap: 20, flexWrap: 'wrap' }}>
            <div style={{ textAlign: 'center' }}>
              <div className="qr-box">
                <QrImage url={result.links.qr_door} />
              </div>
              <div className="lc-dim" style={{ marginTop: 6 }}>门口码</div>
            </div>
            <div style={{ textAlign: 'center' }}>
              <div className="qr-box">
                <QrImage url={result.links.qr_table} />
              </div>
              <div className="lc-dim" style={{ marginTop: 6 }}>桌码</div>
            </div>
          </div>
          <div style={{ marginTop: 14 }}>
            {/* 再次强调物料链接不可变：印好后改链接要重印 */}
            <div className="lc-label">门店页链接（物料印好后不可变）</div>
            <a href={result.links.store_page} target="_blank" rel="noreferrer" style={{ wordBreak: 'break-all' }}>
              {result.links.store_page}
            </a>
          </div>
        </div>
        <a className="lc-btn lc-btn--primary lc-btn--block" href="/admin">
          进入经营后台 →
        </a>
      </div>
    )
  }

  return (
    <div className="lc-stack">
      <p className="lc-muted">
        注册只收手机号。注册后解锁：全量多语菜单、双码物料、复购券、评价数据。
        <span className="lc-badge lc-badge--ok" style={{ marginLeft: 6 }}>本期免费</span>
      </p>
      <div className="lc-card lc-stack" style={{ gap: 10 }}>
        <div>
          <label className="lc-label">店名</label>
          <input className="lc-input" value={storeName} placeholder="老张川菜馆" onChange={(e) => setStoreName(e.target.value)} />
        </div>
        <div>
          <label className="lc-label">手机号</label>
          <div className="lc-row" style={{ gap: 8 }}>
            <input className="lc-input" value={phone} placeholder="13800000000" onChange={(e) => setPhone(e.target.value)} />
            {/* 手机号至少 6 位才允许发码（前端粗校验） */}
            <button className="lc-btn" style={{ flexShrink: 0 }} disabled={phone.length < 6} onClick={sendOtp}>
              {sent ? '重发' : '获取验证码'}
            </button>
          </div>
        </div>
        <div>
          <label className="lc-label">验证码</label>
          <input className="lc-input" value={code} placeholder="6 位数字" onChange={(e) => setCode(e.target.value)} />
        </div>
      </div>
      {msg && <div className="lc-alert lc-alert--info">{msg}</div>}
      {/* 上线请求链较长，busy 期间禁用避免重复建店 */}
      <button
        className="lc-btn lc-btn--primary lc-btn--block"
        disabled={busy || phone.length < 6 || code.length < 4}
        onClick={submit}
      >
        {busy ? '正在上线…' : '注册并上线'}
      </button>
    </div>
  )
}
