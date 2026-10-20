import Taro from '@tarojs/taro'

// API_BASE 由 config/dev.ts 与 config/prod.ts 注入，两端共用同一份业务代码。
declare const API_BASE: string

/** 统一响应信封：抹平 fetch 与 Taro.request 差异后的归一化结果。
 *  `ok` 由各端自行判定（H5 看 res.ok，小程序看 statusCode 是否 2xx），
 *  业务层只认 {ok, status, data} 三件套，错误分支两端一致。 */
export interface HttpResponse<T> {
  ok: boolean
  status: number
  data: T
}

/** 请求配置子集：只收业务层真正用得上的三个字段，与浏览器 RequestInit 兼容。 */
export interface RequestInitLike {
  method?: string
  headers?: Record<string, string>
  body?: string
}

/**
 * ★ 统一请求适配层 —— 这是让 web/src/api/client.ts 业务逻辑「零改动」搬过来的关键。
 *
 * H5 端：直接走浏览器 fetch，行为与现有 web 工程完全一致。
 * 小程序端：走 Taro.request（必须 HTTPS + 微信后台域名白名单）。
 *
 * 业务层（services/）只需要把 fetch(url, init) 换成 http(url, init)，
 * 返回值归一为 { ok, status, data }，其余的错误码分支、token 注入全部照搬。
 */
export async function http<T = any>(
  path: string,
  init: RequestInitLike = {},
): Promise<HttpResponse<T>> {
  const url = /^https?:\/\//.test(path) ? path : `${API_BASE}${path}`

  // ---- H5 端 ----
  if (process.env.TARO_ENV === 'h5') {
    const res = await fetch(url, init as RequestInit)
    let data: any = null
    try {
      data = await res.json()
    } catch {
      data = null
    }
    return { ok: res.ok, status: res.status, data: data as T }
  }

  // ---- 小程序端 ----
  const res = await Taro.request<T>({
    url,
    method: (init.method as any) || 'GET',
    header: init.headers,
    // Taro.request 收对象，业务层传的是 JSON 字符串，这里还原
    data: init.body ? safeParse(init.body) : undefined,
    dataType: 'json',
  })

  return {
    ok: res.statusCode >= 200 && res.statusCode < 300,
    status: res.statusCode,
    data: res.data,
  }
}

function safeParse(s: string): any {
  try {
    return JSON.parse(s)
  } catch {
    return undefined
  }
}

/** 带 token 的便捷封装（商家端 / 店员端用） */
export function authHeader(token?: string): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (token) headers.Authorization = `Bearer ${token}`
  return headers
}
