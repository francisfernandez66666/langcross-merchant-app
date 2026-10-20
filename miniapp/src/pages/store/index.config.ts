import { definePageConfig } from '@tarojs/taro'

export default definePageConfig({
  navigationBarTitleText: '菜单',
  // ★ 下拉刷新：菜单是实时数据（售罄状态会变），保留刷新入口
  enablePullDownRefresh: true,
  backgroundTextStyle: 'dark',
})
