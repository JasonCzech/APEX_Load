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

export function isCompletedTestStatus(status?: string): boolean {
  return COMPLETED_TEST_STATUSES.has(normalizeTestStatus(status))
}

export function isLiveTestStatus(status?: string): boolean {
  return LIVE_TEST_STATUSES.has(normalizeTestStatus(status))
}

export function isPendingTestStatus(status?: string): boolean {
  return PENDING_TEST_STATUSES.has(normalizeTestStatus(status))
}

export function resolveTestStatus(
  test: Pick<TestSummary, 'id' | 'status'>,
  statusOverrides: Record<string, string> = {},
): string {
  const persistedStatus = normalizeTestStatus(test.status)
  const overrideStatus = normalizeTestStatus(statusOverrides[test.id])

  if (!overrideStatus) return persistedStatus
  if (isCompletedTestStatus(persistedStatus) && !isCompletedTestStatus(overrideStatus)) {
    return persistedStatus
  }

  return overrideStatus
}
