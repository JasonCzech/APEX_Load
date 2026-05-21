import type { LatencyTarget } from '../types'

const LATENCY_PREFIX = 'latency_'

export function latencySeriesKeyForTarget(target: Pick<LatencyTarget, 'name' | 'endpoint'>): string {
  const label = target.endpoint || target.name || 'target'
  return `${LATENCY_PREFIX}${encodeURIComponent(label)}`
}

export function routeLatencySeriesLabel(key: string): string {
  const raw = key.startsWith(LATENCY_PREFIX) ? key.slice(LATENCY_PREFIX.length) : key
  try {
    return decodeURIComponent(raw) || raw
  } catch {
    return raw
  }
}
