/**
 * P2 · 上传菜单（OCR 出菜品草稿）。
 * 容错设计：OCR 未接入（need_manual 或识别为空）时不报错阻断，而是提示商家
 * 用「示例菜单」继续，保证任何环境下都能走完整个引导流。
 */
import { useRef, useState } from 'react'
import { api, type DraftItem } from '../api/client'
import { SAMPLE, toBase64 } from './onboardingShared'

/** 上传菜单照片 → OCR 成菜品草稿；识别不可用时用示例菜单保底继续。
 * @param items    当前草稿（用于展示已识别的道数）
 * @param onLoaded 拿到菜品后交给上层，由上层跳到 P3
 */
export function Step2({ items, onLoaded }: { items: DraftItem[]; onLoaded: (its: DraftItem[]) => void }) {
  const [busy, setBusy] = useState(false) // OCR 请求进行中
  const [msg, setMsg] = useState('') // 提示信息
  // 隐藏的 <input type="file">，用 ref 触发点击——因为原生文件选择框无法自定义样式
  const fileRef = useRef<HTMLInputElement>(null)

  /** 读取用户选的图片 → 转 base64 → 调 OCR → 把识别结果交给 onLoaded。 */
  const onFile = async (f: File) => {
    setBusy(true)
    setMsg('')
    try {
      const b64 = await toBase64(f)
      const r = await api.ocr(b64)
      // 识别服务没接好（本地 stub）或没识别出菜：不阻断，提示改用示例菜单
      if (r.result.need_manual || r.result.items.length === 0) {
        setMsg('识别服务当前为本地 stub（未接 PaddleOCR）。可先用「示例菜单」继续，正式环境会走服务端识别。')
      } else {
        // 识别出的每一项默认勾选（selected: true），进 P3 后商家只需「取消不要的」
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
      {/* 真正承载文件选择的原生 input，隐藏起来由下面的按钮代为触发 */}
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
      {/* 保底路径：直接用示例菜单继续。注意 map 里做了浅拷贝，避免改动 SAMPLE 常量本身 */}
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
