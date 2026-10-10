import type { AppView } from './appRouting'
import type { TestStatus, TestSummary } from './types'
import { isPendingTestStatus, normalizeTestStatus, resolveTestStatus } from './utils/testStatusUtils'
import { launcherForImportSurface } from './scriptImportLaunchers'

export type TopbarPageTitleOptions = {
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
      return 'Run results'
    case 'repository':
      return 'Repository'
    case 'tests':
      return 'Scenarios'
    case 'new-test':
      return 'New Test'
    case 'scripts':
      if (view.mode === 'create') return 'Create script'
      if (view.mode === 'edit') return 'Edit script'
      if (view.mode === 'import') return 'Import script'
      return 'Scripts'
    case 'activity':
      return 'Activity'
    case 'results':
      return 'Results'
    // Fleet and Schedules are Monitoring tabs, so they keep the Monitoring title.
    case 'monitoring':
    case 'fleet':
    case 'schedules':
      return 'Monitoring'
    case 'docs':
      return 'Help & documentation'
    case 'swagger':
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

// Source names match the import catalog's card titles. The page is titled
// "Import <source>"; the importer's own heading names the source alone.
const IMPORT_SOURCE_NAMES: Record<string, string> = {
  enterprise_archive: 'LoadRunner Script Archive',
  jmeter: 'JMeter Test Plan',
  bruno: 'Bruno Collection',
  sample: 'Request / Response Sample',
  oracle_ebusiness_review: 'Oracle E-Business / Siebel Review',
  openapi: 'OpenAPI / Swagger',
  postman: 'Postman Collection',
  wsdl: 'WSDL / SOAP',
  asyncapi: 'AsyncAPI / Kafka',
  raml: 'RAML / Anypoint',
  traffic_har: 'HAR Traffic',
  recorder_export: 'Recorder Export / Playwright Codegen',
  devweb: 'DevWeb Source',
  k6: 'k6 Script',
  cloudtest: 'Akamai CloudTest Export',
  eventhubs_capture: 'Event Hubs Capture',
  broker_config: 'Broker Config',
}

/** The source an import surface reads, as its catalog card names it. */
export function importSourceName(launcher: string): string {
  return IMPORT_SOURCE_NAMES[launcher] || 'Source'
}

/** The page title for an import surface. The Oracle surface reviews rather than imports. */
export function importSurfaceTitle(launcher: string): string {
  const name = IMPORT_SOURCE_NAMES[launcher]
  if (!name) return 'Import from source'
  return launcher === 'oracle_ebusiness_review' ? name : `Import ${name}`
}

export function pageTitleForScriptSubView(subView?: string, importLauncher?: string | null): string {
  switch (subView) {
    // Library and Builder are tabs of the Scripts page.
    case 'list':
    case 'create-options':
      return 'Scripts'
    case 'create':
      return 'Create script'
    case 'edit':
      return 'Edit script'
    case 'recorder':
      return 'Record browser'
    case 'protocol-import':
      return importLauncher ? importSurfaceTitle(importLauncher) : 'Import from source'
  }
  const launcher = launcherForImportSurface(subView)
  return launcher ? importSurfaceTitle(launcher) : 'Scripts'
}

export function shouldShowTopbarClientDownloads(view: AppView): boolean {
  return view.page === 'docs' || view.page === 'swagger'
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
