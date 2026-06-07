import type { AppView } from './appRouting'
import type { TestStatus, TestSummary } from './types'

export type TopbarPageTitleOptions = {
  repositoryName?: string
  testName?: string
}

export type TopbarRunSummary = Pick<TestSummary, 'id' | 'name' | 'status'> & Partial<Pick<TestSummary, 'created_at' | 'started_at' | 'pending_since' | 'scheduled_start_at'>>

const ACTIVE_RUN_STATUS_PRIORITY: Record<string, number> = {
  RUNNING: 0,
  INITIALIZING: 1,
  PAUSED: 2,
  STOPPING: 3,
}

export function pageTitleForView(view: AppView, options: TopbarPageTitleOptions = {}): string {
  switch (view.page) {
    case 'dashboard':
      return 'Dashboard'
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
      return 'Results'
    case 'monitoring':
      return 'Monitoring'
    case 'docs':
      return 'API Guide'
    case 'swagger':
      return 'Swagger Docs'
    case 'repository-guide':
      return 'Repository Guide'
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

export function isTopbarActiveRunStatus(status?: string): status is TestStatus {
  return Boolean(status && ACTIVE_RUN_STATUS_PRIORITY[status.toUpperCase()] !== undefined)
}

export function topbarRunStatusLabel(status?: string): string {
  const normalized = status?.toUpperCase() || ''
  if (normalized === 'INITIALIZING') return 'Initializing'
  if (normalized === 'SHAKEOUT') return 'Shakeout'
  if (normalized === 'SHAKEOUT_REVIEW') return 'Shakeout Review'
  if (!normalized) return 'Active'
  return normalized.charAt(0) + normalized.slice(1).toLowerCase()
}

export function topbarRunTone(status?: string): 'running' | 'paused' | 'pending' | 'stopping' {
  const normalized = status?.toUpperCase()
  if (normalized === 'PAUSED') return 'paused'
  if (normalized === 'STOPPING') return 'stopping'
  if (normalized === 'PENDING' || normalized === 'QUEUED' || normalized === 'SCHEDULED' || normalized === 'SHAKEOUT' || normalized === 'SHAKEOUT_REVIEW') {
    return 'pending'
  }
  return 'running'
}

export function selectTopbarActiveRuns(tests: TestSummary[], statusOverrides: Record<string, string> = {}): TopbarRunSummary[] {
  return tests
    .map((test) => ({
      id: test.id,
      name: test.name || test.id,
      status: (statusOverrides[test.id] || test.status).toUpperCase() as TestStatus,
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
  const status = (update.status || existing?.status || '').toUpperCase()
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
