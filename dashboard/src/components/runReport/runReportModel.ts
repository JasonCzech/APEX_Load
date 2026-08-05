import type {
  MetricsHistoryPoint,
  ReportAnnotations,
  RunArchiveOverview,
  RunArchiveReleaseTrendSummary,
  RunArchiveReport,
  RunArchiveSLARow,
} from '../../types'

export type ReportKpiID = 'avgLatency' | 'p95' | 'errorRate' | 'peakTps' | 'peakVusers' | 'completeness'

export interface ReportKpi {
  id: ReportKpiID
  label: string
  value: string
  tone: 'neutral' | 'good' | 'warn' | 'danger'
  delta?: string
  deltaTone?: 'good' | 'warn' | 'danger' | 'neutral'
  sparkline: number[]
}

export interface TimelineBreachWindow {
  start: string
  end: string
}

export interface BaselineDelta {
  label: string
  value: string
  tone: 'good' | 'warn' | 'danger' | 'neutral'
}

export interface BaselineOverviewDiff {
  avgLatency?: BaselineDelta
  p95?: BaselineDelta
  errorRate?: BaselineDelta
  peakTps?: BaselineDelta
  peakVusers?: BaselineDelta
}

function finite(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

export function formatNumber(value: number | undefined, digits = 0): string {
  if (!finite(value)) return 'n/a'
  return value.toLocaleString(undefined, { maximumFractionDigits: digits, minimumFractionDigits: digits })
}

export function formatMs(value: number | undefined): string {
  if (!finite(value)) return 'n/a'
  return `${formatNumber(value, value >= 100 ? 0 : 1)} ms`
}

export function formatPct(value: number | undefined, digits = 2): string {
  if (!finite(value)) return 'n/a'
  return `${formatNumber(value, digits)}%`
}

function signedDelta(value: number | undefined, unit: 'ms' | '%' | 'count', lowerIsBetter: boolean): { label?: string; tone?: ReportKpi['deltaTone'] } {
  if (!finite(value) || value === 0) return {}
  const sign = value > 0 ? '+' : ''
  const label = unit === 'ms'
    ? `${sign}${formatMs(value)}`
    : unit === '%'
      ? `${sign}${formatPct(value)}`
      : `${sign}${formatNumber(value, 1)}`
  const improved = lowerIsBetter ? value < 0 : value > 0
  return { label, tone: improved ? 'good' : 'warn' }
}

function trendDelta(trend: RunArchiveReleaseTrendSummary | undefined, key: string): number | undefined {
  const deltas = trend?.deltas as Record<string, number | undefined> | undefined
  return deltas?.[key]
}

function sparkline(history: MetricsHistoryPoint[], key: keyof MetricsHistoryPoint): number[] {
  return history
    .map(point => point[key])
    .filter(finite)
    .slice(-24)
}

export function buildBaselineOverviewDiff(current?: RunArchiveOverview, baseline?: RunArchiveOverview): BaselineOverviewDiff {
  if (!current || !baseline) return {}
  return {
    avgLatency: buildBaselineDelta(current.avg_response_ms - baseline.avg_response_ms, 'ms', true),
    p95: buildBaselineDelta((current.p95_ms ?? current.avg_response_ms) - (baseline.p95_ms ?? baseline.avg_response_ms), 'ms', true),
    errorRate: buildBaselineDelta(current.error_pct - baseline.error_pct, '%', true),
    peakTps: buildBaselineDelta(current.peak_tps - baseline.peak_tps, 'count', false),
    peakVusers: buildBaselineDelta(current.peak_active_vusers - baseline.peak_active_vusers, 'count', false),
  }
}

function buildBaselineDelta(value: number, unit: 'ms' | '%' | 'count', lowerIsBetter: boolean): BaselineDelta | undefined {
  const delta = signedDelta(value, unit, lowerIsBetter)
  if (!delta.label || !delta.tone) return undefined
  return { label: delta.label, value: delta.label, tone: delta.tone }
}

export function buildReportKpis(report: RunArchiveReport | null, history: MetricsHistoryPoint[], baselineDiff: BaselineOverviewDiff = {}): ReportKpi[] {
  const overview = report?.overview
  const completeness = report?.completeness
  const releaseTrend = report?.release_trend
  const avgDelta = signedDelta(trendDelta(releaseTrend, 'avg_response_ms_delta'), 'ms', true)
  const errorDelta = signedDelta(trendDelta(releaseTrend, 'error_pct_delta'), '%', true)
  const tpsDelta = signedDelta(trendDelta(releaseTrend, 'peak_tps_delta'), 'count', false)

  return [
    {
      id: 'avgLatency',
      label: 'Avg latency',
      value: formatMs(overview?.avg_response_ms),
      tone: 'neutral',
      delta: avgDelta.label || baselineDiff.avgLatency?.label,
      deltaTone: avgDelta.tone || baselineDiff.avgLatency?.tone,
      sparkline: sparkline(history, 'avgMs'),
    },
    {
      id: 'p95',
      label: 'P95 latency',
      value: formatMs(overview?.p95_ms),
      tone: overview?.p95_ms && overview.p95_ms > 0 ? 'neutral' : 'warn',
      delta: baselineDiff.p95?.label,
      deltaTone: baselineDiff.p95?.tone,
      sparkline: sparkline(history, 'p95'),
    },
    {
      id: 'errorRate',
      label: 'Error rate',
      value: formatPct(overview?.error_pct),
      tone: overview?.error_pct ? 'danger' : 'good',
      delta: errorDelta.label || baselineDiff.errorRate?.label,
      deltaTone: errorDelta.tone || baselineDiff.errorRate?.tone,
      sparkline: sparkline(history, 'errorPct'),
    },
    {
      id: 'peakTps',
      label: 'Peak TPS',
      value: formatNumber(overview?.peak_tps, 1),
      tone: 'neutral',
      delta: tpsDelta.label || baselineDiff.peakTps?.label,
      deltaTone: tpsDelta.tone || baselineDiff.peakTps?.tone,
      sparkline: sparkline(history, 'tps'),
    },
    {
      id: 'peakVusers',
      label: 'Peak VUsers',
      value: formatNumber(overview?.peak_active_vusers, 0),
      tone: 'neutral',
      delta: baselineDiff.peakVusers?.label,
      deltaTone: baselineDiff.peakVusers?.tone,
      sparkline: sparkline(history, 'vusers'),
    },
    {
      id: 'completeness',
      label: 'Completeness',
      value: formatPct(completeness?.score_pct, 0),
      tone: completeness?.status && !['complete', 'ok'].includes(completeness.status.toLowerCase()) ? 'warn' : 'good',
      sparkline: [],
    },
  ]
}

export function effectiveReportVerdict(report: RunArchiveReport | null, annotations?: ReportAnnotations | null) {
  const override = annotations?.verdict_override?.trim()
  if (override) {
    const normalized = override.toLowerCase()
    return {
      status: override,
      failed: normalized.includes('fail'),
      conditional: normalized.includes('conditional') || normalized.includes('blocked') || normalized.includes('review'),
      explanation: annotations?.verdict_explanation?.trim() || report?.verdict?.explanation || '',
    }
  }
  return report?.verdict || {
    status: 'Conditional Pass',
    failed: false,
    conditional: true,
    explanation: 'The archive report has not loaded enough evidence to evaluate this run.',
  }
}

export function mergeTimelineHistory(summaryHistory: MetricsHistoryPoint[], resourceHistory: MetricsHistoryPoint[]): MetricsHistoryPoint[] {
  const byTimestamp = new Map<number, MetricsHistoryPoint>()
  summaryHistory.forEach(point => {
    if (typeof point.timestampMs === 'number') byTimestamp.set(point.timestampMs, { ...point })
  })
  resourceHistory.forEach(point => {
    if (typeof point.timestampMs !== 'number') return
    const existing = byTimestamp.get(point.timestampMs)
    byTimestamp.set(point.timestampMs, existing ? {
      ...existing,
      vusers: existing.vusers || point.vusers,
      cpuProcess: point.cpuProcess,
      cpuFree: point.cpuFree,
      memProcess: point.memProcess,
      memFree: point.memFree,
      netSent: point.netSent,
      netRecv: point.netRecv,
      diskRead: point.diskRead,
      diskWrite: point.diskWrite,
      cpuIOWait: point.cpuIOWait,
      cpuSteal: point.cpuSteal,
      gcPauseMs: point.gcPauseMs,
      containerCPUUsagePct: point.containerCPUUsagePct,
      containerCPUThrottledSecondsRate: point.containerCPUThrottledSecondsRate,
      containerMemoryUsagePct: point.containerMemoryUsagePct,
      containerOOMKillCountDelta: point.containerOOMKillCountDelta,
      netDropsPerSec: point.netDropsPerSec,
      netErrorsPerSec: point.netErrorsPerSec,
      tcpRetransmitsPerSec: point.tcpRetransmitsPerSec,
      diskIOTimePct: point.diskIOTimePct,
    } : {
      ...point,
      tps: Number.NaN,
      avgMs: Number.NaN,
      errorPct: Number.NaN,
      p50: Number.NaN,
      p90: Number.NaN,
      p95: Number.NaN,
      p99: Number.NaN,
      resourceSampleOnly: 1,
    })
  })
  return Array.from(byTimestamp.values()).sort((left, right) => (left.timestampMs ?? 0) - (right.timestampMs ?? 0))
}

export function alignBaselineTimeline(current: MetricsHistoryPoint[], baseline: MetricsHistoryPoint[]): MetricsHistoryPoint[] {
  if (current.length === 0 || baseline.length === 0) return current
  const orderedBaseline = baseline
    .filter(point => finite(point.timestampMs))
    .slice()
    .sort((left, right) => left.timestampMs! - right.timestampMs!)
  if (orderedBaseline.length === 0) return current
  return current.map(point => {
    if (!finite(point.timestampMs)) return point
    const timestamp = point.timestampMs!
    let low = 0
    let high = orderedBaseline.length
    while (low < high) {
      const middle = Math.floor((low + high) / 2)
      if (orderedBaseline[middle].timestampMs! < timestamp) low = middle + 1
      else high = middle
    }
    const after = orderedBaseline[low]
    const before = low > 0 ? orderedBaseline[low - 1] : undefined
    const nearest = !before
      ? after
      : !after
        ? before
        : timestamp - before.timestampMs! <= after.timestampMs! - timestamp ? before : after
    return nearest
      ? {
          ...point,
          baselineTps: nearest.tps,
          baselineAvgMs: nearest.avgMs,
          baselineP95: nearest.p95,
          baselineErrorPct: nearest.errorPct,
          baselineVusers: nearest.vusers,
        }
      : point
  })
}

export function buildBreachWindows(history: MetricsHistoryPoint[], p95SlaMs?: number, maxErrorPct?: number): TimelineBreachWindow[] {
  const windows: TimelineBreachWindow[] = []
  let activeStart: string | undefined
  let activeEnd: string | undefined

  history.forEach(point => {
    if (point.resourceSampleOnly === 1) return
    const breached = (finite(p95SlaMs) && finite(point.p95) && point.p95 > p95SlaMs) ||
      (finite(maxErrorPct) && finite(point.errorPct) && point.errorPct > maxErrorPct)
    if (breached) {
      activeStart = activeStart ?? point.time
      activeEnd = point.time
      return
    }
    if (activeStart && activeEnd) {
      windows.push({ start: activeStart, end: activeEnd })
    }
    activeStart = undefined
    activeEnd = undefined
  })
  if (activeStart && activeEnd) windows.push({ start: activeStart, end: activeEnd })
  return windows
}

export function slaRowsForReport(report: RunArchiveReport | null): RunArchiveSLARow[] {
  return Object.values(report?.sla_rows || {}).sort((left, right) => {
    if (left.result_kind === right.result_kind) return right.volume - left.volume
    const rank: Record<string, number> = { fail: 3, warn: 2, pass: 1, na: 0 }
    return (rank[right.result_kind] ?? 0) - (rank[left.result_kind] ?? 0)
  })
}
