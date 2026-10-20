#!/usr/bin/env node
// ─────────────────────────────────────────────────────────────
// LangCross · 客人端门店页渲染回归测试
//
// 为什么需要它：
//   后端接口对了，不等于页面渲染对了。第七轮曾出过一个 P0 级事故——
//   <RateSheet ref="t01" /> 撞上 React 保留属性 ref，评价弹层一打开
//   整页白屏（"Function components cannot have string refs"）。
//   curl 测接口完全测不出来。所以必须真开浏览器、真点、真看 DOM。
//
// 断言什么（对应《多语言能力设计》§3 的渲染纪律 L1–L6）：
//   L1 主标题永远是目标语言        → 菜名文本必须等于接口给的 names[lang]
//   L3 译文缺失显示原文 + 未翻译标 → 未翻译店必须出现 .gx-tag--pending
//   L4 原文只作小字辅助            → .gx-dish-orig 必须是中文原文，且与主标题不同
//   L5 标注「机器翻译」            → 非 zh 时必须出现 .gx-note
//   +  data-lang / <html lang> 与所选语言一致（字体回退、断行、读屏）
//   +  语言选择器必须列出后端注册表里的全部 10 个语种
//   +  切语言后主标题跟着换
//   +  评价弹层能打开且**没有 JS 异常**（上面那个 P0 的回归测试）
//   +  底部三个按钮（AI 助手 / 问店员 / 评价领券）身份各自正确
//   +  AI 助手面板可打开、提问后有真实回复气泡（2026-10-19 补）
//
// ★ 2026-10-19 修订记录（git 98e5bf3 加了 AI 助手 primary 钮，本脚本没同步）：
//   底部钮从 2 个变 3 个、评价钮不再是 .gx-btn--primary、各浮层关闭钮形态
//   不统一（.gx-x / .gx-btn--block / .gx-assist-icon）——旧选择器直接抛
//   TypeError。现在一律按**文案**找钮、用 closeOverlays() 关浮层。
//
// 前置：
//   1. 后端在跑（默认 http://localhost:8080），且必须以 LC_DEV=1 开发模式启动——
//      登录走固定验证码 000000（2026-10-19 起 LC_DEV 默认关闭，`make api` 已带上）
//   2. 前端 dev server 在跑（默认 http://localhost:5173，vite 会把 /api 代理到后端）
//   3. 本机有 Chrome / Chromium（默认用 Playwright 缓存的 Chrome for Testing，
//      可用 CHROME 环境变量覆盖）
//
// 用法：node scripts/guest-render-check.mjs
// 退出码：0 = 全部通过；1 = 存在失败断言或前置不满足
// ─────────────────────────────────────────────────────────────
import { spawn } from 'node:child_process'
import { setTimeout as sleep } from 'node:timers/promises'
import { existsSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const WEB = process.env.WEB || 'http://localhost:5173'
const API = process.env.API || 'http://localhost:8080'
const PHONE = process.env.PHONE || '13800000001'
const PORT = Number(process.env.CDP_PORT || 9333)
const CHROME =
  process.env.CHROME ||
  join(
    process.env.HOME,
    'Library/Caches/ms-playwright/chromium-1243/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
  )

/* ── 断言工具（与后端 smoke.sh 同一套输出风格） ─────────────── */
let PASS = 0
let FAIL = 0
const ok = (m) => (PASS++, console.log(`  \x1b[32m✓\x1b[0m ${m}`))
const bad = (m) => (FAIL++, console.log(`  \x1b[31m✗\x1b[0m ${m}`))
const step = (m) => console.log(`\n\x1b[36m── ${m}\x1b[0m`)
const soft = (m, cond) =>
  cond ? ok(m) : console.log(`  \x1b[33m!\x1b[0m ${m}（译文质量依赖机器翻译，跳过）`)

function eq(label, want, got) {
  if (String(want) === String(got)) ok(label)
  else bad(`${label}（期望「${want}」，实际「${got}」）`)
}
function eqJSON(label, want, got) {
  const a = JSON.stringify(want)
  const b = JSON.stringify(got)
  if (a === b) ok(label)
  else bad(`${label}\n      期望 ${a}\n      实际 ${b}`)
}

/* ── 极简 CDP 客户端（Chrome DevTools Protocol over WebSocket） ── */
class CDP {
  constructor(ws) {
    this.ws = ws
    this.seq = 0
    this.pending = new Map()
    this.listeners = []
  }
  static async connect(url) {
    const ws = new WebSocket(url)
    await new Promise((res, rej) => {
      ws.onopen = res
      ws.onerror = () => rej(new Error('CDP WebSocket 连接失败'))
    })
    const c = new CDP(ws)
    ws.onmessage = (e) => c.#dispatch(JSON.parse(e.data))
    return c
  }
  #dispatch(msg) {
    if (msg.id && this.pending.has(msg.id)) {
      const { res, rej } = this.pending.get(msg.id)
      this.pending.delete(msg.id)
      msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result)
      return
    }
    for (const fn of this.listeners) fn(msg)
  }
  on(fn) {
    this.listeners.push(fn)
  }
  send(method, params = {}, sessionId) {
    const id = ++this.seq
    return new Promise((res, rej) => {
      this.pending.set(id, { res, rej })
      this.ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }))
    })
  }
  close() {
    try {
      this.ws.close()
    } catch {
      /* ignore */
    }
  }
}

/* ── 起 Chrome ──────────────────────────────────────────────── */
async function launchChrome() {
  if (!existsSync(CHROME)) throw new Error(`找不到 Chrome：${CHROME}\n可用 CHROME 环境变量指定`)
  const profile = mkdtempSync(join(tmpdir(), 'lc-render-'))
  const proc = spawn(
    CHROME,
    [
      '--headless=new',
      `--remote-debugging-port=${PORT}`,
      `--user-data-dir=${profile}`,
      // ★ 这三个是必需的，少一个就连不上：
      //   --no-sandbox           本机/CI 沙箱里 Chrome 自带沙箱会 SIGTRAP 崩溃
      //   --remote-allow-origins 否则 DevTools WebSocket 握手被拒（表现为 1006）
      //   --disable-dev-shm-usage 容器里 /dev/shm 太小会崩标签页
      '--no-sandbox',
      '--disable-setuid-sandbox',
      '--disable-dev-shm-usage',
      '--remote-allow-origins=*',
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-gpu',
      '--hide-scrollbars',
      // macOS 无头窗口最小 CSS 宽 500px，低于此值会被裁切；客人端按手机宽度看，取 500
      '--window-size=500,900',
      'about:blank',
    ],
    { stdio: 'ignore' },
  )
  proc.on('exit', (code, sig) => {
    if (code !== null || sig !== 'SIGKILL') console.error(`\n[警告] Chrome 提前退出 code=${code} sig=${sig}`)
  })

  // 轮询 /json/list 拿**页面级** WebSocket。
  // 注意：不要用 browser 级 WS 的 Target.createTarget —— 这条路径在本机
  // 反复出现握手后被断（1006），改用页面级通道稳定得多。
  for (let i = 0; i < 150; i++) {
    try {
      const r = await fetch(`http://127.0.0.1:${PORT}/json/list`)
      if (r.ok) {
        const page = (await r.json()).find((t) => t.type === 'page')
        if (page?.webSocketDebuggerUrl) return { proc, wsUrl: page.webSocketDebuggerUrl }
      }
    } catch {
      /* 还没起来 */
    }
    await sleep(100)
  }
  proc.kill('SIGKILL')
  throw new Error('Chrome 启动超时（远程调试端口未就绪）')
}

/* ── 造测试数据（走公开接口，不碰数据库） ────────────────────── */
async function api(path, { method = 'GET', token, body } = {}) {
  const r = await fetch(`${API}${path}`, {
    method,
    headers: {
      ...(body ? { 'Content-Type': 'application/json' } : {}),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  })
  return r.json()
}

/** 建三家店，覆盖 L1（有译文）与 L3（缺译文）两条纪律，外加 Q1 的零译文态。
 *
 *  ★ 口径已变（2026-10-19 Q1）：**确认即落库**，dish 行不再等翻译时才写。
 *    所以现在三种夹具都能直接造：
 *      - 有译文   ：翻 en/ja，用 en 看                       → 测 L1
 *      - 缺译文   ：只翻 en，用 ko 看（菜在、ko 没译文）      → 测 L3
 *      - 零译文   ：只确认、一道都没翻                        → 测 Q1
 *    （Q1 之前第三种是空菜单，测不到东西，只能靠前两种间接覆盖。）
 */
async function seedFixtures() {
  await api('/api/auth/otp', { method: 'POST', body: { phone: PHONE } })
  const login = await api('/api/auth/login', {
    method: 'POST',
    body: { phone: PHONE, code: '000000' },
  })
  const token = login.token
  if (!token) throw new Error(`登录失败：${JSON.stringify(login)}`)

  // 只用内置词典收录的菜：这样即使翻译链抽风，en 译文也一定有、且不是中文
  const items = [
    { name: '宫保鸡丁', price: 38, category: '热菜', selected: true },
    { name: '麻婆豆腐', price: 32, category: '热菜', selected: true },
    { name: '酸辣汤', price: 22, category: '汤', selected: true },
    { name: '蛋炒饭', price: 18, category: '主食', selected: true },
  ]

  const make = async (label, targets) => {
    const created = await api('/api/stores', {
      method: 'POST',
      token,
      body: { name: `渲染测试店${label}` },
    })
    const sid = created.store.id
    const slug = created.store.slug
    await api(`/api/stores/${sid}/draft`, { method: 'POST', token, body: { lang: 'zh', items } })
    await api(`/api/stores/${sid}/draft/confirm`, { method: 'POST', token, body: {} })
    if (targets.length) {
      await api(`/api/stores/${sid}/translate`, { method: 'POST', token, body: { targets } })
    }
    return { sid, slug, name: created.store.name }
  }

  return {
    translated: await make('', ['en', 'ja']), // 有 en/ja 译文 —— 测 L1
    missing: await make('缺译文', ['en']), // 只有 en —— 用 ko 看就是缺译文态，测 L3
    // ★ Q1（2026-10-19）后新增的关键路径：confirm 即落库原文菜，于是
    // 「只确认、还没翻译」的店客人端也有菜单——但一道译文都没有。
    // 这个态必须渲染「原文 + 未翻译标 + 未翻译提示」，绝不能空菜单。
    untranslated: await make('零译文', []),
  }
}

/** 商家端夹具：一个**只拥有一家零译文店**的独立账号。
 *
 * 为什么另开手机号：多店账号会渲染门店切换器（<select>，受控组件要用原生
 * setter 才能驱动），单店账号则自动选中唯一门店——少一层脆断言。
 *
 * @returns {Promise<{token: string, sid: string}>} 登录 token 与门店 id
 */
async function seedMerchantFixture() {
  const phone = process.env.MERCHANT_PHONE || '13800000009'
  await api('/api/auth/otp', { method: 'POST', body: { phone } })
  const login = await api('/api/auth/login', { method: 'POST', body: { phone, code: '000000' } })
  if (!login.token) throw new Error(`商家端登录失败：${JSON.stringify(login)}`)

  const created = await api('/api/stores', {
    method: 'POST',
    token: login.token,
    body: { name: '渲染测试店零译文' },
  })
  const sid = created.store.id
  await api(`/api/stores/${sid}/draft`, {
    method: 'POST',
    token: login.token,
    body: {
      lang: 'zh',
      items: [
        { name: '宫保鸡丁', price: 38, category: '热菜', selected: true },
        { name: '麻婆豆腐', price: 32, category: '热菜', selected: true },
      ],
    },
  })
  await api(`/api/stores/${sid}/draft/confirm`, {
    method: 'POST',
    token: login.token,
    body: {},
  })
  return { token: login.token, sid }
}

/* ── 商家端探针（与客人端 PROBE 分开：DOM 结构完全不同） ────── */
const ADMIN_PROBE = `(() => {
  const txt = (el) => (el ? el.textContent.trim() : null)
  return {
    ready: !!document.querySelector('.admin-shell'),
    login: !!document.querySelector('.lc-center input.lc-input'),
    tabs: Array.from(document.querySelectorAll('.admin-nav button')).map((b) => b.textContent.trim()),
    banner: txt(document.querySelector('.lc-alert--warn')),
  }
})()`

/* ── 页面探针：在浏览器里执行，返回可断言的结构 ────────────────── */
const PROBE = `(() => {
  const txt = (el) => (el ? el.textContent.trim() : null)
  const all = (sel) => Array.from(document.querySelectorAll(sel))
  const root = document.querySelector('.guest')
  const dishes = all('.gx-dishes .gx-dish')
  return {
    ready: !!root && !document.querySelector('.gx-spinner'),
    dataLang: root ? root.getAttribute('data-lang') : null,
    htmlLang: document.documentElement.lang,
    title: document.title,
    storeName: txt(document.querySelector('.gx-store')),
    dishNames: dishes.map((d) => txt(d.querySelector('.gx-dish-name span'))),
    dishOrigs: dishes.map((d) => txt(d.querySelector('.gx-dish-orig'))),
    pendingNames: dishes.filter((d) => d.querySelector('.gx-dish-name .gx-tag--pending')).length,
    pendingTexts: all('.gx-pending-text').length,
    origHints: all('.gx-dish-orig--hint').length,
    cats: all('.gx-cat').map((e) => e.textContent.trim()),
    note: txt(document.querySelector('.gx-note')),
    bar: all('.gx-bar .gx-btn').map((b) => b.textContent.trim()),
    langBtn: txt(document.querySelector('.gx-langbtn-txt')),
  }
})()`

// 中日韩 + 泰文字符（用于判断「主标题里是不是混进了原文」）
const HAS_CJK = '[\\u3400-\\u9fff\\u3040-\\u30ff\\u0e00-\\u0e7f]'
const hasCJK = (s) => new RegExp(HAS_CJK).test(String(s ?? ''))

async function main() {
  /* ── 前置 ─────────────────────────────────────────────────── */
  step('0) 前置检查')
  for (const [name, url] of [
    ['后端', `${API}/api/health`],
    ['前端', WEB],
  ]) {
    try {
      const r = await fetch(url)
      if (!r.ok) throw new Error(String(r.status))
      ok(`${name}在线（${url}）`)
    } catch (e) {
      bad(`${name}不可达：${url}（${e.message}）`)
      console.log(
        name === '后端'
          ? '     启动后端：cd server && go run ./cmd/api'
          : '     启动前端：cd web && npm run dev',
      )
      process.exit(1)
    }
  }

  step('1) 造测试数据（一家有译文 / 一家只有部分译文 / 一家零译文）')
  const fx = await seedFixtures()
  ok(`有译文店：/s/${fx.translated.slug}`)
  ok(`缺译文店：/s/${fx.missing.slug}`)
  ok(`零译文店：/s/${fx.untranslated.slug}`)

  const { proc, wsUrl } = await launchChrome()
  const cdp = await CDP.connect(wsUrl)
  await cdp.send('Page.enable')
  await cdp.send('Runtime.enable')

  // ★ P0 回归探针：任何未捕获异常都要记下来
  const jsErrors = []
  cdp.on((m) => {
    if (m.method === 'Runtime.exceptionThrown') {
      jsErrors.push(m.params.exceptionDetails.exception?.description || m.params.exceptionDetails.text)
    }
  })

  const evaluate = async (expr) => {
    const r = await cdp.send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true })
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.text)
    return r.result.value
  }

  const goto = async (url) => {
    jsErrors.length = 0
    await cdp.send('Page.navigate', { url })
    for (let i = 0; i < 120; i++) {
      await sleep(120)
      const v = await evaluate(PROBE).catch(() => null)
      if (v?.ready) return v
    }
    throw new Error(`页面未渲染完成：${url}`)
  }

  /* ── 浮层与底部按钮的通用操作 ──────────────────────────────
     各浮层关闭钮形态不统一（2026-10-19 实证）：
       RateSheet / HelpSheet / LangSheet → .gx-sheet .gx-x
       CouponSheet                      → .gx-sheet .gx-btn--block（券层也是 .gx-sheet！）
       GuestAssistant（AI 面板）        → .gx-assist .gx-assist-icon（最后一个 ✕）
     底部按钮按**文案**认身份，不按 --primary/--ghost——
     primary 在 98e5bf3 之后是 AI 助手，评价领券是含 coupon 文案的 ghost 钮。 */
  const closeOverlays = () =>
    evaluate(`(() => {
      const icons = document.querySelectorAll('.gx-assist .gx-assist-icon')
      if (icons.length) { icons[icons.length - 1].click(); return 'assist' }
      const x = document.querySelector('.gx-sheet .gx-x')
      if (x) { x.click(); return 'sheet-x' }
      const block = document.querySelector('.gx-sheet .gx-btn--block')
      if (block) { block.click(); return 'coupon-block' }
      return 'none'
    })()`)

  // re 必须是**正则字面量字符串**（如 '/coupon/i'）——它会被原样嵌进页面代码
  const clickBarButton = (re, label) =>
    evaluate(`(() => {
      const btns = Array.from(document.querySelectorAll('.gx-bar .gx-btn'))
      const hit = btns.find((b) => ${re}.test(b.textContent))
      if (!hit) throw new Error('底部找不到「${label}」钮，现有：' + btns.map((b) => b.textContent.trim()).join(' | '))
      hit.click()
      return hit.textContent.trim()
    })()`)

  /* ── L1 / L4 / L5：有译文时的渲染纪律 ─────────────────────── */
  step('2) 有译文店 · ?lang=en（L1 主标题＝目标语言）')
  const pub = await api(`/api/public/store/${fx.translated.slug}?ref=door`)
  const view = await goto(`${WEB}/s/${fx.translated.slug}?lang=en&ref=door`)

  eq('data-lang = en', 'en', view.dataLang)
  eq('<html lang> = en（字体回退/断行/读屏）', 'en', view.htmlLang)
  eq('语言按钮显示目标语言母语写法', 'English', view.langBtn)
  // ★ L1 的核心断言：渲染出来的菜名必须逐条等于接口给的 names.en，而不是中文原文
  eqJSON(
    '菜名主标题逐条等于接口 names.en',
    pub.dishes.map((d) => d.names?.en),
    view.dishNames,
  )
  // 渲染层硬断言：主标题绝不能等于中文原文（这正是用户报的「英文页突出中文」）
  eq(
    '主标题无一等于中文原文',
    true,
    view.dishNames.every((t, i) => t !== pub.dishes[i].name_zh),
  )
  // 无中日韩字符属于「机器翻译质量」范畴，外网引擎偶有回填中文，故只做软断言
  soft('主标题均为拉丁字母（无中文/日文混入）', !view.dishNames.some(hasCJK))
  // ★ L4：原文只作小字辅助
  eqJSON(
    '原文小字＝name_zh（与主标题不同）',
    pub.dishes.map((d) => d.name_zh),
    view.dishOrigs,
  )
  eq('有译文时不出现「未翻译」标', 0, view.pendingNames)
  eq('有译文时不用弱化斜体', 0, view.pendingTexts)
  // ★ L5：机器翻译必须标注
  eq('非 zh 时标注「机器翻译」', true, Boolean(view.note))
  soft('分类标题也走目标语言', !view.cats.some(hasCJK))
  soft('标签页标题用目标语言的「菜单」', /menu/i.test(view.title))
  // 底部操作条：AI 助手（primary）/ 问店员 / 评价领券 三个钮，按文案认身份
  // （2026-10-19：曾断言 2 个，98e5bf3 加 AI 助手后实际 3 个）
  eq('底部三个动作按钮都在', 3, view.bar.length)
  eq('含 AI 点菜助手钮', true, view.bar.some((t) => /AI/i.test(t)))
  eq('含问店员钮', true, view.bar.some((t) => /need help/i.test(t)))
  eq('含评价领券钮', true, view.bar.some((t) => /coupon/i.test(t)))

  /* ── L3：缺译文时必须标注、且绝不编造 ─────────────────────── */
  step('3) 缺译文店 · ?lang=ko（L3 缺译文显示原文 + 未翻译标）')
  const raw = await goto(`${WEB}/s/${fx.missing.slug}?lang=ko&ref=door`)
  eq('data-lang = ko', 'ko', raw.dataLang)
  eq('菜都在（只是没有 ko 译文）', 4, raw.dishNames.length)
  eqJSON(
    '缺译文时主标题回落为原文',
    ['宫保鸡丁', '麻婆豆腐', '酸辣汤', '蛋炒饭'],
    raw.dishNames,
  )
  eq('每道菜都挂「未翻译」标', raw.dishNames.length, raw.pendingNames)
  eq('缺译文用弱化斜体样式', raw.dishNames.length, raw.pendingTexts)
  eq('缺译文时原文小字换成提示语', raw.dishNames.length, raw.origHints)

  /* ── Q1 核心场景：零译文店（只确认、未翻译） ──────────────────
     2026-10-19 之前 dish 行只在翻译时才落库，这种店客人端是**空菜单**；
     Q1 改为确认即落库原文菜，因此这里必须看到「原文 + 未翻译」而不是空。 */
  step('3.5) 零译文店 · ?lang=en（Q1：确认即有菜单，未翻译如实标注）')
  const none = await goto(`${WEB}/s/${fx.untranslated.slug}?lang=en&ref=door`)
  eq('data-lang = en', 'en', none.dataLang)
  eq('★ 菜品非空（不再空菜单）', 4, none.dishNames.length)
  eqJSON('主标题回落为中文原文', ['宫保鸡丁', '麻婆豆腐', '酸辣汤', '蛋炒饭'], none.dishNames)
  eq('每道菜挂「未翻译」标', 4, none.pendingNames)
  eq('提示语如实说「尚未翻译」（而非机器翻译）', true, /not translated yet/i.test(none.note || ''))
  eq('无 JS 异常', 0, jsErrors.length)

  /* ── 切语言 ─────────────────────────────────────────────── */
  step('4) 切语言：选择器 10 项 + 切换后主标题跟着换')
  // 先回到「有译文店」，否则后面点语言按钮时页面上根本没菜
  await goto(`${WEB}/s/${fx.translated.slug}?lang=en&ref=door`)
  const langs = await api('/api/translation/langs')
  eq('后端注册表 10 个语种', 10, (langs.langs || []).length)

  await evaluate(`document.querySelector('.gx-langbtn').click()`)
  await sleep(250)
  const sheet = await evaluate(`(() => {
    const items = Array.from(document.querySelectorAll('.gx-langgrid .gx-langitem'))
    return {
      count: items.length,
      natives: items.map((b) => b.querySelector('.gx-langitem-native').textContent.trim()),
    }
  })()`)
  eq('语言选择器列出 10 项', 10, sheet.count)
  eqJSON(
    '选择器母语写法与注册表一致（顺序也一致）',
    (langs.langs || []).map((l) => l.native),
    sheet.natives,
  )

  // 切到日语（日语含汉字，故只断言「等于接口 names.ja」而不查 CJK）
  const jaIdx = (langs.langs || []).findIndex((l) => l.code === 'ja')
  const pubJa = await api(`/api/public/store/${fx.translated.slug}?ref=door`)
  await evaluate(`document.querySelectorAll('.gx-langgrid .gx-langitem')[${jaIdx}].click()`)
  await sleep(450)
  const ja = await evaluate(PROBE)
  eq('切换后 data-lang = ja', 'ja', ja.dataLang)
  eq('切换后 <html lang> 同步 = ja', 'ja', ja.htmlLang)
  eqJSON(
    '切换后主标题改用 names.ja',
    pubJa.dishes.map((d) => d.names?.ja ?? d.name_zh),
    ja.dishNames,
  )
  eq('切换语言不刷新页面（无 JS 异常）', 0, jsErrors.length)

  /* ── P0 回归：评价弹层 ────────────────────────────────────── */
  step('5) ★ P0 回归：评价弹层能打开且不抛异常')
  await goto(`${WEB}/s/${fx.translated.slug}?lang=en&ref=t01`)
  const before = await evaluate(`document.querySelectorAll('.gx-sheet').length`)
  // ★ 评价钮按文案找（含 coupon）：primary 钮现在是 AI 助手（2026-10-19）
  await clickBarButton('/coupon/i', '评价领券')
  await sleep(400)
  const rate = await evaluate(`(() => ({
    sheets: document.querySelectorAll('.gx-sheet').length,
    stars: document.querySelectorAll('.gx-sheet .gx-star').length,
    head: (document.querySelector('.gx-sheet-head h2') || {}).textContent || '',
    bodyLen: document.body.innerText.length,
  }))()`)
  eq('打开前无弹层', 0, before)
  eq('评价弹层已打开', 1, rate.sheets)
  eq('三维度共 15 颗星', 15, rate.stars)
  eq('弹层标题非空', true, rate.head.trim().length > 0)
  eqJSON('★ 无 JS 异常（React 保留属性 P0 回归）', [], jsErrors)

  /* ── 问店员卡（离线可用，不依赖网络） ─────────────────────── */
  step('6) 问店员短语卡')
  // 关掉第 5 步打开的评价层。★ 不能再直接点 .gx-sheet .gx-x：万一开着的是
  // 券层（关闭钮是 .gx-btn--block）或助手面板（.gx-assist-icon），旧写法会抛
  // TypeError 把整个脚本打断——2026-10-19 实证过的崩溃路径。
  eq('浮层已关闭', true, (await closeOverlays()) !== 'none')
  await sleep(250)
  await clickBarButton('/need help/i', '问店员')
  await sleep(350)
  const help = await evaluate(`(() => {
    const ps = Array.from(document.querySelectorAll('.gx-phrase'))
    return {
      count: ps.length,
      mains: ps.map((p) => p.querySelector('.gx-phrase-main').textContent.trim()),
      zh: ps.map((p) => (p.querySelector('.gx-phrase-zh') || {}).textContent || ''),
    }
  })()`)
  eq('短语卡有 6 条', 6, help.count)
  eq('短语主文案不是中文', true, !help.mains.some(hasCJK))
  eq('短语都带中文对照（给店员看）', true, help.zh.every((t) => t.trim().length > 0))
  eq('全程无 JS 异常', 0, jsErrors.length)

  /* ── AI 点菜助手：面板可打开、提问后有真实回复 ─────────────
     2026-10-19 补：98e5bf3 把 AI 助手提成 primary 钮后，本脚本一次都没验过它。
     回复走「无 LLM → 规则兜底」路径（后端未配凭据），因此是确定性的：
     提问里带完整菜名（检索口径：问题须包含别名整串）必然命中。 */
  step('7) AI 点菜助手：面板可打开、提问后有回复')
  eq('浮层已关闭', true, (await closeOverlays()) !== 'none')
  await sleep(200)
  await clickBarButton('/AI/i', 'AI 点菜助手')
  await sleep(400)
  const panel = await evaluate(`(() => ({
    open: !!document.querySelector('.gx-assist'),
    title: (document.querySelector('.gx-assist-title') || {}).textContent || '',
  }))()`)
  eq('AI 助手面板已打开', true, panel.open)
  eq('助手标题用目标语言（含 AI 字样）', true, /AI/i.test(panel.title))

  // React 受控 textarea：必须走原生 setter + input 事件，直接赋值不生效
  await evaluate(`(() => {
    const ta = document.querySelector('.gx-assist-input')
    const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value').set
    setter.call(ta, 'do you have mapo doufu')
    ta.dispatchEvent(new Event('input', { bubbles: true }))
  })()`)
  await evaluate(`document.querySelector('.gx-assist-send').click()`)
  // 先出现「Thinking…」占位，回答回来后替换——轮询到最后一条助手气泡非占位
  let reply = ''
  for (let i = 0; i < 40; i++) {
    await sleep(250)
    reply = await evaluate(`(() => {
      const bs = Array.from(document.querySelectorAll('.gx-msg--assistant .gx-bubble'))
      const last = bs[bs.length - 1]
      return last ? last.textContent.trim() : ''
    })()`).catch(() => '')
    if (reply && !/^Thinking/i.test(reply)) break
  }
  eq('提问后收到真实回复（非占位）', true, reply.length > 0 && !/^Thinking/i.test(reply))
  soft('回复提及菜单里的菜（规则兜底命中检索）', /mapo doufu/i.test(reply))
  eq('助手交互全程无 JS 异常', 0, jsErrors.length)
  await closeOverlays()

  /* ── Q1 下半场：商家端的「未翻译提醒」横幅 ─────────────────
     客人端验完还不够：确认即落库之后，商家可能以为「已经有菜单 = 已经上线」，
     于是必须有一条显眼提醒告诉他「一道译文都没有」。这是纯前端逻辑
     （覆盖率全 0 才弹），接口层测不到，只能真开浏览器。 */
  step('8) 商家端：未翻译提醒横幅（Q1：提醒商户账户）')
  const mfx = await seedMerchantFixture()

  // 进商家端：先把 token 写进 localStorage（键名 lc_token，见 api/client.ts），
  // 再跳 /admin。比点登录页稳——登录页是两个受控输入框，UI 一改就断。
  await cdp.send('Page.navigate', { url: `${WEB}/admin` })
  await sleep(400)
  await evaluate(`localStorage.setItem('lc_token', ${JSON.stringify(mfx.token)})`)
  await cdp.send('Page.navigate', { url: `${WEB}/admin` })
  let admin = null
  for (let i = 0; i < 60; i++) {
    await sleep(150)
    admin = await evaluate(ADMIN_PROBE).catch(() => null)
    if (admin?.ready) break
  }
  eq('token 注入后进入商家控制台', true, Boolean(admin?.ready))
  eq('门店级 Tab 齐全（概览/菜单/券与核销…）', true, (admin?.tabs || []).includes('菜单'))

  // 该手机号可能已有多家店（跑过多次 / 别的脚本造的），控制台默认选**第一家**，
  // 不一定是本次造的零译文店。多店时才渲染 <select>，用原生 setter 切到目标店
  // （React 受控 select 直接赋值会被它的 value tracker 吞掉）。
  await evaluate(`(() => {
    const sel = document.querySelector('.lc-select')
    if (!sel) return 'no-switcher'
    const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value').set
    setter.call(sel, ${JSON.stringify(mfx.sid)})
    sel.dispatchEvent(new Event('change', { bubbles: true }))
    return 'switched'
  })()`)
  await sleep(300)

  // 点「菜单」Tab（按文案找，不按序号——平台角色会少几个 Tab）
  await evaluate(`(() => {
    const b = Array.from(document.querySelectorAll('.admin-nav button')).find((x) => x.textContent.trim() === '菜单')
    if (!b) throw new Error('找不到「菜单」Tab')
    b.click()
  })()`)
  await sleep(500)
  const warn = await evaluate(ADMIN_PROBE)
  eq('★ 零译文店弹出未翻译提醒', true, /还没有任何译文/.test(warn.banner || ''))
  eq('提醒里指路「补充语种」', true, /补充语种/.test(warn.banner || ''))

  // 反向：补翻一个语种后刷新，提醒必须消失（避免提醒疲劳）
  await api(`/api/stores/${mfx.sid}/translate`, {
    method: 'POST',
    token: mfx.token,
    body: { targets: ['en'] },
  })
  await cdp.send('Page.navigate', { url: `${WEB}/admin` })
  await sleep(600)
  for (let i = 0; i < 40; i++) {
    admin = await evaluate(ADMIN_PROBE).catch(() => null)
    if (admin?.ready) break
    await sleep(150)
  }
  await evaluate(`(() => {
    const sel = document.querySelector('.lc-select')
    if (sel) {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value').set
      setter.call(sel, ${JSON.stringify(mfx.sid)})
      sel.dispatchEvent(new Event('change', { bubbles: true }))
    }
  })()`)
  await sleep(300)
  await evaluate(`(() => {
    const b = Array.from(document.querySelectorAll('.admin-nav button')).find((x) => x.textContent.trim() === '菜单')
    if (b) b.click()
  })()`)
  await sleep(500)
  const after = await evaluate(ADMIN_PROBE)
  eq('补翻后提醒消失（不打扰已上线的商家）', false, /还没有任何译文/.test(after.banner || ''))
  eq('商家端交互全程无 JS 异常', 0, jsErrors.length)

  /* ── 收尾 ─────────────────────────────────────────────────── */
  cdp.close()
  proc.kill('SIGKILL')

  console.log(`\n\x1b[1m═══ 渲染回归结果：${PASS} 通过 / ${FAIL} 失败 ═══\x1b[0m`)
  console.log('清理本次测试数据：bash server/scripts/reset_test_data.sh')
  process.exit(FAIL === 0 ? 0 : 1)
}

main().catch((e) => {
  console.error(`\n\x1b[31m执行失败：${e.message}\x1b[0m`)
  process.exit(1)
})
