// 生产环境。
// ★ API_BASE 必须是已完成 ICP 备案、且已在微信后台加入 request/uploadFile/downloadFile 白名单的 HTTPS 域名。
//   换域名时同步改这里 + 微信后台「服务器域名」，否则真机全部请求失败。
export default {
  env: {
    NODE_ENV: '"production"',
  },
  defineConstants: {
    API_BASE: '"https://api.langcross.com"',
  },
  h5: {},
  mini: {},
}
