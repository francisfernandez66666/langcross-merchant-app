/** 后端 API 客户端与类型定义。开发期经 Vite 代理到 http://localhost:8080。 */

export type Account = { id: string; phone: string; created_at: string }

/** 受支持的语言。tier 是质量分层：1 承诺质量 / 2 较好 / 3 尽力而为。 */
export type Lang = {
  code: string
  native: string
  name_zh: string
  tier: 1 | 2 | 3
}

export type LangsResponse = {
  source: string
  langs: Lang[]
  engine: string
}

export type Store = {
  id: string
  account_id: string
  name: string
  slug: string
  addr?: string
  wifi_ssid?: string
  wifi_pwd?: string
  coupon_enabled: boolean
  service_tier: 'pure' | 'service'
  order_mode: 'assist' | 'replace'
  /** 分类名多语种表：{lang: {原文分类: 译文}} */
  category_names?: Record<string, Record<string, string>>
}

export type Dish = {
  id: string
  name_zh: string
  name_en?: string
  /** 多语种菜名：{"en":"...","ja":"..."}；name_zh 始终是原文真值 */
  names?: Record<string, string>
  description?: string
  price?: number
  category?: string
  attrs?: Record<string, unknown>
  sold_out: boolean
  remaining?: number
}

export type DraftItem = {
  name: string
  price?: number
  category?: string
  selected: boolean
  name_en?: string
  /** 多语种预览结果 */
  names?: Record<string, string>
}

export type Coupon = {
  id: string
  code: string
  status: 'issued' | 'redeemed' | 'expired'
  expire_at: string
  lang?: string
  redeemed_at?: string
}

export type CouponTemplate = {
  id: string
  content: Record<string, unknown>
  valid_days: number
  enabled: boolean
}

export type Rating = {
  id: string
  ref?: string
  score_dish: number
  score_service: number
  score_return: number
  comment_native?: string
  comment_zh?: string
  created_at: string
}

export type RatingSummary = {
  count: number
  avg_dish: number
  avg_service: number
  avg_return: number
  low_rate: number
  would_return: number
}

export type Insights = {
  rating: RatingSummary
  scans: number
  coupons_issued: number
  coupons_redeemed: number
  recent_ratings: Rating[]
  scope_note: string
}

export type StoreLinks = {
  store_page: string
  door: string
  table: string
  qr_door: string
  qr_table: string
}

const TOKEN_KEY = 'lc_token'

export const tokenStore = {
  get: () => localStorage.getItem(TOKEN_KEY),
  set: (t: string) => localStorage.setItem(TOKEN_KEY, t),
  clear: () => localStorage.removeItem(TOKEN_KEY),
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = tokenStore.get()
  const res = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init?.headers ?? {}),
    },
  })
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>
  if (!res.ok) {
    const err = data.error as { msg?: string } | undefined
    throw new Error(err?.msg ?? `请求失败（${res.status}）`)
  }
  return data as T
}

const post = <T,>(p: string, body: unknown) =>
  request<T>(p, { method: 'POST', body: JSON.stringify(body) })

const patch = <T,>(p: string, body: unknown) =>
  request<T>(p, { method: 'PATCH', body: JSON.stringify(body) })

export const api = {
  health: () => request<{ ocr_ready: boolean; translate_ready: boolean; translate_mode: string }>('/api/health'),

  // ── 认证 ──
  sendOtp: (phone: string) => post<{ dev_code?: string }>('/api/auth/otp', { phone }),
  login: (phone: string, code: string) =>
    post<{ token: string; account: Account }>('/api/auth/login', { phone, code }),
  me: () => request<{ account: Account; stores: Store[] }>('/api/me'),

  // ── 商家端 ──
  createStore: (name: string, addr = '', wifi_ssid = '', wifi_pwd = '') =>
    post<{ store: Store; coupon_template: CouponTemplate; links: StoreLinks }>('/api/stores', {
      name,
      addr,
      wifi_ssid,
      wifi_pwd,
    }),
  getStore: (id: string) =>
    request<{ store: Store; dishes: Dish[]; links: StoreLinks; draft?: unknown }>(`/api/stores/${id}`),
  patchStore: (id: string, body: Partial<Store>) => patch<{ store: Store }>(`/api/stores/${id}`, body),
  saveDraft: (id: string, items: DraftItem[]) =>
    post<{ draft: { id: string } }>(`/api/stores/${id}/draft`, { lang: 'zh', items }),
  confirmDraft: (id: string) => post<{ draft_id: string }>(`/api/stores/${id}/draft/confirm`, {}),
  translate: (id: string, targets: string[] = []) =>
    post<{ dishes: Dish[]; translated_count: number; targets: string[]; category_names?: Record<string, Record<string, string>> }>(
      `/api/stores/${id}/translate`,
      { targets },
    ),
  listDishes: (id: string) => request<{ dishes: Dish[] }>(`/api/stores/${id}/dishes`),
  patchDish: (id: string, dishId: string, body: { sold_out: boolean; remaining?: number }) =>
    patch<{ ok: boolean }>(`/api/stores/${id}/dishes/${dishId}`, body),
  insights: (id: string) => request<Insights>(`/api/stores/${id}/insights`),
  listCoupons: (id: string) => request<{ coupons: Coupon[] }>(`/api/stores/${id}/coupons`),
  redeem: (id: string, code: string) => post<{ coupon: Coupon }>(`/api/stores/${id}/coupon/redeem`, { code }),

  // ── 客人端（公开） ──
  publicStore: (slug: string, ref: string) =>
    request<{ store: Store; dishes: Dish[]; coupon_template?: CouponTemplate }>(
      `/api/public/store/${slug}?ref=${encodeURIComponent(ref)}`,
    ),
  submitRating: (
    slug: string,
    body: { ref: string; score_dish: number; score_service: number; score_return: number; comment?: string; lang?: string },
  ) =>
    post<{ rating: Rating; coupon: Coupon | null; coupon_gift?: string }>(
      `/api/public/store/${slug}/rating`,
      body,
    ),

  // ── 多语种能力 ──
  langs: () => request<LangsResponse>('/api/translation/langs'),

  // ── P4 注册前多语预览 ──
  previewTranslate: (items: DraftItem[], targets: string[]) =>
    post<{
      items: DraftItem[]
      categories: Record<string, Record<string, string>>
      source_cats: string[]
      targets: string[]
      engine: string
    }>('/api/public/translate-preview', { items, from: 'zh', targets }),

  // ── 工具 ──
  ocr: (imageBase64: string) =>
    post<{ result: { items: { name: string; price?: number }[]; need_manual: boolean; engine: string } }>(
      '/api/ocr/extract',
      { image_base64: imageBase64 },
    ),
  track: (store_id: string, name: string, payload?: Record<string, unknown>) =>
    post<{ ok: boolean }>('/api/events', { store_id, name, payload }).catch(() => ({ ok: false })),
}
