/**
 * 后端 API 客户端与类型定义。
 *
 * 约定（全站统一）：
 *   - 开发期经 Vite 代理到 http://localhost:8080，因此这里一律用相对路径 `/api/...`。
 *   - 所有响应形如 `{ ok: true, ... }` 或 `{ ok: false, error: { code, msg } }`；
 *     非 2xx 或 `ok:false` 时 `request()` 会抛出带 `msg` 的 Error。
 *   - 登录后请求自动带 `Authorization: Bearer <token>`，token 存 localStorage。
 *
 * 本文件只做「传输 + 类型」，不含业务判断；页面拿到的都是后端已加工好的数据。
 */

/** 商家账号。phone 即登录标识，id 是后端主键。 */
export type Account = { id: string; phone: string; created_at: string }

/**
 * 受支持的语言（来自后端注册表，前端不硬编码清单）。
 *   - `code`：短码（zh/en/…），是前后端对齐、与界面词典匹配的唯一键。
 *   - `native`：该语言的母语写法（选择器主显示名）。
 *   - `name_zh`：中文名（选择器副显示名，方便商家侧辨认）。
 *   - `tier`：质量分层——1 承诺质量 / 2 较好 / 3 尽力而为。
 *     T1=zh/en（承诺），T2=ja/ko/th（较好），T3=fr/es/de/pt/vi（尽力而为）。
 */
export type Lang = {
  code: string
  native: string
  name_zh: string
  tier: 1 | 2 | 3
}

/** `GET /api/translation/langs` 的响应：注册表全量 + 源语言 + 翻译引擎标识。 */
export type LangsResponse = {
  source: string
  langs: Lang[]
  engine: string
}

/** 门店（公开信息 + 商家配置）。客人端只用其中的展示字段。 */
export type Store = {
  id: string
  account_id: string
  name: string
  slug: string
  addr?: string
  wifi_ssid?: string
  wifi_pwd?: string
  coupon_enabled: boolean // 是否开启评价发券；false 时客人端不显示「评价领券」
  service_tier: 'pure' | 'service' // 服务档位：纯自助 / 含人工服务
  order_mode: 'assist' | 'replace' // 点单模式：辅助 / 替代
  /** 分类名多语种表：{lang: {原文分类: 译文}} */
  category_names?: Record<string, Record<string, string>>
}

/** 菜品。`name_zh` 永远是原文真值，`names` 是各语种译文（可能缺某语种）。 */
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
  remaining?: number // 剩余份数；null/undefined 表示不限量
}

/** 商家录入菜单的草稿项（P4 预览翻译也复用此结构）。 */
export type DraftItem = {
  name: string
  price?: number
  category?: string
  selected: boolean // 是否勾选参与入库/翻译
  name_en?: string
  /** 多语种预览结果 */
  names?: Record<string, string>
}

/** 优惠券实例。`code` 是到店核销凭据，`status` 描述生命周期。 */
export type Coupon = {
  id: string
  code: string
  status: 'issued' | 'redeemed' | 'expired'
  expire_at: string
  lang?: string // 发券时客人所用语言，用于券面文案本地化
  redeemed_at?: string
}

/** 券模板（店级配置）：content 是各语种的券面文案模板。 */
export type CouponTemplate = {
  id: string
  content: Record<string, unknown>
  valid_days: number
  enabled: boolean
}

/** 单条评价。`comment_native` 是客人母语原文，`comment_zh` 是回译中文（供商家看）。 */
export type Rating = {
  id: string
  ref?: string // 来源码（桌码/门口码），用于归因
  score_dish: number
  score_service: number
  score_return: number
  comment_native?: string
  comment_zh?: string
  created_at: string
}

/** 评价聚合：商家看板的汇总指标。 */
export type RatingSummary = {
  count: number
  avg_dish: number
  avg_service: number
  avg_return: number
  low_rate: number // 低分占比
  would_return: number // 「会再来」占比
}

/** 商家经营看板数据。`scope_note` 说明统计口径（避免商家误读）。 */
export type Insights = {
  rating: RatingSummary
  scans: number
  coupons_issued: number
  coupons_redeemed: number
  recent_ratings: Rating[]
  scope_note: string
}

/** 门店对外链接与二维码（商家可下载物料）。 */
export type StoreLinks = {
  store_page: string
  door: string
  table: string
  qr_door: string
  qr_table: string
}

/** localStorage 中登录 token 的键名。 */
const TOKEN_KEY = 'lc_token'

/** 登录态存取封装：统一键名，避免各处硬编码字符串。 */
export const tokenStore = {
  get: () => localStorage.getItem(TOKEN_KEY),
  set: (t: string) => localStorage.setItem(TOKEN_KEY, t),
  clear: () => localStorage.removeItem(TOKEN_KEY),
}

/**
 * 底层请求封装：拼头、发请求、按统一约定解析与报错。
 *
 * 行为：
 *   - 默认 `Content-Type: application/json`；带 token 时自动加 Bearer 头。
 *   - `init.headers` 可覆盖默认头（展开顺序在最后）。
 *   - 响应体解析失败（如空体/非 JSON）时退化为 `{}`，避免解析异常盖过真正的 HTTP 错误。
 *   - 非 2xx 时抛 Error，优先用后端 `error.msg`，否则给「请求失败（状态码）」。
 */
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

/** POST 简写：JSON 序列化请求体。 */
const post = <T,>(p: string, body: unknown) =>
  request<T>(p, { method: 'POST', body: JSON.stringify(body) })

/** PATCH 简写：JSON 序列化请求体。 */
const patch = <T,>(p: string, body: unknown) =>
  request<T>(p, { method: 'PATCH', body: JSON.stringify(body) })

/**
 * 后端接口集合。按「认证 / 商家端 / 客人端 / 多语种 / 预览 / 工具」分区。
 * 每个方法返回已类型化的响应；错误统一由 `request` 抛出。
 */
export const api = {
  // ── 健康检查：探测 OCR 与翻译引擎是否就绪 ──
  health: () => request<{ ocr_ready: boolean; translate_ready: boolean; translate_mode: string }>('/api/health'),

  // ── 认证 ──
  // 发送短信验证码；开发环境返回 dev_code 便于免短信登录
  sendOtp: (phone: string) => post<{ dev_code?: string }>('/api/auth/otp', { phone }),
  // 手机号 + 验证码换 token
  login: (phone: string, code: string) =>
    post<{ token: string; account: Account }>('/api/auth/login', { phone, code }),
  // 当前登录账号及其名下门店
  me: () => request<{ account: Account; stores: Store[] }>('/api/me'),

  // ── 商家端 ──
  // 新建门店，同时后端会生成默认券模板与物料链接
  createStore: (name: string, addr = '', wifi_ssid = '', wifi_pwd = '') =>
    post<{ store: Store; coupon_template: CouponTemplate; links: StoreLinks }>('/api/stores', {
      name,
      addr,
      wifi_ssid,
      wifi_pwd,
    }),
  // 取单个门店全量数据（含菜品、物料链接、可选草稿）
  getStore: (id: string) =>
    request<{ store: Store; dishes: Dish[]; links: StoreLinks; draft?: unknown }>(`/api/stores/${id}`),
  // 局部更新门店配置（如开关券、改 WiFi）
  patchStore: (id: string, body: Partial<Store>) => patch<{ store: Store }>(`/api/stores/${id}`, body),
  // 保存菜单草稿（源语言固定 zh，逐项 items）
  saveDraft: (id: string, items: DraftItem[]) =>
    post<{ draft: { id: string } }>(`/api/stores/${id}/draft`, { lang: 'zh', items }),
  // 确认草稿：把草稿项正式入库为菜品
  confirmDraft: (id: string) => post<{ draft_id: string }>(`/api/stores/${id}/draft/confirm`, {}),
  // 触发翻译：targets 为空则由后端按注册表决定目标语种；返回更新后的菜品与分类名
  translate: (id: string, targets: string[] = []) =>
    post<{ dishes: Dish[]; translated_count: number; targets: string[]; category_names?: Record<string, Record<string, string>> }>(
      `/api/stores/${id}/translate`,
      { targets },
    ),
  // 列出某门店菜品
  listDishes: (id: string) => request<{ dishes: Dish[] }>(`/api/stores/${id}/dishes`),
  // 更新单个菜品状态（售罄 / 剩余份数）——商家现场改菜用
  patchDish: (id: string, dishId: string, body: { sold_out: boolean; remaining?: number }) =>
    patch<{ ok: boolean }>(`/api/stores/${id}/dishes/${dishId}`, body),
  // 经营看板数据
  insights: (id: string) => request<Insights>(`/api/stores/${id}/insights`),
  // 已发券列表
  listCoupons: (id: string) => request<{ coupons: Coupon[] }>(`/api/stores/${id}/coupons`),
  // 到店核销：用券码核销
  redeem: (id: string, code: string) => post<{ coupon: Coupon }>(`/api/stores/${id}/coupon/redeem`, { code }),

  // ── 客人端（公开，无需登录） ──
  // 拉门店公开页数据；ref 是来源码，需 encodeURIComponent 防注入/截断
  publicStore: (slug: string, ref: string) =>
    request<{ store: Store; dishes: Dish[]; coupon_template?: CouponTemplate }>(
      `/api/public/store/${slug}?ref=${encodeURIComponent(ref)}`,
    ),
  // 提交三维度评价，成功返回新券（可能为 null，如该店未开券）与券面文案
  submitRating: (
    slug: string,
    body: { ref: string; score_dish: number; score_service: number; score_return: number; comment?: string; lang?: string },
  ) =>
    post<{ rating: Rating; coupon: Coupon | null; coupon_gift?: string }>(
      `/api/public/store/${slug}/rating`,
      body,
    ),

  // ── 多语种能力 ──
  // 语种注册表：前端语言清单的唯一来源，不硬编码
  langs: () => request<LangsResponse>('/api/translation/langs'),

  // ── P4 注册前多语预览 ──
  // 未注册也能试用翻译：源语言固定 zh，返回译文项与分类译文
  previewTranslate: (items: DraftItem[], targets: string[]) =>
    post<{
      items: DraftItem[]
      categories: Record<string, Record<string, string>>
      source_cats: string[]
      targets: string[]
      engine: string
    }>('/api/public/translate-preview', { items, from: 'zh', targets }),

  // ── 工具 ──
  // 拍照识别菜单：传 base64 图，返回识别项与是否需人工校对
  ocr: (imageBase64: string) =>
    post<{ result: { items: { name: string; price?: number }[]; need_manual: boolean; engine: string } }>(
      '/api/ocr/extract',
      { image_base64: imageBase64 },
    ),
  // 埋点上报：失败静默（catch 成 {ok:false}），绝不因埋点失败影响主流程
  track: (store_id: string, name: string, payload?: Record<string, unknown>) =>
    post<{ ok: boolean }>('/api/events', { store_id, name, payload }).catch(() => ({ ok: false })),
}
