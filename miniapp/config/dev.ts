// 开发环境。
// ★ 小程序真机调试不能用 localhost：把 DEV_API_BASE 换成你机器的局域网 IP（如 http://192.168.1.20:8080），
//   并在微信开发者工具「详情 → 本地设置」勾选「不校验合法域名、web-view、TLS 版本」。
// ★ 正式提审前必须切回 prod，且后端域名已完成 ICP 备案 + HTTPS。
export default {
  env: {
    NODE_ENV: '"development"',
  },
  defineConstants: {
    API_BASE: '"http://localhost:8080"',
  },
  h5: {
    devServer: {
      port: 5174,
      proxy: {
        '/api': {
          target: 'http://localhost:8080',
          changeOrigin: true,
        },
      },
    },
  },
  mini: {},
}
