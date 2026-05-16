import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) {
            return undefined
          }
          if (id.includes('/recharts/')) {
            return 'vendor-recharts'
          }
          if (id.includes('/react/') || id.includes('/react-dom/')) {
            return 'vendor-react'
          }
          return 'vendor'
        },
      },
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: process.env.STORMRUNNER_API_PROXY || 'http://localhost:8080',
        ws: true,
      },
    },
  },
})
