/**
 * 商家上线引导流 · P1–P5（路由 /admin/onboarding，见 App.tsx）。
 *
 * 目标：让一个新商家在几分钟内、零思考地完成「把纸质菜单变成多语菜单并上线」。
 * 五个步骤是「不可跳过的顺序依赖」——每一步的产物是下一步的输入：
 *   P1 算损失   ：本地算一笔账（纯说服用，不落任何数据、不联网）
 *   P2 上传菜单 ：OCR 出菜品草稿（需联网，识别不可用时退回示例菜单）
 *   P3 挑要翻译的菜 + 改价（决定后面翻译的输入）
 *   P4 选目标语种 → 多语预览（全程唯一一次「真生成」，需联网）
 *   P5 注册/登录 → 建店 → 存草稿 → 确认原文稿 → 批量翻译 → 出双码
 *
 * 关键不变量（改代码时务必守住）：
 *   - 原文语种固定为 zh；item.name / Dish.name_zh 是原文真值，任何时候都不能被译文覆盖。
 *   - 必须先「确认原文稿」才能翻译：P5 里 confirmDraft 一定要排在 translate 之前。
 *   - 目标语种清单来自后端注册表 GET /api/translation/langs，前端不硬编码，
 *     这样后端新增语种时前端零改动。
 *
 * 涉及的接口：
 *   - POST /api/ocr/extract            ：菜单图片 OCR
 *   - GET  /api/translation/langs      ：语种注册表
 *   - POST /api/public/translate-preview：注册前的多语预览（免登录）
 *   - POST /api/auth/otp、/api/auth/login：注册 / 登录
 *   - POST /api/stores                 ：建店
 *   - POST /api/stores/:id/draft、/draft/confirm、/translate：落草稿并生成多语
 *
 * ★ 2026-10-21 FIX-13：原先这个文件 661 行、五个步骤全在里面。
 *   现在根组件只保留「步骤状态机」——记录当前第几步、以及跨步骤共享的三份数据；
 *   各步骤独立成文件（OnboardingStep1~5），共用常量与 toBase64 在 onboardingShared.ts。
 */
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { type CouponTemplate, type DraftItem, type Store, type StoreLinks } from '../api/client'
import { DEFAULT_TARGETS } from './onboardingShared'
import { Step1 } from './OnboardingStep1'
import { Step2 } from './OnboardingStep2'
import { Step3 } from './OnboardingStep3'
import { Step4 } from './OnboardingStep4'
import { Step5 } from './OnboardingStep5'

// 五个步骤的标题，下标 = step - 1。顺序即流程顺序，不要随意调整。
const STEP_TITLES = ['算损失', '上传菜单', '挑要翻译的菜', '核心多语言', '注册解锁']

/**
 * 引导流根组件：只负责「步骤状态机」——记录当前第几步、以及跨步骤共享的数据。
 * 各步骤的具体 UI 都拆到 Step1~Step5 子组件，由本组件按 step 挂载。
 *
 * 跨步骤共享的三份状态（因为要在多个步骤间传递，所以提升到这里）：
 *   - items  ：菜品草稿（P2 产出 → P3 编辑 → P4 预览 → P5 落库）
 *   - targets：目标语种（P4 选择 → P5 翻译）
 *   - result ：上线结果（P5 产出后用于展示成功页，非空即代表已完成上线）
 */
export default function Onboarding() {
  const nav = useNavigate()
  const [step, setStep] = useState(1) // 当前步骤 1~5
  const [items, setItems] = useState<DraftItem[]>([]) // 跨步骤共享的菜品草稿
  const [targets, setTargets] = useState<string[]>(DEFAULT_TARGETS) // 跨步骤共享的目标语种
  // 上线结果：一旦有值，P5 就切换成「上线成功」页；同时它也用来隐藏「上一步」按钮。
  const [result, setResult] = useState<{ store: Store; links: StoreLinks; coupon?: CouponTemplate } | null>(null)

  /** 步骤跳转：把目标步数夹在 1~5 之间，防止越界。 */
  const go = (n: number) => setStep(Math.min(5, Math.max(1, n)))

  return (
    <div className="warm" style={{ minHeight: '100%', background: 'var(--lc-bg)', color: 'var(--lc-text)' }}>
      <div className="lc-wrap" style={{ maxWidth: 720, paddingTop: 34 }}>
        <div className="lc-row lc-row--between" style={{ marginBottom: 14 }}>
          <div className="lc-dim">能言翻译助手 · 商家上线（{step}/5）</div>
          {/* 「跳过」直接去管理台：商家可能已建过店，不强制走引导 */}
          <button className="lc-btn lc-btn--sm lc-btn--ghost" onClick={() => nav('/admin')}>
            跳过
          </button>
        </div>

        {/* 顶部进度条：i <= step 的格子上色，直观显示当前进度 */}
        <div className="lc-steps">
          {[1, 2, 3, 4, 5].map((i) => (
            <div key={i} className={`lc-step ${i <= step ? 'is-on' : ''}`} />
          ))}
        </div>

        <h1>{STEP_TITLES[step - 1]}</h1>

        {/* 按 step 挂载对应步骤组件。每一步的「下一步」回调里带上必要的状态写入 */}
        {step === 1 && <Step1 onNext={() => go(2)} />}
        {step === 2 && (
          <Step2
            items={items}
            onLoaded={(its) => {
              setItems(its)
              go(3)
            }}
          />
        )}
        {/* P3 需要回写 items（勾选/改价），所以把 setItems 直接透传下去 */}
        {step === 3 && <Step3 items={items} setItems={setItems} onNext={() => go(4)} />}
        {step === 4 && <Step4 items={items} targets={targets} setTargets={setTargets} onNext={() => go(5)} />}
        {step === 5 && <Step5 items={items} targets={targets} result={result} setResult={setResult} />}

        {/* 上一步按钮：第 1 步没有上一步；已上线（result 有值）后也不再显示，避免误退 */}
        {step > 1 && !result && (
          <div style={{ marginTop: 22 }}>
            <button className="lc-btn lc-btn--ghost lc-btn--sm" onClick={() => go(step - 1)}>
              ← 上一步
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
