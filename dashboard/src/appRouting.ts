import { API_GUIDE_DEFAULT_PAGE, isApiGuideContentPageID, type ApiGuideContentPageID } from './apiGuideNavigation'
import type { NavPage } from './components/Sidebar'

export type AppView =
  | { page: Exclude<NavPage, 'command-center' | 'new-test' | 'scripts' | 'tests' | 'docs'> }
  | { page: 'docs'; guidePage?: ApiGuideContentPageID }
  | { page: 'command-center'; testId?: string }
  | { page: 'tests'; scenarioView?: 'library' | 'builder' | 'plans' }
  | { page: 'new-test'; scenarioId?: string }
  | { page: 'scripts'; mode?: 'list' | 'create' | 'edit' | 'import'; scriptId?: string }
  | { page: 'swagger' }
  | { page: 'repository-guide' }
  | { page: 'recorder-panel'; sessionId?: string }
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
  'docs',
  'access',
  'settings',
]

const SCRIPT_MODES = ['list', 'create', 'edit', 'import'] as const
const SCENARIO_VIEWS = ['library', 'builder', 'plans'] as const
const TEST_WORKSPACE_TABS = ['overview', 'control', 'graphs', 'diagnostics', 'archive', 'action-summary', 'load-impact', 'transaction-drilldown'] as const

export type TestWorkspaceTabID = typeof TEST_WORKSPACE_TABS[number]

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
    return scenarioId ? { page: 'new-test', scenarioId } : { page: 'new-test' }
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

  if (page === 'swagger') {
    return { page: 'swagger' }
  }

  if (page === 'repository-guide') {
    return { page: 'repository-guide' }
  }

  if (page === 'recorder-panel') {
    const sessionId = params.get('session') || undefined
    return sessionId ? { page: 'recorder-panel', sessionId } : { page: 'recorder-panel' }
  }

  return isNavPage(page) ? { page } : { page: 'dashboard' }
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

  if (view.page === 'swagger') {
    return '#/swagger'
  }

  if (view.page === 'repository-guide') {
    return '#/repository-guide'
  }

  if (view.page === 'recorder-panel') {
    return view.sessionId
      ? `#/recorder-panel?session=${encodeURIComponent(view.sessionId)}`
      : '#/recorder-panel'
  }

  return `#/${view.page}`
}
