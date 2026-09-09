import { reportAcceptance, reportPopulationOverview, defaultReportPopulation, formatReportPercent, type MetricPopulation } from './reportPresentation'
import type {
  MetricsHistoryPoint,
  ReportAnnotations,
  RunArchiveOverview,
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
  return formatReportPercent(value, digits)
}

function signedDelta(value: number | undefined, unit: 'ms' | '%' | 'count', lowerIsBetter: boolean): { label?: string; tone?: ReportKpi['deltaTone'] } {
  if (!finite(value)) return {}
  if (value === 0) return { label: unit === 'ms' ? '0 ms' : unit === '%' ? '0 pp' : '0', tone: 'neutral' }
  const sign = value > 0 ? '+' : ''
  const label = unit === 'ms'
    ? `${sign}${formatMs(value)}`
    : unit === '%'
      ? `${sign}${formatNumber(value, 2)} pp`
      : `${sign}${formatNumber(value, 1)}`
  const improved = lowerIsBetter ? value < 0 : value > 0
  return { label, tone: improved ? 'good' : 'warn' }
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
    p95: buildBaselineDelta((current.p95_ms ?? Number.NaN) - (baseline.p95_ms ?? Number.NaN), 'ms', true),
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

export function buildReportKpis(report: RunArchiveReport | null, history: MetricsHistoryPoint[], baselineDiff: BaselineOverviewDiff = {}, population: MetricPopulation = defaultReportPopulation(report)): ReportKpi[] {
  const overview = reportPopulationOverview(report, population)
  const completeness = report?.completeness

  return [
    {
      id: 'avgLatency',
      label: 'Avg latency',
      value: formatMs(overview?.avg_response_ms),
      tone: 'neutral',
      delta: baselineDiff.avgLatency?.label,
      deltaTone: baselineDiff.avgLatency?.tone,
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
      tone: !overview ? 'neutral' : overview.error_pct ? 'danger' : 'good',
      delta: baselineDiff.errorRate?.label,
      deltaTone: baselineDiff.errorRate?.tone,
      sparkline: sparkline(history, 'errorPct'),
    },
    {
      id: 'peakTps',
      label: 'Peak attempts/s',
      value: formatNumber(overview?.peak_tps, 1),
      tone: 'neutral',
      delta: baselineDiff.peakTps?.label,
      deltaTone: baselineDiff.peakTps?.tone,
      sparkline: sparkline(history, 'tps'),
    },
    {
      id: 'peakVusers',
      label: 'Peak VUsers',
      value: formatNumber(report?.overview?.peak_active_vusers, 0),
      tone: 'neutral',
      delta: baselineDiff.peakVusers?.label,
      deltaTone: baselineDiff.peakVusers?.tone,
      sparkline: sparkline(history, 'vusers'),
    },
    {
      id: 'completeness',
      label: 'Evidence coverage',
      value: formatPct(completeness?.score_pct, 0),
      tone: completeness?.status && !['complete', 'ok'].includes(completeness.status.toLowerCase()) ? 'warn' : 'good',
      sparkline: [],
    },
  ]
}

export function effectiveReportVerdict(report: RunArchiveReport | null, annotations?: ReportAnnotations | null) {
  // Analyst sign-off is displayed separately and never changes acceptance.
  void annotations
  return reportAcceptance(report)
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
      // Preserve an explicitly observed zero-load interval. Resource samples
      // fill a missing gauge only; they do not override the summary timeline.
      vusers: existing.vusers ?? point.vusers,
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
  const currentStart = current.find(point => finite(point.timestampMs))?.timestampMs
  if (currentStart === undefined) return current
  return current.map(point => {
    if (!finite(point.timestampMs)) return point
    const timestamp = orderedBaseline[0].timestampMs! + point.timestampMs! - currentStart
    if (timestamp > orderedBaseline[orderedBaseline.length - 1].timestampMs!) return point
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
