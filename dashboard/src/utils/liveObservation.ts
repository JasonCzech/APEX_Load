import type { LiveMetrics } from '../types'

export type LiveObservationState = 'current' | 'stale' | 'historical' | 'missing'

// Must match the API's presentation bound, including for older API responses.
const freshnessMs = 5000

export function liveObservationState(status: string, metrics: LiveMetrics | undefined, now: number): LiveObservationState {
  if (['COMPLETE', 'FAILED', 'ABORTED', 'STOPPED'].includes(status)) return 'historical'
  if (!metrics) return 'missing'
  if (metrics.observation_state === 'historical') return 'historical'
  if (metrics.observation_state === 'stale') return 'stale'
  const at = Date.parse(metrics.timestamp)
  if (!Number.isFinite(at) || at > now) return 'stale'
  const declared = metrics.observation_valid_until === undefined ? at + freshnessMs : Date.parse(metrics.observation_valid_until)
  if (!Number.isFinite(declared) || now >= Math.min(declared, at + freshnessMs)) return 'stale'
  return 'current'
}
