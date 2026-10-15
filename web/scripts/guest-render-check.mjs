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
//
// 前置：
//   1. 后端在跑（默认 http://localhost:8080）
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

/** 建两家店，覆盖 L1（有译文）与 L3（缺译文）两条纪律。
 *
 *  ★ 坑：`dish` 行是在**翻译时**才落库的。一家完全没翻过译的店，
 *    客人端拿到的是空菜单，根本测不到「缺译文」态。
 *    所以「缺译文」夹具的正确做法是：只翻 en，然后用 ko 去看
 *    —— 菜都在，只是没有 ko 译文，于是走原文 + 「未翻译」标。
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
  }
}

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

  step('1) 造测试数据（一家有译文 / 一家只有部分译文）')
  const fx = await seedFixtures()
  ok(`有译文店：/s/${fx.translated.slug}`)
  ok(`缺译文店：/s/${fx.missing.slug}`)

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
  eq('底部两个动作按钮都在', 2, view.bar.length)
  soft('标签页标题用目标语言的「菜单」', /menu/i.test(view.title))

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
  await evaluate(`document.querySelector('.gx-bar .gx-btn--primary').click()`)
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
  await evaluate(`document.querySelector('.gx-sheet .gx-x').click()`)
  await sleep(250)
  await evaluate(`document.querySelector('.gx-bar .gx-btn--ghost').click()`)
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
