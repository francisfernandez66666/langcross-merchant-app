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
export type Account = {
  id: string
  phone: string
  /** 所属租户；空串 = 平台级（超管）。前端据此显隐「后台管理」入口。 */
  tenant_id: string
  /** 角色：super_admin / tenant_admin / org_admin / manager / staff。服务端为准，前端只做体验优化。 */
  role: string
  /** 显示名（后台列表展示用；登录时不填则空串）。 */
  display_name?: string
  /** 账号状态：active / disabled。disabled 的账号登录后后端会拒。 */
  status?: string
  created_at: string
}

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
  image_url?: string // 菜品图片 URL（相对路径 /api/uploads/xxx）；空 = 无图，客人端渲染占位块
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

/** 助手建议的一个动作（加菜 / 服务铃 / 加水）。客人端填 Key/Name/Arg，URL 留空。 */
export type Action = {
  key: string // add_dish / water / bell / bill
  name: string // 按钮文案（跟随客人语言）
  url?: string // 商家端用，客人端留空
  arg?: string // 客人端用：add_dish 的菜品 ID
}

/** 一次 AI 点菜助手问答的产出。 */
export type AssistReply = {
  content: string // 正文（已是客人语言，且经守卫核对，不会编造菜品/价格）
  actions?: Action[] // 建议动作（渲染成按钮，客人点一下才真正发起）
  source: string // llm / rule / flow / fallback
  model?: string // 实际使用的模型（排障用）
}

/** 客人服务请求（服务铃 / 加水 / 加菜 / 买单）。商家端待办列表的数据单元。 */
export type GuestRequest = {
  id: string
  store_id: string
  table_ref: string // 桌码 ref（t01 等），空 = 未关联桌
  kind: 'bell' | 'water' | 'add_dish' | 'bill' | 'other'
  payload?: { dish_id?: string; qty?: number } // add_dish 时带菜品 ID 与数量
  lang: string // 客人界面语言（商家端据此展示对应提示）
  note: string // 客人原话（语音转写后的文本），便于服务员理解上下文
  status: 'open' | 'done' | 'cancelled'
  created_at: number // Unix 秒
  dish_name?: string // 加菜请求时附带的菜名（后端批量补，省得服务员回查）
}

/** 平台 LLM 凭据（超管维护）。Key 只返回脱敏串，明文只在 reveal 一次性返回。 */
export type AICredential = {
  id: string
  /** 空串 = 平台级默认（所有租户共用）；具体租户 ID = 该租户专用。 */
  tenant_id: string
  name: string // 别名，便于人识别（如「平台默认 DeepSeek」）
  base_url: string // 如 https://api.deepseek.com/v1
  model: string // 如 deepseek-chat / gpt-4o-mini
  key_masked: string // 脱敏展示（如 sk-****1234）
  status: 'active' | 'disabled'
  created_by?: string
  created_at: number
  updated_at: number
}

/** 租户（平台超管维护）。active/disabled/expired 是生命周期三态。 */
export type Tenant = {
  id: string
  code: string // 对外短编码（3-32 位小写字母/数字/中划线）
  name: string
  status: 'active' | 'disabled' | 'expired'
  expires_at?: number // 到期时间（Unix 秒）；缺省 = 永久
  perms?: Record<string, unknown> // 配额与开关（语种白名单 / 日字符上限等）
  created_at: number
  stores?: number // 后台列表附带的门店数（不落库）
  accounts?: number // 后台列表附带的账号数（不落库）
}

/** 租户翻译 Key（能言开放 API 的凭据）。明文只在「查看明文」时一次性返回。 */
export type TranslateKey = {
  id: string
  tenant_id: string
  name: string // 别名，便于人识别
  provider: string // 提供方：lexicorn
  key_masked: string // 脱敏展示（如 lx-ab…wxyz）
  mode: string // fast / pro
  status: 'active' | 'disabled'
  daily_limit: number // 当日调用上限；0 = 不限
  last_used_at?: number // 最近一次成功使用（Unix 秒）
  created_by?: string
  created_at: number
  updated_at: number
}

/** 审计日志一条：谁在什么时间对什么目标做了什么。 */
export type AuditEntry = {
  id: number
  tenant_id: string
  actor_id: string
  actor_role: string
  action: string // 如 create_tenant / key_test
  target: string // 目标对象 ID
  detail: string // 人话描述
  created_at: number // Unix 秒
}

/** 平台统计（超管概览用的四个总数）。 */
export type PlatformStats = {
  ok: boolean
  tenants: number
  accounts: number
  stores: number
  translation_keys: number
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
 * 文件上传（multipart）。
 *
 * 与 post 的区别：multipart 的 boundary 必须由浏览器随 body 自动生成，
 * 因此这里**不能**复用 request（它默认写死 `application/json`），
 * 单独走一遍 fetch，只补 Authorization；错误处理与 request 保持一致。
 */
async function upload<T>(p: string, file: File, field = 'image'): Promise<T> {
  const token = tokenStore.get()
  const fd = new FormData()
  fd.append(field, file)
  const res = await fetch(p, {
    method: 'POST',
    body: fd,
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>
  if (!res.ok) {
    const err = data.error as { msg?: string } | undefined
    throw new Error(err?.msg ?? `请求失败（${res.status}）`)
  }
  return data as T
}

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
  // 商家看服务请求（默认只看待办）；status 可传 open/done/cancelled，limit 控制条数
  listGuestRequests: (id: string, status = '', limit = 50) =>
    request<{ requests: GuestRequest[]; open_count: number }>(
      `/api/stores/${id}/requests?status=${encodeURIComponent(status)}&limit=${limit}`,
    ),
  // 标记请求完成 / 取消（status: done / cancelled）
  doneGuestRequest: (id: string, rid: string, status: 'done' | 'cancelled') =>
    post<{ ok: boolean; status: string }>(`/api/stores/${id}/requests/${rid}/done`, { status }),

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

  // ── 客人端 AI 点菜助手（语音转文字后走同一个接口） ──
  // 提问一次；lang 决定助手用哪种语言作答（也是 RAG 取菜名的语种）。
  // 多轮靠服务端 cookie（lc_guest）隔离，前端无需管会话 ID。
  assist: (slug: string, body: { question: string; lang: string }) =>
    post<{ reply: AssistReply; lang: string; store_id: string }>(
      `/api/public/store/${slug}/assist`,
      body,
    ),
  // 结束当前助手会话（清流程 + 历史）。
  clearAssist: (slug: string) => post<{ ok: boolean }>(`/api/public/store/${slug}/assist/clear`, {}),
  // 发起一条服务请求（助手所有动作的落点）：加菜 / 加水 / 服务铃 / 买单。
  // kind 必填；add_dish 需带 dish_id 与 qty；note 是客人原话（语音转写）。
  createGuestRequest: (
    slug: string,
    body: { kind: string; dish_id?: string; qty?: number; note?: string; lang?: string; ref?: string },
  ) => post<{ ok: boolean; request_id: string; message: string }>(`/api/public/store/${slug}/requests`, body),

  // ── 商家端 AI 经营助手（需登录） ──
  // 一次问答；store_id 可空（空 = 平台级问答，只答产品怎么用）。
  assistChat: (store_id: string, question: string) =>
    post<{ reply: AssistReply; store_id: string; assist_llm: boolean }>('/api/assist/chat', { store_id, question }),
  // 清空对话（「重新开始」）。
  clearAssistChat: (store_id: string) => post<{ ok: boolean }>('/api/assist/clear', { store_id }),

  // ── 平台 AI 凭据管理（超管；tenant_admin 仅只读脱敏列表） ──
  // 列出凭据（脱敏）；can_write 标识当前账号是否可写（仅超管为 true）。
  listAICredentials: () =>
    request<{ credentials: AICredential[]; can_write: boolean }>('/api/admin/ai-credentials'),
  // 新增凭据（密文落库）；明文只在本次响应里返回一次。
  createAICredential: (body: { name: string; tenant_id: string; base_url: string; model: string; api_key: string }) =>
    post<{ credential: AICredential; note: string }>('/api/admin/ai-credentials', body),
  // 启用 / 停用凭据。
  setAICredentialStatus: (id: string, status: 'active' | 'disabled') =>
    post<{ ok: boolean; status: string }>(`/api/admin/ai-credentials/${id}/status`, { status }),
  // 轮换 API Key（新密文覆盖，旧的立即失效）；返回新的脱敏串。
  rotateAICredential: (id: string, api_key: string) =>
    post<{ ok: boolean; key_masked: string }>(`/api/admin/ai-credentials/${id}/rotate`, { api_key }),
  // 查看 API Key 明文（一次性返回 + 落审计）。
  revealAICredential: (id: string) => post<{ ok: boolean; api_key: string }>(`/api/admin/ai-credentials/${id}/reveal`, {}),
  // 删除凭据。
  deleteAICredential: (id: string) => request<{ ok: boolean }>(`/api/admin/ai-credentials/${id}`, { method: 'DELETE' }),
  // 试译一次，验证凭据可用（地址 / 模型 / Key 是否都对）。
  testAICredential: (id: string) =>
    post<{ ok: boolean; source: string; model: string; content: string; note: string }>(
      `/api/admin/ai-credentials/${id}/test`,
      {},
    ),

  // ── 菜品图片（商家端，需登录） ──
  // 上传/替换菜品图片：multipart 字段名 image；≤5MB，仅 PNG/JPEG/WebP/GIF。
  // 后端会删掉被替换的旧文件，返回新的图片 URL。
  uploadDishImage: (storeId: string, dishId: string, file: File) =>
    upload<{ ok: boolean; image_url: string }>(`/api/stores/${storeId}/dishes/${dishId}/image`, file),
  // 删除菜品图片（幂等；文件不存在也算成功）。
  deleteDishImage: (storeId: string, dishId: string) =>
    request<{ ok: boolean }>(`/api/stores/${storeId}/dishes/${dishId}/image`, { method: 'DELETE' }),

  // ── 平台管理：租户（超管专属） ──
  listTenants: () => request<{ ok: boolean; tenants: Tenant[] }>('/api/admin/tenants'),
  // 新建租户：code 是 3-32 位小写字母/数字/中划线，不可与已有租户重复。
  createTenant: (body: { code: string; name: string; expires_at?: number; perms?: Record<string, unknown> }) =>
    post<{ ok: boolean; tenant: Tenant }>('/api/admin/tenants', body),
  // 改租户名 / 停用启用 / 到期时间 / 配额开关。
  patchTenant: (
    id: string,
    body: {
      name?: string
      status?: 'active' | 'disabled' | 'expired'
      expires_at?: number | null // null = 改为永久
      perms?: Record<string, unknown>
    },
  ) => patch<{ ok: boolean; tenant: Tenant }>(`/api/admin/tenants/${id}`, body),
  // 某租户名下的门店清单（超管跨租户只读查看）。
  tenantStores: (id: string) => request<{ ok: boolean; stores: Store[] }>(`/api/admin/tenants/${id}/stores`),
  // 平台总数概览（租户 / 账号 / 门店 / 翻译 Key）。
  platformStats: () => request<PlatformStats>('/api/admin/stats'),
  // 我所属的租户信息 + 我的角色（租户管理员看自己，超管返回空）。
  myTenant: () => request<{ ok: boolean; tenant: Tenant | null; my_role: string }>('/api/admin/my-tenant'),

  // ── 平台管理：账号与角色（超管 / 租户管理员） ──
  listAccounts: () => request<{ ok: boolean; accounts: Account[] }>('/api/admin/accounts'),
  // 新建账号：角色不能高于或等于自己的等级；手机号已存在则改为就地改角色。
  createAccount: (body: { phone: string; role: string; display_name?: string }) =>
    post<{ ok: boolean; created: boolean; account: Account }>('/api/admin/accounts', body),
  // 调整已有账号角色（不能改自己、不能授予同级或更高级角色）。
  patchAccountRole: (id: string, role: string) =>
    patch<{ ok: boolean; account: Account }>(`/api/admin/accounts/${id}/role`, { role }),
  // 停用 / 启用账号（不能停用同级或更高级成员）。
  patchAccountStatus: (id: string, status: 'active' | 'disabled') =>
    patch<{ ok: boolean; account: Account }>(`/api/admin/accounts/${id}/status`, { status }),

  // ── 平台管理：翻译 Key（★ 超管专属增删改查，租户一律不接触） ──
  listTranslateKeys: () =>
    request<{ ok: boolean; keys: TranslateKey[]; active_key_id: string }>('/api/admin/translate-keys'),
  // 新增翻译 Key：明文只在创建响应里出现一次，此后只能「查看明文」复制。
  createTranslateKey: (body: { name: string; key: string; mode?: string; daily_limit?: number }) =>
    post<{ ok: boolean; key: TranslateKey; note: string }>('/api/admin/translate-keys', body),
  // 查看 Key 明文（一次性返回，且会落审计日志）。
  revealTranslateKey: (id: string) =>
    post<{ ok: boolean; api_key: string }>(`/api/admin/translate-keys/${id}/reveal`, {}),
  // 轮换 Key：新 Key 覆盖，旧的立即失效。
  rotateTranslateKey: (id: string, key: string) =>
    post<{ ok: boolean }>(`/api/admin/translate-keys/${id}/rotate`, { key }),
  // 启用 / 停用 Key（停用后翻译链回落，最多 30s 缓存 TTL 后生效）。
  setTranslateKeyStatus: (id: string, status: 'active' | 'disabled') =>
    post<{ ok: boolean }>(`/api/admin/translate-keys/${id}/status`, { status }),
  // 试译：用这把 Key 真翻一句，验证可用性并顺带带回余额。
  testTranslateKey: (id: string, text: string, to: string) =>
    post<{
      ok: boolean
      source: string
      target: string
      to: string
      elapsed_ms: number
      balance?: Record<string, unknown>
      error?: string
    }>(`/api/admin/translate-keys/${id}/test`, { text, to }),
  deleteTranslateKey: (id: string) =>
    request<{ ok: boolean }>(`/api/admin/translate-keys/${id}`, { method: 'DELETE' }),

  // ── 平台管理：审计（超管全量 / 租户管理员只看本租户） ──
  listAudit: () => request<{ ok: boolean; audit: AuditEntry[] }>('/api/admin/audit'),

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
