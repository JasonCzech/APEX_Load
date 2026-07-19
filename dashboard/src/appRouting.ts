import { API_GUIDE_DEFAULT_PAGE, isApiGuideContentPageID, type ApiGuideContentPageID } from './apiGuideNavigation'
import type { NavPage } from './components/Sidebar'

export type AppView =
  | { page: Exclude<NavPage, 'command-center' | 'new-test' | 'scripts' | 'tests' | 'docs' | 'monitoring'> }
  | { page: 'monitoring'; category?: MonitoringCategoryID }
  | { page: 'docs'; guidePage?: ApiGuideContentPageID }
  | { page: 'command-center'; testId?: string }
  | { page: 'report'; testId: string; lens?: ReportLens; compare?: string }
  | { page: 'tests'; scenarioView?: 'library' | 'builder' | 'plans' }
  | { page: 'new-test'; scenarioId?: string; prefillStartLocal?: string }
  | { page: 'scripts'; mode?: 'list' | 'create' | 'edit' | 'import'; scriptId?: string }
  | { page: 'swagger' }
  | { page: 'repository-guide' }
  | { page: 'recorder-panel'; sessionId: string }
  | { page: 'test'; testId: string; tab?: TestWorkspaceTabID }

const NAV_PAGES: NavPage[] = [
  'dashboard',
  'command-center',
  'repository',
  'tests',
  'new-test',
  'scripts',
  'results',
  'monitoring',
  'fleet',
  'schedules',
  'docs',
  'access',
  'settings',
]

const SCRIPT_MODES = ['list', 'create', 'edit', 'import'] as const
const SCENARIO_VIEWS = ['library', 'builder', 'plans'] as const
const TEST_WORKSPACE_TABS = ['overview', 'control', 'graphs', 'diagnostics', 'archive', 'action-summary', 'load-impact', 'transaction-drilldown'] as const
const MONITORING_CATEGORIES = ['overview', 'telemetry'] as const
export const REPORT_LENSES = ['executive', 'engineer', 'audit'] as const

export type TestWorkspaceTabID = typeof TEST_WORKSPACE_TABS[number]
export type MonitoringCategoryID = typeof MONITORING_CATEGORIES[number]
export type ReportLens = typeof REPORT_LENSES[number]

export const APP_HISTORY_INDEX_KEY = '__apexloadHistoryIndex'

export function readAppHistoryIndex(state: unknown): number | null {
  if (!state || typeof state !== 'object') return null
  const index = (state as Record<string, unknown>)[APP_HISTORY_INDEX_KEY]
  return typeof index === 'number' && Number.isInteger(index) && index >= 0 ? index : null
}

export function withAppHistoryIndex(state: unknown, index: number): Record<string, unknown> {
  const existing = state && typeof state === 'object' && !Array.isArray(state)
    ? state as Record<string, unknown>
    : {}
  return {
    ...existing,
    [APP_HISTORY_INDEX_KEY]: Math.max(0, Math.trunc(index)),
  }
}

function cleanSegment(value: string | undefined) {
  if (!value) return ''
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

function isNavPage(value: string): value is NavPage {
  return NAV_PAGES.includes(value as NavPage)
}

function isScriptMode(value: string | null): value is NonNullable<Extract<AppView, { page: 'scripts' }>['mode']> {
  return SCRIPT_MODES.includes(value as NonNullable<Extract<AppView, { page: 'scripts' }>['mode']>)
}

function isScenarioView(value: string | null): value is NonNullable<Extract<AppView, { page: 'tests' }>['scenarioView']> {
  return SCENARIO_VIEWS.includes(value as NonNullable<Extract<AppView, { page: 'tests' }>['scenarioView']>)
}

function isTestWorkspaceTab(value: string | null): value is TestWorkspaceTabID {
  return TEST_WORKSPACE_TABS.includes(value as TestWorkspaceTabID)
}

function isMonitoringCategory(value: string | null): value is MonitoringCategoryID {
  return MONITORING_CATEGORIES.includes(value as MonitoringCategoryID)
}

export function isReportLens(value: string | null | undefined): value is ReportLens {
  return REPORT_LENSES.includes(value as ReportLens)
}

export function parseAppViewFromHash(hash: string): AppView {
  const route = hash.replace(/^#/, '').replace(/^\/+/, '')
  const [pathPart, queryString = ''] = route.split('?')
  const segments = pathPart.split('/').filter(Boolean).map(cleanSegment)
  const page = segments[0] || 'dashboard'
  const params = new URLSearchParams(queryString)

  if (page === 'test' && segments[1]) {
    const tab = params.get('tab')
    return {
      page: 'test',
      testId: segments[1],
      ...(isTestWorkspaceTab(tab) ? { tab } : {}),
    }
  }

  if (page === 'new-test') {
    const scenarioId = params.get('scenarioId') || params.get('scenario') || undefined
    const prefillStartLocal = params.get('prefillStartLocal') || undefined
    return {
      page: 'new-test',
      ...(scenarioId ? { scenarioId } : {}),
      ...(prefillStartLocal ? { prefillStartLocal } : {}),
    }
  }

  if (page === 'tests') {
    const scenarioView = params.get('view')
    return {
      page: 'tests',
      ...(isScenarioView(scenarioView) ? { scenarioView } : {}),
    }
  }

  if (page === 'command-center') {
    const testId = segments[1] || params.get('testId') || undefined
    return testId ? { page: 'command-center', testId } : { page: 'command-center' }
  }

  if (page === 'report' && segments[1]) {
    const lens = params.get('lens')
    const compare = params.get('compare') || undefined
    return {
      page: 'report',
      testId: segments[1],
      ...(isReportLens(lens) ? { lens } : {}),
      ...(compare ? { compare } : {}),
    }
  }

  if (page === 'scripts') {
    const mode = params.get('mode')
    const scriptId = params.get('scriptId') || undefined
    return {
      page: 'scripts',
      ...(isScriptMode(mode) ? { mode } : {}),
      ...(scriptId ? { scriptId } : {}),
    }
  }

  if (page === 'docs') {
    const guidePage = params.get('guide')
    return {
      page: 'docs',
      ...(isApiGuideContentPageID(guidePage) ? { guidePage } : {}),
    }
  }

  if (page === 'monitoring') {
    const category = params.get('category')
    return {
      page: 'monitoring',
      ...(isMonitoringCategory(category) && category !== 'overview' ? { category } : {}),
    }
  }

  if (page === 'swagger') {
    return { page: 'swagger' }
  }

  if (page === 'repository-guide') {
    return { page: 'repository-guide' }
  }

  if (page === 'recorder-panel') {
    const sessionId = params.get('session') || undefined
    // The compact recorder panel cannot function without a session. Recover to
    // the script library instead of leaving the normal application shell blank.
    return sessionId ? { page: 'recorder-panel', sessionId } : { page: 'scripts' }
  }

  return isNavPage(page) ? { page } : { page: 'dashboard' }
}

const DESKTOP_DEEP_LINK_PAGES = new Set<string>([
  ...NAV_PAGES,
  'report',
  'test',
  'swagger',
  'repository-guide',
  'recorder-panel',
])
const DESKTOP_DEEP_LINK_COMMAND_HOSTS = new Set(['app', 'navigate', 'open'])

function normalizedDesktopRoute(candidate: string): string | null {
  const trimmed = candidate.trim()
  if (!trimmed || /^\w+:\/\//.test(trimmed)) return null
  const hash = trimmed.startsWith('#')
    ? trimmed
    : trimmed.startsWith('/')
      ? `#${trimmed}`
      : `#/${trimmed}`
  const route = hash.replace(/^#/, '').replace(/^\/+/, '')
  const first = cleanSegment(route.split(/[/?]/, 1)[0]).toLowerCase()
  return DESKTOP_DEEP_LINK_PAGES.has(first) ? hash.replace(/^#(?!\/)/, '#/') : null
}

// parseDesktopDeepLink accepts only the registered apexload: scheme and a
// small, explicit navigation grammar:
//   apexload://scripts?mode=create       (host/path form)
//   apexload:///test/run-1?tab=archive  (path form)
//   apexload://open#/results            (hash form)
//   apexload://open?route=%23%2Fresults (query form)
// Unknown commands/pages are rejected rather than silently navigating home.
export function parseDesktopDeepLink(raw: string): AppView | null {
  let url: URL
  try {
    url = new URL(raw.trim())
  } catch {
    return null
  }
  if (url.protocol.toLowerCase() !== 'apexload:' || url.username || url.password || url.port) return null

  const host = url.hostname.toLowerCase()
  let candidate = ''
  if (url.hash) {
    candidate = url.hash
  } else if (url.searchParams.has('route')) {
    if (host && !DESKTOP_DEEP_LINK_COMMAND_HOSTS.has(host)) return null
    candidate = url.searchParams.get('route') || ''
  } else if (DESKTOP_DEEP_LINK_COMMAND_HOSTS.has(host)) {
    candidate = `${url.pathname}${url.search}`
  } else {
    candidate = `/${host}${url.pathname}${url.search}`
  }

  const route = normalizedDesktopRoute(candidate)
  return route ? parseAppViewFromHash(route) : null
}

export function viewToLocationHash(view: AppView): string {
  if (view.page === 'test') {
    const params = new URLSearchParams()
    if (view.tab) params.set('tab', view.tab)
    const query = params.toString()
    return query ? `#/test/${encodeURIComponent(view.testId)}?${query}` : `#/test/${encodeURIComponent(view.testId)}`
  }

  if (view.page === 'new-test') {
    const params = new URLSearchParams()
    if (view.scenarioId) params.set('scenarioId', view.scenarioId)
    if (view.prefillStartLocal) params.set('prefillStartLocal', view.prefillStartLocal)
    const query = params.toString()
    return query ? `#/new-test?${query}` : '#/new-test'
  }

  if (view.page === 'tests') {
    const params = new URLSearchParams()
    if (view.scenarioView) params.set('view', view.scenarioView)
    const query = params.toString()
    return query ? `#/tests?${query}` : '#/tests'
  }

  if (view.page === 'command-center') {
    return view.testId ? `#/command-center/${encodeURIComponent(view.testId)}` : '#/command-center'
  }

  if (view.page === 'report') {
    const params = new URLSearchParams()
    if (view.lens) params.set('lens', view.lens)
    if (view.compare) params.set('compare', view.compare)
    const query = params.toString()
    return query ? `#/report/${encodeURIComponent(view.testId)}?${query}` : `#/report/${encodeURIComponent(view.testId)}`
  }

  if (view.page === 'scripts') {
    const params = new URLSearchParams()
    if (view.mode) params.set('mode', view.mode)
    if (view.scriptId) params.set('scriptId', view.scriptId)
    const query = params.toString()
    return query ? `#/scripts?${query}` : '#/scripts'
  }

  if (view.page === 'docs') {
    const params = new URLSearchParams()
    if (view.guidePage && view.guidePage !== API_GUIDE_DEFAULT_PAGE) params.set('guide', view.guidePage)
    const query = params.toString()
    return query ? `#/docs?${query}` : '#/docs'
  }

  if (view.page === 'monitoring') {
    const params = new URLSearchParams()
    if (view.category && view.category !== 'overview') params.set('category', view.category)
    const query = params.toString()
    return query ? `#/monitoring?${query}` : '#/monitoring'
  }

  if (view.page === 'swagger') {
    return '#/swagger'
  }

  if (view.page === 'repository-guide') {
    return '#/repository-guide'
  }

  if (view.page === 'recorder-panel') {
    return `#/recorder-panel?session=${encodeURIComponent(view.sessionId)}`
  }

  return `#/${view.page}`
}

export function normalizeReportViewForFeatureFlag(view: AppView, runReportEnabled: boolean): AppView {
  if (runReportEnabled || view.page !== 'report') return view
  return { page: 'test', testId: view.testId, tab: 'archive' }
}

interface GuardedRouteChangeOptions {
  currentView: AppView
  locationHash: string
  hasDirtyGuard: () => boolean
  runGuarded: (proceed: () => void) => void
  restoreHash: (hash: string) => void
  applyView: (view: AppView) => void
}

export function handleGuardedRouteChange({
  currentView,
  locationHash,
  hasDirtyGuard,
  runGuarded,
  restoreHash,
  applyView,
}: GuardedRouteChangeOptions) {
  const nextView = parseAppViewFromHash(locationHash)
  const nextHash = viewToLocationHash(nextView)
  const currentHash = viewToLocationHash(currentView)
  if (currentHash === nextHash) return

  const proceed = () => applyView(nextView)
  if (!hasDirtyGuard()) {
    proceed()
    return
  }

  if (locationHash !== currentHash) {
    restoreHash(currentHash)
  }
  runGuarded(proceed)
}
