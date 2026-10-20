import type { PropsWithChildren } from 'react'
import { useLaunch } from '@tarojs/taro'
import './app.css'

function App({ children }: PropsWithChildren) {
  useLaunch(() => {
    // ★ 静默登录：拿 openid 作为客人标识，免手机号登录摩擦。
    //   只在需要时调用（评价 / 领券 / 服务铃），不要在首屏无条件触发。
    //   Taro.login() → 拿 code → POST /api/auth/wx-login 换 openid + token
  })

  return children
}

export default App
