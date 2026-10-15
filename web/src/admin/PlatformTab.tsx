/**
 * 平台管理 Tab（超管控制台 + 租户管理员后台）。
 *
 * 与后端 handlers_admin.go 的门禁一一对应（前端显隐只是体验优化，真准入以服务端为准）：
 *   - 概览 / 租户：仅 super_admin（平台级数据，跨租户可见）；
 *   - 账号：super_admin 与 tenant_admin 都可（后者只能管本租户成员，由后端 effectiveTenant 收口）；
 *   - 翻译 Key：★ 超管专属增删改查（Key 是平台成本凭据，租户一律不接触，连列表也不给）；
 *   - 审计：super_admin 全量、tenant_admin 只看本租户（后端已过滤）。
 *
 * 为什么租户管理员也进这个 Tab：他的日常就是「维护自己人」，
 * 这恰恰是账号区；超管才多出概览、租户、翻译 Key 三个平台级分区。
 *
 * 涉及的接口全部经 ../api/client（client.ts 里有逐条中文说明）。
 */
import { useCallback, useEffect, useState } from 'react'
import { api, type Account, type AuditEntry, type PlatformStats, type Tenant, type TranslateKey } from '../api/client'

/* ── 小工具 ────────────────────────────────────────────── */

// 角色 → 中文标签（后台界面固定中文）。
const ROLE_LABEL: Record<string, string> = {
  super_admin: '超管',
  tenant_admin: '租户管理员',
  org_admin: '区域管理员',
  manager: '店长',
  staff: '店员',
}

// 把 Unix 秒格式化成「刚刚 / N 分钟前 / HH:MM」。
function relTime(ts: number): string {
  const diff = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  const d = new Date(ts * 1000)
  return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

// 把 Unix 秒格式化成「YYYY-MM-DD」（到期时间展示用）。
function dateOf(ts?: number): string {
  if (!ts) return '永久'
  const d = new Date(ts * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

// 状态徽标：active=绿、disabled=灰、expired=红。
function StatusBadge({ status }: { status: string }) {
  if (status === 'active') return <span className="lc-badge lc-badge--ok">启用</span>
  if (status === 'expired') return <span className="lc-badge lc-badge--err">已过期</span>
  return <span className="lc-badge">停用</span>
}

/* ── 分区一：平台概览（超管） ──────────────────────────── */

/** 平台概览：四个总数卡片 + 一句说明。 */
function Overview({ stats }: { stats: PlatformStats | null }) {
  const items: { label: string; value: number }[] = [
    { label: '租户', value: stats?.tenants ?? 0 },
    { label: '账号', value: stats?.accounts ?? 0 },
    { label: '门店', value: stats?.stores ?? 0 },
    { label: '翻译 Key', value: stats?.translation_keys ?? 0 },
  ]
  return (
    <div className="lc-stack">
      <div className="lc-row" style={{ gap: 12, flexWrap: 'wrap' }}>
        {items.map((it) => (
          <div key={it.label} className="lc-card" style={{ minWidth: 140, flex: '0 0 auto' }}>
            <div className="lc-dim">{it.label}</div>
            <div style={{ fontSize: 28, fontWeight: 700 }}>{stats ? it.value : '—'}</div>
          </div>
        ))}
      </div>
      <p className="lc-muted">平台级总数。租户、账号、翻译 Key 的明细在下面各分区。</p>
    </div>
  )
}

/* ── 分区二：租户（超管） ──────────────────────────────── */

/** 新建租户表单的受控状态。code 是唯一约束，name 必填。 */
type TenantForm = { code: string; name: string }

/** 租户管理：列表 + 新建 + 启停 + 展开查看门店。 */
function Tenants({ msg, setMsg }: { msg: string; setMsg: (s: string) => void }) {
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [form, setForm] = useState<TenantForm>({ code: '', name: '' })
  const [formOpen, setFormOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [expanded, setExpanded] = useState<string>('') // 正在查看门店的租户 ID

  /** 拉取租户列表（写操作后刷新）。 */
  const load = useCallback(() => {
    api
      .listTenants()
      .then((d) => setTenants(d.tenants ?? []))
      .catch((e: Error) => setMsg(e.message))
  }, [setMsg])

  useEffect(() => {
    load()
  }, [load])

  /** 新建租户。 */
  const create = async () => {
    setBusy(true)
    setMsg('')
    try {
      await api.createTenant({ code: form.code.trim().toLowerCase(), name: form.name.trim() })
      setMsg(`已创建租户「${form.name.trim()}」。`)
      setForm({ code: '', name: '' })
      setFormOpen(false)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 停用 / 启用租户（disabled 的租户下成员无法再登录写操作）。 */
  const toggle = async (t: Tenant) => {
    const next = t.status === 'active' ? 'disabled' : 'active'
    setBusy(true)
    setMsg('')
    try {
      await api.patchTenant(t.id, { status: next })
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <p className="lc-muted" style={{ margin: 0 }}>
          租户是平台的商家分组：每个租户有独立的菜单、账号与翻译 Key。
        </p>
        <button className="lc-btn lc-btn--sm" onClick={() => setFormOpen((v) => !v)}>
          {formOpen ? '收起' : '新建租户'}
        </button>
      </div>

      {formOpen && (
        <div className="lc-card lc-stack" style={{ gap: 12, maxWidth: 480 }}>
          <div className="lc-label">新建租户</div>
          <div className="lc-row" style={{ gap: 10 }}>
            <input
              className="lc-input"
              placeholder="编码（3-32 位小写字母/数字/中划线，如 hualian）"
              value={form.code}
              onChange={(e) => setForm({ ...form, code: e.target.value })}
            />
            <input
              className="lc-input"
              placeholder="租户名称（如 华联餐饮集团）"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </div>
          <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={create}>
            {busy ? '创建中…' : '创建'}
          </button>
        </div>
      )}

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>编码</th>
              <th>门店</th>
              <th>账号</th>
              <th>到期</th>
              <th>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {tenants.map((t) => (
              <tr key={t.id}>
                <td>{t.name}</td>
                <td>
                  <code>{t.code}</code>
                </td>
                <td>{t.stores ?? 0}</td>
                <td>{t.accounts ?? 0}</td>
                <td className="lc-dim">{dateOf(t.expires_at)}</td>
                <td>
                  <StatusBadge status={t.status} />
                </td>
                <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                  <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => setExpanded(expanded === t.id ? '' : t.id)}>
                    {expanded === t.id ? '收起门店' : '查看门店'}
                  </button>
                  <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 6 }} onClick={() => toggle(t)}>
                    {t.status === 'active' ? '停用' : '启用'}
                  </button>
                </td>
              </tr>
            ))}
            {tenants.length === 0 && (
              <tr>
                <td colSpan={7} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  还没有租户。新建后即可把账号与门店划归该租户。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {msg && <div className="lc-alert lc-alert--err">{msg}</div>}
    </div>
  )
}

/* ── 分区三：账号与角色（超管 / 租户管理员） ────────────── */

/** 新建账号表单。角色不能高于自己，由后端强制。 */
type AccountForm = { phone: string; role: string; display_name: string }

/** 账号管理：列表 + 新建 + 改角色 + 启停。 */
function Accounts({ me, msg, setMsg }: { me: Account; msg: string; setMsg: (s: string) => void }) {
  const [accounts, setAccounts] = useState<Account[]>([])
  const [form, setForm] = useState<AccountForm>({ phone: '', role: 'manager', display_name: '' })
  const [formOpen, setFormOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  /** 拉账号列表（超管看全部、租户管理员看本租户，后端已收口）。 */
  const load = useCallback(() => {
    api
      .listAccounts()
      .then((d) => setAccounts(d.accounts ?? []))
      .catch((e: Error) => setMsg(e.message))
  }, [setMsg])

  useEffect(() => {
    load()
  }, [load])

  /** 新建账号；手机号已存在时后端会就地改角色（响应 created=false）。 */
  const create = async () => {
    setBusy(true)
    setMsg('')
    try {
      const r = await api.createAccount({
        phone: form.phone.trim(),
        role: form.role,
        display_name: form.display_name.trim(),
      })
      setMsg(r.created ? `已创建账号 ${form.phone.trim()}。` : `该手机号已存在，已更新其角色。`)
      setForm({ phone: '', role: 'manager', display_name: '' })
      setFormOpen(false)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 就地改角色（不能改自己，后端强制）。 */
  const setRole = async (a: Account, role: string) => {
    setBusy(true)
    setMsg('')
    try {
      await api.patchAccountRole(a.id, role)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 停用 / 启用账号（不能停用同级或更高级成员，后端强制）。 */
  const toggleStatus = async (a: Account) => {
    const next = a.status === 'active' ? 'disabled' : 'active'
    setBusy(true)
    setMsg('')
    try {
      await api.patchAccountStatus(a.id, next)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // 可选角色清单：低于自己等级的才列出（前端体验优化；后端再兜底）。
  const levelOf = (r: string) => ({ super_admin: 4, tenant_admin: 3, org_admin: 2, manager: 1, staff: 0 }[r] ?? 0)
  const myLevel = levelOf(me.role)
  const roleOptions = ['tenant_admin', 'org_admin', 'manager', 'staff'].filter((r) => levelOf(r) < myLevel)

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <p className="lc-muted" style={{ margin: 0 }}>
          账号即登录手机号。角色决定他能做什么：超管管平台，租户管理员管自己人，店长管门店。
        </p>
        <button className="lc-btn lc-btn--sm" onClick={() => setFormOpen((v) => !v)}>
          {formOpen ? '收起' : '新建账号'}
        </button>
      </div>

      {formOpen && (
        <div className="lc-card lc-stack" style={{ gap: 12, maxWidth: 480 }}>
          <div className="lc-label">新建账号</div>
          <div className="lc-row" style={{ gap: 10 }}>
            <input
              className="lc-input"
              placeholder="手机号"
              value={form.phone}
              onChange={(e) => setForm({ ...form, phone: e.target.value })}
            />
            <select
              className="lc-input"
              value={form.role}
              onChange={(e) => setForm({ ...form, role: e.target.value })}
            >
              {roleOptions.map((r) => (
                <option key={r} value={r}>
                  {ROLE_LABEL[r]}
                </option>
              ))}
            </select>
          </div>
          <input
            className="lc-input"
            placeholder="姓名（可选，便于团队辨认）"
            value={form.display_name}
            onChange={(e) => setForm({ ...form, display_name: e.target.value })}
          />
          <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={create}>
            {busy ? '创建中…' : '创建'}
          </button>
        </div>
      )}

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>手机号</th>
              <th>姓名</th>
              <th>角色</th>
              <th>租户</th>
              <th>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {accounts.map((a) => (
              <tr key={a.id}>
                <td>
                  {a.phone}
                  {/* 自己这一行标出来：不能给自己改角色 / 停用自己 */}
                  {a.id === me.id && <span className="lc-dim">（我）</span>}
                </td>
                <td>{a.display_name || <span className="lc-dim">—</span>}</td>
                <td>
                  {a.id === me.id ? (
                    <span>{ROLE_LABEL[a.role] ?? a.role}</span>
                  ) : (
                    <select
                      className="lc-input lc-input--sm"
                      value={a.role}
                      onChange={(e) => setRole(a, e.target.value)}
                    >
                      {/* 当前角色可能不低于自己，也列出来避免「看不见」 */}
                      <option value={a.role}>{ROLE_LABEL[a.role] ?? a.role}</option>
                      {roleOptions
                        .filter((r) => r !== a.role)
                        .map((r) => (
                          <option key={r} value={r}>
                            {ROLE_LABEL[r]}
                          </option>
                        ))}
                    </select>
                  )}
                </td>
                <td className="lc-dim">{a.tenant_id || '平台级'}</td>
                <td>
                  {a.status === 'active' ? (
                    <span className="lc-badge lc-badge--ok">启用</span>
                  ) : (
                    <span className="lc-badge">停用</span>
                  )}
                </td>
                <td style={{ textAlign: 'right' }}>
                  {a.id !== me.id && (
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => toggleStatus(a)}>
                      {a.status === 'active' ? '停用' : '启用'}
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {accounts.length === 0 && (
              <tr>
                <td colSpan={6} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无账号。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {msg && <div className="lc-alert lc-alert--err">{msg}</div>}
    </div>
  )
}

/* ── 分区四：翻译 Key（超管 / 租户管理员） ──────────────── */

/** 新建 Key 表单。Key 明文只在创建响应里出现一次。 */
type KeyForm = { name: string; key: string; mode: string; daily_limit: string }

/** 某把 Key 的就地展开态：明文 / 试译结果 / 轮换输入。 */
type KeyRowUI = {
  revealed?: string
  test?: { source: string; target: string; to: string; elapsed_ms: number; error?: string }
  rotateOpen?: boolean
  rotateKey?: string
}

/**
 * 翻译 Key 管理：列表 + 新建 + 明文 + 轮换 + 启停 + 试译 + 删除。
 *
 * 仅超管可见（后端 superKeyAdmin 门禁：增删改查全归超管，租户一律不接触）。
 * 能言 Key 是真金白银的平台成本凭据，脱敏列表对租户也没有实际用途——
 * 他既不能用也不能改，索性不看，少一个信息暴露面。
 */
function TranslateKeys({ msg, setMsg }: { msg: string; setMsg: (s: string) => void }) {
  const [keys, setKeys] = useState<TranslateKey[]>([])
  const [activeId, setActiveId] = useState('') // 后端标记的当前生效 Key
  const [ui, setUI] = useState<Record<string, KeyRowUI>>({})
  const [form, setForm] = useState<KeyForm>({ name: '', key: '', mode: 'fast', daily_limit: '' })
  const [formOpen, setFormOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    api
      .listTranslateKeys()
      .then((d) => {
        setKeys(d.keys ?? [])
        setActiveId(d.active_key_id ?? '')
      })
      .catch((e: Error) => setMsg(e.message))
  }, [setMsg])

  useEffect(() => {
    load()
  }, [load])

  /** 合并式更新某一行的展开态。 */
  const patchRow = (id: string, p: Partial<KeyRowUI>) => setUI((cur) => ({ ...cur, [id]: { ...cur[id], ...p } }))

  /** 新建 Key。 */
  const create = async () => {
    setBusy(true)
    setMsg('')
    try {
      await api.createTranslateKey({
        name: form.name.trim(),
        key: form.key.trim(),
        mode: form.mode,
        daily_limit: Number(form.daily_limit) || 0,
      })
      setMsg('已保存；Key 已加密落库，此后只能「查看明文」复制。')
      setForm({ name: '', key: '', mode: 'fast', daily_limit: '' })
      setFormOpen(false)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 查看明文（一次性 + 落审计）。 */
  const reveal = async (k: TranslateKey) => {
    setMsg('')
    try {
      const r = await api.revealTranslateKey(k.id)
      patchRow(k.id, { revealed: r.api_key })
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  /** 复制明文到剪贴板。 */
  const copy = (text: string) => {
    navigator.clipboard?.writeText(text).then(
      () => setMsg('已复制明文到剪贴板，请注意保密'),
      () => setMsg('复制失败，请手动选择文本复制'),
    )
  }

  /** 轮换 Key。 */
  const rotate = async (k: TranslateKey) => {
    const key = (ui[k.id]?.rotateKey ?? '').trim()
    if (key.length < 8) {
      setMsg('新 Key 长度看起来不完整，请重新复制')
      return
    }
    setBusy(true)
    setMsg('')
    try {
      await api.rotateTranslateKey(k.id, key)
      patchRow(k.id, { rotateOpen: false, rotateKey: '', revealed: undefined })
      setMsg(`已轮换「${k.name}」的 Key，旧 Key 立即失效。`)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 启停 Key。 */
  const toggleStatus = async (k: TranslateKey) => {
    const next = k.status === 'active' ? 'disabled' : 'active'
    setBusy(true)
    setMsg('')
    try {
      await api.setTranslateKeyStatus(k.id, next)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 试译：用这把 Key 真翻一句，验证可用性并顺带带回余额。 */
  const test = async (k: TranslateKey) => {
    setMsg('')
    try {
      const r = await api.testTranslateKey(k.id, '麻婆豆腐', 'en')
      patchRow(k.id, { test: { source: r.source, target: r.target, to: r.to, elapsed_ms: r.elapsed_ms, error: r.error } })
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  /** 删除 Key（前端二次确认）。 */
  const remove = async (k: TranslateKey) => {
    if (!confirm(`确定删除翻译 Key「${k.name}」？删除后该租户的翻译将回落到内置词典。`)) return
    setBusy(true)
    setMsg('')
    try {
      await api.deleteTranslateKey(k.id)
      setMsg(`已删除「${k.name}」。`)
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <p className="lc-muted" style={{ margin: 0 }}>
          翻译 Key 是能言开放平台的凭据，密文落库。停用一把 Key 后翻译链会回落，最多 30 秒生效。
        </p>
        <button className="lc-btn lc-btn--sm" onClick={() => setFormOpen((v) => !v)}>
          {formOpen ? '收起' : '新增 Key'}
        </button>
      </div>

      {formOpen && (
        <div className="lc-card lc-stack" style={{ gap: 12, maxWidth: 560 }}>
          <div className="lc-label">新增翻译 Key</div>
          <div className="lc-row" style={{ gap: 10 }}>
            <input
              className="lc-input"
              placeholder="别名（如 生产 Key）"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
            <select className="lc-input" value={form.mode} onChange={(e) => setForm({ ...form, mode: e.target.value })}>
              <option value="fast">fast（快）</option>
              <option value="pro">pro（高质量）</option>
            </select>
            <input
              className="lc-input"
              placeholder="日上限（留空=不限）"
              value={form.daily_limit}
              onChange={(e) => setForm({ ...form, daily_limit: e.target.value })}
            />
          </div>
          <input
            className="lc-input"
            placeholder="API Key（lx- 开头）"
            value={form.key}
            onChange={(e) => setForm({ ...form, key: e.target.value })}
          />
          <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={create}>
            {busy ? '保存中…' : '保存（Key 加密落库）'}
          </button>
        </div>
      )}

      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>别名</th>
              <th>模式</th>
              <th>Key</th>
              <th>日上限</th>
              <th>最近使用</th>
              <th>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => {
              const row = ui[k.id] ?? {}
              return (
                <tr key={k.id}>
                  <td>
                    {k.name}
                    {/* 后端标记的当前生效 Key 给个角标 */}
                    {k.id === activeId && <span className="lc-badge lc-badge--ok" style={{ marginLeft: 6 }}>生效中</span>}
                  </td>
                  <td>{k.mode}</td>
                  <td>
                    <code>{k.key_masked}</code>
                    {row.revealed && (
                      <div style={{ marginTop: 6 }}>
                        <code style={{ color: 'var(--lc-accent)' }}>{row.revealed}</code>
                        <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 8 }} onClick={() => copy(row.revealed!)}>
                          复制
                        </button>
                        <div className="lc-dim" style={{ marginTop: 4 }}>明文仅本次展示，刷新即丢</div>
                      </div>
                    )}
                  </td>
                  <td>{k.daily_limit > 0 ? k.daily_limit : '不限'}</td>
                  <td className="lc-dim">{k.last_used_at ? relTime(k.last_used_at) : '未使用'}</td>
                  <td>
                    <StatusBadge status={k.status} />
                  </td>
                  <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => reveal(k)}>
                      明文
                    </button>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 6 }} onClick={() => test(k)}>
                      试译
                    </button>
                    <button
                      className="lc-btn lc-btn--sm lc-btn--ghost"
                      style={{ marginLeft: 6 }}
                      onClick={() => patchRow(k.id, { rotateOpen: !row.rotateOpen })}
                    >
                      轮换
                    </button>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 6 }} onClick={() => toggleStatus(k)}>
                      {k.status === 'active' ? '停用' : '启用'}
                    </button>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" style={{ marginLeft: 6 }} onClick={() => remove(k)}>
                      删除
                    </button>
                  </td>
                </tr>
              )
            })}
            {keys.length === 0 && (
              <tr>
                <td colSpan={7} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无翻译 Key；翻译将使用内置词典（覆盖常见菜名，冷门词会保留原文）。
                </td>
              </tr>
            )}
          </tbody>
        </table>

        {/* 逐行就地展开：轮换输入 / 试译结果 */}
        {keys.map((k) => {
          const row = ui[k.id] ?? {}
          if (!row.rotateOpen && !row.test) return null
          return (
            <div key={`${k.id}-x`} style={{ padding: '12px 16px', borderTop: '1px solid var(--lc-border)' }}>
              {row.rotateOpen && (
                <div className="lc-row" style={{ gap: 8 }}>
                  <input
                    className="lc-input"
                    placeholder="粘贴新的 API Key"
                    value={row.rotateKey ?? ''}
                    onChange={(e) => patchRow(k.id, { rotateKey: e.target.value })}
                  />
                  <button className="lc-btn lc-btn--sm lc-btn--primary" disabled={busy} onClick={() => rotate(k)}>
                    提交轮换
                  </button>
                </div>
              )}
              {row.test && (
                <div className="lc-dim">
                  {row.test.error ? (
                    <>试译失败：{row.test.error}</>
                  ) : (
                    <>
                      试译（{row.test.elapsed_ms}ms）：「{row.test.source}」→ {row.test.to} = <b>{row.test.target}</b>
                    </>
                  )}
                </div>
              )}
            </div>
          )
        })}
      </div>
      {msg && <div className="lc-alert lc-alert--err">{msg}</div>}
    </div>
  )
}

/* ── 分区五：审计（超管全量 / 租户管理员本租户） ────────── */

/** 审计日志：谁在何时做了什么。超管看全量，租户管理员后端已按租户过滤。 */
function Audit({ msg, setMsg }: { msg: string; setMsg: (s: string) => void }) {
  const [entries, setEntries] = useState<AuditEntry[]>([])

  useEffect(() => {
    api
      .listAudit()
      .then((d) => setEntries(d.audit ?? []))
      .catch((e: Error) => setMsg(e.message))
  }, [setMsg])

  return (
    <div className="lc-stack">
      <p className="lc-muted" style={{ margin: 0 }}>
        后台关键操作（建租户、改角色、维护 Key、查看明文等）都会留痕。最近 200 条。
      </p>
      <div className="lc-card" style={{ padding: 0 }}>
        <table className="lc-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>操作</th>
              <th>操作者</th>
              <th>目标</th>
              <th>详情</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => (
              <tr key={e.id}>
                <td className="lc-dim" style={{ whiteSpace: 'nowrap' }}>
                  {relTime(e.created_at)}
                </td>
                <td>
                  <code>{e.action}</code>
                </td>
                <td className="lc-dim">{ROLE_LABEL[e.actor_role] ?? e.actor_role}</td>
                <td className="lc-dim" style={{ wordBreak: 'break-all' }}>
                  {e.target}
                </td>
                <td>{e.detail}</td>
              </tr>
            ))}
            {entries.length === 0 && (
              <tr>
                <td colSpan={5} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无审计记录。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {msg && <div className="lc-alert lc-alert--err">{msg}</div>}
    </div>
  )
}

/* ── 外壳：分区切换 ────────────────────────────────────── */

/** 分区 ID。按角色过滤后剩下的就是这个人实际能用的分区。 */
type Section = 'overview' | 'tenants' | 'accounts' | 'tkeys' | 'audit'

/**
 * 平台管理 Tab。
 *
 * @param account 当前登录账号（role 决定能看到哪些分区；后端还会再校验一次）
 */
export default function PlatformTab({ account }: { account: Account }) {
  const isSuper = account.role === 'super_admin'
  const [section, setSection] = useState<Section>(isSuper ? 'overview' : 'accounts')
  const [stats, setStats] = useState<PlatformStats | null>(null)
  const [msg, setMsg] = useState('')
  const setMsgOnce = (s: string) => setMsg(s)

  // 分区清单：超管全见；租户管理员只有「账号 / 审计」两个区
  // （翻译 Key 是平台成本凭据，超管专属增删改查，租户一律不接触）。
  const sections: { id: Section; label: string }[] = [
    ...(isSuper ? [{ id: 'overview' as Section, label: '概览' }] : []),
    ...(isSuper ? [{ id: 'tenants' as Section, label: '租户' }] : []),
    { id: 'accounts', label: '账号' },
    ...(isSuper ? [{ id: 'tkeys' as Section, label: '翻译 Key' }] : []),
    { id: 'audit', label: '审计' },
  ]

  // 概览数据只在超管视图拉一次；切走再切回也不重复打扰后端。
  useEffect(() => {
    if (isSuper && section === 'overview' && !stats) {
      api
        .platformStats()
        .then((d) => setStats(d))
        .catch((e: Error) => setMsg(e.message))
    }
  }, [isSuper, section, stats])

  // 切换分区时清掉上一条提示，避免张冠李戴。
  useEffect(() => {
    setMsg('')
  }, [section])

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <h1 style={{ margin: 0 }}>平台管理</h1>
        <span className="lc-dim">
          {account.phone} · {ROLE_LABEL[account.role] ?? account.role}
        </span>
      </div>

      {/* 分区切换（类似左侧导航的二级 Tab） */}
      <div className="lc-row" style={{ gap: 8, flexWrap: 'wrap' }}>
        {sections.map((s) => (
          <button
            key={s.id}
            className="lc-btn lc-btn--sm"
            style={
              section === s.id
                ? { borderColor: 'var(--lc-accent)', background: 'var(--lc-accent)', color: '#fff' }
                : undefined
            }
            onClick={() => setSection(s.id)}
          >
            {s.label}
          </button>
        ))}
      </div>

      {section === 'overview' && isSuper && <Overview stats={stats} />}
      {section === 'tenants' && isSuper && <Tenants msg={msg} setMsg={setMsgOnce} />}
      {section === 'accounts' && <Accounts me={account} msg={msg} setMsg={setMsgOnce} />}
      {section === 'tkeys' && isSuper && <TranslateKeys msg={msg} setMsg={setMsgOnce} />}
      {section === 'audit' && <Audit msg={msg} setMsg={setMsgOnce} />}
    </div>
  )
}
