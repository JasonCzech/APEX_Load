import type { MetricsHistoryPoint } from '../types'
import type { AnalysisPoint } from '../types/analysis'
import type { MonitorEntity } from '../types/runMonitoring'

export const monitorLoadOverlays = {
  latency: { label: 'Response time', unit: 'ms', field: 'avgMs' },
  tps: { label: 'TPS', unit: '/s', field: 'tps' },
  vusers: { label: 'VUsers', unit: 'users', field: 'vusers' },
  errors: { label: 'Errors', unit: '%', field: 'errorPct' },
} as const
export type MonitorLoadOverlay = keyof typeof monitorLoadOverlays
export interface MonitorTimelineRow { time: number; monitor?: number | null; load?: number | null }

export function monitorSeriesIdentity(point: AnalysisPoint): string {
  const windowKind = point.provider?.window_kind || (point.provider?.query_window ? 'query_window' : 'bucket')
  const resolution = windowKind === 'bucket' ? point.provider?.resolution_seconds : undefined
  return JSON.stringify([point.source, point.metric_name, point.unit, point.kind, point.host, point.provider?.name, point.provider?.backend, point.provider?.query_id, point.provider?.definition_hash, point.provider?.backend_profile, point.provider?.mode, point.provider?.evaluation_step_seconds, point.provider?.metric_id, point.provider?.statistic, windowKind, resolution, Object.entries(point.dimensions || {}).sort(([a], [b]) => a.localeCompare(b)), Object.entries(point.provider?.entity_ids || {}).sort(([a], [b]) => a.localeCompare(b))])
}

export function latestMonitorQueryAggregate(points: AnalysisPoint[]): AnalysisPoint | undefined {
  return points.filter(point => point.provider?.query_window).sort((a, b) => Date.parse(b.provider!.bucket_end) - Date.parse(a.provider!.bucket_end) || Date.parse(a.provider!.bucket_start) - Date.parse(b.provider!.bucket_start))[0]
}

export function monitorEntityLabel(point: AnalysisPoint, topology: MonitorEntity[] = []): string {
  const dimensions = point.provider?.name === 'prometheus' ? point.provider.display_labels || point.dimensions || {} : point.provider?.entity_ids || point.dimensions || {}
  return Object.entries(dimensions).map(([kind, id]) => {
    const entity = topology.find(item => item.type === kind && item.id === id && (!dimensions.application || item.entity_ids?.application === dimensions.application))
    return `${kind.replace(/_/g, ' ')}: ${entity?.name ? `${entity.name} (${id})` : id}`
  }).join(', ') || point.host || 'Selected scope'
}

// Insert an explicit null between nonadjacent provider buckets. Merely setting
// connectNulls=false cannot reveal an absent row returned by a sparse provider.
export function buildMonitorTimelineRows(points: AnalysisPoint[], history: MetricsHistoryPoint[], overlay: MonitorLoadOverlay, window?: { from: string; to: string }): MonitorTimelineRow[] {
  const rows: MonitorTimelineRow[] = []
  const sorted = [...points].sort((a, b) => Date.parse(a.timestamp_utc) - Date.parse(b.timestamp_utc))
  sorted.forEach((point, index) => {
    const time = Date.parse(point.timestamp_utc)
    if (!Number.isFinite(time)) return
    const previous = sorted[index - 1]
    const previousEnd = previous?.provider?.name === 'prometheus' && previous.provider.mode === 'evaluation_point' ? Date.parse(previous.timestamp_utc) + (previous.provider.evaluation_step_seconds || 0) * 1000 : previous?.provider && (!previous.provider.window_kind || previous.provider.window_kind === 'bucket') ? Date.parse(previous.provider.bucket_end) : NaN
    if (Number.isFinite(previousEnd) && time > previousEnd) rows.push({ time: previousEnd, monitor: null })
    rows.push({ time, monitor: point.quality === 'valid' && Number.isFinite(point.value) ? point.value : null })
  })
  const field = monitorLoadOverlays[overlay].field
  for (const point of history) {
    const time = point.timestampMs
    if (time === undefined || !Number.isFinite(time) || (window && (time < Date.parse(window.from) || time > Date.parse(window.to)))) continue
    rows.push({ time, load: Number.isFinite(point[field]) ? point[field] : null })
  }
  return rows.sort((a, b) => a.time - b.time)
}

export function providerBucketRange(point: AnalysisPoint, loadStart?: string, loadEnd?: string): string {
  if (!point.provider) return ''
  const { bucket_start, bucket_end, settled } = point.provider
  const overlapsBoundary = Boolean((loadStart && Date.parse(bucket_start) < Date.parse(loadStart)) || (loadEnd && Date.parse(bucket_end) > Date.parse(loadEnd)))
  return `${bucket_start} – ${bucket_end} · ${settled ? 'settled' : 'provisional'}${overlapsBoundary ? ' · partially overlaps the load window' : ''}`
}
