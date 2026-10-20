import Taro from '@tarojs/taro'

/**
 * ★ 缓存适配层 —— 替换 web/src 里的 4 处浏览器缓存读写。
 * Taro 的同步 API 在 H5 端会自动映射到 localStorage，两端行为一致。
 */
export const store = {
  get<T = string>(key: string): T | null {
    try {
      const v = Taro.getStorageSync(key)
      return v === '' || v === undefined || v === null ? null : (v as T)
    } catch {
      return null
    }
  },
  set(key: string, value: unknown): void {
    try {
      Taro.setStorageSync(key, value)
    } catch {
      /* 存储超限时静默失败，不影响主流程 */
    }
  },
  remove(key: string): void {
    try {
      Taro.removeStorageSync(key)
    } catch {
      /* ignore */
    }
  },
}

// 登录 token 的存储键。商家 / 店员分包登录后写入，请求时经 authHeader 带上。
export const TOKEN_KEY = 'lc.token'
// 客人端界面语种的存储键（与 web 端客人端同一键名，双端行为一致）。
export const LANG_KEY = 'guest.lang'
