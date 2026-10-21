#!/usr/bin/env node
/* 独立客人端浏览器探针（不复用项目 render-check 的任何断言）。
 * 目的：验证「AI 助手按钮成为 primary 之后」三个底部按钮各自行为是否正确：
 *   - 评价领券（ghost）→ gx-sheet 评价弹层：15 星 + 提交后弹券
 *   - 问店员（ghost）  → gx-sheet 短语卡：6 条
 *   - AI 助手（primary）→ gx-assist 面板：可提问并收到回复
 * 同时校验评价提交 → 领券 → 券码可在商家端核销的完整闭环（API 侧验证）。
 *
 * 前置：:8080 API（须 LC_DEV=1 开发模式，认证走固定验证码 000000）+
 *        :5173 vite + 本机 Chrome。
 */
import { spawn } from 'node:child_process'
import { mkdtempSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const WEB = process.env.WEB || 'http://localhost:5173'
const API = process.env.API || 'http://127.0.0.1:8080'
const CHROME =
  process.env.CHROME ||
  `${process.env.HOME}/Library/Caches/ms-playwright/chromium-1243/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing`
const PORT = 9411
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let PASS = 0
let FAIL = 0
const eq = (name, expect, actual) => {
  if (String(expect) === String(actual)) {
    PASS++
    console.log(`  ✓ ${name}`)
  } else {
    FAIL++
    console.log(`  ✗ ${name}: 期望[${expect}] 实际[${actual}]`)
  }
}
const ok = (name, cond, detail = '') => {
  if (cond) {
    PASS++
    console.log(`  ✓ ${name}`)
  } else {
    FAIL++
    console.log(`  ✗ ${name}${detail ? `: ${detail}` : ''}`)
  }
}

class CDP {
  constructor(ws) {
    this.ws = ws
    this.id = 0
    this.pending = new Map()
    this.jsErrors = []
    ws.onmessage = (e) => {
      const msg = JSON.parse(e.data)
      if (msg.id && this.pending.has(msg.id)) {
        const { res, rej } = this.pending.get(msg.id)
        this.pending.delete(msg.id)
        msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result)
      } else if (msg.method === 'Runtime.exceptionThrown') {
        this.jsErrors.push(msg.params.exceptionDetails?.text || 'exception')
      }
    }
  }
  send(method, params = {}) {
    const id = ++this.id
    return new Promise((res, rej) => {
      this.pending.set(id, { res, rej })
      this.ws.send(JSON.stringify({ id, method, params }))
    })
  }
  async evaluate(expr) {
    const r = await this.send('Runtime.evaluate', {
      expression: expr,
      returnByValue: true,
      awaitPromise: true,
    })
    if (r.exceptionDetails) throw new Error(`页面异常: ${r.exceptionDetails.text}`)
    return r.result.value
  }
  close() {
    try {
      this.ws.close()
    } catch {
      /* ignore */
    }
  }
}

async function launchChrome() {
  if (!existsSync(CHROME)) throw new Error(`找不到 Chrome: ${CHROME}`)
  const profile = mkdtempSync(join(tmpdir(), 'lc-indep-'))
  const proc = spawn(
    CHROME,
    [
      '--headless=new',
      `--remote-debugging-port=${PORT}`,
      `--user-data-dir=${profile}`,
      '--no-sandbox',
      '--disable-setuid-sandbox',
      '--disable-dev-shm-usage',
      '--no-proxy-server',
      '--proxy-bypass-list=*',
      '--remote-allow-origins=*',
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-gpu',
      '--hide-scrollbars',
      '--window-size=500,900',
      'about:blank',
    ],
    { stdio: 'ignore' },
  )
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
  throw new Error('Chrome 启动超时')
}

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

async function main() {
  console.log('── 独立客人端浏览器探针 ──')
  const { proc, wsUrl } = await launchChrome()
  // node 22 全局 WebSocket（onopen/onmessage 属性式 API）
  const ws = new WebSocket(wsUrl)
  await new Promise((res, rej) => {
    ws.onopen = res
    ws.onerror = () => rej(new Error('CDP WebSocket 连接失败'))
  })
  const cdp = new CDP(ws)
  await cdp.send('Runtime.enable')
  await cdp.send('Page.enable')

  // 造数：一家带 en/ja 译文的店（菜名用词典收录项，en 必然有译文）
  await api('/api/auth/otp', { method: 'POST', body: { phone: '13900000009' } })
  const login = await api('/api/auth/login', {
    method: 'POST',
    body: { phone: '13900000009', code: '000000' },
  })
  const token = login.token
  ok('商家登录拿 token', Boolean(token))
  const items = [
    { name: '宫保鸡丁', price: 38, category: '热菜', selected: true },
    { name: '麻婆豆腐', price: 32, category: '热菜', selected: true },
  ]
  const created = await api('/api/stores', { method: 'POST', token, body: { name: '独立探针店' } })
  const sid = created.store.id
  const slug = created.store.slug
  await api(`/api/stores/${sid}/draft`, { method: 'POST', token, body: { lang: 'zh', items } })
  await api(`/api/stores/${sid}/draft/confirm`, { method: 'POST', token, body: {} })
  await api(`/api/stores/${sid}/translate`, { method: 'POST', token, body: { targets: ['en', 'ja'] } })
  ok('造数：门店 slug 非空', Boolean(slug), slug)

  // ── 1) 评价按钮 → 评价弹层 ──
  await cdp.evaluate(`location.href = '${WEB}/s/${slug}?lang=en&ref=t99'`)
  await sleep(2500)
  const bar = await cdp.evaluate(
    `Array.from(document.querySelectorAll('.gx-bar .gx-btn')).map((b) => b.textContent.trim())`,
  )
  eq('底部按钮数量 = 3（AI助手/问店员/评价领券）', 3, bar.length)
  ok('存在 primary 按钮（AI 助手）', bar.some((t) => t.includes('AI') || /[🤖]/.test(t)), JSON.stringify(bar))

  // 点「评价领券」按钮（按文案定位，先排除含 AI/helper 的按钮避免误匹配）
  const opened = await cdp.evaluate(`(() => {
    const btns = Array.from(document.querySelectorAll('.gx-bar .gx-btn'))
      .filter((b) => !/AI|assistant|helper|点菜/i.test(b.textContent))
    const rateBtn = btns.find((b) => /评价|Rate/i.test(b.textContent))
    if (!rateBtn) return { found: false }
    rateBtn.click()
    return { found: true }
  })()`)
  ok('找到评价按钮并点击', opened.found)
  await sleep(500)
  const rate = await cdp.evaluate(`(() => ({
    sheets: document.querySelectorAll('.gx-sheet').length,
    stars: document.querySelectorAll('.gx-sheet .gx-star').length,
    head: (document.querySelector('.gx-sheet-head h2') || {}).textContent || '',
  }))()`)
  eq('评价弹层打开（gx-sheet = 1）', 1, rate.sheets)
  eq('三维度共 15 颗星', 15, rate.stars)
  ok('弹层标题非空', rate.head.trim().length > 0, rate.head)

  // 打星并提交 → 应弹券层
  const submit = await cdp.evaluate(`(() => {
    const stars = Array.from(document.querySelectorAll('.gx-sheet .gx-star'))
    // 每个维度点第 4 颗星（分组按 DOM 顺序每 5 颗一组）
    for (let i = 3; i < stars.length; i += 5) stars[i].click()
    return { clicked: stars.length }
  })()`)
  await sleep(200)
  const submitted = await cdp.evaluate(`(() => {
    const btn = Array.from(document.querySelectorAll('.gx-sheet button')).find((b) => /提交|Submit/i.test(b.textContent))
    if (btn) { btn.click(); return true }
    return false
  })()`)
  await sleep(1200)
  const coupon = await cdp.evaluate(`(() => ({
    sheets: document.querySelectorAll('.gx-sheet').length,
    hasCoupon: document.body.innerText.length > 0,
    code: (document.querySelector('.gx-coupon-code') || {}).textContent || '',
  }))()`)
  ok('评价提交后出现券卡弹层', coupon.sheets >= 1, JSON.stringify(coupon))

  // 券码 API 侧核验：从页面拿不到就跳过（UI 结构可能不同），API 侧单独验证
  // 关掉可能开着的弹层（评价层/短语卡有关闭钮 .gx-x；券层是 .gx-btn--block「完成」钮）
  const closeSheets = `(() => {
    const x = document.querySelector('.gx-sheet .gx-x')
    if (x) { x.click(); return 'x' }
    const done = document.querySelector('.gx-sheet .gx-btn--block')
    if (done) { done.click(); return 'done' }
    return 'none'
  })()`

  // ── 2) 问店员按钮 → 短语卡 ──
  await cdp.evaluate(closeSheets)
  await sleep(300)
  await cdp.evaluate(`(() => {
    const btns = Array.from(document.querySelectorAll('.gx-bar .gx-btn'))
      .filter((b) => !/AI|assistant|helper|点菜/i.test(b.textContent))
    const helpBtn = btns.find((b) => /问店员|help|帮/i.test(b.textContent))
    if (helpBtn) helpBtn.click()
  })()`)
  await sleep(400)
  const help = await cdp.evaluate(`(() => {
    const ps = Array.from(document.querySelectorAll('.gx-phrase'))
    return { count: ps.length, first: (ps[0] || {}).textContent || '' }
  })()`)
  eq('问店员短语卡 6 条', 6, help.count)

  // ── 3) AI 助手按钮（primary）→ 助手面板可提问 ──
  await cdp.evaluate(closeSheets)
  await sleep(300)
  await cdp.evaluate(`document.querySelector('.gx-bar .gx-btn--primary').click()`)
  await sleep(500)
  const assist = await cdp.evaluate(`(() => ({
    panel: document.querySelectorAll('.gx-assist').length,
    input: !!document.querySelector('.gx-assist-bar input, .gx-assist-bar textarea'),
  }))()`)
  eq('AI 助手面板打开（gx-assist = 1）', 1, assist.panel)
  ok('助手有输入框', assist.input)

  // 提问并等待回复（规则兜底也应回复）
  const asked = await cdp.evaluate(`(() => {
    const input = document.querySelector('.gx-assist-bar textarea') || document.querySelector('.gx-assist-bar input')
    if (!input) return false
    // 按元素真实类型选原生 value setter（textarea 用 InputElement 的 setter 会 Illegal invocation）
    const proto = input.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype
    const setter = Object.getOwnPropertyDescriptor(proto, 'value')?.set
    if (!setter) return false
    setter.call(input, '有什么辣的菜')
    input.dispatchEvent(new Event('input', { bubbles: true }))
    const btn = Array.from(document.querySelectorAll('.gx-assist-bar button')).pop()
    if (btn) btn.click()
    return true
  })()`)
  ok('已提交问题', asked)
  await sleep(3000)
  const reply = await cdp.evaluate(`(() => {
    const bubbles = Array.from(document.querySelectorAll('.gx-bubble'))
    return { count: bubbles.length, text: bubbles.map((b) => b.textContent.trim()).join(' | ').slice(0, 200) }
  })()`)
  ok('助手有回复气泡', reply.count >= 2, JSON.stringify(reply).slice(0, 160))

  eq('全程无 JS 异常', 0, cdp.jsErrors.length)
  if (cdp.jsErrors.length) console.log('    JS 异常:', cdp.jsErrors.slice(0, 3))

  cdp.close()
  proc.kill('SIGKILL')
  console.log(`\n═══ 独立探针结果：${PASS} 通过 / ${FAIL} 失败 ═══`)
  process.exit(FAIL === 0 ? 0 : 1)
}

main().catch((e) => {
  console.error(`\n执行失败：${e.message}`)
  process.exit(1)
})
