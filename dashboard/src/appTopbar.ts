import type { AppView } from './appRouting'
import type { TestStatus, TestSummary } from './types'
import { isPendingTestStatus, normalizeTestStatus, resolveTestStatus } from './utils/testStatusUtils'

export type TopbarPageTitleOptions = {
  repositoryName?: string
  testName?: string
}

export type TopbarRunSummary = Pick<TestSummary, 'id' | 'name' | 'status'> & Partial<Pick<TestSummary, 'created_at' | 'started_at' | 'pending_since' | 'scheduled_start_at'>>

const ACTIVE_RUN_STATUS_PRIORITY: Record<string, number> = {
  RUNNING: 0,
  SHAKEOUT: 1,
  INITIALIZING: 2,
  PAUSED: 3,
  STOPPING: 4,
  SHAKEOUT_REVIEW: 5,
  PENDING: 6,
  QUEUED: 7,
  RESERVED: 8,
  SCHEDULED: 9,
}

export function pageTitleForView(view: AppView, options: TopbarPageTitleOptions = {}): string {
  switch (view.page) {
    case 'dashboard':
      return 'Dashboard'
    case 'command-center':
      return 'Command Center'
    case 'report':
      return 'Run Results'
    case 'repository':
      return options.repositoryName || 'Application Repository'
    case 'tests':
      return view.scenarioView === 'builder' ? 'Scenario Builder' : 'Scenario Library'
    case 'new-test':
      return 'New Test'
    case 'scripts':
      if (view.mode === 'create') return 'Create Script'
      if (view.mode === 'edit') return 'Edit Script'
      if (view.mode === 'import') return 'Import Script'
      return 'Script Library'
    case 'results':
      return 'Results Library'
    case 'monitoring':
      if (view.category === 'analysis') return 'Analysis'
      return view.category === 'telemetry' ? 'Telemetry' : 'Monitoring'
    case 'fleet':
      return 'Fleet'
    case 'schedules':
      return 'Schedules'
    case 'docs':
      return 'Help & documentation'
    case 'swagger':
      return 'Help & documentation'
    case 'repository-guide':
      return 'Help & documentation'
    case 'recorder-panel':
      return 'Browser Recorder'
    case 'access':
      return 'Access'
    case 'settings':
      return 'Settings'
    case 'test':
      return options.testName || 'Test Run'
    default:
      return 'Dashboard'
  }
}

export function pageTitleForScriptSubView(subView?: string): string {
  switch (subView) {
    case 'list':
      return 'Script Library'
    case 'create-options':
      return 'Script Builder'
    case 'create':
      return 'Create Script'
    case 'edit':
      return 'Edit Script'
    case 'import':
      return 'Import Loadrunner Script Archive'
    case 'jmeter-import':
      return 'Import JMeter'
    case 'bruno-import':
      return 'Import Bruno'
    case 'recorder':
      return 'Record Browser'
    case 'sample':
      return 'Import Sample'
    case 'protocol-import':
      return 'Import From Source'
    case 'oracle-review':
      return 'Oracle E-Business Review'
    case 'devweb-import':
      return 'Import DevWeb'
    case 'har-import':
      return 'Import HAR'
    case 'recorder-import':
      return 'Import Browser Recording'
    case 'openapi-import':
      return 'Import OpenAPI'
    case 'raml-import':
      return 'Import RAML'
    case 'postman-import':
      return 'Import Postman'
    case 'wsdl-import':
      return 'Import WSDL'
    case 'asyncapi-import':
      return 'Import AsyncAPI'
    default:
      return 'Script Library'
  }
}

export function shouldShowTopbarClientDownloads(view: AppView): boolean {
  return view.page === 'docs' || view.page === 'swagger' || view.page === 'repository-guide'
}

export function isTopbarActiveRunStatus(status?: string): status is TestStatus {
  return Boolean(status && (ACTIVE_RUN_STATUS_PRIORITY[normalizeTestStatus(status)] !== undefined || isPendingTestStatus(status)))
}

export function topbarRunStatusLabel(status?: string): string {
  const normalized = normalizeTestStatus(status)
  if (normalized === 'INITIALIZING') return 'Initializing'
  if (normalized === 'SHAKEOUT') return 'Shakeout'
  if (normalized === 'SHAKEOUT_REVIEW') return 'Shakeout Review'
  if (!normalized) return 'Active'
  return normalized.charAt(0) + normalized.slice(1).toLowerCase()
}

export function topbarRunTone(status?: string): 'running' | 'paused' | 'pending' | 'stopping' {
  const normalized = normalizeTestStatus(status)
  if (normalized === 'PAUSED') return 'paused'
  if (normalized === 'STOPPING') return 'stopping'
  if (normalized === 'PENDING' || normalized === 'QUEUED' || normalized === 'RESERVED' || normalized === 'SCHEDULED' || normalized === 'SHAKEOUT' || normalized === 'SHAKEOUT_REVIEW') {
    return 'pending'
  }
  return 'running'
}

export function selectTopbarActiveRuns(tests: TestSummary[], statusOverrides: Record<string, string> = {}): TopbarRunSummary[] {
  return tests
    .map((test) => ({
      id: test.id,
      name: test.name || test.id,
      status: resolveTestStatus(test, statusOverrides) as TestStatus,
      created_at: test.created_at,
      started_at: test.started_at,
      pending_since: test.pending_since,
      scheduled_start_at: test.scheduled_start_at,
    }))
    .filter((test) => isTopbarActiveRunStatus(test.status))
    .sort(compareTopbarRuns)
}

export function updateTopbarActiveRuns(
  runs: TopbarRunSummary[],
  update: { id: string; name?: string; status?: string; timestamp?: string },
): TopbarRunSummary[] {
  const existing = runs.find((run) => run.id === update.id)
  const status = normalizeTestStatus(update.status || existing?.status)
  const remaining = runs.filter((run) => run.id !== update.id)

  if (!isTopbarActiveRunStatus(status)) {
    return remaining
  }

  return [
    ...remaining,
    {
      id: update.id,
      name: update.name?.trim() || existing?.name || update.id,
      status,
      created_at: existing?.created_at || update.timestamp,
      started_at: existing?.started_at || (status === 'RUNNING' || status === 'INITIALIZING' ? update.timestamp : undefined),
      pending_since: existing?.pending_since,
      scheduled_start_at: existing?.scheduled_start_at,
    },
  ].sort(compareTopbarRuns)
}

function compareTopbarRuns(a: TopbarRunSummary, b: TopbarRunSummary): number {
  const priorityDelta = ACTIVE_RUN_STATUS_PRIORITY[a.status] - ACTIVE_RUN_STATUS_PRIORITY[b.status]
  if (priorityDelta !== 0) return priorityDelta

  const aTime = topbarRunTimestamp(a)
  const bTime = topbarRunTimestamp(b)
  if (aTime !== bTime) return bTime - aTime

  return a.name.localeCompare(b.name)
}

function topbarRunTimestamp(run: TopbarRunSummary): number {
  const timestamp = run.started_at || run.pending_since || run.scheduled_start_at || run.created_at || ''
  const parsed = Date.parse(timestamp)
  return Number.isFinite(parsed) ? parsed : 0
}
