// ★ 这里**故意没有 import**：defineAppConfig 在 Taro 4 里是全局声明
// （@tarojs/taro 的 types/index.d.ts 用 `declare global { const defineAppConfig }`
// 暴露，而模块本身是 `export = Taro`，并没有这个命名导出）。写成
// `import { defineAppConfig } from '@tarojs/taro'` 会报 TS2305「没有导出成员」。
// 全局之所以可见，靠的是 tsconfig 的 "types": ["@tarojs/taro"]。

// ★ 分包策略（主包上限 2MB）：
//   1. 客人端门店页必须在主包 —— 它是扫码直达入口，放分包会出现白屏。
//   2. 店员端、商家后台放分包，按需加载。
//   3. preloadRule：客人进店时后台预拉店员分包，店员点开待办不等待。
export default defineAppConfig({
  pages: ['pages/store/index'],
  subpackages: [
    {
      root: 'package-staff',
      pages: ['pages/requests/index'],
    },
    {
      root: 'package-admin',
      pages: ['pages/home/index'],
    },
  ],
  preloadRule: {
    'pages/store/index': {
      network: 'all',
      packages: ['package-staff'],
    },
  },
  window: {
    backgroundTextStyle: 'dark',
    navigationBarBackgroundColor: '#111111',
    navigationBarTitleText: '能言翻译助手',
    navigationBarTextStyle: 'white',
  },
  // ★ 深色后台 / warm 客人端的两套配色，与 @langcross/ui 的 tokens.css 保持一致；
  //   三端不走 tabBar，靠页面内导航切换，故此处不声明。
  permission: {},
  requiredPrivateInfos: [],
})
