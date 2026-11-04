import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
// `@/` 指向移植自 ompweb 的渲染栈副本（src/omp）。那份源码保持原样，
// 用别名而不是逐文件改 import，升级时能整体替换。
const ompRender = fileURLToPath(new URL('./src/omp', import.meta.url))

// Dev proxy: 把 /api/v1 转发到 omp-lark-bridge 的 management API。
// 地址可被 VITE_API_BASE 环境变量覆盖（默认相对路径走此代理）。
export default defineConfig({
  resolve: {
    alias: { '@': ompRender },
  },
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
