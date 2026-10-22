/**
 * 商家端管理台共用零件（从 AdminApp.tsx 拆出）。
 *
 * Tab 类型与 TABS 清单是「数据驱动渲染」的源头：导航项、可见角色、顺序
 * 全在这一份清单里，新增一个 Tab 只改这里，导航栏自动出现。
 * 抽出来的理由：Console（渲染导航）与将来任何按 Tab 分发的逻辑都要用，
 * 留在 Console 文件里别的组件就得依赖整个控制台外壳。
 */


// Tab 用字符串联合类型而非枚举，配合下面的 TABS 常量做「数据驱动」渲染，
// 新增一个 Tab 只需在联合类型和 TABS 各加一处，导航栏自动出现。
export type Tab = 'overview' | 'menu' | 'coupon' | 'insights' | 'requests' | 'platform' | 'ai' | 'settings'

// 左侧导航项：id 用于匹配当前 Tab，label 是展示文案。
// 顺序即商家日常使用的优先级：先看概览、再维护菜单，券/洞察/服务请求靠后。
// roles 标注了角色的 Tab 只对这些角色可见（权限以服务端为准，前端只是体验优化）：
//   - 平台管理：超管与租户管理员（前者多出概览/租户分区，后者管账号/翻译 Key）
//   - AI 凭据：超管可写；tenant_admin 只读（后端同款门禁，列表里 can_write 区分）
export const TABS: { id: Tab; label: string; roles?: string[] }[] = [
  { id: 'overview', label: '概览' },
  { id: 'menu', label: '菜单' },
  { id: 'coupon', label: '券与核销' },
  { id: 'insights', label: '经营洞察' },
  { id: 'requests', label: '服务请求' },
  { id: 'platform', label: '平台管理', roles: ['super_admin', 'tenant_admin'] },
  { id: 'ai', label: 'AI 凭据', roles: ['super_admin', 'tenant_admin'] },
  { id: 'settings', label: '设置' },
]
