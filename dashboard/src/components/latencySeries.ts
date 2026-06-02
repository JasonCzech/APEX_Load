import type { LatencyTarget } from '../types'

const LATENCY_PREFIX = 'latency_'
const ROUTE_LATENCY_METRIC_PREFIXES = {
  dns: 'latency_dns_',
  connect: 'latency_connect_',
  tls: 'latency_tls_',
  ttfb: 'latency_ttfb_',
  jitter: 'latency_jitter_',
  successRate: 'latency_success_rate_',
} as const

export type RouteLatencyDetailMetric = keyof typeof ROUTE_LATENCY_METRIC_PREFIXES

export function latencySeriesKeyForTarget(target: Pick<LatencyTarget, 'name' | 'endpoint'>): string {
  const label = target.endpoint || target.name || 'target'
  return `${LATENCY_PREFIX}${encodeURIComponent(label)}`
}

export function routeLatencyDetailSeriesKeyForTarget(
  target: Pick<LatencyTarget, 'name' | 'endpoint'>,
  metric: RouteLatencyDetailMetric,
): string {
  const label = target.endpoint || target.name || 'target'
  return `${ROUTE_LATENCY_METRIC_PREFIXES[metric]}${encodeURIComponent(label)}`
}

export function routeLatencyDetailSeriesKey(totalKey: string, metric: RouteLatencyDetailMetric): string {
  const encodedLabel = totalKey.startsWith(LATENCY_PREFIX) ? totalKey.slice(LATENCY_PREFIX.length) : encodeURIComponent(totalKey)
  return `${ROUTE_LATENCY_METRIC_PREFIXES[metric]}${encodedLabel}`
}

export function isRouteLatencyDetailSeriesKey(key: string): boolean {
  return Object.values(ROUTE_LATENCY_METRIC_PREFIXES).some(prefix => key.startsWith(prefix))
}

export function isRouteLatencyTotalSeriesKey(key: string): boolean {
  return key.startsWith(LATENCY_PREFIX) && !isRouteLatencyDetailSeriesKey(key)
}

export function routeLatencyTargetLabelFromKey(key: string): string {
  let raw = key
  const detailPrefix = Object.values(ROUTE_LATENCY_METRIC_PREFIXES).find(prefix => key.startsWith(prefix))
  if (detailPrefix) {
    raw = key.slice(detailPrefix.length)
  } else if (key.startsWith(LATENCY_PREFIX)) {
    raw = key.slice(LATENCY_PREFIX.length)
  }
  try {
    return decodeURIComponent(raw) || raw
  } catch {
    return raw
  }
}

export function routeLatencySeriesLabel(key: string): string {
  if (key.startsWith(ROUTE_LATENCY_METRIC_PREFIXES.dns)) return `DNS · ${routeLatencyTargetLabelFromKey(key)}`
  if (key.startsWith(ROUTE_LATENCY_METRIC_PREFIXES.connect)) return `TCP · ${routeLatencyTargetLabelFromKey(key)}`
  if (key.startsWith(ROUTE_LATENCY_METRIC_PREFIXES.tls)) return `TLS · ${routeLatencyTargetLabelFromKey(key)}`
  if (key.startsWith(ROUTE_LATENCY_METRIC_PREFIXES.ttfb)) return `TTFB · ${routeLatencyTargetLabelFromKey(key)}`
  if (key.startsWith(ROUTE_LATENCY_METRIC_PREFIXES.jitter)) return `Jitter · ${routeLatencyTargetLabelFromKey(key)}`
  if (key.startsWith(ROUTE_LATENCY_METRIC_PREFIXES.successRate)) return `Success · ${routeLatencyTargetLabelFromKey(key)}`
  return routeLatencyTargetLabelFromKey(key)
}

function finiteNumber(value: number | undefined): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

export function routeLatencySeriesValuesForTarget(target: LatencyTarget): Record<string, number> {
  const values: Record<string, number> = {}
  const total = finiteNumber(target.probe_total_ms) && target.probe_total_ms > 0
    ? target.probe_total_ms
    : target.latency_ms
  if (finiteNumber(total)) {
    values[latencySeriesKeyForTarget(target)] = total
  }
  if (finiteNumber(target.dns_ms)) values[routeLatencyDetailSeriesKeyForTarget(target, 'dns')] = target.dns_ms
  if (finiteNumber(target.connect_ms)) values[routeLatencyDetailSeriesKeyForTarget(target, 'connect')] = target.connect_ms
  if (finiteNumber(target.tls_ms)) values[routeLatencyDetailSeriesKeyForTarget(target, 'tls')] = target.tls_ms
  if (finiteNumber(target.ttfb_ms)) values[routeLatencyDetailSeriesKeyForTarget(target, 'ttfb')] = target.ttfb_ms
  if (finiteNumber(target.jitter_ms)) values[routeLatencyDetailSeriesKeyForTarget(target, 'jitter')] = target.jitter_ms
  if (finiteNumber(target.success_rate_pct)) values[routeLatencyDetailSeriesKeyForTarget(target, 'successRate')] = target.success_rate_pct
  return values
}
