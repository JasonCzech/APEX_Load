import { isAccessView, type AccessView } from './components/accessDestinations'
import { API_GUIDE_DEFAULT_PAGE, isApiGuideContentPageID, type ApiGuideContentPageID } from './apiGuideNavigation'
import type { NavPage } from './components/Sidebar'

export type AppWorkspaceScope = {
  projectId?: string
  applicationId?: string
  releaseId?: string
}

export type AppView = AppWorkspaceScope & (
  | { page: Exclude<NavPage, 'command-center' | 'new-test' | 'scripts' | 'tests' | 'docs' | 'monitoring' | 'settings' | 'access' | 'repository'> }
  | { page: 'settings' | 'access' | 'repository'; setting?: string; returnTo?: string; resourceId?: string; accessView?: AccessView }
  | { page: 'monitoring'; category?: MonitoringCategoryID }
  | { page: 'docs'; guidePage?: ApiGuideContentPageID; guideArticle?: string; guideSection?: string }
  | { page: 'command-center'; testId?: string }
  | { page: 'report'; testId: string; lens?: ReportLens; compare?: string; comparisonId?: string; section?: ReportSection; transaction?: string; population?: 'action' | 'transaction' | 'unknown' } & RootCauseRouteState
  | { page: 'tests'; scenarioView?: 'library' | 'builder' | 'plans' }
  | { page: 'new-test'; scenarioId?: string; prefillStartLocal?: string }
  | { page: 'scripts'; mode?: 'list' | 'create' | 'edit' | 'import'; scriptId?: string }
  | { page: 'swagger'; search?: string }
  | { page: 'repository-guide' }
  | { page: 'recorder-panel'; sessionId: string; folderId?: string }
  | { page: 'test'; testId: string; tab?: TestWorkspaceTabID }
)

const NAV_PAGES: NavPage[] = [
  'dashboard',
  'command-center',
  'repository',
  'tests',
  'new-test',
  'scripts',
  'results',
  'activity',
  'monitoring',
  'fleet',
  'schedules',
  'docs',
  'access',
  'settings',
]

const SCRIPT_MODES = ['list', 'create', 'edit', 'import'] as const
const SCENARIO_VIEWS = ['library', 'builder', 'plans'] as const
const TEST_WORKSPACE_TABS = ['overview', 'diagnostics', 'configuration', 'infrastructure', 'control', 'graphs', 'archive', 'action-summary', 'load-impact', 'transaction-drilldown'] as const
const MONITORING_CATEGORIES = ['overview', 'telemetry', 'alerts'] as const
// RootCauseRouteState is a root-cause investigation's shareable state: the
// analysis and its revision, the hypothesis being investigated and the time
// window in focus (two RFC 3339 times joined by "~").
export interface RootCauseRouteState {
  analysis?: string
  rev?: string
  hypothesis?: string
  window?: string
}

const ROOT_CAUSE_ID = /^[A-Za-z0-9._:-]{1,128}$/
const ROOT_CAUSE_WINDOW = /^[0-9T:.+Z-]{10,40}~[0-9T:.+Z-]{10,40}$/

export function rootCauseRouteState(params: URLSearchParams): RootCauseRouteState {
  const analysis = params.get('analysis') || ''
  const rev = params.get('rev') || ''
  const hypothesis = params.get('hypothesis') || ''
  const window = params.get('window') || ''
  return {
    ...(ROOT_CAUSE_ID.test(analysis) ? { analysis } : {}),
    ...(/^[0-9]{1,9}$/.test(rev) ? { rev } : {}),
    ...(ROOT_CAUSE_ID.test(hypothesis) ? { hypothesis } : {}),
    ...(ROOT_CAUSE_WINDOW.test(window) ? { window } : {}),
  }
}

export const REPORT_SECTIONS = ['summary', 'performance', 'comparison', 'diagnostics', 'evidence', 'execution'] as const
export type ReportSection = typeof REPORT_SECTIONS[number]
export const REPORT_LENSES = ['executive', 'engineer', 'audit'] as const

export type TestWorkspaceTabID = typeof TEST_WORKSPACE_TABS[number]
export type MonitoringCategoryID = typeof MONITORING_CATEGORIES[number]
export type ReportLens = typeof REPORT_LENSES[number]

const ARCHIVE_TEST_WORKSPACE_TABS = ['archive', 'action-summary', 'load-impact', 'transaction-drilldown'] as const satisfies readonly TestWorkspaceTabID[]

export const APP_HISTORY_INDEX_KEY = '__apexloadHistoryIndex'

export function isArchiveTestWorkspaceTab(value: string | null | undefined): value is typeof ARCHIVE_TEST_WORKSPACE_TABS[number] {
  return ARCHIVE_TEST_WORKSPACE_TABS.includes(value as typeof ARCHIVE_TEST_WORKSPACE_TABS[number])
}

export function navPageForAppView(view: AppView): NavPage {
  if (view.page === 'test') {
    return isArchiveTestWorkspaceTab(view.tab) ? 'results' : 'command-center'
  }
  if (view.page === 'report') return 'results'
  if (view.page === 'swagger' || view.page === 'repository-guide') return 'docs'
  if (view.page === 'recorder-panel') return 'scripts'
  return view.page
}

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

export function isTestWorkspaceTab(value: string | null): value is TestWorkspaceTabID {
  return TEST_WORKSPACE_TABS.includes(value as TestWorkspaceTabID)
}

function isMonitoringCategory(value: string | null): value is MonitoringCategoryID {
  return MONITORING_CATEGORIES.includes(value as MonitoringCategoryID)
}

export function isReportLens(value: string | null | undefined): value is ReportLens {
  return REPORT_LENSES.includes(value as ReportLens)
}

function workspaceScopeFromParams(params: URLSearchParams): AppWorkspaceScope {
  const projectId = params.get('projectId') || params.get('project') || undefined
  const applicationId = params.get('applicationId') || params.get('application') || undefined
  const releaseId = params.get('releaseId') || params.get('release') || undefined
  return {
    ...(projectId ? { projectId } : {}),
    ...(applicationId ? { applicationId } : {}),
    ...(releaseId ? { releaseId } : {}),
  }
}

function appendWorkspaceScope(params: URLSearchParams, view: AppView) {
  if (view.projectId) params.set('projectId', view.projectId)
  if (view.applicationId) params.set('applicationId', view.applicationId)
  if (view.releaseId) params.set('releaseId', view.releaseId)
}

export function parseAppViewFromHash(hash: string): AppView {
  const route = hash.replace(/^#/, '').replace(/^\/+/, '')
  const [pathPart, queryString = ''] = route.split('?')
  const segments = pathPart.split('/').filter(Boolean).map(cleanSegment)
  const page = segments[0] || 'dashboard'
  const params = new URLSearchParams(queryString)
  const workspaceScope = workspaceScopeFromParams(params)

  if (page === 'test' && segments[1]) {
    const tab = params.get('tab')
    return {
      page: 'test',
      testId: segments[1],
      ...(isTestWorkspaceTab(tab) ? { tab } : {}),
      ...workspaceScope,
    }
  }

  if (page === 'new-test') {
    const scenarioId = params.get('scenarioId') || params.get('scenario') || undefined
    const prefillStartLocal = params.get('prefillStartLocal') || undefined
    return {
      page: 'new-test',
      ...(scenarioId ? { scenarioId } : {}),
      ...(prefillStartLocal ? { prefillStartLocal } : {}),
      ...workspaceScope,
    }
  }

  if (page === 'tests') {
    const scenarioView = params.get('view')
    return {
      page: 'tests',
      ...(isScenarioView(scenarioView) ? { scenarioView } : {}),
      ...workspaceScope,
    }
  }

  if (page === 'command-center') {
    const testId = segments[1] || params.get('testId') || undefined
    return testId
      ? { page: 'command-center', testId, ...workspaceScope }
      : { page: 'command-center', ...workspaceScope }
  }

  if (page === 'report' && segments[1]) {
    const section = params.get('section') as ReportSection | null
    const lens = params.get('lens')
    const compare = params.get('compare') || undefined
    const transaction = params.get('transaction') || undefined
    const population = params.get('population')
    return {
      page: 'report',
      testId: segments[1],
      ...rootCauseRouteState(params),
      ...(isReportLens(lens) ? { lens } : {}),
      ...(section && REPORT_SECTIONS.includes(section) ? { section } : {}),
      ...(compare ? { compare } : {}),
      ...(params.get('comparison_id') ? { comparisonId: params.get('comparison_id')! } : {}),
      ...(transaction ? { transaction } : {}),
      ...(population === 'action' || population === 'transaction' || population === 'unknown' ? { population } : {}),
      ...workspaceScope,
    }
  }

  if (page === 'scripts') {
    const mode = params.get('mode')
    const scriptId = params.get('scriptId') || undefined
    return {
      page: 'scripts',
      ...(isScriptMode(mode) ? { mode } : {}),
      ...(scriptId ? { scriptId } : {}),
      ...workspaceScope,
    }
  }

  if (page === 'docs') {
    const guidePage = params.get('guide')
    return {
      page: 'docs',
      ...(isApiGuideContentPageID(guidePage) ? { guidePage } : {}),
      ...(params.get('article') ? { guideArticle: params.get('article')!.slice(0, 160) } : {}),
      ...(params.get('section') ? { guideSection: params.get('section')!.slice(0, 240) } : {}),
      ...workspaceScope,
    }
  }

  if (page === 'settings' || page === 'access' || page === 'repository') {
    const setting = params.get('setting') || undefined
    const returnTo = params.get('returnTo') || ''
    const resourceId = params.get('resourceId') || ''
    return { page, ...(page === 'access' && isAccessView(params.get('accessView')) ? { accessView: params.get('accessView') as AccessView } : {}), ...(resourceId ? { resourceId } : {}), ...(setting ? { setting } : {}), ...(/^#\/(repository|monitoring|fleet|dashboard|schedules|command-center|new-test|tests|test\/[^?#]+)(\?|$)/.test(returnTo) ? { returnTo } : {}), ...workspaceScope }
  }

  if (page === 'monitoring') {
    const category = params.get('category')
    return {
      page: 'monitoring',
      ...(isMonitoringCategory(category) && category !== 'overview' ? { category } : {}),
      ...workspaceScope,
    }
  }

  if (page === 'swagger') {
    return { page: 'swagger', ...(params.get('search') ? { search: params.get('search')!.slice(0, 240) } : {}), ...workspaceScope }
  }

  if (page === 'repository-guide') {
    return { page: 'repository-guide', ...workspaceScope }
  }

  if (page === 'recorder-panel') {
    const sessionId = params.get('session') || undefined
    const folderId = params.get('folderId') || undefined
    // The compact recorder panel cannot function without a session. Recover to
    // the script library instead of leaving the normal application shell blank.
    return sessionId
      ? { page: 'recorder-panel', sessionId, ...(folderId ? { folderId } : {}), ...workspaceScope }
      : { page: 'scripts', ...workspaceScope }
  }

  return isNavPage(page) ? { page, ...workspaceScope } : { page: 'dashboard' }
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
  if (view.page === 'settings' || view.page === 'access' || view.page === 'repository') {
    const params = new URLSearchParams()
    if (view.setting) params.set('setting', view.setting)
    if (view.page === 'access' && view.accessView) params.set('accessView', view.accessView)
    if (view.resourceId) params.set('resourceId', view.resourceId)
    if (view.returnTo && /^#\/(repository|monitoring|fleet|dashboard|schedules|command-center|new-test|tests|test\/[^?#]+)(\?|$)/.test(view.returnTo)) params.set('returnTo', view.returnTo)
    appendWorkspaceScope(params, view)
    return params.size ? `#/${view.page}?${params}` : `#/${view.page}`
  }

  if (view.page === 'test') {
    const params = new URLSearchParams()
    if (view.tab) params.set('tab', view.tab)
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/test/${encodeURIComponent(view.testId)}?${query}` : `#/test/${encodeURIComponent(view.testId)}`
  }

  if (view.page === 'new-test') {
    const params = new URLSearchParams()
    if (view.scenarioId) params.set('scenarioId', view.scenarioId)
    if (view.prefillStartLocal) params.set('prefillStartLocal', view.prefillStartLocal)
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/new-test?${query}` : '#/new-test'
  }

  if (view.page === 'tests') {
    const params = new URLSearchParams()
    if (view.scenarioView) params.set('view', view.scenarioView)
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/tests?${query}` : '#/tests'
  }

  if (view.page === 'command-center') {
    const params = new URLSearchParams()
    appendWorkspaceScope(params, view)
    const query = params.toString()
    const path = view.testId ? `#/command-center/${encodeURIComponent(view.testId)}` : '#/command-center'
    return query ? `${path}?${query}` : path
  }

  if (view.page === 'report') {
    const params = new URLSearchParams()
    if (view.section) params.set('section', view.section)
    if (view.lens) params.set('lens', view.lens)
    if (view.compare) params.set('compare', view.compare)
    if (view.comparisonId) params.set('comparison_id', view.comparisonId)
    if (view.transaction) params.set('transaction', view.transaction)
    if (view.population) params.set('population', view.population)
    if (view.analysis) params.set('analysis', view.analysis)
    if (view.rev) params.set('rev', view.rev)
    if (view.hypothesis) params.set('hypothesis', view.hypothesis)
    if (view.window) params.set('window', view.window)
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/report/${encodeURIComponent(view.testId)}?${query}` : `#/report/${encodeURIComponent(view.testId)}`
  }

  if (view.page === 'scripts') {
    const params = new URLSearchParams()
    if (view.mode) params.set('mode', view.mode)
    if (view.scriptId) params.set('scriptId', view.scriptId)
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/scripts?${query}` : '#/scripts'
  }

  if (view.page === 'docs') {
    const params = new URLSearchParams()
    if (view.guidePage && view.guidePage !== API_GUIDE_DEFAULT_PAGE) params.set('guide', view.guidePage)
    if (view.guideArticle) params.set('article', view.guideArticle)
    if (view.guideSection) params.set('section', view.guideSection)
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/docs?${query}` : '#/docs'
  }

  if (view.page === 'monitoring') {
    const params = new URLSearchParams()
    if (view.category && view.category !== 'overview') params.set('category', view.category)
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/monitoring?${query}` : '#/monitoring'
  }

  if (view.page === 'swagger') {
    const params = new URLSearchParams()
    if (view.search) params.set('search', view.search)
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/swagger?${query}` : '#/swagger'
  }

  if (view.page === 'repository-guide') {
    const params = new URLSearchParams()
    appendWorkspaceScope(params, view)
    const query = params.toString()
    return query ? `#/repository-guide?${query}` : '#/repository-guide'
  }

  if (view.page === 'recorder-panel') {
    const params = new URLSearchParams({ session: view.sessionId })
    if (view.folderId) params.set('folderId', view.folderId)
    appendWorkspaceScope(params, view)
    return `#/recorder-panel?${params.toString()}`
  }

  const params = new URLSearchParams()
  appendWorkspaceScope(params, view)
  const query = params.toString()
  return query ? `#/${view.page}?${query}` : `#/${view.page}`
}

export function projectIdForAppView(view: AppView): string | undefined {
  return view.projectId
}

export function workspaceScopeForAppView(view: AppView): AppWorkspaceScope {
  return {
    ...(view.projectId ? { projectId: view.projectId } : {}),
    ...(view.applicationId ? { applicationId: view.applicationId } : {}),
    ...(view.releaseId ? { releaseId: view.releaseId } : {}),
  }
}

export function scopeAppViewToWorkspace(view: AppView, scope: AppWorkspaceScope): AppView {
  const next = { ...view }
  delete next.projectId
  delete next.applicationId
  delete next.releaseId
  return {
    ...next,
    ...(scope.projectId ? { projectId: scope.projectId } : {}),
    ...(scope.applicationId ? { applicationId: scope.applicationId } : {}),
    ...(scope.releaseId ? { releaseId: scope.releaseId } : {}),
  } as AppView
}

// Internal navigation inherits the current workspace, but only while each
// parent scope still matches. This prevents a route targeting another project
// or application from accidentally serializing stale child context.
export function mergeAppViewWithWorkspace(view: AppView, current: AppWorkspaceScope): AppView {
  const projectId = view.projectId || current.projectId
  const applicationId = view.applicationId || (
    projectId && projectId === current.projectId ? current.applicationId : undefined
  )
  const releaseId = view.releaseId || (
    applicationId
    && applicationId === current.applicationId
    && projectId === current.projectId
      ? current.releaseId
      : undefined
  )
  return scopeAppViewToWorkspace(view, { projectId, applicationId, releaseId })
}

export function testIdForAppView(view: AppView): string | undefined {
  if (view.page === 'test' || view.page === 'report') return view.testId
  if (view.page === 'command-center') return view.testId
  return undefined
}

export type UnscopedProjectAsset = {
  kind: 'script' | 'scenario' | 'test'
  id: string
}

export function projectAssetForAppView(view: AppView): UnscopedProjectAsset | null {
  if (view.page === 'scripts' && view.scriptId) return { kind: 'script', id: view.scriptId }
  if (view.page === 'new-test' && view.scenarioId) return { kind: 'scenario', id: view.scenarioId }
  if (view.page === 'test' || view.page === 'report') return { kind: 'test', id: view.testId }
  if (view.page === 'command-center' && view.testId) return { kind: 'test', id: view.testId }
  return null
}

// Older bookmarks and desktop recents predate project-aware routes. Resolve
// their owning project before mounting an asset page so the currently selected
// project can never be applied to an unrelated script, scenario, or run.
export function unscopedProjectAssetForAppView(view: AppView): UnscopedProjectAsset | null {
  if (projectIdForAppView(view)) return null
  return projectAssetForAppView(view)
}

export function scopeAppViewToProject(view: AppView, projectId: string): AppView {
  if (view.page === 'scripts') return scopeAppViewToWorkspace(view, { projectId })
  if (view.page === 'new-test') return scopeAppViewToWorkspace(view, { projectId })
  if (view.page === 'test') return scopeAppViewToWorkspace(view, { projectId })
  if (view.page === 'report') return scopeAppViewToWorkspace(view, { projectId })
  if (view.page === 'command-center') return scopeAppViewToWorkspace(view, { projectId })
  return view
}

export function recoveryViewForUnscopedProjectAsset(asset: UnscopedProjectAsset): AppView {
  if (asset.kind === 'script') return { page: 'scripts' }
  if (asset.kind === 'scenario') return { page: 'tests' }
  return { page: 'results' }
}

// A manual workspace-scope change intentionally clears entity identifiers from
// the previous project. Keeping those identifiers would make the destination
// page request an asset that cannot belong to the newly selected project.
export function resetProjectScopedAssetView(view: AppView, projectId?: string): AppView {
  if (view.page === 'tests' && view.scenarioView === 'builder') {
    return { page: 'tests', scenarioView: 'library' }
  }
  if (view.page === 'scripts') {
    return {
      page: 'scripts',
      ...(view.mode && view.mode !== 'edit' ? { mode: view.mode } : {}),
      ...(projectId ? { projectId } : {}),
    }
  }
  if (view.page === 'new-test') {
    return {
      page: 'new-test',
      ...(view.prefillStartLocal ? { prefillStartLocal: view.prefillStartLocal } : {}),
      ...(projectId ? { projectId } : {}),
    }
  }
  if (view.page === 'command-center') {
    return { page: 'command-center', ...(projectId ? { projectId } : {}) }
  }
  if (view.page === 'test') {
    if (isArchiveTestWorkspaceTab(view.tab)) return { page: 'results' }
    return { page: 'command-center', ...(projectId ? { projectId } : {}) }
  }
  if (view.page === 'report') return { page: 'results' }
  return view
}

export function normalizeReportViewForFeatureFlag(view: AppView, runReportEnabled: boolean): AppView {
  if (runReportEnabled || view.page !== 'report') return view
  return {
    page: 'test',
    testId: view.testId,
    tab: 'archive',
    ...workspaceScopeForAppView(view),
  }
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

/** Preserve old bookmarks while keeping ended runs in the Results workspace. */
export function terminalRunReportView(view: AppView, status: string, enabled: boolean): AppView {
  if (!enabled || !['COMPLETE', 'FAILED', 'ABORTED'].includes(status)) return view
  if (view.page !== 'test' && view.page !== 'command-center') return view
  if (!view.testId) return view
  const sections: Partial<Record<TestWorkspaceTabID, ReportSection>> = {
    overview: 'execution', configuration: 'execution', infrastructure: 'execution', control: 'execution', graphs: 'performance', diagnostics: 'diagnostics',
    archive: 'evidence', 'action-summary': 'performance', 'load-impact': 'performance', 'transaction-drilldown': 'diagnostics',
  }
  return { page: 'report', testId: view.testId, ...workspaceScopeForAppView(view),
    section: view.page === 'test' && view.tab ? sections[view.tab] || 'execution' : 'execution' }
}
