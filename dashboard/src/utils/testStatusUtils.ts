import type { TestSummary } from '../types'

const STATUS_ALIASES: Record<string, string> = {
  COMPLETED: 'COMPLETE',
  STOPPED: 'COMPLETE',
  CANCELLED: 'ABORTED',
  CANCELED: 'ABORTED',
}

const COMPLETED_TEST_STATUSES = new Set(['COMPLETE', 'FAILED', 'ABORTED'])
const LIVE_TEST_STATUSES = new Set(['RUNNING', 'INITIALIZING', 'STOPPING'])
const PENDING_TEST_STATUSES = new Set(['PENDING', 'QUEUED', 'RESERVED', 'SCHEDULED'])

export function normalizeTestStatus(status?: string): string {
  const normalized = status?.trim().toUpperCase() || ''
  return STATUS_ALIASES[normalized] || normalized
}

/** Display outcome; lifecycle normalization remains separate for execution controls. */
export function testOutcomeStatus(status?: string, stoppedByOperator = false): string {
  const normalized = normalizeTestStatus(status)
  return normalized === 'COMPLETE' && (stoppedByOperator || status?.trim().toUpperCase() === 'STOPPED') ? 'STOPPED' : normalized
}

export function isCompletedTestStatus(status?: string): boolean {
  return COMPLETED_TEST_STATUSES.has(normalizeTestStatus(status))
}

export function isLiveTestStatus(status?: string): boolean {
  return LIVE_TEST_STATUSES.has(normalizeTestStatus(status))
}

export function isPendingTestStatus(status?: string): boolean {
  return PENDING_TEST_STATUSES.has(normalizeTestStatus(status))
}

// Lifecycle progress ranks. A status override is a broadcast snapshot that can
// be older than what the caller already knows (the socket is quiet while a run
// sits PAUSED, and a locally-issued Stop is known before its event arrives), so
// it may only carry the run forward. RUNNING and PAUSED share a rank because
// pause/resume moves freely between them in both directions.
const TEST_STATUS_PROGRESS: Record<string, number> = {
  PENDING: 0,
  QUEUED: 0,
  RESERVED: 0,
  SCHEDULED: 0,
  SHAKEOUT: 1,
  SHAKEOUT_REVIEW: 1,
  INITIALIZING: 1,
  RUNNING: 2,
  PAUSED: 2,
  STOPPING: 3,
  COMPLETE: 4,
  FAILED: 4,
  ABORTED: 4,
}

export function resolveTestStatus(
  test: Pick<TestSummary, 'id' | 'status'>,
  statusOverrides: Record<string, string> = {},
): string {
  const persistedStatus = normalizeTestStatus(test.status)
  const overrideStatus = normalizeTestStatus(statusOverrides[test.id])

  if (!overrideStatus) return persistedStatus

  const persistedProgress = TEST_STATUS_PROGRESS[persistedStatus]
  const overrideProgress = TEST_STATUS_PROGRESS[overrideStatus]
  // An unrecognized status on either side has no comparable position, so keep
  // the previous behaviour and let the override win.
  if (persistedProgress !== undefined && overrideProgress !== undefined && overrideProgress < persistedProgress) {
    return persistedStatus
  }

  return overrideStatus
}
