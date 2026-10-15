import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 客人端门店页走短链 /s/{slug}，商家端走 /admin/*，统一单页应用 + SPA fallback。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      // 开发期把 /api 代理到本地 Go 后端，避免跨域
      '/api': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
})
