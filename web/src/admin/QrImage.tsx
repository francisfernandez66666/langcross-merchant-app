import { useEffect, useState } from 'react'
import { tokenStore } from '../api/client'

/**
 * 二维码图片：带鉴权地把二维码字节取回来，转成 <img> 能用的地址。
 *
 * 为什么要自己取而不是直接 <img src={url}>：二维码接口在登录后才能访问，
 * 而 <img> 不会带上我们的 Authorization 头（那是 Bearer token，不是 Cookie），
 * 直接引用会拿到 401。所以这里用 fetch 取 blob，再转成 objectURL 交给 <img>。
 *
 * ── 为什么必须 revoke ──────────────────────────────────
 * createObjectURL 造出来的地址由浏览器**一直持有**，直到显式 revoke
 * 或页面卸载——它不参与 GC。门店页会反复切换门店、每次都新建一张二维码，
 * 不 revoke 就是一个随使用时长线性增长的内存泄漏（在低配收银平板上很明显）。
 * 因此这里在 cleanup 里 revoke，并且顺带处理「URL 变了」的情况：
 * 换门店时旧的那个也要回收，不能只留最后一个。
 *
 * ── 为什么要单独抽成文件 ──────────────────────────────
 * 修复前这段逻辑在 AdminApp.tsx 与 Onboarding.tsx 里各有一份**逐字重复**的
 * 副本，且两份都漏了 revoke。抽出来之后：漏一次只修一处，也不会再出现
 * 「这边修了、那边没修」这种靠肉眼同步的事。
 */
export function QrImage({ url }: { url: string }) {
  const [src, setSrc] = useState('')

  useEffect(() => {
    let alive = true
    let created: string | null = null // 本次 effect 造出来的 objectURL，cleanup 时回收

    const token = tokenStore.get()
    fetch(url, { headers: token ? { Authorization: `Bearer ${token}` } : {} })
      .then((r) => r.blob())
      .then((b) => {
        if (!alive) return // 已卸载：不再 setState，也不再造 URL（否则没人回收）
        created = URL.createObjectURL(b)
        setSrc(created)
      })
      .catch(() => undefined)

    return () => {
      alive = false
      // 卸载或 url 变化时回收：不 revoke 的话浏览器会一直持有这块 blob
      if (created) URL.revokeObjectURL(created)
    }
  }, [url])

  // 未拿到 blob 前显示占位，避免布局跳动（与二维码尺寸接近的留白）
  return src ? <img src={src} alt="二维码" /> : <div className="lc-dim" style={{ padding: 60 }}>生成中…</div>
}
