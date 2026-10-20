import { useEffect, useMemo, useState } from 'react'
import { View, Text, Image, ScrollView } from '@tarojs/components'
import { useRouter, usePullDownRefresh, stopPullDownRefresh } from '@tarojs/taro'
import { http } from '../../utils/http'
import { store, LANG_KEY } from '../../utils/storage'
import { resolveLang, normalizeLang } from '../../utils/lang'
import { t } from '../../i18n'
import './index.css'

declare const API_BASE: string

type Names = Record<string, string>

type Dish = {
  id: string
  name_zh: string
  names?: Names
  price?: number
  category?: string
  image_url?: string
  sold_out?: boolean
}

type StoreData = {
  store: {
    id: string
    name: string
    category_names?: Record<string, Names>
    wifi_ssid?: string
    // ★ 字段名与后端 handlePublicStore 对齐：wifi_pwd（不是 wifi_password）
    wifi_pwd?: string
  }
  dishes: Dish[]
}

// 受支持语种（来自 GET /api/translation/langs）。★ 纪律 12：前端不硬编码语种清单，
// 新增语种 = 后端注册表登记一行；这里只在注册表还没拉到时做占位。
type Lang = { code: string; native: string; name_zh: string; tier: 1 | 2 | 3 }

// 注册表未就绪时的静态占位（与 web 端常用语种一致；拉到注册表后立即被替换）
const FALLBACK_LANGS: Lang[] = [
  { code: 'zh', native: '中文', name_zh: '中文', tier: 1 },
  { code: 'en', native: 'English', name_zh: '英语', tier: 1 },
  { code: 'ja', native: '日本語', name_zh: '日语', tier: 2 },
  { code: 'ko', native: '한국어', name_zh: '韩语', tier: 2 },
  { code: 'th', native: 'ไทย', name_zh: '泰语', tier: 2 },
]

/**
 * ★ 微信「扫普通链接二维码打开小程序」会把原 URL 通过 options.q 传进来（需 decodeURIComponent）。
 *   形如 https://api.langcross.com/s/{slug}?ref=t01 → 解析出 slug 与 ref。
 *   这样已印刷的门口码、桌码不用重印，见方案 §6.1。
 */
function parseScan(rawUrl: string): { slug: string; ref: string } {
  try {
    const u = new URL(rawUrl)
    const slug = u.pathname.split('/').filter(Boolean).pop() || ''
    return { slug, ref: u.searchParams.get('ref') || '' }
  } catch {
    return { slug: rawUrl, ref: '' }
  }
}

/**
 * 小程序客人端门店页：扫桌码 / 门口码进入，按客人选定语种渲染菜单。
 *
 * 与 web 端 StorePage.tsx 的对应关系：同一套公开接口（/api/public/store/{slug}），
 * 同一套渲染纪律（标题永远是目标语言、缺译文如实标注「未翻译」）；
 * 差异只在入口参数——微信「扫普通链接二维码打开小程序」把原 URL 放在
 * options.q 里，需 decodeURIComponent 后解析（见 parseScan）。
 * 语种选择持久化在 LANG_KEY，刷新后不丢。
 */
export default function StorePage() {
  const router = useRouter()
  const [lang, setLang] = useState<string>('en')
  const [data, setData] = useState<StoreData | null>(null)
  const [langs, setLangs] = useState<Lang[]>([]) // 后端注册表返回的受支持语种（唯一真值）
  const [loading, setLoading] = useState(true)

  // 扫码参数：微信扫普通链接码走 q，小程序码 / 页面跳转走 slug
  const { slug, ref } = useMemo(() => {
    const q = router.params.q ? decodeURIComponent(router.params.q) : ''
    if (q) return parseScan(q)
    return { slug: router.params.slug || '', ref: router.params.ref || '' }
  }, [router.params])

  const load = async () => {
    if (!slug) {
      setLoading(false)
      return
    }
    const res = await http<StoreData>(`/api/public/store/${slug}?ref=${encodeURIComponent(ref)}`)
    if (res.ok) setData(res.data)
    setLoading(false)
    // ★ 每次下拉刷新后结束动画
    stopPullDownRefresh()
  }

  useEffect(() => {
    // 语言优先级链：URL ?lang= → 上次选择 → 系统语言 → en
    setLang(resolveLang(router.params.lang, store.get<string>(LANG_KEY)))
    load()
    // 语种注册表（纪律 12：后端是唯一真值，前端不硬编码）。
    // 拉取失败时保持空数组，选择器回落 FALLBACK_LANGS 静态占位，不阻塞主流程。
    http<{ langs: Lang[] }>('/api/translation/langs').then((res) => {
      if (res.ok && res.data.langs?.length) setLangs(res.data.langs)
    })
  }, [slug, ref])

  usePullDownRefresh(() => {
    load()
  })

  const pickLang = (next: string) => {
    const code = normalizeLang(next)
    setLang(code)
    store.set(LANG_KEY, code)
  }

  /** ★ 多语菜名：未翻译的语种不出现该 key，回落原文 —— 「绝不编造」纪律 */
  const dishName = (d: Dish) => d.names?.[lang] || d.name_zh
  const isTranslated = (d: Dish) => Boolean(d.names?.[lang])
  const catName = (c?: string) => {
    if (!c) return ''
    return data?.store.category_names?.[lang]?.[c] || c
  }

  const grouped = useMemo(() => {
    const m = new Map<string, Dish[]>()
    for (const d of data?.dishes || []) {
      const key = d.category || ''
      if (!m.has(key)) m.set(key, [])
      m.get(key)!.push(d)
    }
    return Array.from(m.entries())
  }, [data])

  if (loading) {
    return (
      <View className='st-wrap st-center'>
        <Text className='st-muted'>{t('loading', lang)}</Text>
      </View>
    )
  }

  if (!data) {
    return (
      <View className='st-wrap st-center'>
        <Text className='st-muted'>{t('unavailable', lang)}</Text>
      </View>
    )
  }

  return (
    <ScrollView className='st-wrap' scrollY>
      <View className='st-header'>
        <Text className='st-title'>{data.store.name}</Text>
        {/* ★ 未填 WiFi 时字段不出现，据此条件渲染 */}
        {data.store.wifi_ssid ? (
          <View className='st-wifi'>
            <Text className='st-muted'>WiFi {data.store.wifi_ssid}</Text>
            {data.store.wifi_pwd ? (
              <Text className='st-muted'> · {data.store.wifi_pwd}</Text>
            ) : null}
          </View>
        ) : null}
      </View>

      <View className='st-langrow'>
        {/* ★ 语种清单来自 GET /api/translation/langs（纪律 12）；未拉到前用静态常用语种占位。
               标签用母语写法（native）——客人认的是自己语言的写法，不是语种短码 */}
        {(langs.length ? langs : FALLBACK_LANGS).map((l) => (
          <View
            key={l.code}
            className={l.code === lang ? 'st-chip st-chip-on' : 'st-chip'}
            onClick={() => pickLang(l.code)}
          >
            <Text className={l.code === lang ? 'st-chip-txt st-chip-txt-on' : 'st-chip-txt'}>{l.native}</Text>
          </View>
        ))}
      </View>

      {grouped.length === 0 ? (
        <View className='st-empty'>
          <Text className='st-muted'>{t('emptyMenu', lang)}</Text>
        </View>
      ) : null}

      {grouped.map(([cat, list]) => (
        <View key={cat} className='st-cat'>
          {cat ? <Text className='st-catname'>{catName(cat)}</Text> : null}
          {list.map((d) => (
            <View key={d.id} className='st-dish'>
              {d.image_url ? (
                <Image className='st-img' src={absUrl(d.image_url)} mode='aspectFill' />
              ) : null}
              <View className='st-dishmain'>
                <View className='st-namerow'>
                  <Text className='st-dname'>{dishName(d)}</Text>
                  {!isTranslated(d) && lang !== 'zh' ? (
                    <Text className='st-untrans'>{t('pending', lang)}</Text>
                  ) : null}
                </View>
                {lang !== 'zh' ? <Text className='st-dname-zh'>{d.name_zh}</Text> : null}
                {typeof d.price === 'number' ? (
                  <Text className='st-price'>¥{d.price.toFixed(2)}</Text>
                ) : null}
                {d.sold_out ? <Text className='st-soldout'>{t('soldOut', lang)}</Text> : null}
              </View>
            </View>
          ))}
        </View>
      ))}

      {/* ★ 服务铃 / 问店员：提交瞬间请求订阅消息授权，授权意愿最高（方案 §6.2）。
            接口待从《数据模型与接口契约_全量》补齐后接入。 */}
      <View className='st-footer'>
        <View className='st-btn' onClick={() => {}}>
          <Text className='st-btn-txt'>{t('ask', lang)}</Text>
        </View>
        {/* ★ 机器翻译免责声明：与「绝不编造」纪律配套，非中文界面始终展示 */}
        {lang !== 'zh' ? <Text className='st-note'>{t('machineNote', lang)}</Text> : null}
      </View>
    </ScrollView>
  )
}

/** 后端返回的 image_url 可能是相对路径，小程序 Image 需要完整 URL */
function absUrl(u: string): string {
  return /^https?:\/\//.test(u) ? u : `${API_BASE}${u.startsWith('/') ? '' : '/'}${u}`
}
