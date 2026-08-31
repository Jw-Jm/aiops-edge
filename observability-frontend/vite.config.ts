import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    // All application routes are lazy-loaded.  Keep the graphing/runtime
    // libraries in explicit async vendor chunks so normal pages do not pay
    // their cost on first load.  G6 is currently a single ~1.4 MB package;
    // the warning limit is an explicit, time-bounded exception for that
    // upstream bundle, not a license to grow application chunks unchecked.
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        manualChunks: {
          'vendor-g6': ['@antv/g6'],
          'vendor-echarts': ['echarts', 'echarts-for-react'],
          'vendor-antd': ['antd', '@ant-design/icons'],
        },
      },
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': 'http://localhost:8080'
    }
  }
})
