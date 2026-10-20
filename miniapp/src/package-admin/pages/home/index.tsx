import { useEffect, useState } from 'react'
import { View, Text } from '@tarojs/components'
import { http, authHeader } from '../../../utils/http'
import { store, TOKEN_KEY } from '../../../utils/storage'
import './index.css'

/**
 * 商家后台（分包 package-admin）。
 *
 * ★ 迁移来源：web/src/admin/AdminApp.tsx(164) + PlatformTab.tsx(126) + Onboarding.tsx(98)
 *   + AICredentialsTab.tsx(49)，共 437 个 JSX 标签，是全项目最重的一块。
 *
 * ★★ 最大的改造点是表格：全项目 78 个 table/tr/td 都在这里，而小程序没有表格元素。
 *     改法见方案 §4.3：
 *       table → <View className="lc-table">（display:flex; flex-direction:column）
 *       tr    → <View className="lc-tr">（flex-direction:row）
 *       td    → <View className="lc-td">（flex:1）
 *     另：select → <Picker>（5 处）、input → <Input>（32 处）。
 *
 * 本文件是占位骨架，S4 阶段按上述规则逐 Tab 迁移。
 */

type StoreBrief = { id: string; name: string }

// 商家后台分包首页（占位骨架）：拉当前账号的门店列表。
// 完整迁移方案见文件头；列表用 flex 行替代 web 端的 table 结构。
export default function AdminHomePage() {
  const [stores, setStores] = useState<StoreBrief[]>([])

  useEffect(() => {
    const token = store.get<string>(TOKEN_KEY)
    if (!token) return
    http<{ stores: StoreBrief[] }>('/api/stores', { headers: authHeader(token) }).then((res) => {
      if (res.ok) setStores(res.data.stores || [])
    })
  }, [])

  return (
    <View className='ad-wrap'>
      <Text className='ad-title'>我的门店</Text>
      {stores.length === 0 ? (
        <View className='ad-empty'>
          <Text className='ad-empty-txt'>暂无门店，请先登录</Text>
        </View>
      ) : (
        // ★ 列表用 flex 行替代原 table 结构
        stores.map((s) => (
          <View key={s.id} className='ad-row'>
            <Text className='ad-row-txt'>{s.name}</Text>
          </View>
        ))
      )}
    </View>
  )
}
