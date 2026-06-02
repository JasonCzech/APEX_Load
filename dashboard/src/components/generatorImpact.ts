import type { GeneratorResourceSnapshot, LiveMetrics, MetricsHistoryPoint, ResourceSample, SystemResources } from '../types'

export type GeneratorImpactVerdict = 'clear' | 'warning' | 'danger' | 'unknown'

export interface GeneratorImpactRow {
  id: string
  target?: string
  region?: string
  activeVusers?: number
  tps?: number
  p95Ms?: number
  p99Ms?: number
  errorPct?: number
  verdict: GeneratorImpactVerdict
  reasons: string[]
  metrics: Partial<MetricsHistoryPoint>
}

export interface GeneratorImpactView {
  verdict: GeneratorImpactVerdict
  rows: GeneratorImpactRow[]
}

type ResourceLike = Partial<SystemResources> | Partial<ResourceSample> | undefined

const SLOW_TRANSACTION_MS = 1000

const IMPACT_HISTORY_KEYS: Array<keyof MetricsHistoryPoint> = [
  'cpuIOWait',
  'cpuSteal',
  'gcPauseMs',
  'containerCPUUsagePct',
  'containerCPUThrottledSecondsRate',
  'containerMemoryUsagePct',
  'containerOOMKillCountDelta',
  'netDropsPerSec',
  'netErrorsPerSec',
  'tcpRetransmitsPerSec',
  'diskIOTimePct',
]

function finiteValue(value: number | undefined): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function optionalSum(...values: Array<number | undefined>): number | undefined {
  const finiteValues = values.filter((value): value is number => finiteValue(value) !== undefined)
  if (finiteValues.length === 0) return undefined
  return finiteValues.reduce((sum, value) => sum + value, 0)
}

function normalizeVerdict(value?: string): GeneratorImpactVerdict | undefined {
  return value === 'clear' || value === 'warning' || value === 'danger' || value === 'unknown'
    ? value
    : undefined
}

function worseVerdict(left: GeneratorImpactVerdict, right: GeneratorImpactVerdict): GeneratorImpactVerdict {
  const rank: Record<GeneratorImpactVerdict, number> = { unknown: 0, clear: 1, warning: 2, danger: 3 }
  return rank[right] > rank[left] ? right : left
}

function addReason(reasons: string[], condition: boolean, reason: string) {
  if (condition && !reasons.includes(reason)) {
    reasons.push(reason)
  }
}

export function resourceImpactHistoryValues(resources: ResourceLike): Partial<MetricsHistoryPoint> {
  if (!resources) {
    return {}
  }
  return {
    cpuIOWait: finiteValue(resources.cpu_iowait_pct),
    cpuSteal: finiteValue(resources.cpu_steal_pct),
    gcPauseMs: finiteValue(resources.gc_pause_ms),
    containerCPUUsagePct: finiteValue(resources.container_cpu_usage_pct),
    containerCPUThrottledSecondsRate: finiteValue(resources.container_cpu_throttled_seconds_rate),
    containerMemoryUsagePct: finiteValue(resources.container_memory_usage_pct),
    containerOOMKillCountDelta: finiteValue(resources.container_oom_kill_count_delta),
    netDropsPerSec: optionalSum(resources.net_drop_in_per_sec, resources.net_drop_out_per_sec),
    netErrorsPerSec: optionalSum(resources.net_error_in_per_sec, resources.net_error_out_per_sec),
    tcpRetransmitsPerSec: finiteValue(resources.tcp_retransmits_per_sec),
    diskIOTimePct: finiteValue(resources.disk_io_time_pct),
  }
}

function hasImpactMetrics(metrics: Partial<MetricsHistoryPoint>) {
  return IMPACT_HISTORY_KEYS.some(key => finiteValue(metrics[key] as number | undefined) !== undefined)
}

function hasResourceSignals(resources: ResourceLike) {
  if (!resources) return false
  return [
    resources.cpu_process_pct,
    resources.cpu_free_pct,
    resources.mem_process_mb,
    resources.mem_free_mb,
    resources.net_sent_kbps,
    resources.net_recv_kbps,
    resources.disk_read_kbps,
    resources.disk_write_kbps,
    ...Object.values(resourceImpactHistoryValues(resources)),
  ].some(value => finiteValue(value as number | undefined) !== undefined)
}

export function classifyGeneratorImpact(
  resources: ResourceLike,
  p95Ms?: number,
  errorPct?: number,
): { verdict: GeneratorImpactVerdict; reasons: string[]; metrics: Partial<MetricsHistoryPoint> } {
  const metrics = resourceImpactHistoryValues(resources)
  if (!hasResourceSignals(resources)) {
    return { verdict: 'unknown', reasons: [], metrics }
  }

  const reasons: string[] = []
  addReason(
    reasons,
    (resources?.container_cpu_throttled_periods_delta ?? 0) > 0 || (metrics.containerCPUThrottledSecondsRate ?? 0) > 0,
    'CPU throttling',
  )
  addReason(
    reasons,
    (resources?.container_cpu_usage_pct ?? 0) >= 95 || (resources?.cpu_process_pct ?? 0) >= 95,
    'CPU saturation',
  )
  addReason(reasons, (metrics.containerMemoryUsagePct ?? 0) >= 90, 'Memory near limit')
  addReason(reasons, (metrics.containerOOMKillCountDelta ?? 0) > 0, 'OOM kill observed')
  addReason(reasons, (metrics.gcPauseMs ?? 0) >= 100, 'GC pause pressure')
  addReason(
    reasons,
    (metrics.netDropsPerSec ?? 0) > 0 || (metrics.netErrorsPerSec ?? 0) > 0 || (metrics.tcpRetransmitsPerSec ?? 0) > 0,
    'Network loss/retransmits',
  )
  addReason(
    reasons,
    (metrics.diskIOTimePct ?? 0) >= 90 || (resources?.disk_weighted_io_time_pct ?? 0) >= 90,
    'Disk I/O pressure',
  )
  addReason(reasons, (metrics.cpuIOWait ?? 0) >= 20, 'CPU I/O wait')
  addReason(reasons, (metrics.cpuSteal ?? 0) >= 10, 'CPU steal')

  if (reasons.length === 0) {
    return { verdict: 'clear', reasons: [], metrics }
  }
  const degraded = (errorPct ?? 0) > 0 || (p95Ms ?? 0) >= SLOW_TRANSACTION_MS
  return { verdict: degraded ? 'danger' : 'warning', reasons, metrics }
}

function rowFromSnapshot(snapshot: GeneratorResourceSnapshot): GeneratorImpactRow {
  const classified = classifyGeneratorImpact(snapshot.resources, snapshot.p95_ms, snapshot.error_pct)
  const explicitVerdict = normalizeVerdict(snapshot.saturation_state)
  return {
    id: snapshot.generator_id || snapshot.target || 'generator',
    target: snapshot.target,
    region: snapshot.region,
    activeVusers: snapshot.active_vusers,
    tps: snapshot.tps,
    p95Ms: snapshot.p95_ms,
    p99Ms: snapshot.p99_ms,
    errorPct: snapshot.error_pct,
    verdict: explicitVerdict && explicitVerdict !== 'unknown' ? explicitVerdict : classified.verdict,
    reasons: snapshot.impact_reasons?.length ? snapshot.impact_reasons : classified.reasons,
    metrics: classified.metrics,
  }
}

function rowFromLiveResources(metrics: LiveMetrics): GeneratorImpactRow | null {
  if (!metrics.resources) return null
  const classified = classifyGeneratorImpact(metrics.resources, metrics.p95_ms, metrics.error_pct)
  return {
    id: 'local',
    target: 'Local generator',
    activeVusers: metrics.active_vusers,
    tps: metrics.tps,
    p95Ms: metrics.p95_ms,
    p99Ms: metrics.p99_ms,
    errorPct: metrics.error_pct,
    verdict: classified.verdict,
    reasons: classified.reasons,
    metrics: classified.metrics,
  }
}

function rowFromHistoryPoint(point: MetricsHistoryPoint | undefined): GeneratorImpactRow | null {
  if (!point) return null
  const metrics = Object.fromEntries(
    IMPACT_HISTORY_KEYS
      .map(key => [key, finiteValue(point[key] as number | undefined)])
      .filter((entry): entry is [keyof MetricsHistoryPoint, number] => entry[1] !== undefined),
  ) as Partial<MetricsHistoryPoint>
  if (!hasImpactMetrics(metrics)) return null
  const resources: ResourceLike = {
    cpu_iowait_pct: metrics.cpuIOWait,
    cpu_steal_pct: metrics.cpuSteal,
    gc_pause_ms: metrics.gcPauseMs,
    container_cpu_usage_pct: metrics.containerCPUUsagePct,
    container_cpu_throttled_seconds_rate: metrics.containerCPUThrottledSecondsRate,
    container_memory_usage_pct: metrics.containerMemoryUsagePct,
    container_oom_kill_count_delta: metrics.containerOOMKillCountDelta,
    net_drop_in_per_sec: metrics.netDropsPerSec,
    net_error_in_per_sec: metrics.netErrorsPerSec,
    tcp_retransmits_per_sec: metrics.tcpRetransmitsPerSec,
    disk_io_time_pct: metrics.diskIOTimePct,
  }
  const classified = classifyGeneratorImpact(resources, point.p95, point.errorPct)
  return {
    id: 'archive',
    target: 'Archived resources',
    activeVusers: point.vusers,
    tps: point.tps,
    p95Ms: point.p95,
    p99Ms: point.p99,
    errorPct: point.errorPct,
    verdict: classified.verdict,
    reasons: classified.reasons,
    metrics,
  }
}

function applyGeneratorImbalance(rows: GeneratorImpactRow[]): GeneratorImpactRow[] {
  if (rows.length < 2) return rows
  const activeRows = rows.filter(row => (row.activeVusers ?? 0) > 0)
  if (activeRows.length < 2) return rows
  const counts = activeRows.map(row => row.activeVusers ?? 0)
  const max = Math.max(...counts)
  const min = Math.min(...counts)
  if (min <= 0 || max / min < 1.5) return rows

  const degraded = rows.some(row => (row.errorPct ?? 0) > 0 || (row.p95Ms ?? 0) >= SLOW_TRANSACTION_MS)
  const imbalanceVerdict: GeneratorImpactVerdict = degraded ? 'danger' : 'warning'
  return rows.map(row => ({
    ...row,
    verdict: worseVerdict(row.verdict, imbalanceVerdict),
    reasons: row.activeVusers === max || row.activeVusers === min
      ? [...new Set([...row.reasons, 'Generator imbalance'])]
      : row.reasons,
  }))
}

export function buildGeneratorImpactView(
  latestMetrics: LiveMetrics | undefined,
  history: MetricsHistoryPoint[] = [],
): GeneratorImpactView {
  let rows = latestMetrics?.generator_resources
    ? Object.values(latestMetrics.generator_resources)
        .map(rowFromSnapshot)
        .sort((left, right) => left.id.localeCompare(right.id))
    : []

  if (rows.length === 0) {
    const localRow = latestMetrics ? rowFromLiveResources(latestMetrics) : null
    if (localRow) {
      rows = [localRow]
    }
  }

  if (rows.length === 0) {
    const historyRow = rowFromHistoryPoint(history[history.length - 1])
    if (historyRow) {
      rows = [historyRow]
    }
  }

  rows = applyGeneratorImbalance(rows)
  const verdict = rows.reduce<GeneratorImpactVerdict>((current, row) => worseVerdict(current, row.verdict), rows.length > 0 ? 'clear' : 'unknown')
  return { verdict, rows }
}
