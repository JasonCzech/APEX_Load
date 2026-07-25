import type { MetricsHistoryPoint } from './types'

// Keep enough 1-second samples to cover long-running sessions in execution
// views where Display Window=All is expected to show the full run timeline.
export const MAX_LIVE_METRICS_POINTS_PER_TEST = 21600
export const MAX_LIVE_METRICS_TESTS = 24

/**
 * Appends a live sample while keeping the shell's cross-test cache bounded.
 * Object insertion order acts as a small LRU: the test receiving the newest
 * sample is moved to the end and the least-recently-updated inactive test is
 * evicted. Protected (normally active) runs may temporarily exceed maxTests so
 * concurrent live sparklines do not continuously evict one another.
 */
export function appendBoundedMetricsHistory(
  history: Record<string, MetricsHistoryPoint[]>,
  testId: string,
  point: MetricsHistoryPoint,
  maxTests = MAX_LIVE_METRICS_TESTS,
  maxPointsPerTest = MAX_LIVE_METRICS_POINTS_PER_TEST,
  protectedTestIds: ReadonlySet<string> = new Set(),
): Record<string, MetricsHistoryPoint[]> {
  const { [testId]: existing = [], ...otherTests } = history
  const next: Record<string, MetricsHistoryPoint[]> = {
    ...otherTests,
    [testId]: [...existing, point].slice(-Math.max(1, maxPointsPerTest)),
  }

  const testIds = Object.keys(next)
  const excess = testIds.length - Math.max(1, maxTests)
  const evictableTestIds = testIds.filter(id => !protectedTestIds.has(id))
  for (let index = 0; index < excess && index < evictableTestIds.length; index += 1) {
    delete next[evictableTestIds[index]]
  }
  return next
}
