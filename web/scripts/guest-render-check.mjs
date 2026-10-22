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
  // ★ 等 40 秒而不是 15 秒：本机实测在负载较高时（跑过 go test -race、
  //   vite build、批量种子数据之后 load 能到 100+），Chrome 冷启动会超过 15 秒。
  //   只等 15 秒时报「启动超时」，看着像脚本坏了，其实只是机器忙。
  for (let i = 0; i < 400; i++) {
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
  // ★ 超时时先分清「Chrome 根本不存在」还是「只是起得慢」：
  //   这两种情况的修法完全不同，报一样的错等于让人瞎猜。
  if (!existsSync(CHROME)) {
    throw new Error(`找不到 Chrome：${CHROME}\n  用 CHROME 环境变量指定，或先装 Playwright 的 Chrome for Testing`)
  }
  throw new Error(
    `Chrome 启动超时（等了 40s，远程调试端口未就绪）。\n` +
      `  已找到 Chrome：${CHROME}\n` +
      `  常见原因是机器负载过高（刚跑过编译/构建），过一会重试通常就好。`,
  )
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

/* ── 平台管理探针：把当前分区渲染出来的所有表格单元格与卡片标签摊平 ──
   ★ 为什么取「所有单元格」而不是按分区写选择器：五个分区的表格结构相似
     （.lc-card 里一张 .lc-table），按 class 挑会挑错；摊平后按文本断言，
     分区按钮点对了、组件挂对了，表头就一定在。 */
const PLATFORM_PROBE = `(() => {
  const cells = Array.from(document.querySelectorAll('.lc-table th, .lc-table td'))
    .map((e) => e.textContent.trim())
    .filter(Boolean)
  // 概览分区是卡片不是表格，标签在 .lc-card 里
  const cards = Array.from(document.querySelectorAll('.lc-card .lc-dim'))
    .map((e) => e.textContent.trim())
    .filter(Boolean)
  return { cells, cards }
})()`

/** 造一个超管账号并登录（FIX-13 平台管理渲染回归的前置）。
 *
 * 为什么必须 psql 提权：平台管理 Tab 只对 super_admin / tenant_admin 可见，
 * 而登录接口只会建默认 manager。沿用 smoke_admin.sh 的做法——psql 把角色
 * 改成 super_admin 且 tenant_id 置空（平台级）。提权后必须**重新取验证码
 * 再登录**：角色不烘焙进 token，旧 token 仍是 manager 身份。
 *
 * @returns {Promise<{token: string}|null>} 成功返回 token；psql 不可用时返回 null（调用方跳过）
 */
async function seedSuperAdminFixture() {
  const phone = process.env.SUPER_PHONE || '13800001234'
  const psqlBin = process.env.PSQL_BIN || '/opt/homebrew/bin/psql'
  const dsn = process.env.PSQL_DSN || `-h 127.0.0.1 -U ${process.env.USER || process.env.LOGNAME || 'postgres'} -d langcross`
  try {
    await api('/api/auth/otp', { method: 'POST', body: { phone } })
    const first = await api('/api/auth/login', { method: 'POST', body: { phone, code: '000000' } })
    if (!first.token) throw new Error(`首次登录失败：${JSON.stringify(first)}`)
    const { execFileSync } = await import('node:child_process')
    execFileSync(psqlBin, [...dsn.split(' '), '-tAc',
      `UPDATE account SET role='super_admin', tenant_id='' WHERE phone='${phone}'`], { stdio: 'ignore' })
    // 角色变更即时生效，但 token 里的身份是旧的——重新登录拿新主体
    await api('/api/auth/otp', { method: 'POST', body: { phone } })
    const second = await api('/api/auth/login', { method: 'POST', body: { phone, code: '000000' } })
    if (!second.token) throw new Error(`提权后登录失败：${JSON.stringify(second)}`)
    return { token: second.token }
  } catch (e) {
    console.log(`      （超管夹具失败：${e.message}）`)
    return null
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

  /* ── DOM 交互辅助（按文案找钮 / 取标题） ──────────────────────
     ★ 定义在 main() 里而不是模块顶层：它们要用上面这个闭包 evaluate。
     ★ 按文案而不是按 class / 序号找钮：平台管理的教训是同一份文案可能出现在
       多个层级（左侧导航也有「概览」），所以「点得对」靠调用方先圈定范围。 */
  const clickByText = async (text) => {
    // ★ 一律用「包含 / 正则 test」而不是全等：按钮文案在 JSX 里是换行的，
    //   而且常带动态尾巴（「用示例菜单继续（11 道）」）、或排版箭头（「… →」）。
    //   写死全等的话，文案一个标点没变、只是多了个箭头就会假红。
    const isRe = text instanceof RegExp
    const m = isRe
      ? `new RegExp(${JSON.stringify(text.source)}).test(x.textContent.trim())`
      : `x.textContent.trim().includes(${JSON.stringify(text)})`
    // ★ 命中多个就报错，不静默点第一个：否则「上一步」和「下一步」文案相近时
    //   会点错钮，测试还是绿的——那就等于在骗自己。
    const label = JSON.stringify(isRe ? text.source : text)
    await evaluate(`(() => {
      const all = Array.from(document.querySelectorAll('button')).filter((x) => ${m})
      if (all.length === 0) throw new Error('找不到按钮：' + ${label})
      if (all.length > 1) throw new Error('按钮文案不唯一：' + ${label} + '（命中 ' + all.length + ' 个）')
      all[0].click()
    })()`)
  }
  const h1Text = async () => evaluate(`document.querySelector('h1')?.textContent.trim() ?? null`)

  /**
   * 等某个按钮从 disabled 变可用（不点）。
   * ★ 为什么不能用固定 sleep：P4 的「下一步」在预览接口返回前一直是禁用的，
   *   接口一慢就「点了没反应、还不抛错」——测试照样绿，
   *   结果是这条最该守的主流程从来没被真正走过。
   * ★ 超时前先把原因查出来：否则只会看到一句干巴巴的超时，
   *   不知道是接口报错还是真的在慢，排障全靠猜。
   */
  const waitEnabled = async (text, ms = 20000) => {
    const lit = JSON.stringify(text)
    const t0 = Date.now()
    for (;;) {
      const st = await evaluate(`(() => {
        const b = Array.from(document.querySelectorAll('button'))
          .find((x) => x.textContent.trim().includes(${lit}))
        if (!b) return 'missing'
        return b.disabled ? 'disabled' : 'ready'
      })()`)
      if (st === 'ready') return
      if (st === 'missing') throw new Error('找不到按钮：' + text)
      if (Date.now() - t0 > ms) {
        const why = await evaluate(`(() => {
          const t = document.body.textContent
          return {
            err: document.querySelector('.lc-alert--err')?.textContent ?? null,
            noTarget: t.includes('至少勾选一个语种'),
            generating: t.includes('正在生成'),
            mlCards: document.querySelectorAll('.ml-card').length,
          }
        })()`)
        throw new Error('按钮一直不可用（超时 ' + ms + 'ms）：' + text + ' 原因=' + JSON.stringify(why))
      }
      await sleep(250)
    }
  }
  /** 等可用后再点：把「等待」和「点击」分开，是为了能在两者之间插断言。 */
  const clickWhenEnabled = async (text, ms = 20000) => {
    await waitEnabled(text, ms)
    await clickByText(text)
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

  /* ── FIX-13：平台管理五个分区的渲染回归 ──────────────────────
     2026-10-21 PlatformTab.tsx 从 860 行拆成「外壳 + 五个分区 + 共用工具」共
     七个文件。这种纯搬运拆分，tsc 只证明「类型对」，vite build 只证明
     「模块解析对」——都不证明「点进去真的渲染出来了」。
     render-check 是这轮拆分**唯一的安全网**，所以在这里逐分区点一遍。 */
  step('9) 平台管理：五个分区逐一点开并渲染（FIX-13 拆分的回归网）')
  const sup = await seedSuperAdminFixture()
  if (!sup) {
    console.log('  \x1b[33m!\x1b[0m 拿不到超管（psql 不可用或未配置 DSN），跳过平台管理渲染断言')
  } else {
    await cdp.send('Page.navigate', { url: `${WEB}/admin` })
    await sleep(400)
    await evaluate(`localStorage.setItem('lc_token', ${JSON.stringify(sup.token)})`)
    await cdp.send('Page.navigate', { url: `${WEB}/admin` })
    let supAdmin = null
    for (let i = 0; i < 60; i++) {
      await sleep(150)
      supAdmin = await evaluate(ADMIN_PROBE).catch(() => null)
      if (supAdmin?.ready) break
    }
    eq('超管进入商家控制台', true, Boolean(supAdmin?.ready))
    eq('超管能看到「平台管理」Tab', true, (supAdmin?.tabs || []).includes('平台管理'))

    await evaluate(`(() => {
      const b = Array.from(document.querySelectorAll('.admin-nav button')).find((x) => x.textContent.trim() === '平台管理')
      if (!b) throw new Error('找不到「平台管理」Tab')
      b.click()
    })()`)
    await sleep(500)

    // 外壳要点：按角色过滤后的分区清单（超管应五个全见）
    const secTabs = await evaluate(
      `Array.from(document.querySelectorAll('.lc-stack > .lc-row button')).map((b) => b.textContent.trim())`,
    )
    for (const s of ['概览', '租户', '账号', '翻译 Key', '审计']) {
      eq(`超管分区清单含「${s}」`, true, secTabs.includes(s))
    }

    // 逐个分区点开，断言各自的表头/卡片真的渲染出来。
    // ★ 按表头文本断言而不是按行数：数据可能为空（刚清的库），但表头恒在。
    // ★ 点按钮必须先圈定范围：商家端左侧导航也有「概览」，直接按文案找会点到
    //   门店级 Tab 上去（第一次写就踩了这个坑，表现为概览分区「没渲染」）。
    //   圈法是找「同时含五个分区名的那个 .lc-row」——那是平台管理的二级 Tab 行。
    const SECTIONS = [
      { name: '概览', headers: ['租户', '账号', '门店', '翻译 Key'] },
      { name: '租户', headers: ['名称', '编码', '门店', '账号', '到期', '状态'] },
      { name: '账号', headers: ['手机号', '姓名', '角色', '租户', '状态'] },
      { name: '翻译 Key', headers: ['别名', '模式', 'Key', '日上限', '最近使用', '状态'] },
      { name: '审计', headers: ['时间', '操作', '操作者', '目标', '详情'] },
    ]
    const clickSection = (name) =>
      `(() => {
        const row = Array.from(document.querySelectorAll('.lc-row')).find((r) => {
          const ts = Array.from(r.querySelectorAll('button')).map((b) => b.textContent.trim())
          return ['概览', '租户', '账号', '翻译 Key', '审计'].every((t) => ts.includes(t))
        })
        if (!row) throw new Error('找不到平台管理的分区 Tab 行')
        const b = Array.from(row.querySelectorAll('button')).find((x) => x.textContent.trim() === ${JSON.stringify(name)})
        if (!b) throw new Error('分区行里找不到「${name}」')
        b.click()
      })()`
    for (const sec of SECTIONS) {
      await evaluate(clickSection(sec.name))
      await sleep(350)
      const probe = await evaluate(PLATFORM_PROBE)
      const got = [...(probe.cells || []), ...(probe.cards || [])]
      const missing = sec.headers.filter((h) => !got.includes(h))
      eq(`「${sec.name}」分区渲染出表头/卡片（${sec.headers.length} 项）`, true, missing.length === 0)
      if (missing.length) bad(`「${sec.name}」缺少：${missing.join('、')}`)
    }
    eq('平台管理交互全程无 JS 异常', 0, jsErrors.length)
  }

  /* ── FIX-13 第二半：商家上线引导流 P1→P5 ────────────────────
     2026-10-21 Onboarding.tsx 从 661 行拆成「根组件 + 五个步骤 + 共用工具」
     八个文件。与平台管理同理：tsc 只证明类型对、build 只证明模块解析对，
     都不证明**五个步骤点下去真的能走通**。这条流程还是获客转化的主路径
     （商家几分钟内从纸质菜单到多语菜单上线），走不通等于获客断流。

     ★ 为什么必须走到 P5 并真的提交：P5 是「登录 → 建店 → 存草稿 →
     确认原文稿 → 翻译」的顺序依赖链，任一步顺序错了都会留下半成品门店
     或「译了但没确认」的脏数据——而那正是纪律 2 要防的。 */
  step('10) 商家上线引导流：P1 算损失 → P5 上线成功（FIX-13 拆分的回归网）')
  const ONB_PHONE = process.env.ONB_PHONE || '13800000077'
  await cdp.send('Page.navigate', { url: `${WEB}/admin/onboarding` })
  await sleep(600)

  // P1：标题 + 三个本地输入 + 一个月度流失估算（纯本地算术，不发请求）
  const p1 = await evaluate(`(() => {
    const h = document.querySelector('h1')
    return {
      title: h ? h.textContent.trim() : null,
      inputs: document.querySelectorAll('.lc-card input.lc-input').length,
      steps: document.querySelectorAll('.lc-step').length,
    }
  })()`)
  eq('P1 标题是「算损失」', '算损失', p1.title)
  eq('P1 有三个本地输入（桌数/人均/流失比例）', 3, p1.inputs)
  eq('顶部进度条五格', 5, p1.steps)

  // 改一个输入，验证本地估算跟着变（证明 P1 是纯本地计算，不依赖接口）
  // ★ 变量名带 p1 前缀：step 5 里已经有一个 before/after（评价弹层），同名会撞
  const p1Before = await evaluate(`document.body.textContent.match(/¥[\\d,]+/)?.[0] ?? null`)
  await evaluate(`(() => {
    const ins = document.querySelectorAll('.lc-card input.lc-input')
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set
    setter.call(ins[0], '20')
    ins[0].dispatchEvent(new Event('input', { bubbles: true }))
  })()`)
  await sleep(300)
  const p1After = await evaluate(`document.body.textContent.match(/¥[\\d,]+/)?.[0] ?? null`)
  eq('改桌数后月度估算跟着变（纯本地计算）', true, p1Before !== null && p1After !== null && p1Before !== p1After)

  // P1 → P2
  await clickByText('看看怎么挽回')
  await sleep(500)
  eq('P2 标题是「上传菜单」', '上传菜单', await h1Text())

  // P2：OCR 在本地是 stub，走「用示例菜单继续」这条保底路径
  // （这条路径本身就是容错设计：识别不可用时也不能让商家卡住）
  await clickByText('用示例菜单继续')
  await sleep(500)
  eq('P3 标题是「挑要翻译的菜」', '挑要翻译的菜', await h1Text())
  const picked = await evaluate(`document.body.textContent.match(/生成多语菜单（(\\d+) 道）/)?.[1] ?? null`)
  eq('P3 示例菜单已带入且默认全选（11 道）', '11', picked)

  // P3 → P4：一道都没勾会被禁用，这里默认全选所以能过
  await clickByText(/^生成多语菜单（\d+ 道）→$/)
  // P4 一挂载就要打语种注册表 + 预览接口，给足时间
  await sleep(1500)
  eq('P4 标题是「核心多语言」', '核心多语言', await h1Text())

  // P4：语种清单必须来自后端注册表（纪律 12：前端不硬编码）
  // ★ 语种是 <button> 标签（可点选的胶囊），不是 <input type=checkbox>——
  //   按 checkbox 数会恒等于 0，断言就永远是对的假绿灯。
  // ⇒ 断言出现的语种数 > 我们默认勾选的 3 个，证明不是写死的
  const langCount = await evaluate(
    `document.querySelectorAll('.lc-card button.lc-btn--sm').length`,
  )
  eq('P4 语种来自后端注册表（> 默认勾选的 3 个）', true, langCount > 3)
  // 等预览出结果（这个钮在预览返回前是禁用的），再断言译文，最后才进 P5
  // ★ 等 60 秒而不是默认的 20 秒：冷缓存时「11 道菜 + 5 分类」× 3 语种
  //   要打几十次外部翻译引擎往返，服务端给的整包截止时间是 45 秒。
  //   只等 20 秒的话，冷缓存下这条会假红——而它红起来看着像功能坏了，
  //   其实是测试没给够时间。
  await waitEnabled('注册后即可对外使用', 60000)
  // ★ 预览卡片是 .ml-card，不是 .lc-card：按 .lc-card 找永远命中 0 条，
  //   断言就变成一条「结构上恒真的假绿灯」。
  // 译文质量依赖机器翻译，没配 AI 凭据时仍会走词典引擎，所以这条可以硬断言。
  const previewNames = await evaluate(
    `Array.from(document.querySelectorAll('.ml-card')).map((c) => c.textContent).join('|')`,
  )
  eq('P4 多语预览真的翻出了译文（免登录预览接口生效）', true, /Kung Pao|KungPao|ゴンバオ|공바오/.test(previewNames))
  eq('P4 每个语种一列（默认 3 个语种）', 3, await evaluate(`document.querySelectorAll('.ml-card').length`))
  // 译文旁边必须跟着中文原文作为对照真值（纪律：原文只作辅助，但必须有）
  eq('P4 译文旁都带中文原文对照', true, await evaluate(`document.querySelectorAll('.ml-card .ml-zh').length >= 11`))

  // P4 → P5
  await clickByText('注册后即可对外使用')
  await sleep(500)
  await sleep(500)
  eq('P5 标题是「注册解锁」', '注册解锁', await h1Text())

  // P5：登录 → 建店 → 存草稿 → 确认 → 翻译。开发模式验证码固定 000000。
  // ★ 按 placeholder 定位输入框，不按序号/类型：三个框都是 text，
  //   按序号填会把手机号写进「店名」，后面的请求全都 400，报出来的错
  //   看着像后端问题，其实是测试自己填错了。
  const fill = (ph, v) => `(() => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set
    const el = document.querySelector('input[placeholder=' + ${JSON.stringify(JSON.stringify(ph))} + ']')
    if (!el) throw new Error('找不到输入框：${ph}')
    setter.call(el, ${JSON.stringify(v)})
    el.dispatchEvent(new Event('input', { bubbles: true }))
  })()`
  await evaluate(fill('13800000000', ONB_PHONE))
  await sleep(200)
  // 按钮实际文案是「获取验证码」，我原来写的「发送验证码」根本不存在
  await clickWhenEnabled('获取验证码')
  await sleep(1200)
  // 开发模式会把验证码回显在提示里（LC_DEV=1），直接取；取不到就用固定值
  const devCode = await evaluate(
    `(document.body.textContent.match(/开发模式：(\\d{6})/)?.[1]) ?? '000000'`,
  )
  await evaluate(fill('6 位数字', devCode))
  await sleep(300)
  await clickWhenEnabled('注册并上线')
  // 上线链要打四五个接口，给足时间
  for (let i = 0; i < 60; i++) {
    await sleep(250)
    if (await evaluate(`/上线成功/.test(document.body.textContent)`)) break
  }
  eq('★ P5 走完整条链并显示「上线成功」', true, await evaluate(`/上线成功/.test(document.body.textContent)`))
  // ★ 双码要「等」而不是「立刻数」：二维码是 QrImage 组件挂载后才 fetch blob
  //   再转 objectURL，比成功页文字晚一拍。平时几十毫秒就好了，机器忙时
  //   会晚很多——断言写在文字出现的那一刻，等于把测试绑在机器快慢上。
  const QR_COUNT = `document.querySelectorAll('img[alt="二维码"]').length`
  let qrImgs = 0
  for (let i = 0; i < 80; i++) {
    qrImgs = await evaluate(QR_COUNT)
    if (qrImgs >= 2) break
    await sleep(250)
  }
  eq('上线成功页展示双码（门口码 + 桌码）', 2, qrImgs)
  // 双码是「打印物料」，地址印出来就不可改，所以必须真的加载出图片而不是留占位
  eq('双码都拿到了真实图片（不是占位/加载中）', 2, await evaluate(
    `Array.from(document.querySelectorAll('img[alt="二维码"]')).filter((i) => i.src.startsWith('blob:')).length`,
  ))
  eq('引导流全程无 JS 异常', 0, jsErrors.length)

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
