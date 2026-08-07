// Manual chunk classification for the dashboard production build.
//
// This lives outside vite.config.ts so the classification is unit testable:
// the build wires it into `build.rolldownOptions.output.codeSplitting`, and
// src/viteChunkStrategy.test.ts pins the routing decisions that the bundle
// budgets in scripts/check-bundle-budget.mjs depend on.
//
// Returning `undefined` leaves the module to Rolldown's automatic chunking,
// which is the same meaning the value carried under the deprecated
// `output.manualChunks` hook.
export function chunkNameForModule(id) {
  // The execution route only needs the tiny, dependency-free HTTP version
  // formatter. Keep Rolldown from coalescing it with shared runtime-editor
  // components (and their CodeMirror dependencies).
  if (id.endsWith('/src/components/httpVersionModel.ts')) {
    return 'http-version-model'
  }
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
}
