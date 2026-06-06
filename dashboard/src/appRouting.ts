import type { NavPage } from './components/Sidebar'

export type AppView =
  | { page: Exclude<NavPage, 'new-test' | 'scripts' | 'tests'> }
  | { page: 'tests'; scenarioView?: 'library' | 'builder' }
  | { page: 'new-test'; scenarioId?: string }
  | { page: 'scripts'; mode?: 'list' | 'create' | 'edit' | 'import'; scriptId?: string }
  | { page: 'swagger' }
  | { page: 'repository-guide' }
  | { page: 'test'; testId: string }

const NAV_PAGES: NavPage[] = [
  'dashboard',
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
const SCENARIO_VIEWS = ['library', 'builder'] as const

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

export function parseAppViewFromHash(hash: string): AppView {
  const route = hash.replace(/^#/, '').replace(/^\/+/, '')
  const [pathPart, queryString = ''] = route.split('?')
  const segments = pathPart.split('/').filter(Boolean).map(cleanSegment)
  const page = segments[0] || 'dashboard'
  const params = new URLSearchParams(queryString)

  if (page === 'test' && segments[1]) {
    return { page: 'test', testId: segments[1] }
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

  if (page === 'scripts') {
    const mode = params.get('mode')
    const scriptId = params.get('scriptId') || undefined
    return {
      page: 'scripts',
      ...(isScriptMode(mode) ? { mode } : {}),
      ...(scriptId ? { scriptId } : {}),
    }
  }

  if (page === 'swagger') {
    return { page: 'swagger' }
  }

  if (page === 'repository-guide') {
    return { page: 'repository-guide' }
  }

  return isNavPage(page) ? { page } : { page: 'dashboard' }
}

export function viewToLocationHash(view: AppView): string {
  if (view.page === 'test') {
    return `#/test/${encodeURIComponent(view.testId)}`
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

  if (view.page === 'scripts') {
    const params = new URLSearchParams()
    if (view.mode) params.set('mode', view.mode)
    if (view.scriptId) params.set('scriptId', view.scriptId)
    const query = params.toString()
    return query ? `#/scripts?${query}` : '#/scripts'
  }

  if (view.page === 'swagger') {
    return '#/swagger'
  }

  if (view.page === 'repository-guide') {
    return '#/repository-guide'
  }

  return `#/${view.page}`
}
