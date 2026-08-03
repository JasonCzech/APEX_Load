import './codemirrorJsdomPolyfill'

// React 18 uses this flag to distinguish a test harness that supports act().
// Keep it in the shared setup so every jsdom suite gets the same behavior.
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
