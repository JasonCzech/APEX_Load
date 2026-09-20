import { configDefaults, defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

import { chunkNameForModule } from './scripts/chunk-strategy.mjs'

const require = createRequire(import.meta.url)
const dashboardPackage = require('./package.json') as { version?: string }
const vitePackage = require('vite/package.json') as { version?: string }
const dashboardRoot = fileURLToPath(new URL('.', import.meta.url))
const docsRoot = fileURLToPath(new URL('../docs', import.meta.url))
const deploymentsRoot = fileURLToPath(new URL('../deployments', import.meta.url))
const mcpRoot = fileURLToPath(new URL('../mcp', import.meta.url))
const apiProxyTarget = process.env.APEXLOAD_API_PROXY || 'http://localhost:8080'
const apiProxyOrigin = new URL(apiProxyTarget).origin

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
    exclude: [...configDefaults.exclude, 'e2e/**', 'e2e-recording/**/*.spec.ts'],
    setupFiles: ['./src/test/vitestSetup.ts'],
  },
  build: {
    // The largest lazy chunks are the editor and Three.js visualization. Their
    // compressed transfer sizes remain well below this boundary; keep a tight
    // ceiling that still reports meaningful growth beyond their current size.
    chunkSizeWarningLimit: 550,
    // Vite 8 defaults CSS minification to Lightning CSS. Declare it explicitly
    // so a future Vite default cannot silently change generated dashboard CSS.
    cssMinify: 'lightningcss',
    rolldownOptions: {
      output: {
        // Rolldown deprecated `output.manualChunks` (and its `advancedChunks`
        // successor) in favour of `output.codeSplitting`. A single name-function
        // group is exactly what Rolldown's compatibility shim built from the old
        // hook, so the emitted chunks are unchanged. The classification itself
        // lives in scripts/chunk-strategy.mjs so it can be unit tested.
        codeSplitting: {
          groups: [{ name: (moduleId) => chunkNameForModule(moduleId) }],
        },
      },
    },
  },
  server: {
    port: Number(process.env.PORT) || 4000,
    // The desktop controller's CORS/WebSocket allow-list is intentionally
    // limited to the configured Wails dev origin. Do not let Vite silently
    // move to another port when that origin is occupied.
    strictPort: true,
    fs: {
      // The dashboard intentionally embeds reviewed repository documentation.
      // Keep Vite's file-serving boundary to the package and the reviewed guide directories.
      allow: [dashboardRoot, docsRoot, mcpRoot, deploymentsRoot],
    },
    proxy: {
      '/api': {
        target: apiProxyTarget,
        changeOrigin: true,
        ws: true,
        rewriteWsOrigin: true,
        configure(proxy) {
          proxy.on('proxyReq', (proxyRequest, request) => {
            if (request.headers.origin) {
              proxyRequest.setHeader('Origin', apiProxyOrigin)
            }
          })
        },
      },
    },
  },
})
