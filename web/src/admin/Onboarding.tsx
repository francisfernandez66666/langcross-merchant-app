import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, tokenStore, type CouponTemplate, type DraftItem, type Store, type StoreLinks } from '../api/client'

/** 示例菜单：用于本地无 OCR 服务时快速演示后续步骤。 */
const SAMPLE: DraftItem[] = [
  { name: '宫保鸡丁', price: 38, category: '热菜', selected: true },
  { name: '麻婆豆腐', price: 32, category: '热菜', selected: true },
  { name: '水煮鱼', price: 68, category: '热菜', selected: true },
  { name: '糖醋里脊', price: 42, category: '热菜', selected: true },
  { name: '番茄炒蛋', price: 26, category: '热菜', selected: true },
  { name: '清炒时蔬', price: 22, category: '素菜', selected: true },
  { name: '蛋炒饭', price: 18, category: '主食', selected: true },
  { name: '米饭', price: 3, category: '主食', selected: true },
  { name: '酸辣汤', price: 22, category: '汤', selected: true },
  { name: '可乐', price: 8, category: '饮品', selected: true },
  { name: '啤酒', price: 12, category: '饮品', selected: true },
]

const STEP_TITLES = ['算损失', '上传菜单', '挑要翻译的菜', '核心多语言', '注册解锁']

export default function Onboarding() {
  const nav = useNavigate()
  const [step, setStep] = useState(1)
  const [items, setItems] = useState<DraftItem[]>([])
  const [result, setResult] = useState<{ store: Store; links: StoreLinks; coupon?: CouponTemplate } | null>(null)

  const go = (n: number) => setStep(Math.min(5, Math.max(1, n)))

  return (
    <div className="warm" style={{ minHeight: '100%', background: 'var(--lc-bg)', color: 'var(--lc-text)' }}>
      <div className="lc-wrap" style={{ maxWidth: 620, paddingTop: 34 }}>
        <div className="lc-row lc-row--between" style={{ marginBottom: 14 }}>
          <div className="lc-dim">能言翻译助手 · 商家上线（{step}/5）</div>
          <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => nav('/admin')}>
            跳过
          </button>
        </div>

        <div className="lc-steps">
          {[1, 2, 3, 4, 5].map((i) => (
            <div key={i} className={`lc-step ${i <= step ? 'is-on' : ''}`} />
          ))}
        </div>

        <h1>{STEP_TITLES[step - 1]}</h1>

        {step === 1 && <Step1 onNext={() => go(2)} />}
        {step === 2 && (
          <Step2
            items={items}
            onLoaded={(its) => {
              setItems(its)
              go(3)
            }}
          />
        )}
        {step === 3 && <Step3 items={items} setItems={setItems} onNext={() => go(4)} />}
        {step === 4 && <Step4 items={items} onNext={() => go(5)} />}
        {step === 5 && (
          <Step5
            items={items}
            result={result}
            setResult={setResult}
          />
        )}

        {step > 1 && !result && (
          <div style={{ marginTop: 22 }}>
            <button className="lc-btn lc-btn--ghost lc-btn--sm" onClick={() => go(step - 1)}>
              ← 上一步
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

/** P1 · 算损失（本地算账，离线可用，零等待）。 */
function Step1({ onNext }: { onNext: () => void }) {
  const [guests, setGuests] = useState(6)
  const [avg, setAvg] = useState(85)
  const [lost, setLost] = useState(30)
  const monthly = Math.round(guests * 30 * (lost / 100) * avg)

  return (
    <div className="lc-stack">
      <p className="lc-muted">
        先算一笔账：语言不通，最贵的一桌客人可能就在门口走了。以下都在你的手机上本地计算，不上传任何数据。
      </p>
      <div className="lc-card">
        <label className="lc-label">每天进店的外国客人（桌）</label>
        <input
          className="lc-input"
          type="number"
          min={0}
          value={guests}
          onChange={(e) => setGuests(Number(e.target.value) || 0)}
        />
        <div style={{ height: 12 }} />
        <label className="lc-label">人均消费（元）</label>
        <input
          className="lc-input"
          type="number"
          min={0}
          value={avg}
          onChange={(e) => setAvg(Number(e.target.value) || 0)}
        />
        <div style={{ height: 12 }} />
        <label className="lc-label">其中因语言不通而流失的比例（%）</label>
        <input
          className="lc-input"
          type="number"
          min={0}
          max={100}
          value={lost}
          onChange={(e) => setLost(Number(e.target.value) || 0)}
        />
      </div>
      <div className="lc-card lc-card--accent">
        <div className="lc-dim">按此估算，每月可能流失</div>
        <div className="kpi-num" style={{ fontSize: 36 }}>
          ¥{monthly.toLocaleString()}
        </div>
      </div>
      <button className="lc-btn lc-btn--primary lc-btn--block" onClick={onNext}>
        看看怎么挽回 →
      </button>
    </div>
  )
}

/** P2 · 上传一页菜单（服务端识别 · 需联网；无服务时走手动保底）。 */
function Step2({ items, onLoaded }: { items: DraftItem[]; onLoaded: (its: DraftItem[]) => void }) {
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const onFile = async (f: File) => {
    setBusy(true)
    setMsg('')
    try {
      const b64 = await toBase64(f)
      const r = await api.ocr(b64)
      if (r.result.need_manual || r.result.items.length === 0) {
        setMsg('识别服务当前为本地 stub（未接 PaddleOCR）。可先用「示例菜单」继续，正式环境会走服务端识别。')
      } else {
        onLoaded(r.result.items.map((i) => ({ name: i.name, price: i.price, selected: true })))
      }
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="lc-stack">
      <p className="lc-muted">
        拍一页菜单就行。<span className="lc-badge lc-badge--warn">需联网</span> 识别在服务端完成；识别不准时可直接手动录入。
      </p>
      <input
        ref={fileRef}
        type="file"
        accept="image/*"
        style={{ display: 'none' }}
        onChange={(e) => {
          const f = e.target.files?.[0]
          if (f) void onFile(f)
        }}
      />
      <button className="lc-btn lc-btn--block" disabled={busy} onClick={() => fileRef.current?.click()}>
        {busy ? '识别中…' : '上传菜单照片'}
      </button>
      <button className="lc-btn lc-btn--ghost lc-btn--block" onClick={() => onLoaded(SAMPLE.map((s) => ({ ...s })))}>
        用示例菜单继续（{SAMPLE.length} 道）
      </button>
      {msg && <div className="lc-alert lc-alert--info">{msg}</div>}
      {items.length > 0 && (
        <div className="lc-alert lc-alert--info">已识别 {items.length} 道菜，点「上传菜单照片」可重新识别。</div>
      )}
    </div>
  )
}

/** P3 · 挑要翻译的菜（只勾选 + 改价，零等待）。 */
function Step3({
  items,
  setItems,
  onNext,
}: {
  items: DraftItem[]
  setItems: (v: DraftItem[]) => void
  onNext: () => void
}) {
  const picked = items.filter((i) => i.selected).length
  const patch = (idx: number, p: Partial<DraftItem>) =>
    setItems(items.map((it, i) => (i === idx ? { ...it, ...p } : it)))

  return (
    <div className="lc-stack">
      <p className="lc-muted">
        勾选要翻译的菜（只翻你选的，省钱也更快），价格可顺手改。
      </p>
      <div className="lc-card" style={{ padding: 8 }}>
        {items.map((it, i) => (
          <div key={i} className="lc-row" style={{ gap: 10, padding: '8px 6px' }}>
            <input
              type="checkbox"
              checked={it.selected}
              onChange={(e) => patch(i, { selected: e.target.checked })}
              style={{ width: 20, height: 20, accentColor: 'var(--lc-accent)' }}
            />
            <input
              className="lc-input"
              style={{ height: 38 }}
              value={it.name}
              onChange={(e) => patch(i, { name: e.target.value })}
            />
            <input
              className="lc-input"
              style={{ height: 38, width: 92, textAlign: 'right' }}
              type="number"
              value={it.price ?? ''}
              onChange={(e) => patch(i, { price: Number(e.target.value) || 0 })}
            />
          </div>
        ))}
      </div>
      <button className="lc-btn lc-btn--primary lc-btn--block" disabled={picked === 0} onClick={onNext}>
        生成多语菜单（{picked} 道）→
      </button>
    </div>
  )
}

/** P4 · 核心多语言（全程唯一一次真生成 · 需联网）。 */
function Step4({ items, onNext }: { items: DraftItem[]; onNext: () => void }) {
  const [out, setOut] = useState<DraftItem[]>([])
  const [busy, setBusy] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    const picked = items.filter((i) => i.selected)
    api
      .previewTranslate(picked)
      .then((r) => setOut(r.items))
      .catch((e: Error) => setErr(e.message))
      .finally(() => setBusy(false))
  }, [items])

  return (
    <div className="lc-stack">
      <p className="lc-muted">
        这是你店里的菜单，翻成客人看得懂的样子。<span className="lc-badge lc-badge--warn">需联网</span>
      </p>
      {busy && <div className="lc-card lc-dim">正在生成…</div>}
      {err && <div className="lc-alert lc-alert--err">{err}</div>}
      {!busy && (
        <div className="lc-card">
          {out.map((it, i) => (
            <div key={i} className="dish">
              <div className="dish-main">
                <div className="dish-name">{it.name_en || it.name}</div>
                {it.name_en ? <div className="dish-zh">{it.name}</div> : <div className="dish-zh">（未收录，保持原文，待人工确认）</div>}
              </div>
              {it.price != null && <div className="dish-price">¥{it.price}</div>}
            </div>
          ))}
        </div>
      )}
      <button className="lc-btn lc-btn--primary lc-btn--block" disabled={busy} onClick={onNext}>
        注册后即可对外使用 →
      </button>
    </div>
  )
}

/** P5 · 引导注册（免费；注册后自动配券 + 生成双码）。 */
function Step5({
  items,
  result,
  setResult,
}: {
  items: DraftItem[]
  result: { store: Store; links: StoreLinks; coupon?: CouponTemplate } | null
  setResult: (v: { store: Store; links: StoreLinks; coupon?: CouponTemplate }) => void
}) {
  const [phone, setPhone] = useState('')
  const [code, setCode] = useState('')
  const [storeName, setStoreName] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [sent, setSent] = useState(false)

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

  const submit = async () => {
    setBusy(true)
    setMsg('')
    try {
      const { token } = await api.login(phone, code)
      tokenStore.set(token)
      const created = await api.createStore(storeName || '我的门店')
      if (items.length > 0) {
        await api.saveDraft(created.store.id, items)
        await api.confirmDraft(created.store.id)
        await api.translate(created.store.id)
      }
      setResult({ store: created.store, links: created.links, coupon: created.coupon_template })
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (result) {
    return (
      <div className="lc-stack">
        <div className="lc-card lc-card--accent">
          <h2>上线成功 🎉</h2>
          <p className="lc-muted" style={{ marginBottom: 0 }}>
            菜单已多语化，复购券模板已自动配好（发券免费）。把下面两张码打印出来贴好即可。
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

/** 二维码接口需要鉴权，故用 fetch 取 blob 再渲染。 */
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
  return src ? <img src={src} alt="二维码" /> : <div className="lc-dim" style={{ padding: 60 }}>生成中…</div>
}

function toBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => {
      const s = String(r.result)
      resolve(s.includes(',') ? s.split(',')[1] : s)
    }
    r.onerror = () => reject(new Error('读取图片失败'))
    r.readAsDataURL(file)
  })
}
