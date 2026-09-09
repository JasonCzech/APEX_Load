import './codemirrorJsdomPolyfill'

// React 18 uses this flag to distinguish a test harness that supports act().
// Keep it in the shared setup so every jsdom suite gets the same behavior.
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// Component suites exercise their own dialog contents in their local render root.
// Real body-level layering is covered by DialogLayer.test and browser workflows.
import { vi } from 'vitest'
vi.mock('../components/ux/DialogLayer', () => ({ default: ({ children }: { children: import('react').ReactNode }) => children }))
// Existing component suites control the decision; the shared asynchronous dialog
// has its own unmocked focus, cancellation, and concurrency coverage.
vi.mock('../components/ux/confirmAction', () => {
  let pending = false
  return { confirmAction: async (message: string) => {
    if (pending) return false
    pending = true
    try { return await Promise.resolve(window.confirm(message)) } finally { pending = false }
  } }
})
