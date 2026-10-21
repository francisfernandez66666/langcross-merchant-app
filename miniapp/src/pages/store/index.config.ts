// definePageConfig 是 Taro 4 的全局声明（见 app.config.ts 的同款说明），不能 import。

export default definePageConfig({
  navigationBarTitleText: '菜单',
  // ★ 下拉刷新：菜单是实时数据（售罄状态会变），保留刷新入口
  enablePullDownRefresh: true,
  backgroundTextStyle: 'dark',
})
