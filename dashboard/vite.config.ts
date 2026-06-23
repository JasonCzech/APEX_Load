import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const dashboardPackage = require('./package.json') as { version?: string }
const vitePackage = require('vite/package.json') as { version?: string }

function resolveBuildTime(explicit?: string) {
  const raw = explicit || process.env.SOURCE_DATE_EPOCH
  if (raw) {
    if (/^\d+$/.test(raw)) {
      return new Date(Number(raw) * 1000).toISOString()
    }
    const parsed = new Date(raw)
    return Number.isNaN(parsed.getTime()) ? raw : parsed.toISOString()
  }
  return new Date().toISOString()
}

const sharedBuildTime = resolveBuildTime(process.env.APEXLOAD_UX_BUILD_TIME || process.env.BUILD_TIME)

export default defineConfig({
  plugins: [react()],
  define: {
    __APEXLOAD_UX_VERSION__: JSON.stringify(process.env.APEXLOAD_UX_VERSION || dashboardPackage.version || 'dev'),
    __APEXLOAD_UX_BUILD_TIME__: JSON.stringify(sharedBuildTime),
    __APEXLOAD_VITE_VERSION__: JSON.stringify(process.env.APEXLOAD_VITE_VERSION || vitePackage.version || 'unknown'),
    __APEXLOAD_VITE_BUILD_TIME__: JSON.stringify(resolveBuildTime(process.env.APEXLOAD_VITE_BUILD_TIME || sharedBuildTime)),
  },
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
