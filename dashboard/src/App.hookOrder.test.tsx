// @vitest-environment jsdom

import { act, type ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import App from './App'
import { readAppHistoryIndex, withAppHistoryIndex } from './appRouting'
import type { WailsDesktopBridge } from './desktop/desktopBridge'
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

vi.mock('./components/TestExecution', async () => {
  const { useEffect, useState } = await import('react')
  const tabs = [
    ['overview', 'Overview'],
    ['control', 'Control'],
    ['graphs', 'Graphs'],
    ['diagnostics', 'Diagnostics'],
    ['archive', 'Archive'],
    ['action-summary', 'Action Summary'],
    ['load-impact', 'Load Impact'],
    ['transaction-drilldown', 'Transaction Drill-Down'],
  ] as const

  function persistedTab() {
    try {
      const profiles = JSON.parse(window.localStorage.getItem('apexload.execution.workspaceProfiles.v1') || '[]') as Array<{ activeTabId?: string }>
      return profiles.find(profile => profile.activeTabId)?.activeTabId
    } catch {
      return undefined
    }
  }

  function MockTestExecution({
    initialWorkspaceTabId,
    onBack,
    onWorkspaceTabChange,
  }: {
    initialWorkspaceTabId?: string
    onBack: () => void
    onWorkspaceTabChange?: (tabId: typeof tabs[number][0], options: { replace: boolean }) => void
  }) {
    const desiredTab = initialWorkspaceTabId || persistedTab() || 'overview'
    const [activeTab, setActiveTab] = useState(desiredTab)

    useEffect(() => {
      setActiveTab(desiredTab)
      onWorkspaceTabChange?.(desiredTab as typeof tabs[number][0], { replace: true })
    }, [desiredTab, onWorkspaceTabChange])

    return (
      <section aria-label="Mock test execution">
        <button type="button" data-testid="mock-execution-back" onClick={onBack}>Back</button>
        <div role="tablist" aria-label="Test execution workspace tabs">
          {tabs.map(([id, label]) => (
            <button
              key={id}
              type="button"
              role="tab"
              aria-selected={activeTab === id}
              onClick={() => {
                setActiveTab(id)
                onWorkspaceTabChange?.(id, { replace: false })
              }}
            >
              {label}
            </button>
          ))}
        </div>
      </section>
    )
  }

  return {
    default: MockTestExecution,
  }
})

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
  useNavigationGuard: () => undefined,
  useNavigationGuardRunner: () => navigationGuardMock.runner,
}))

vi.mock('./hooks/NavigationGuardRoot', () => ({
  NavigationGuardProvider: ({ children }: { children: ReactNode }) => children,
}))

class StubWebSocket {
  static instances: StubWebSocket[] = []
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  readyState = StubWebSocket.OPEN
  constructor(public url: string) {
    StubWebSocket.instances.push(this)
  }
  emit(message: unknown) {
    this.onmessage?.({ data: JSON.stringify(message) })
  }
  send() {}
  close() {}
  addEventListener() {}
  removeEventListener() {}
}

type DesktopWindow = typeof window & {
  go?: { main?: { App?: WailsDesktopBridge } }
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

let scopeFixturesEnabled = false
let domainCatalogFails = false

const scopeDomains = [
  { id: 'domain-a', name: 'Domain A', created_at: '', updated_at: '' },
  { id: 'domain-b', name: 'Domain B', created_at: '', updated_at: '' },
]
const scopeProjects = [
  { id: 'project-a', domain_id: 'domain-a', name: 'Project A', created_at: '', updated_at: '' },
  { id: 'project-b', domain_id: 'domain-a', name: 'Project B', created_at: '', updated_at: '' },
  { id: 'project-c', domain_id: 'domain-b', name: 'Project C', created_at: '', updated_at: '' },
  { id: 'project-orphan', domain_id: 'domain-missing', name: 'Orphan Project', created_at: '', updated_at: '' },
]
const projectAWorkspaceQuery = 'projectId=project-a&applicationId=project-a-app-a&releaseId=project-a-app-a-release-a'
const projectBWorkspaceQuery = 'projectId=project-b&applicationId=project-b-app-b&releaseId=project-b-app-b-release-b'

// Minimal, shape-correct payloads for the endpoints the shell touches on mount /
// navigation. Anything else resolves to {} (callers all guard with `|| []` etc).
function payloadForUrl(url: string): unknown {
  if (scopeFixturesEnabled && url.includes('/domains')) {
    return { domains: scopeDomains, count: scopeDomains.length }
  }
  if (scopeFixturesEnabled && /\/applications\?project_id=/.test(url)) {
    const projectID = new URL(url, window.location.origin).searchParams.get('project_id') || 'project-a'
    return {
      applications: [
        { id: `${projectID}-app-a`, project_id: projectID, name: 'Application A', created_at: '', updated_at: '' },
        { id: `${projectID}-app-b`, project_id: projectID, name: 'Application B', created_at: '', updated_at: '' },
      ],
      count: 2,
    }
  }
  if (scopeFixturesEnabled && /\/applications\/[^/]+\/releases/.test(url)) {
    const applicationID = url.match(/\/applications\/([^/]+)\/releases/)?.[1] || ''
    return {
      releases: [
        { id: `${applicationID}-release-a`, application_id: applicationID, name: 'Release A', created_at: '', updated_at: '' },
        { id: `${applicationID}-release-b`, application_id: applicationID, name: 'Release B', created_at: '', updated_at: '' },
      ],
      count: 2,
    }
  }
  if (scopeFixturesEnabled && url.includes('/projects')) {
    return { projects: scopeProjects, count: scopeProjects.length }
  }
  if (scopeFixturesEnabled && url.includes('/scripts/script-b')) {
    return {
      id: 'script-b',
      name: 'Script B',
      protocol: 'http',
      version: '1',
      project_id: 'project-b',
      created_at: '',
      script: { name: 'Script B', protocol: 'http', actions: [] },
    }
  }
  if (scopeFixturesEnabled && url.includes('/scenarios/scenario-b')) {
    return {
      id: 'scenario-b',
      name: 'Scenario B',
      project_id: 'project-b',
      duration: '1m',
      scripts: [],
      created_at: '',
      updated_at: '',
      script_count: 0,
    }
  }
  if (scopeFixturesEnabled && url.includes('/tests/run-b')) {
    return {
      id: 'run-b',
      name: 'Run B',
      project_id: 'project-b',
      application_id: 'project-b-app-b',
      release_id: 'project-b-app-b-release-b',
      status: 'COMPLETED',
      groups: [],
      total_vusers: 0,
      duration: '1m',
      created_at: '',
    }
  }
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
    StubWebSocket.instances = []
    hookOrderErrors.length = 0
    navigationGuardMock.state.dirty = false
    navigationGuardMock.state.pending = null
    scopeFixturesEnabled = false
    domainCatalogFails = false
    window.localStorage.clear()
    delete (window as DesktopWindow).go
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
      if (domainCatalogFails && url.includes('/domains')) {
        return {
          ...jsonResponse({ error: 'domain catalog unavailable' }),
          ok: false,
          status: 503,
          statusText: 'Service Unavailable',
        } as Response
      }
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
    delete (window as DesktopWindow).go
    window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
    vi.clearAllMocks()
    navigationGuardMock.state.dirty = false
    navigationGuardMock.state.pending = null
    scopeFixturesEnabled = false
    domainCatalogFails = false
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

  it('guards every sidebar workspace scope change before applying it', async () => {
    scopeFixturesEnabled = true
    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const selector = (prefix: string) => container.querySelector<HTMLButtonElement>(`button[aria-label^="${prefix}"]`)
    const choose = async (selectorPrefix: string, option: string) => {
      const trigger = selector(selectorPrefix)
      expect(trigger).toBeTruthy()
      await act(async () => trigger?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      const choices = Array.from(container.querySelectorAll<HTMLButtonElement>('.project-menu-item'))
      const choice = choices.find(button => button.textContent?.trim().endsWith(option))
      expect(choice, `expected ${option}; available options: ${choices.map(button => button.textContent?.trim()).join(', ')}`).toBeTruthy()
      navigationGuardMock.state.dirty = true
      await act(async () => choice?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      expect(navigationGuardMock.state.pending).not.toBeNull()
    }
    const discard = async () => {
      const pending = navigationGuardMock.state.pending
      navigationGuardMock.state.pending = null
      navigationGuardMock.state.dirty = false
      await act(async () => pending?.())
      await flush()
    }

    expect(selector('Select project')?.getAttribute('aria-label')).toContain('Project A')
    await choose('Select project', 'Project B')
    expect(selector('Select project')?.getAttribute('aria-label')).toContain('Project A')
    await discard()
    expect(selector('Select project')?.getAttribute('aria-label')).toContain('Project B')

    await choose('Select application and release', 'Release B')
    expect(selector('Select application and release')?.getAttribute('aria-label')).toContain('Release A')
    await discard()
    expect(selector('Select application and release')?.getAttribute('aria-label')).toContain('Release B')

    await choose('Select application and release', 'Application B')
    expect(selector('Select application and release')?.getAttribute('aria-label')).toContain('Application A')
    await discard()
    expect(selector('Select application and release')?.getAttribute('aria-label')).toContain('Application B')

    await choose('Select domain', 'Domain B')
    expect(selector('Select domain')?.getAttribute('aria-label')).toContain('Domain A')
    await discard()
    expect(selector('Select domain')?.getAttribute('aria-label')).toContain('Domain B')
  })

  it('corrects a stale project-scoped scenario link before mounting its asset page', async () => {
    scopeFixturesEnabled = true
    window.history.replaceState({}, '', '#/new-test?scenarioId=scenario-b&projectId=project-a')

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const projectSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select project"]')
    expect(projectSelector?.getAttribute('aria-label')).toContain('Project B')
    expect(window.location.hash).toContain('projectId=project-b')
    expect(window.location.hash).not.toContain('projectId=project-a')
    expect(container.textContent).not.toContain('Project project-b is unavailable')

    const scenarioRequests = vi.mocked(fetch).mock.calls
      .map(([input]) => typeof input === 'string' ? input : String((input as { url?: string })?.url ?? input))
      .filter(url => url.includes('/scenarios'))
    expect(scenarioRequests.some(url => url.includes('project_id=project-b'))).toBe(true)
    expect(scenarioRequests.some(url => url.includes('project_id=project-a'))).toBe(false)
  })

  it('corrects a stale project-scoped script link before mounting the editor', async () => {
    scopeFixturesEnabled = true
    window.history.replaceState({}, '', '#/scripts?mode=edit&scriptId=script-b&projectId=project-a')

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const projectSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select project"]')
    expect(projectSelector?.getAttribute('aria-label')).toContain('Project B')
    expect(window.location.hash).toContain('projectId=project-b')
    expect(window.location.hash).not.toContain('projectId=project-a')
    const scriptRequests = vi.mocked(fetch).mock.calls
      .map(([input]) => typeof input === 'string' ? input : String((input as { url?: string })?.url ?? input))
      .filter(url => url.includes('/scripts'))
    expect(scriptRequests.some(url => url.includes('/scripts/script-b'))).toBe(true)
    expect(scriptRequests.some(url => url.includes('project_id=project-a'))).toBe(false)
  })

  it('reports an unavailable owning domain instead of leaving a scoped route pending forever', async () => {
    scopeFixturesEnabled = true
    window.history.replaceState({}, '', '#/new-test?projectId=project-orphan')

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('owning domain domain-missing')
    expect(alert?.textContent).toContain('project project-orphan')
    expect(container.querySelector('[role="status"]')?.textContent).not.toContain('Loading project workspace')
  })

  it('reconciles a run deep link to its owning application and release before navigating to Results', async () => {
    scopeFixturesEnabled = true
    window.localStorage.setItem('apexload.activeApplicationId', 'project-b-app-a')
    window.localStorage.setItem('apexload.activeReleaseId', 'project-b-app-a-release-a')
    window.history.replaceState({}, '', '#/test/run-b?projectId=project-b')

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const projectSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select project"]')
    const applicationSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select application and release"]')
    expect(projectSelector?.getAttribute('aria-label')).toContain('Project B')
    expect(applicationSelector?.getAttribute('aria-label')).toContain('Application B')
    expect(applicationSelector?.getAttribute('aria-label')).toContain('Release B')
    await clickNav('Results')

    expect(currentPage()).toBe('page-results')
    expect(applicationSelector?.getAttribute('aria-label')).toContain('Application B')
    expect(applicationSelector?.getAttribute('aria-label')).toContain('Release B')
  })

  it('restores every workspace level from a fully scoped generic deep link before mounting the page', async () => {
    scopeFixturesEnabled = true
    window.localStorage.setItem('apexload.activeProjectId', 'project-a')
    window.localStorage.setItem('apexload.activeApplicationId', 'project-a-app-a')
    window.localStorage.setItem('apexload.activeReleaseId', 'project-a-app-a-release-a')
    window.history.replaceState({}, '', `#/results?${projectBWorkspaceQuery}`)

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const projectSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select project"]')
    const applicationSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select application and release"]')
    expect(projectSelector?.getAttribute('aria-label')).toContain('Project B')
    expect(applicationSelector?.getAttribute('aria-label')).toContain('Application B')
    expect(applicationSelector?.getAttribute('aria-label')).toContain('Release B')
    expect(window.location.hash).toBe(`#/results?${projectBWorkspaceQuery}`)
  })

  it('restores the complete generic workspace when browser Back changes only route scope', async () => {
    scopeFixturesEnabled = true
    window.localStorage.setItem('apexload.activeProjectId', 'project-a')
    window.localStorage.setItem('apexload.activeApplicationId', 'project-a-app-a')
    window.localStorage.setItem('apexload.activeReleaseId', 'project-a-app-a-release-a')
    window.history.replaceState(
      withAppHistoryIndex({}, 0),
      '',
      `#/results?${projectAWorkspaceQuery}`,
    )
    window.history.pushState(
      withAppHistoryIndex({}, 1),
      '',
      `#/results?${projectBWorkspaceQuery}`,
    )

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const projectSelector = () => container.querySelector<HTMLButtonElement>('button[aria-label^="Select project"]')
    const applicationSelector = () => container.querySelector<HTMLButtonElement>('button[aria-label^="Select application and release"]')
    expect(projectSelector()?.getAttribute('aria-label')).toContain('Project B')
    expect(applicationSelector()?.getAttribute('aria-label')).toContain('Application B')
    expect(applicationSelector()?.getAttribute('aria-label')).toContain('Release B')

    await act(async () => {
      window.history.back()
      await new Promise(resolve => setTimeout(resolve, 20))
    })
    await flush()

    expect(window.location.hash).toBe(`#/results?${projectAWorkspaceQuery}`)
    expect(readAppHistoryIndex(window.history.state)).toBe(0)
    expect(projectSelector()?.getAttribute('aria-label')).toContain('Project A')
    expect(applicationSelector()?.getAttribute('aria-label')).toContain('Application A')
    expect(applicationSelector()?.getAttribute('aria-label')).toContain('Release A')
  })

  it('preserves a legacy unscoped generic link while using the stored workspace', async () => {
    scopeFixturesEnabled = true
    window.localStorage.setItem('apexload.activeProjectId', 'project-b')
    window.localStorage.setItem('apexload.activeApplicationId', 'project-b-app-b')
    window.localStorage.setItem('apexload.activeReleaseId', 'project-b-app-b-release-b')
    window.history.replaceState({}, '', '#/results')

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const projectSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select project"]')
    const applicationSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select application and release"]')
    expect(projectSelector?.getAttribute('aria-label')).toContain('Project B')
    expect(applicationSelector?.getAttribute('aria-label')).toContain('Application B')
    expect(applicationSelector?.getAttribute('aria-label')).toContain('Release B')
    expect(window.location.hash).toBe('#/results')
  })

  it('blocks a generic page whose child scope does not belong to its requested project', async () => {
    scopeFixturesEnabled = true
    const staleScope = 'projectId=project-b&applicationId=project-a-app-b&releaseId=project-a-app-b-release-b'
    window.history.replaceState({}, '', `#/results?${staleScope}`)

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const projectSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select project"]')
    const alerts = Array.from(container.querySelectorAll('[role="alert"]'))
    expect(projectSelector?.getAttribute('aria-label')).toContain('Project B')
    expect(alerts.some(alert => alert.textContent?.includes(
      'Application project-a-app-b is unavailable in project project-b.',
    ))).toBe(true)
    expect(container.querySelector('main h2')?.textContent || '').not.toContain('Results Library')
    expect(window.location.hash).toBe(`#/results?${staleScope}`)
  })

  it('surfaces a domain catalog failure without inventing a client-only default domain', async () => {
    scopeFixturesEnabled = true
    domainCatalogFails = true

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const alerts = Array.from(container.querySelectorAll('[role="alert"]'))
    expect(alerts.some(alert => alert.textContent?.includes('Unable to load domains'))).toBe(true)
    const domainSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select domain"]')
    expect(domainSelector?.getAttribute('aria-label') || '').not.toContain('Default Domain')
    expect(window.localStorage.getItem('apexload.activeDomainId')).not.toBe('default')
  })

  it.each([
    ['script editor', '#/scripts?mode=edit&scriptId=script-b', false],
    ['scenario launch', '#/new-test?scenarioId=scenario-b', false],
    ['test workspace', '#/test/run-b', false],
    ['command center', '#/command-center/run-b', false],
    ['run report', '#/report/run-b', true],
  ])('resolves the owning project before mounting a legacy unscoped %s bookmark', async (_label, hash, enableReport) => {
    scopeFixturesEnabled = true
    if (enableReport) window.localStorage.setItem('apexload.featureFlags.runReport', 'true')
    window.history.replaceState({}, '', hash)

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const projectSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select project"]')
    expect(projectSelector?.getAttribute('aria-label')).toContain('Project B')
    expect(window.location.hash).toContain('projectId=project-b')
    expect(container.textContent).not.toContain('does not include project scope')
    if (hash.includes('run-b')) {
      const applicationSelector = container.querySelector<HTMLButtonElement>('button[aria-label^="Select application and release"]')
      expect(applicationSelector?.getAttribute('aria-label')).toContain('Application B')
      expect(applicationSelector?.getAttribute('aria-label')).toContain('Release B')
    }
  })

  it('shows a recovery action when a legacy bookmark owner cannot be determined', async () => {
    scopeFixturesEnabled = true
    window.history.replaceState({}, '', '#/scripts?mode=edit&scriptId=missing-script')

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('does not include project scope')
    const recovery = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Open Script Library')
    expect(recovery).toBeTruthy()

    await act(async () => recovery?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flush()
    expect(window.location.hash).toBe(`#/scripts?${projectAWorkspaceQuery}`)
  })

  it('refreshes app-wide effective access when the server reports an access review change', async () => {
    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const accessRequestCount = () => vi.mocked(fetch).mock.calls.filter(([input]) => {
      const url = typeof input === 'string' ? input : String((input as { url?: string })?.url ?? input)
      return url.includes('/auth/effective-access')
    }).length
    const beforeEvent = accessRequestCount()
    expect(beforeEvent).toBeGreaterThan(0)

    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'))
    })
    const socket = StubWebSocket.instances[StubWebSocket.instances.length - 1]
    expect(socket).toBeDefined()
    await act(async () => {
      socket?.emit({ type: 'access_review_changed' })
    })
    await flush()

    expect(accessRequestCount()).toBeGreaterThan(beforeEvent)
  })

  it('replays an early Help browser action and surfaces a native launch failure', async () => {
    const openInBrowser = vi.fn().mockRejectedValue(new Error('System browser launch was blocked'))
    const rendererReady = vi.fn<(session: string) => Promise<string>>().mockResolvedValue(JSON.stringify({
      delivery_id: '1',
      events: [{ name: 'menu:open-browser' }],
    }))
    const rendererReadyAck = vi.fn<(session: string, deliveryId: string) => Promise<string>>().mockResolvedValue(JSON.stringify({
      events: [],
    }))
    ;(window as DesktopWindow).go = {
      main: {
        App: { OpenInBrowser: openInBrowser, RendererReady: rendererReady, RendererReadyAck: rendererReadyAck },
      },
    }

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    expect(rendererReady).toHaveBeenCalledTimes(1)
    expect(rendererReadyAck).toHaveBeenCalledWith(rendererReady.mock.calls[0]?.[0], '1')
    expect(openInBrowser).toHaveBeenCalledWith('admin', '')
    const alerts = Array.from(container.querySelectorAll('[role="alert"]'))
    expect(alerts.some(alert => alert.textContent?.includes('System browser launch was blocked'))).toBe(true)
  })

  it('keeps execution tab routes, sidebar ownership, and browser history in sync', async () => {
    scopeFixturesEnabled = true
    window.localStorage.setItem('apexload.activeApplicationId', 'project-b-app-b')
    window.localStorage.setItem('apexload.activeReleaseId', 'project-b-app-b-release-b')
    window.history.replaceState(
      withAppHistoryIndex({}, 0),
      '',
      '#/test/run-b?tab=archive&projectId=project-b',
    )

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const activeNavLabel = () => container.querySelector('.sidebar-nav [aria-current="page"]')?.textContent?.trim()
    const workspaceTab = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tab"]'))
      .find(button => button.textContent?.includes(label))

    expect(workspaceTab('Archive')?.getAttribute('aria-selected')).toBe('true')
    expect(activeNavLabel()).toContain('Results')
    expect(readAppHistoryIndex(window.history.state)).toBe(0)

    await act(async () => {
      workspaceTab('Overview')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(window.location.hash).toBe(`#/test/run-b?tab=overview&${projectBWorkspaceQuery}`)
    expect(activeNavLabel()).toContain('Command Center')
    expect(readAppHistoryIndex(window.history.state)).toBe(1)

    await act(async () => {
      window.history.back()
      await new Promise(resolve => setTimeout(resolve, 20))
    })
    await flush()

    expect(window.location.hash).toBe(`#/test/run-b?tab=archive&${projectBWorkspaceQuery}`)
    expect(workspaceTab('Archive')?.getAttribute('aria-selected')).toBe('true')
    expect(activeNavLabel()).toContain('Results')
    expect(readAppHistoryIndex(window.history.state)).toBe(0)
  })

  it('replaces the current route when a persisted execution tab supplies the initial destination', async () => {
    scopeFixturesEnabled = true
    window.localStorage.setItem('apexload.execution.workspaceProfiles.v1', JSON.stringify([{
      id: 'default',
      name: 'Default Execution Layout',
      activeTabId: 'load-impact',
      tabs: [{ id: 'load-impact', label: 'Load Impact', cardIds: ['load-impact'] }],
      unassignedCardIds: [],
    }]))
    window.history.replaceState(
      withAppHistoryIndex({}, 0),
      '',
      '#/test/run-b?projectId=project-b',
    )

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const selectedTab = container.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]')
    const activeNav = container.querySelector('.sidebar-nav [aria-current="page"]')
    expect(selectedTab?.textContent).toContain('Load Impact')
    expect(window.location.hash).toBe(`#/test/run-b?tab=load-impact&${projectBWorkspaceQuery}`)
    expect(activeNav?.textContent).toContain('Results')
    expect(readAppHistoryIndex(window.history.state)).toBe(0)
  })

  it.each([
    ['archive-owned', 'action-summary', `#/results?${projectBWorkspaceQuery}`],
    ['live-owned', 'diagnostics', `#/command-center?${projectBWorkspaceQuery}`],
  ])('uses the owning top-level destination for first-entry Back from an %s test view', async (_label, tab, expectedHash) => {
    scopeFixturesEnabled = true
    window.history.replaceState(
      withAppHistoryIndex({}, 0),
      '',
      `#/test/run-b?tab=${tab}&projectId=project-b`,
    )

    await act(async () => {
      root.render(<ApiProvider><App /></ApiProvider>)
    })
    await flush()

    const backButton = container.querySelector<HTMLButtonElement>('[data-testid="mock-execution-back"]')
    expect(backButton).toBeTruthy()

    await act(async () => {
      backButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()

    expect(window.location.hash).toBe(expectedHash)
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
