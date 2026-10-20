import Taro from '@tarojs/taro'

/**
 * ★ 替换 web/src/guest/StorePage.tsx 里的 navigator.languages 判定。
 *
 * 小程序没有 navigator.languages，只有 getSystemInfoSync().language，
 * 形如 'zh_CN' / 'en' / 'ja'。这里做前缀归一，与后端语种注册表的 code 对齐。
 */

const FALLBACK = 'en'

/** 'zh_CN' → 'zh'，'en-US' → 'en' */
export function normalizeLang(raw?: string): string {
  if (!raw) return FALLBACK
  const lower = raw.toLowerCase().replace(/_/g, '-')
  const base = lower.split('-')[0]
  return base || FALLBACK
}

/** 读系统语言作为客人端默认语种 */
export function detectLang(): string {
  try {
    const info = Taro.getSystemInfoSync()
    return normalizeLang((info as any).language)
  } catch {
    return FALLBACK
  }
}

/**
 * 语言优先级链（与现有 H5 一致，只是把浏览器 API 换掉）：
 *   URL 参数 ?lang=  →  上次选择（缓存）  →  系统语言  →  'en'
 */
export function resolveLang(urlLang?: string, cached?: string | null): string {
  // ★ 只判断「有没有传」，不能拿是否等于 'en' 做判据 —— 否则 ?lang=en 会被跳过而误用缓存
  if (urlLang) return normalizeLang(urlLang)
  if (cached) return normalizeLang(cached)
  return detectLang()
}
