/**
 * 引导流共用的常量与工具。
 *
 * 为什么单独一个文件：SAMPLE（示例菜单）给 P2 的「识别不可用」保底路径用，
 * DEFAULT_TARGETS（默认勾选语种）给根组件的初始状态用，toBase64 给 P2 的
 * OCR 上传用——三个使用者分散在不同步骤里，抽出来才不用互相 import。
 *
 * ── 拆分背景（2026-10-21 FIX-13）─────────────────────────
 * Onboarding.tsx 661 行、五个步骤全在一个文件里。现在按 P1~P5 拆开，
 * 根组件只保留「步骤状态机」。
 */
import type { DraftItem } from '../api/client'

/**
 * 示例菜单：用于本地无 OCR 服务时快速演示后续步骤。
 * 选的都是最常见的中餐菜品，方便商家一眼看懂「这就是我的菜单」，
 * 从而愿意继续走完 P4/P5；所有项默认 selected: true，进 P3 时已是「全选」状态。
 */
export const SAMPLE: DraftItem[] = [
  { name: '宫保鸡丁', price: 38, category: '热菜', selected: true },
  { name: '麻婆豆腐', price: 32, category: '热菜', selected: true },
  { name: '水煮鱼', price: 68, category: '热菜', selected: true },
  { name: '糖醋里脊', price: 42, category: '热菜', selected: true },
  { name: '番茄炒蛋', price: 26, category: '热菜', selected: true },
  { name: '清炒时蔬', price: 22, category: '素菜', selected: true },
  { name: '蛋炒饭', price: 18, category: '主食', selected: true },
  { name: '米饭', price: 3, category: '主食', selected: true },
  { name: '酸辣汤', price: 22, category: '汤', selected: true },
  { name: '可乐', price: 8, category: '饮品', selected: true },
  { name: '啤酒', price: 12, category: '饮品', selected: true },
]

/**
 * P4 默认勾选的语种：覆盖最大的三拨客人（欧美 / 日 / 韩）。
 * 这三个是来华客人里占比最高的语种，默认勾上能让商家零思考走完流程；
 * 其余语种（th/fr/es/de/pt/vi）不默认勾选，由商家按需自行加选。
 * 注意这里是「默认值」而非「语种清单」——真正的可选项一律来自后端注册表，
 * 所以即便后端新增/删除语种，这个默认值也不需要跟着改（不存在的 code 会被忽略）。
 */
export const DEFAULT_TARGETS = ['en', 'ja', 'ko']

/**
 * 把本地图片文件读成 base64 字符串（去掉 dataURL 前缀，只留纯数据段）。
 * OCR 接口 POST /api/ocr/extract 的 image_base64 字段要的就是纯 base64。
 */
export function toBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => {
      const s = String(r.result)
      // readAsDataURL 得到的是 "data:image/png;base64,xxxx"，逗号后才是纯 base64
      resolve(s.includes(',') ? s.split(',')[1] : s)
    }
    r.onerror = () => reject(new Error('读取图片失败'))
    r.readAsDataURL(file)
  })
}
