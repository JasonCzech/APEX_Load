import { BrowserDeviceLiveMetrics, BrowserEngineLiveMetrics, BrowserLiveMetrics, BrowserPlatformLiveMetrics } from '../types'
import { comparisonRunColor } from './resultsComparisonChartModel'

export type BrowserBreakdown = 'overall' | 'platform' | 'engine' | 'device'

export interface BrowserBreakdownDatum {
  segment: string
  [key: string]: string | number | undefined
}

export interface BrowserBreakdownSeries {
  dataKey: string
  name: string
  stroke: string
  strokeDasharray?: string
  yAxisId?: 'timings' | 'cls'
  valueFormat?: 'ms' | 'score'
}

export interface BrowserBreakdownSource {
  summary?: Pick<BrowserLiveMetrics, 'avg_lcp_ms' | 'avg_inp_ms' | 'avg_cls_score'>
  devices?: Record<string, BrowserDeviceLiveMetrics>
  platforms?: Record<string, BrowserPlatformLiveMetrics>
  engines?: Record<string, BrowserEngineLiveMetrics>
}

export type BrowserUXSeverity = 'good' | 'watch' | 'poor'

export interface BrowserUXSignal {
  label: string
  value: string
  severity: BrowserUXSeverity
  detail: string
}

export interface BrowserUXInsight {
  score: number
  severity: BrowserUXSeverity
  label: string
  summary: string
  signals: BrowserUXSignal[]
  worstDevice?: string
}

type BrowserUXMetricSource = Pick<
  BrowserLiveMetrics,
  | 'samples'
  | 'avg_lcp_ms'
  | 'avg_inp_ms'
  | 'avg_cls_score'
  | 'avg_blocking_resources'
  | 'avg_long_task_count'
  | 'avg_browser_process_cpu_pct'
  | 'avg_browser_process_mem_mb'
>

export function formatBrowserBreakdownTooltipValue(value: unknown, format: 'ms' | 'score' = 'ms'): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    return 'n/a'
  }
  if (format === 'score') {
    return value.toFixed(3)
  }
  return `${value.toFixed(1)} ms`
}

export function formatBrowserEngineLabel(value?: string): string {
  if (value === 'webkit') return 'WebKit'
  if (value === 'chromium') return 'Chromium'
  return value || 'browser'
}

export function formatDevicePlatformLabel(value?: string): string {
  if (value === 'ios') return 'iOS'
  if (value === 'ipados') return 'iPadOS'
  if (value === 'android') return 'Android'
  if (value === 'desktop') return 'Desktop'
  return value || 'unknown'
}

export function formatBrowserBreakdownTooltipLabel(value: unknown, segmentLabel = 'Segment'): string {
  if (typeof value !== 'string' || value.length === 0) {
    return `${segmentLabel}: unknown`
  }
  return `${segmentLabel}: ${value}`
}

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function scoreThreshold(value: number | undefined, good: number, poor: number): number {
  if (value === undefined) return 100
  if (value <= good) return 100
  if (value >= poor) return 0
  return Math.max(0, Math.round(100 - ((value - good) / (poor - good)) * 100))
}

function severityFromScore(score: number): BrowserUXSeverity {
  if (score >= 85) return 'good'
  if (score >= 65) return 'watch'
  return 'poor'
}

function severityLabel(severity: BrowserUXSeverity): string {
  if (severity === 'good') return 'Healthy'
  if (severity === 'watch') return 'Watch'
  return 'Poor'
}

function formatSignalNumber(value: number | undefined, suffix: string, digits = 1): string {
  if (value === undefined) return 'n/a'
  return `${value.toFixed(digits)}${suffix}`
}

function buildSignal(label: string, value: number | undefined, good: number, poor: number, suffix: string, detail: string, digits = 1): BrowserUXSignal {
  const score = scoreThreshold(value, good, poor)
  return {
    label,
    value: formatSignalNumber(value, suffix, digits),
    severity: severityFromScore(score),
    detail,
  }
}

function buildBrowserUXSignals(metrics: BrowserUXMetricSource): BrowserUXSignal[] {
  return [
    buildSignal('LCP', finiteNumber(metrics.avg_lcp_ms), 2500, 4000, ' ms', 'Largest Contentful Paint reflects perceived page-load speed.'),
    buildSignal('INP', finiteNumber(metrics.avg_inp_ms), 200, 500, ' ms', 'Interaction to Next Paint highlights interaction responsiveness.'),
    buildSignal('CLS', finiteNumber(metrics.avg_cls_score), 0.1, 0.25, '', 'Cumulative Layout Shift flags visual instability.', 3),
    buildSignal('Long tasks', finiteNumber(metrics.avg_long_task_count), 1, 5, '', 'Long main-thread tasks indicate JavaScript contention.'),
    buildSignal('Blocking resources', finiteNumber(metrics.avg_blocking_resources), 2, 10, '', 'Blocking resources point to render-path pressure.'),
    buildSignal('Browser CPU', finiteNumber(metrics.avg_browser_process_cpu_pct), 65, 90, '%', 'High browser CPU can hide server improvements from users.'),
    buildSignal('Browser RSS', finiteNumber(metrics.avg_browser_process_mem_mb), 200, 300, ' MB', 'Browser memory pressure limits safe canary density.'),
  ]
}

export function buildBrowserUXInsight(
  summary?: BrowserUXMetricSource,
  devices?: Record<string, BrowserDeviceLiveMetrics>,
): BrowserUXInsight | null {
  if (!summary || !summary.samples) {
    return null
  }

  const scores = [
    scoreThreshold(finiteNumber(summary.avg_lcp_ms), 2500, 4000),
    scoreThreshold(finiteNumber(summary.avg_inp_ms), 200, 500),
    scoreThreshold(finiteNumber(summary.avg_cls_score), 0.1, 0.25),
    scoreThreshold(finiteNumber(summary.avg_long_task_count), 1, 5),
    scoreThreshold(finiteNumber(summary.avg_blocking_resources), 2, 10),
    scoreThreshold(finiteNumber(summary.avg_browser_process_cpu_pct), 65, 90),
    scoreThreshold(finiteNumber(summary.avg_browser_process_mem_mb), 200, 300),
  ]
  const score = Math.round(scores.reduce((sum, item) => sum + item, 0) / scores.length)
  const severity = severityFromScore(score)
  const deviceEntries = Object.entries(devices ?? {})
    .map(([profile, metrics]) => ({
      profile,
      score: buildBrowserUXInsight(metrics, undefined)?.score ?? 100,
    }))
    .sort((left, right) => left.score - right.score || left.profile.localeCompare(right.profile))
  const worstDevice = deviceEntries.length > 0 ? deviceEntries[0].profile : undefined
  const weakSignals = buildBrowserUXSignals(summary).filter(signal => signal.severity !== 'good')

  return {
    score,
    severity,
    label: severityLabel(severity),
    summary: weakSignals.length > 0
      ? `${weakSignals.length} UX signal${weakSignals.length === 1 ? '' : 's'} need attention${worstDevice ? `; weakest device is ${worstDevice}` : ''}.`
      : `Browser UX signals are within the current canary thresholds${worstDevice ? `; weakest device is ${worstDevice}` : ''}.`,
    signals: buildBrowserUXSignals(summary),
    worstDevice,
  }
}

export function getBrowserBreakdownSegmentLabel(breakdown: BrowserBreakdown): string {
  if (breakdown === 'overall') return 'Overview'
  if (breakdown === 'platform') return 'Platform'
  if (breakdown === 'engine') return 'Engine'
  return 'Device'
}

function derivePlatformBreakdownData(devices?: Record<string, BrowserDeviceLiveMetrics>): BrowserBreakdownDatum[] {
  const entries = Object.values(devices ?? {})
  if (entries.length === 0) {
    return []
  }

  const grouped = new Map<string, { segment: string; samples: number; lcpTotal: number; inpTotal: number; clsTotal: number }>()
  for (const metrics of entries) {
    if (!metrics) continue
    const segment = formatDevicePlatformLabel(metrics.device_platform)
    const current = grouped.get(segment) ?? {
      segment,
      samples: 0,
      lcpTotal: 0,
      inpTotal: 0,
      clsTotal: 0,
    }
    const samples = metrics.samples || 0
    current.samples += samples
    current.lcpTotal += (metrics.avg_lcp_ms ?? 0) * samples
    current.inpTotal += (metrics.avg_inp_ms ?? 0) * samples
    current.clsTotal += (metrics.avg_cls_score ?? 0) * samples
    grouped.set(segment, current)
  }

  return Array.from(grouped.values())
    .map(entry => ({
      segment: entry.segment,
      lcp: entry.samples > 0 ? entry.lcpTotal / entry.samples : undefined,
      inp: entry.samples > 0 ? entry.inpTotal / entry.samples : undefined,
      cls: entry.samples > 0 ? entry.clsTotal / entry.samples : undefined,
    }))
    .sort((left, right) => left.segment.localeCompare(right.segment))
}

function deriveEngineBreakdownData(devices?: Record<string, BrowserDeviceLiveMetrics>): BrowserBreakdownDatum[] {
  const entries = Object.values(devices ?? {})
  if (entries.length === 0) {
    return []
  }

  const grouped = new Map<string, { segment: string; samples: number; lcpTotal: number; inpTotal: number; clsTotal: number }>()
  for (const metrics of entries) {
    if (!metrics) continue
    const segment = formatBrowserEngineLabel(metrics.browser_engine)
    const current = grouped.get(segment) ?? {
      segment,
      samples: 0,
      lcpTotal: 0,
      inpTotal: 0,
      clsTotal: 0,
    }
    const samples = metrics.samples || 0
    current.samples += samples
    current.lcpTotal += (metrics.avg_lcp_ms ?? 0) * samples
    current.inpTotal += (metrics.avg_inp_ms ?? 0) * samples
    current.clsTotal += (metrics.avg_cls_score ?? 0) * samples
    grouped.set(segment, current)
  }

  return Array.from(grouped.values())
    .map(entry => ({
      segment: entry.segment,
      lcp: entry.samples > 0 ? entry.lcpTotal / entry.samples : undefined,
      inp: entry.samples > 0 ? entry.inpTotal / entry.samples : undefined,
      cls: entry.samples > 0 ? entry.clsTotal / entry.samples : undefined,
    }))
    .sort((left, right) => left.segment.localeCompare(right.segment))
}

export function buildBrowserBreakdownData(breakdown: BrowserBreakdown, source: BrowserBreakdownSource): BrowserBreakdownDatum[] {
  if (breakdown === 'overall') {
    return source.summary
      ? [{
          segment: 'Overall',
          lcp: source.summary.avg_lcp_ms,
          inp: source.summary.avg_inp_ms,
          cls: source.summary.avg_cls_score,
        }]
      : []
  }

  if (breakdown === 'platform') {
    const archivedEntries = Object.values(source.platforms ?? {})
    if (archivedEntries.length > 0) {
      return archivedEntries
        .map(metrics => ({
          segment: formatDevicePlatformLabel(metrics.device_platform),
          lcp: metrics.avg_lcp_ms,
          inp: metrics.avg_inp_ms,
          cls: metrics.avg_cls_score,
        }))
        .sort((left, right) => left.segment.localeCompare(right.segment))
    }
    return derivePlatformBreakdownData(source.devices)
  }

  if (breakdown === 'engine') {
    const archivedEntries = Object.values(source.engines ?? {})
    if (archivedEntries.length > 0) {
      return archivedEntries
        .map(metrics => ({
          segment: formatBrowserEngineLabel(metrics.browser_engine),
          lcp: metrics.avg_lcp_ms,
          inp: metrics.avg_inp_ms,
          cls: metrics.avg_cls_score,
        }))
        .sort((left, right) => left.segment.localeCompare(right.segment))
    }
    return deriveEngineBreakdownData(source.devices)
  }

  return Object.entries(source.devices ?? {})
    .map(([profile, metrics]) => ({
      segment: profile,
      lcp: metrics.avg_lcp_ms,
      inp: metrics.avg_inp_ms,
      cls: metrics.avg_cls_score,
    }))
    .sort((left, right) => left.segment.localeCompare(right.segment))
}

export function getBrowserBreakdownTitle(breakdown: BrowserBreakdown, context: 'Comparison' | 'Breakdown', metricsLabel: string): string {
  if (breakdown === 'overall') return `Browser Overview ${context} (${metricsLabel})`
  if (breakdown === 'platform') return `Browser Platform ${context} (${metricsLabel})`
  if (breakdown === 'engine') return `Browser Engine ${context} (${metricsLabel})`
  return `Browser Device ${context} (${metricsLabel})`
}

export function getBrowserBreakdownChartCopy(series: BrowserBreakdownSeries[]): string {
  if (series.some(item => item.valueFormat === 'score')) {
    return 'Series: LCP and INP on the left axis, CLS on the right axis.'
  }
  if (series.some(item => item.strokeDasharray)) {
    return 'Series: solid LCP and dashed INP for each selected run.'
  }
  return 'Series: browser metric comparison by segment.'
}

export function buildArchiveBrowserBreakdownSeries(): BrowserBreakdownSeries[] {
  return [
    {
      dataKey: 'lcp',
      name: 'LCP',
      stroke: 'var(--accent)',
      yAxisId: 'timings',
      valueFormat: 'ms',
    },
    {
      dataKey: 'inp',
      name: 'INP',
      stroke: 'var(--accent-2, #d97706)',
      yAxisId: 'timings',
      valueFormat: 'ms',
    },
    {
      dataKey: 'cls',
      name: 'CLS',
      stroke: 'var(--chart-4, #10b981)',
      yAxisId: 'cls',
      valueFormat: 'score',
    },
  ]
}

export function buildComparisonBrowserBreakdownSeries(runs: Array<{ id: string; name: string }>): BrowserBreakdownSeries[] {
  return runs.flatMap((run, index) => {
    const color = comparisonRunColor(index)
    return [
      {
        dataKey: run.id,
        name: `${run.name} LCP`,
        stroke: color,
        valueFormat: 'ms',
      },
      {
        dataKey: `${run.id}_inp`,
        name: `${run.name} INP`,
        stroke: color,
        strokeDasharray: '4 2',
        valueFormat: 'ms',
      },
    ]
  })
}
