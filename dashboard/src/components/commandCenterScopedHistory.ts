import type { LiveMetrics, MetricsHistoryPoint } from '../types'
import { mapExecutionHistory, mergeMetricsHistory } from './testExecutionMetricsModel'
import { EXECUTION_STREAM_HISTORY_LIMIT } from './useExecutionMetricsStream'

export interface ScopedLiveHistory {
  /** Whether the server filtered the answers to the selected groups. */
  honored: boolean
  history: MetricsHistoryPoint[]
}

/**
 * Brings a live run's selected-group history up to date: the whole window the
 * first time, then only the points since the last one. A filtered and an
 * unfiltered answer do not mix; when the kind changes, the points since the
 * last one would be all that is left, so the whole window is asked for again.
 * The retained history is read after each answer, so one that a concurrent
 * refresh replaced is merged into, not overwritten.
 */
export async function refreshScopedLiveHistory<Response extends { metrics?: LiveMetrics; history?: unknown[] }>(
  readRetained: () => ScopedLiveHistory | null,
  fetchHistory: (since?: string) => Promise<Response>,
  honorsFilter: (metrics?: LiveMetrics) => boolean,
  limit = EXECUTION_STREAM_HISTORY_LIMIT,
): Promise<ScopedLiveHistory & { response: Response }> {
  const before = readRetained()?.history
  const lastTimestampMs = before?.[before.length - 1]?.timestampMs
  const since = typeof lastTimestampMs === 'number' ? new Date(lastTimestampMs).toISOString() : undefined
  let response = await fetchHistory(since)
  let honored = honorsFilter(response.metrics)
  let retained = readRetained()
  if (since !== undefined && (!retained || retained.honored !== honored)) {
    response = await fetchHistory()
    honored = honorsFilter(response.metrics)
    retained = null
  }
  const history = mergeMetricsHistory(
    retained && retained.honored === honored ? retained.history : [],
    mapExecutionHistory((response.history || []) as LiveMetrics[]),
  ).slice(-limit)
  return { response, honored, history }
}
