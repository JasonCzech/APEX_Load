import { configDefaults, defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const dashboardPackage = require('./package.json') as { version?: string }
const vitePackage = require('vite/package.json') as { version?: string }
const dashboardRoot = fileURLToPath(new URL('.', import.meta.url))
const docsRoot = fileURLToPath(new URL('../docs', import.meta.url))

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
    exclude: [...configDefaults.exclude, 'e2e/**'],
    setupFiles: ['./src/test/codemirrorJsdomPolyfill.ts'],
  },
  build: {
    // The largest lazy chunks are the editor and Three.js visualization. Their
    // compressed transfer sizes remain well below this boundary; keep a tight
    // ceiling that still reports meaningful growth beyond their current size.
    chunkSizeWarningLimit: 550,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) {
            return undefined
          }
          if (id.includes('/three/')) {
            return 'vendor-three'
          }
          if (id.includes('/recharts/')) {
            return 'vendor-recharts'
          }
          if (id.includes('/@codemirror/lang-')) {
            return 'vendor-codemirror-languages'
          }
          if (id.includes('/@lezer/')) {
            return 'vendor-codemirror-parser'
          }
          if (id.includes('/@codemirror/') || id.includes('/@uiw/react-codemirror/') || id.includes('/@uiw/codemirror') || id.includes('/codemirror/')) {
            return 'vendor-codemirror-core'
          }
          return 'vendor'
        },
      },
    },
  },
  server: {
    port: Number(process.env.PORT) || 3000,
    // The desktop controller's CORS/WebSocket allow-list is intentionally
    // limited to the configured Wails dev origin. Do not let Vite silently
    // move to another port when that origin is occupied.
    strictPort: true,
    fs: {
      // The dashboard intentionally embeds reviewed repository documentation.
      // Keep Vite's file-serving boundary to the package and that directory.
      allow: [dashboardRoot, docsRoot],
    },
    proxy: {
      '/api': {
        target: process.env.APEXLOAD_API_PROXY || 'http://localhost:8080',
        ws: true,
      },
    },
  },
})
