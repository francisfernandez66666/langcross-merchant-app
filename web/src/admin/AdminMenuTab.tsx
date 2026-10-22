/**
 * 菜单 Tab：语种覆盖一览 + 补充语种批量补翻 + 售罄开关 + 菜品图片。从 AdminApp.tsx 拆出，行段逐字照搬。
 */
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  api,
  type Dish,
  type Lang,
  type Store,
} from '../api/client'

/* ── 菜单：多语种覆盖 / 补翻 / 售罄 ──────────────────────── */
/**
 * 菜单 Tab：语种覆盖一览 + 「补充语种」批量补翻 + 售罄开关 + 菜品图片。
 *
 * @param store 当前选中的门店
 *
 * 业务要点：
 *   - 语种清单来自后端注册表 GET /api/translation/langs，前端绝不硬编码：
 *     后端新增一个语种只需登记一行，这里会自动多出一个语种标签，前端零改动。
 *   - 覆盖率 = 该语种已有译文的菜数 / 总菜数（百分比），用颜色一眼看出哪门语言还没翻。
 *   - 一个语种都没翻时顶部弹「未翻译提醒」（Q1）：客人端此时只有中文原文可看。
 *   - 「补充语种」是增量补翻：后端对已有译文不重复翻，所以可以放心重复点，省翻译配额。
 *   - 售罄是唯一需要天天维护的字段；不维护就不显示，也不会报错。
 *   - 菜品图片走 POST/DELETE /api/stores/{id}/dishes/{dishId}/image：
 *     每行一个「上传」按钮触发隐藏的文件选择框，选完即传；有图时才出现「删除」。
 *     前端只做类型与体积的粗校验（省一次往返），真正的边界以后端为准。
 */
export function MenuTab({ store }: { store: Store }) {
  const [dishes, setDishes] = useState<Dish[]>([]) // 门店全部菜品（含多语名 names 与售罄状态）
  const [langs, setLangs] = useState<Lang[]>([]) // 后端注册表返回的受支持语种
  const [msg, setMsg] = useState('') // 统一提示条
  const [busy, setBusy] = useState(false) // 补翻请求进行中
  const [pickOpen, setPickOpen] = useState(false) // 「补充语种」面板是否展开
  const [picked, setPicked] = useState<string[]>([]) // 面板里勾选待补翻的语种 code
  const [imgDishId, setImgDishId] = useState('') // 当前正在上传图片的菜品 id（空 = 无）
  const [imgBusy, setImgBusy] = useState(false) // 图片上传进行中（禁用按钮防重复提交）
  const fileRef = useRef<HTMLInputElement>(null) // 隐藏的文件选择框（全局一个，按 imgDishId 归属）

  /** 重新拉取菜品列表（补翻 / 切售罄后都要刷新）。 */
  const load = useCallback(() => {
    api.listDishes(store.id).then((d) => setDishes(d.dishes ?? [])).catch((e: Error) => setMsg(e.message))
  }, [store.id])

  useEffect(() => {
    load()
    // 语种注册表加载失败静默：覆盖一览会空着，但不影响菜单本身的操作。
    api.langs().then((d) => setLangs(d.langs ?? [])).catch(() => undefined)
  }, [load])

  // 目标语种 = 注册表里除 zh 之外的全部。zh 是原文语种，永远不作为翻译目标。
  const targets = langs.filter((l) => l.code !== 'zh')

  /** 某语种的覆盖率：有译文的菜 / 总菜数。空菜单返回 0 以避免除零。 */
  const coverage = (code: string) => {
    if (dishes.length === 0) return 0
    const hit = dishes.filter((d) => d.names?.[code]).length
    return Math.round((hit / dishes.length) * 100)
  }

  /** 切换单个菜品的售罄状态，成功后重新拉列表以拿到服务端的真值。 */
  const toggle = async (d: Dish) => {
    try {
      await api.patchDish(store.id, d.id, { sold_out: !d.sold_out })
      load()
    } catch (e) {
      setMsg((e as Error).message)
    }
  }

  /** 对勾选的语种执行批量补翻。成功后收起面板、清空选择并刷新覆盖率。 */
  const runTranslate = async () => {
    if (picked.length === 0) return
    setBusy(true)
    setMsg('')
    try {
      const r = await api.translate(store.id, picked)
      setMsg(`已生成 ${picked.length} 个语种，新增译文 ${r.translated_count} 条。`)
      setPickOpen(false)
      setPicked([])
      load()
    } catch (e) {
      setMsg((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  /** 点「上传」：记下菜品 id 并弹出隐藏的文件选择框。 */
  const pickImage = (d: Dish) => {
    setImgDishId(d.id)
    fileRef.current?.click()
  }

  /** 文件选定后上传。先做类型/体积粗校验（与后端常量一致），过了再发请求。 */
  const onFilePicked = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    // 无论成败都要清空 input：否则同一个文件第二次选不会触发 change
    e.target.value = ''
    if (!file || !imgDishId) return
    if (!file.type.startsWith('image/')) {
      setMsg('请选择图片文件（PNG / JPEG / WebP / GIF）')
      return
    }
    if (file.size > 5 << 20) {
      setMsg('图片不能超过 5MB，请压缩后重试')
      return
    }
    setImgBusy(true)
    setMsg('')
    try {
      await api.uploadDishImage(store.id, imgDishId, file)
      setMsg('图片已更新。')
      load()
    } catch (err) {
      setMsg((err as Error).message)
    } finally {
      setImgBusy(false)
      setImgDishId('')
    }
  }

  /** 删除菜品图片（幂等）。 */
  const removeImage = async (d: Dish) => {
    setImgBusy(true)
    setMsg('')
    try {
      await api.deleteDishImage(store.id, d.id)
      setMsg('图片已删除。')
      load()
    } catch (err) {
      setMsg((err as Error).message)
    } finally {
      setImgBusy(false)
    }
  }

  return (
    <div className="lc-stack">
      <div className="lc-row lc-row--between">
        <h1 style={{ margin: 0 }}>菜单</h1>
        {/* 展开 / 收起「补充语种」面板 */}
        <button className="lc-btn lc-btn--sm" onClick={() => setPickOpen((v) => !v)}>
          补充语种
        </button>
      </div>
      {/* 说明「每日剩余」是可选字段，降低商家的维护心理负担 */}
      <p className="lc-muted">「每日剩余」是唯一需要每天维护的字段；不维护就不显示，不会报错。</p>

      {/* ★ Q1（2026-10-19 用户拍板）：未翻译提醒。
          确认原文稿即落库菜品后，客人端已有菜单可看——但客人看到的是
          「中文原文 + 未翻译标」，产品上仍等于没上线。因此一个语种都还没翻时
          给一条醒目提醒，并指路右上角「补充语种」。部分语种未翻时不弹
          （那种缺口由下方覆盖率一览呈现，避免提醒疲劳）。 */}
      {dishes.length > 0 && targets.length > 0 && targets.every((l) => coverage(l.code) === 0) && (
        <div className="lc-alert lc-alert--warn">
          <b>菜单还没有任何译文。</b>客人现在打开门店页，会看到中文原文并标注「未翻译」。
          点右上角「补充语种」批量生成译文，外国客人才能用他们自己的语言点菜。
        </div>
      )}

      {/* 语种覆盖一览：一眼看出哪些语言还没翻 */}
      <div className="lc-card">
        <div className="lc-label">语种覆盖（{dishes.length} 道菜）</div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 7 }}>
          {targets.map((l) => {
            const pct = coverage(l.code)
            // 100% 绿色、0% 默认色、其余黄色，让缺口一眼可见
            return (
              <span
                key={l.code}
                className={`lc-badge ${pct === 100 ? 'lc-badge--ok' : pct === 0 ? '' : 'lc-badge--warn'}`}
                title={`${l.native} · ${l.name_zh}`}
              >
                {l.native} {pct}%
              </span>
            )
          })}
        </div>
      </div>

      {pickOpen && (
        <div className="lc-card">
          {/* 说明补翻是增量的，打消「会不会重复花钱」的顾虑 */}
          <div className="lc-label">选择要补充生成的语种（已有译文不会重复翻，省配额）</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 7, marginBottom: 12 }}>
            {targets.map((l) => {
              const on = picked.includes(l.code)
              return (
                // 多选标签：选中时用主题色填充；点击即在 picked 里增删该 code
                <button
                  key={l.code}
                  className="lc-btn lc-btn--sm"
                  style={{
                    borderColor: on ? 'var(--lc-accent)' : undefined,
                    background: on ? 'var(--lc-accent)' : undefined,
                    color: on ? '#fff' : undefined,
                  }}
                  onClick={() => setPicked(on ? picked.filter((c) => c !== l.code) : [...picked, l.code])}
                >
                  {l.native}
                </button>
              )
            })}
          </div>
          {/* 一个都没选时禁用，避免发空请求 */}
          <button className="lc-btn lc-btn--primary lc-btn--sm" disabled={busy || picked.length === 0} onClick={runTranslate}>
            {busy ? '生成中…' : `生成所选 ${picked.length} 个语种`}
          </button>
        </div>
      )}

      {msg && <div className="lc-alert lc-alert--info">{msg}</div>}

      <div className="lc-card" style={{ padding: 0 }}>
        {/* 隐藏的文件选择框：由每行的「上传」按钮触发，选定后上传到当前 imgDishId */}
        <input ref={fileRef} type="file" accept="image/*" hidden onChange={onFilePicked} />
        <table className="lc-table">
          <thead>
            <tr>
              <th>中文原文</th>
              <th>English</th>
              <th>分类</th>
              <th>图片</th>
              <th style={{ textAlign: 'right' }}>价格</th>
              <th style={{ textAlign: 'right' }}>已译语种</th>
              <th style={{ textAlign: 'right' }}>状态</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {dishes.map((d) => {
              // have：该菜已有译文的语种条目（过滤掉空字符串，兼容旧数据里的空值）
              const have = Object.entries(d.names ?? {}).filter(([, v]) => v)
              return (
                <tr key={d.id}>
                  {/* name_zh 是原文真值，只读展示，任何时候都不被译文覆盖 */}
                  <td>{d.name_zh}</td>
                  {/* English 列直接取 names.en（即兼容字段 name_en 的来源），没有则显示「未命中」 */}
                  <td>{d.names?.en || <span className="lc-dim">未命中</span>}</td>
                  <td>{d.category || '-'}</td>
                  {/* 图片列：有图显示缩略图 + 删除；无图只有上传按钮 */}
                  <td>
                    <div className="lc-row" style={{ gap: 6, alignItems: 'center' }}>
                      {d.image_url ? (
                        <img
                          className="lc-dish-thumb"
                          src={d.image_url}
                          alt={d.name_zh}
                          loading="lazy"
                          onError={(e) => {
                            // 图挂了就藏掉 alt 占位，别留一个碎图icon
                            ;(e.target as HTMLImageElement).style.visibility = 'hidden'
                          }}
                        />
                      ) : (
                        <span className="lc-dim">无图</span>
                      )}
                      <button
                        className="lc-btn lc-btn--sm lc-btn--ghost"
                        disabled={imgBusy}
                        onClick={() => pickImage(d)}
                      >
                        {imgBusy && imgDishId === d.id ? '上传中…' : d.image_url ? '换图' : '上传'}
                      </button>
                      {d.image_url && (
                        <button className="lc-btn lc-btn--sm lc-btn--ghost" disabled={imgBusy} onClick={() => removeImage(d)}>
                          删除
                        </button>
                      )}
                    </div>
                  </td>
                  <td style={{ textAlign: 'right' }}>{d.price != null ? `¥${d.price}` : '-'}</td>
                  <td style={{ textAlign: 'right' }}>
                    {/* 覆盖率胶囊：已译语种数 / 目标语种总数；hover 显示具体是哪几个语种 */}
                    <span className="lc-dim" title={have.map(([k]) => k).join(', ')}>
                      {have.length}/{targets.length}
                    </span>
                  </td>
                  <td style={{ textAlign: 'right' }}>
                    {d.sold_out ? <span className="lc-badge">售罄</span> : <span className="lc-badge lc-badge--ok">在售</span>}
                  </td>
                  <td style={{ textAlign: 'right' }}>
                    <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => toggle(d)}>
                      {d.sold_out ? '恢复' : '售罄'}
                    </button>
                  </td>
                </tr>
              )
            })}
            {/* 空态：引导商家去引导流上传菜单 */}
            {dishes.length === 0 && (
              <tr>
                <td colSpan={8} className="lc-dim" style={{ padding: 20, textAlign: 'center' }}>
                  暂无菜品，去引导流上传菜单
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
