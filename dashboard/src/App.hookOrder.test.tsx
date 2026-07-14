// @vitest-environment jsdom

import { act, type ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import App from './App'
import { readAppHistoryIndex, withAppHistoryIndex } from './appRouting'
import { ApiProvider } from './hooks/useApi'

// Regression guard for a reported React Rules-of-Hooks violation:
//   "React has detected a change in the order of Hooks called by AppShell …
//    64. useMemo changed to useCallback"
// said to fire when navigating between top-level left-nav pages
// (Dashboard -> Scenarios -> Execution Plans -> …).
//
// This mounts the *real* AppShell tree (all real providers + real custom hooks)
// and drives navigation by clicking the real Sidebar, exactly as a user would.
// Only the network boundary (fetch + WebSocket), animation-frame timers, and the
// auth gate are stubbed, so the component tree — and its hook ordering — is
// exercised unmodified. React emits the hook-order warning via console.error
// before the invariant throws, so the spy below fails the test if any component
// in the tree produces such a warning during these transitions.

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// Make AuthGate render its children without any network round-trip.
vi.mock('./hooks/useAuthStatus', () => ({
  useAuthStatus: () => ({
    status: 'ready',
    auth: { enabled: false, mode: 'dev', authenticated: false },
  }),
}))

const navigationGuardMock = vi.hoisted(() => {
  const state = {
    dirty: false,
    pending: null as (() => void) | null,
  }
  return {
    state,
    runner: {
      hasDirtyGuard: () => state.dirty,
      runGuarded: (proceed: () => void) => {
        if (state.dirty) {
          state.pending = proceed
        } else {
          proceed()
        }
      },
    },
  }
})

vi.mock('./hooks/NavigationGuardProvider', () => ({
  NavigationGuardProvider: ({ children }: { children: ReactNode }) => children,
  useNavigationGuard: () => undefined,
  useNavigationGuardRunner: () => navigationGuardMock.runner,
}))

class StubWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  readyState = StubWebSocket.OPEN
  constructor(public url: string) {}
  send() {}
  close() {}
  addEventListener() {}
  removeEventListener() {}
}

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: new Headers({ 'content-type': 'application/json' }),
    json: async () => body,
    text: async () => JSON.stringify(body),
    blob: async () => new Blob([JSON.stringify(body)]),
  } as unknown as Response
}

// Minimal, shape-correct payloads for the endpoints the shell touches on mount /
// navigation. Anything else resolves to {} (callers all guard with `|| []` etc).
function payloadForUrl(url: string): unknown {
  if (url.includes('/system/version')) {
    return {
      version: 'dev',
      display_version: 'dev',
      commit: 'abc1234',
      build_time: '2026-01-01T00:00:00Z',
      environment: 'local',
      go_version: 'go1.26',
    }
  }
  if (url.includes('/system/services')) return { services: [] }
  if (url.includes('/desktop/status')) return { desktop_mode: false }
  if (url.includes('/auth/effective-access')) return { resources: [] }
  if (url.includes('/domains')) return { domains: [], count: 0 }
  if (url.includes('/projects') && url.includes('/releases')) return { releases: [], count: 0 }
  if (url.includes('/projects')) return { projects: [], count: 0 }
  if (url.includes('/applications')) return { applications: [], count: 0 }
  if (url.includes('/scenarios')) return { scenarios: [], count: 0 }
  if (url.includes('/tests')) return { tests: [], count: 0 }
  return {}
}

async function flush() {
  // Let provider effects (refreshProjects/refreshApplications/refreshDomains/…)
  // and the lazy page chunks (React.lazy dynamic imports) resolve and re-render.
  await act(async () => {
    for (let i = 0; i < 6; i += 1) {
      await new Promise((resolve) => setTimeout(resolve, 0))
      for (let j = 0; j < 4; j += 1) {
        await Promise.resolve()
      }
    }
  })
}

describe('AppShell hook-order stability across navigation', () => {
  let container: HTMLDivElement
  let root: Root
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>
  const hookOrderErrors: string[] = []
  const animationGlobals = globalThis as typeof globalThis & {
    requestAnimationFrame?: (cb: FrameRequestCallback) => number
    cancelAnimationFrame?: (handle: number) => void
  }
  const originalWebSocket = globalThis.WebSocket
  const originalRaf = animationGlobals.requestAnimationFrame
  const originalCancelRaf = animationGlobals.cancelAnimationFrame

  function currentPage(): string {
    const cls = container.querySelector('.main-content')?.className || ''
    return cls.split(/\s+/).find((token) => token.startsWith('page-')) || ''
  }

  async function clickNav(label: string) {
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('.sidebar-nav button')).find(
      (el) => el.textContent?.trim() === label,
    )
    expect(button, `expected a sidebar nav button labelled "${label}"`).toBeTruthy()
    await act(async () => {
      button!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()
  }

  beforeEach(() => {
    hookOrderErrors.length = 0
    navigationGuardMock.state.dirty = false
    navigationGuardMock.state.pending = null
    window.localStorage.clear()
    window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    globalThis.WebSocket = StubWebSocket as unknown as typeof WebSocket

    // jsdom lacks requestAnimationFrame/cancelAnimationFrame, which several pages
    // use on mount/unmount; without them the navigation re-render throws and the
    // page never switches. Map them onto timers for the duration of the test.
    if (typeof animationGlobals.requestAnimationFrame !== 'function') {
      animationGlobals.requestAnimationFrame = (cb: FrameRequestCallback) =>
        setTimeout(() => cb(0), 0) as unknown as number
    }
    if (typeof animationGlobals.cancelAnimationFrame !== 'function') {
      animationGlobals.cancelAnimationFrame = (handle: number) =>
        clearTimeout(handle as unknown as ReturnType<typeof setTimeout>)
    }

    vi.stubGlobal('fetch', vi.fn(async (input: unknown) => {
      const url = typeof input === 'string' ? input : String((input as { url?: string })?.url ?? input)
      return jsonResponse(payloadForUrl(url))
    }))

    // Capture (without suppressing real signal) React's dev-mode hook-order
    // warning, which is emitted via console.error before the invariant throws.
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
      const message = args.map(String).join(' ')
      if (/order of Hooks|Rendered more hooks|Rendered fewer hooks/i.test(message)) {
        hookOrderErrors.push(message)
      }
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    consoleErrorSpy.mockRestore()
    vi.unstubAllGlobals()
    globalThis.WebSocket = originalWebSocket
    animationGlobals.requestAnimationFrame = originalRaf
    animationGlobals.cancelAnimationFrame = originalCancelRaf
    window.localStorage.clear()
    window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
    vi.clearAllMocks()
    navigationGuardMock.state.dirty = false
    navigationGuardMock.state.pending = null
  })

  it('does not change hook order navigating between top-level pages via the left nav', async () => {
    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    expect(currentPage()).toBe('page-dashboard')

    // Reproduce the reported transition (Dashboard <-> Scenarios, the page that
    // hosts the Execution Plans view) and loop it several times in both
    // directions. AppShell contributes an identical hook run for every page, so a
    // real divergence at hook #64 fires on any of these re-renders; the route is
    // kept to pages that render with empty backend data so the assertion targets
    // hook order, not page-specific data wiring.
    const route: Array<[string, string]> = [
      ['Scenarios', 'page-tests'],
      ['Dashboard', 'page-dashboard'],
      ['Scenarios', 'page-tests'],
      ['New Test', 'page-new-test'],
      ['Dashboard', 'page-dashboard'],
    ]

    for (let lap = 0; lap < 3; lap += 1) {
      for (const [label, expectedPage] of route) {
        await clickNav(label)
        expect(currentPage(), `after clicking "${label}"`).toBe(expectedPage)
        // Fail fast at the exact transition that diverged, not just at the end.
        expect(hookOrderErrors, `hook-order warning after navigating to ${label}`).toEqual([])
      }
    }

    expect(hookOrderErrors).toEqual([])
  })

  it('applies one history-index transition when browsers emit popstate followed by hashchange', async () => {
    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    expect(readAppHistoryIndex(window.history.state)).toBe(0)
    await clickNav('Scenarios')
    expect(currentPage()).toBe('page-tests')
    expect(readAppHistoryIndex(window.history.state)).toBe(1)
    expect(container.querySelector('main h1')?.textContent).toContain('Scenario Library')
    expect(document.title).toBe('Scenario Library | APEX Load')
    expect(document.activeElement).toBe(container.querySelector('main'))

    const oldURL = window.location.href
    const dashboardState = withAppHistoryIndex(window.history.state, 0)
    window.history.replaceState(dashboardState, '', '#/dashboard')
    const newURL = window.location.href
    await act(async () => {
      window.dispatchEvent(new PopStateEvent('popstate', { state: dashboardState }))
      window.dispatchEvent(new HashChangeEvent('hashchange', { oldURL, newURL }))
    })
    await flush()

    expect(currentPage()).toBe('page-dashboard')
    expect(window.location.hash).toBe('#/dashboard')
    expect(readAppHistoryIndex(window.history.state)).toBe(0)
  })

  it('restores an indexed browser Back before confirmation without creating duplicate entries', async () => {
    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()
    await clickNav('Scenarios')
    expect(readAppHistoryIndex(window.history.state)).toBe(1)
    const historyLength = window.history.length

    navigationGuardMock.state.dirty = true
    await act(async () => {
      window.history.back()
      await new Promise(resolve => setTimeout(resolve, 20))
    })
    await flush()

    expect(currentPage()).toBe('page-tests')
    expect(window.location.hash).toBe('#/tests')
    expect(readAppHistoryIndex(window.history.state)).toBe(1)
    expect(window.history.length).toBe(historyLength)
    expect(navigationGuardMock.state.pending).not.toBeNull()

    // Cancel leaves the restored origin as the current entry.
    navigationGuardMock.state.pending = null
    expect(window.history.length).toBe(historyLength)

    // A second Back followed by discard/confirmation traverses to the existing
    // target entry; it does not replace or push a duplicate target.
    await act(async () => {
      window.history.back()
      await new Promise(resolve => setTimeout(resolve, 20))
    })
    await flush()
    expect(navigationGuardMock.state.pending).not.toBeNull()

    navigationGuardMock.state.dirty = false
    const proceed = navigationGuardMock.state.pending as (() => void) | null
    navigationGuardMock.state.pending = null
    await act(async () => {
      proceed?.()
      await new Promise(resolve => setTimeout(resolve, 20))
    })
    await flush()

    expect(currentPage()).toBe('page-dashboard')
    expect(window.location.hash).toBe('#/dashboard')
    expect(readAppHistoryIndex(window.history.state)).toBe(0)
    expect(window.history.length).toBe(historyLength)
  })
})
