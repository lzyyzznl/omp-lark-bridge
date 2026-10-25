import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Dev proxy: 把 /api/v1 转发到 omp-lark-bridge 的 management API。
// 地址可被 VITE_API_BASE 环境变量覆盖（默认相对路径走此代理）。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_API_BASE || 'http://127.0.0.1:31990',
        changeOrigin: true,
      },
    },
  },
})
