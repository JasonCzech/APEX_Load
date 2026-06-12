import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    setupFiles: ['./src/test/codemirrorJsdomPolyfill.ts'],
  },
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
          if (
            id.includes('/@codemirror/') ||
            id.includes('/@uiw/react-codemirror/') ||
            id.includes('/@uiw/codemirror') ||
            id.includes('/@lezer/') ||
            id.includes('/codemirror/')
          ) {
            return 'vendor-codemirror'
          }
          return 'vendor'
        },
      },
    },
  },
  server: {
    port: Number(process.env.PORT) || 3000,
    proxy: {
      '/api': {
        target: process.env.APEXLOAD_API_PROXY || 'http://localhost:8080',
        ws: true,
      },
    },
  },
})
