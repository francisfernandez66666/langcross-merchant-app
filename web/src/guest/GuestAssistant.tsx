/**
 * 客人端 AI 点菜助手面板（抽屉式聊天层）。
 *
 * 定位：扫桌码的外国客人「问菜单、问价格、点菜引导、叫服务」的统一入口。
 * 与「问店员」短语卡（HelpSheet）的关系：助手是首选，短语卡是无网兜底——
 * 助手面板里也保留一个「问真人店员」入口，一键唤起 HelpSheet。
 *
 * ─────────────────────────────────────────────────────────────
 * 核心纪律（与后端 guard 同源，前端只负责呈现，不负责编造）
 * ─────────────────────────────────────────────────────────────
 *   - 助手只「提议」动作（加菜 / 加水 / 服务铃），客人点一下才真正发起请求。
 *     这是「提议—确认」边界：绝不能让 AI 替客人把菜加进订单。
 *   - 语音识别在浏览器端做（Web Speech API），转成文字后走同一个 chat 接口；
 *     服务端不碰音频（见 handlers_guest_assist.go 注释），隐私更稳。
 *   - 多轮靠服务端 cookie（lc_guest）隔离，前端无需管理会话 ID，
 *     只管把每轮问题带上 lang 发过去。
 *
 * props：
 *   - slug：门店短链（接口路径需要）
 *   - lang：客人界面语言（决定助手作答语言 + 语音识别语种）
 *   - ref：桌码来源标记（t01 等），随加菜/服务请求带上
 *   - onClose：关闭面板
 *   - onTalkToStaff：唤起无网兜底短语卡（可选）
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type Action } from '../api/client'
import { t } from './i18n'

// 一条对话消息（前端本地状态，不落库）。
type Msg = {
  role: 'user' | 'assistant' | 'system'
  text: string
  actions?: Action[] // 仅 assistant 消息带：建议客人点的动作
  ts: number
}

// 语种短码 → Web Speech API 期望的 BCP-47 标签（识别更准确）。
// 缺省回落 en-US，不影响主流程（识别失败也只是文字为空，客人仍可手打）。
const SPEECH_LOCALE: Record<string, string> = {
  zh: 'zh-CN',
  en: 'en-US',
  ja: 'ja-JP',
  ko: 'ko-KR',
  th: 'th-TH',
  fr: 'fr-FR',
  es: 'es-ES',
  de: 'de-DE',
  pt: 'pt-BR',
  vi: 'vi-VN',
}

// 浏览器是否提供语音识别（Web Speech API）。桌面 Chrome / 移动端普遍支持，
// 不支持时麦克风按钮直接禁用，不抛错。
function speechSupported(): boolean {
  return typeof window !== 'undefined' && Boolean((window as any).SpeechRecognition || (window as any).webkitSpeechRecognition)
}

export default function GuestAssistant({
  slug,
  lang,
  refCode,
  onClose,
  onTalkToStaff,
}: {
  slug: string
  lang: string
  refCode: string
  onClose: () => void
  onTalkToStaff?: () => void
}) {
  const [msgs, setMsgs] = useState<Msg[]>([])
  const [input, setInput] = useState('') // 输入框文字（含语音 interim 结果）
  const [busy, setBusy] = useState(false) // 等待助手回答
  const [toast, setToast] = useState('') // 操作成功/错误提示（自动消失）
  const [sent, setSent] = useState<Set<string>>(new Set()) // 已发起的动作 key+arg，防重复点击
  const [listening, setListening] = useState(false) // 语音识别进行中

  const listRef = useRef<HTMLDivElement>(null) // 消息列表容器，用于自动滚到底
  const recogRef = useRef<any>(null) // 当前 SpeechRecognition 实例
  const inputRef = useRef<HTMLTextAreaElement>(null)

  // 打开即给一句开场白，降低「跟机器说话」的违和感。
  useEffect(() => {
    setMsgs([{ role: 'assistant', text: t('aiHello', lang), ts: Date.now() }])
    // 进入面板自动聚焦输入框（移动端会唤起键盘，体验更顺）
    setTimeout(() => inputRef.current?.focus(), 150)
  }, [lang])

  // 新消息后自动滚到底。
  useEffect(() => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight, behavior: 'smooth' })
  }, [msgs])

  // toast 自动消失。
  useEffect(() => {
    if (!toast) return
    const id = setTimeout(() => setToast(''), 2600)
    return () => clearTimeout(id)
  }, [toast])

  /** 发一条问题。无 LLM 时后端走确定性兜底，仍然会返回菜单事实，不报错。 */
  const send = useCallback(
    async (text: string) => {
      const q = text.trim()
      if (!q || busy) return
      setInput('')
      setBusy(true)
      setMsgs((m) => [...m, { role: 'user', text: q, ts: Date.now() }])
      // 先放一个「思考中」占位，回答回来再替换。
      const placeholderTs = Date.now()
      setMsgs((m) => [...m, { role: 'assistant', text: t('aiThinking', lang), ts: placeholderTs }])
      try {
        const r = await api.assist(slug, { question: q, lang })
        const rep = r.reply
        setMsgs((m) =>
          m.map((x) =>
            x.ts === placeholderTs
              ? { role: 'assistant', text: rep.content || t('aiNetErr', lang), actions: rep.actions, ts: placeholderTs }
              : x,
          ),
        )
      } catch {
        // 网络错了也不要把「思考中」一直挂着：换成一句可重试的提示。
        setMsgs((m) =>
          m.map((x) => (x.ts === placeholderTs ? { role: 'assistant', text: t('aiNetErr', lang), ts: placeholderTs } : x)),
        )
      } finally {
        setBusy(false)
      }
    },
    [slug, lang, busy],
  )

  /** 执行助手提议的一个动作（加菜 / 加水 / 服务铃 / 买单）。
   *  ★ 客人点一下才真正发起：这就是「提议—确认」边界。 */
  const runAction = useCallback(
    async (act: Action) => {
      const key = act.key + ':' + (act.arg ?? '')
      if (sent.has(key)) return
      const kind = act.key // add_dish / water / bell / bill / other
      try {
        await api.createGuestRequest(slug, {
          kind,
          dish_id: act.arg,
          qty: 1,
          note: input.trim(),
          lang,
          ref: refCode,
        })
        setSent((s) => new Set(s).add(key))
        setToast(t('aiNotify', lang))
      } catch (e) {
        setToast((e as Error).message)
      }
    },
    [slug, lang, refCode, input, sent],
  )

  /** 语音识别：点击麦克风开始，再次点击或自动结束时停止。识别到的文字直接进输入框。 */
  const toggleMic = useCallback(() => {
    if (!speechSupported()) {
      setToast(t('aiNetErr', lang)) // 不支持时给个温和提示（无语音文案就复用网络提示）
      return
    }
    const SR = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
    if (listening) {
      recogRef.current?.stop()
      return
    }
    const rec = new SR()
    rec.lang = SPEECH_LOCALE[lang] ?? 'en-US'
    rec.interimResults = true
    rec.continuous = false
    rec.onresult = (ev: any) => {
      let txt = ''
      for (const res of ev.results) txt += res[0].transcript
      setInput(txt)
    }
    rec.onend = () => {
      setListening(false)
      recogRef.current = null
    }
    rec.onerror = () => {
      setListening(false)
      recogRef.current = null
    }
    recogRef.current = rec
    setListening(true)
    rec.start()
  }, [lang, listening, refCode, t])

  // 关闭面板时停掉可能的语音识别，避免后台继续听。
  useEffect(() => () => recogRef.current?.stop(), [])

  const canSend = input.trim().length > 0 && !busy

  return (
    <div className="gx-mask gx-assist-mask" onClick={onClose}>
      <div className="gx-assist" onClick={(e) => e.stopPropagation()}>
        {/* 头部：标题 + 新对话 + 关闭 */}
        <div className="gx-assist-head">
          <div className="gx-assist-title">{t('aiTitle', lang)}</div>
          <div className="gx-assist-head-actions">
            <button className="gx-assist-icon" onClick={() => setMsgs([{ role: 'assistant', text: t('aiHello', lang), ts: Date.now() }])} title={t('aiNewChat', lang)}>
              ⟲
            </button>
            <button className="gx-assist-icon" onClick={onClose} aria-label={t('close', lang)}>
              ✕
            </button>
          </div>
        </div>

        {/* 消息列表 */}
        <div className="gx-assist-list" ref={listRef}>
          {msgs.map((m, i) => (
            <div key={m.ts + '-' + i} className={`gx-msg gx-msg--${m.role}`}>
              <div className="gx-bubble">
                {m.text}
                {/* 助手消息末尾附上动作按钮（加菜 / 加水 / 服务铃） */}
                {m.role === 'assistant' && m.actions && m.actions.length > 0 && (
                  <div className="gx-actions">
                    {m.actions.map((a, j) => {
                      const k = a.key + ':' + (a.arg ?? '')
                      return (
                        <button
                          key={k + '-' + j}
                          className="gx-chip"
                          disabled={sent.has(k)}
                          onClick={() => runAction(a)}
                        >
                          {sent.has(k) ? '✓ ' : ''}
                          {a.name}
                        </button>
                      )
                    })}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>

        {/* 操作提示 toast */}
        {toast && <div className="gx-assist-toast">{toast}</div>}

        {/* 底部输入条：语音 / 文本框 / 发送 */}
        <div className="gx-assist-bar">
          <button
            className={`gx-assist-mic ${listening ? 'is-on' : ''}`}
            onClick={toggleMic}
            disabled={!speechSupported()}
            aria-label={t('aiMic', lang)}
            title={t('aiMic', lang)}
          >
            {listening ? '●' : '🎤'}
          </button>
          <textarea
            ref={inputRef}
            className="gx-assist-input"
            value={input}
            rows={1}
            placeholder={t('aiPlaceholder', lang)}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              // Enter 发送，Shift+Enter 换行（移动端键盘无 Shift，单按 Enter 即发送）
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                void send(input)
              }
            }}
          />
          <button className="gx-assist-send" onClick={() => void send(input)} disabled={!canSend}>
            {t('aiSend', lang)}
          </button>
        </div>

        {/* 无网兜底：转真人店员 */}
        {onTalkToStaff && (
          <button className="gx-assist-staff" onClick={onTalkToStaff}>
            {t('aiStaff', lang)}
          </button>
        )}
      </div>
    </div>
  )
}
