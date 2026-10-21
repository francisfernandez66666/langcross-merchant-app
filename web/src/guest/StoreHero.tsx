/**
 * 客人端 Hero 头部：店名（原文，不翻译）+ 语言切换入口 + WiFi 行（可选）。
 * 从 StorePage.tsx 拆出，行段逐字照搬。
 */
import type { Lang, Store } from '../api/client'
import { langLabel, t } from './i18n'

/** 顶部 Hero。店名是专有名词，按原文展示、不进翻译链。 */
export function Hero({
  store,
  lang,
  current,
  onLangOpen,
}: {
  store: Store
  lang: string
  current: Lang | undefined
  onLangOpen: () => void
}) {
  // 店名（专有名词，不翻译）+ 语言切换入口 + WiFi（有才渲染）
  return (
      <header className="gx-hero">
        <div className="gx-hero-row">
          <div className="gx-brand">
            {/* 店名是专有名词，按原文展示，不进翻译链 */}
            <h1 className="gx-store">{store.name}</h1>
            {/* 只有开了券的店才提「评价领券」，避免客人做了评价却拿不到券 */}
            {store.coupon_enabled && <div className="gx-sub">{t('rate', lang)}</div>}
          </div>
          {/* 语言切换入口：显示当前语言的母语写法，未知语种时退回大写短码 */}
          <button className="gx-langbtn" onClick={onLangOpen} aria-label={t('chooseLang', lang)}>
            <span className="gx-globe" aria-hidden>
              ◍
            </span>
            <span className="gx-langbtn-txt">{current ? langLabel(current) : lang.toUpperCase()}</span>
            <span className="gx-caret" aria-hidden>
              ⌄
            </span>
          </button>
        </div>

        {/* ★ 兜底不依赖 WiFi：未填 WiFi 时整行不渲染。
            注意这是「告知客人 WiFi 密码」的行，不是页面自身的联网依赖。 */}
        {store.wifi_ssid && (
          <div className="gx-wifi">
            <span className="gx-wifi-k">{t('wifi', lang)}</span>
            <span className="gx-wifi-v">
              {store.wifi_ssid}
              {store.wifi_pwd ? ` · ${store.wifi_pwd}` : ''}
            </span>
          </div>
        )}
      </header>
  )
}
