import { useEffect, useState } from 'react'
import { View, Text } from '@tarojs/components'
import { requestSubscribeMessage } from '@tarojs/taro'
import { http, authHeader } from '../../../utils/http'
import { store, TOKEN_KEY } from '../../../utils/storage'
import './index.css'

/**
 * 店员端：服务铃 / 客人提问待办。
 *
 * ★ 迁移来源：web/src/admin/RequestsTab.tsx（25 个 JSX 标签，本分包里最轻的一页）
 * ★ 订阅消息：客人提交服务铃时请求授权，本页负责在店员侧接收待办（方案 §6.2）。
 *   模板 ID 需在微信后台申请后填入 SUBSCRIBE_TEMPLATE_IDS。
 */

const SUBSCRIBE_TEMPLATE_IDS: string[] = []

type RequestItem = {
  id: string
  kind: string
  content?: string
  status: string
  // ★ 与后端 domain.GuestRequest.CreatedAt 对齐：Unix 秒（number），不是字符串
  created_at: number
}

// 店员端待办页：列出本店服务铃 / 客人提问，支持标记完成。
// 由 web/src/admin/RequestsTab.tsx 迁移而来（迁移说明见文件头）。
export default function StaffRequestsPage() {
  const [list, setList] = useState<RequestItem[]>([])
  const [openCount, setOpenCount] = useState(0)

  const load = async () => {
    const storeId = store.get<string>('staff.store_id')
    if (!storeId) return
    // ★ 字段名与后端 handleListGuestRequests 的响应严格对齐：`requests`（曾错读 items，静默空列表）
    const res = await http<{ requests: RequestItem[]; open_count: number }>(
      `/api/stores/${storeId}/requests`,
      { headers: authHeader(store.get<string>(TOKEN_KEY) || undefined) },
    )
    if (res.ok) {
      setList(res.data.requests || [])
      setOpenCount(res.data.open_count || 0)
    }
  }

  useEffect(() => {
    load()
  }, [])

  /** ★ 授权时机：客人提交服务铃的瞬间，而不是进店首屏（方案 §6.2） */
  const askSubscribe = async () => {
    if (!SUBSCRIBE_TEMPLATE_IDS.length) return
    try {
      await requestSubscribeMessage({ tmplIds: SUBSCRIBE_TEMPLATE_IDS })
    } catch {
      /* 用户拒绝或环境不支持时静默跳过，不阻塞主流程 */
    }
  }

  const markDone = async (rid: string) => {
    const storeId = store.get<string>('staff.store_id')
    if (!storeId) return
    await http(`/api/stores/${storeId}/requests/${rid}/done`, {
      method: 'POST',
      headers: authHeader(store.get<string>(TOKEN_KEY) || undefined),
      body: JSON.stringify({ status: 'done' }),
    })
    load()
  }

  return (
    <View className='rq-wrap'>
      <View className='rq-head'>
        <Text className='rq-head-txt'>待处理 {openCount}</Text>
        <View className='rq-sub' onClick={askSubscribe}>
          <Text className='rq-sub-txt'>开启通知</Text>
        </View>
      </View>

      {list.length === 0 ? (
        <View className='rq-empty'>
          <Text className='rq-empty-txt'>暂无待办</Text>
        </View>
      ) : (
        list.map((it) => (
          <View key={it.id} className='rq-item'>
            <View className='rq-item-main'>
              <Text className='rq-kind'>{it.kind}</Text>
              {it.content ? <Text className='rq-content'>{it.content}</Text> : null}
            </View>
            <View className='rq-done' onClick={() => markDone(it.id)}>
              <Text className='rq-done-txt'>完成</Text>
            </View>
          </View>
        ))
      )}
    </View>
  )
}
