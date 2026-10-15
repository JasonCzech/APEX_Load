import { BrowserDeviceLiveMetrics, BrowserEngineLiveMetrics, BrowserLiveMetrics, BrowserPlatformLiveMetrics } from '../types'
import { comparisonRunColor } from './resultsComparisonChartModel'
import { MISSING_VALUE } from './runReport/reportPresentation'

export type BrowserBreakdown = 'overall' | 'platform' | 'engine' | 'device'

export interface BrowserBreakdownDatum {
  segment: string
  cohortKey?: string
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
  summary?: Pick<BrowserLiveMetrics, 'samples' | 'avg_lcp_ms' | 'avg_inp_ms' | 'avg_cls_score'>
  devices?: Record<string, BrowserDeviceLiveMetrics>
  platforms?: Record<string, BrowserPlatformLiveMetrics>
  engines?: Record<string, BrowserEngineLiveMetrics>
}

export const BROWSER_UX_MODEL_VERSION = 'legacy-summary-availability-v2' as const
export const BROWSER_UX_APPLICABILITY = 'Legacy browser action summaries. Document finality and per-metric populations are unavailable; this is not a standards-qualified Core Web Vitals score.'

export type BrowserUXSeverity = 'good' | 'watch' | 'poor' | 'unscored'

export interface BrowserUXSignal {
  label: string
  value: string
  severity: BrowserUXSeverity
  detail: string
  status: 'observed' | 'missing' | 'invalid'
  score: number | null
  weight: number
  contribution: number | null
  reason?: string
}

export interface BrowserUXInsight {
  version: typeof BROWSER_UX_MODEL_VERSION
  score: number | null
  severity: BrowserUXSeverity
  label: string
  summary: string
  applicability: string
  signals: BrowserUXSignal[]
  coverage: { observed: number; required: number }
  sampleReason?: string
  worstDevice?: string
  rankedDevices: number
  unrankedDevices: number
  unrankedDeviceReasons: Array<{ profile: string; reason: string }>
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
  const observed = finiteBrowserMetric(value)
  if (observed === undefined) return MISSING_VALUE
  if (format === 'score') return observed.toFixed(3)
  return `${observed.toFixed(1)} ms`
}

export function formatBrowserEngineLabel(value?: unknown): string {
  if (value === 'webkit') return 'WebKit'
  if (value === 'chromium') return 'Chromium'
  return typeof value === 'string' && value.length > 0 ? value : 'browser'
}

export function formatDevicePlatformLabel(value?: unknown): string {
  if (value === 'ios') return 'iOS'
  if (value === 'ipados') return 'iPadOS'
  if (value === 'android') return 'Android'
  if (value === 'desktop') return 'Desktop'
  return typeof value === 'string' && value.length > 0 ? value : 'unknown'
}

export function formatBrowserBreakdownTooltipLabel(value: unknown, segmentLabel = 'Segment'): string {
  if (typeof value !== 'string' || value.length === 0) {
    return `${segmentLabel}: unknown`
  }
  return `${segmentLabel}: ${value}`
}

// Explicitly present zero is valid. Legacy JSON may omit zero; absence cannot
// be recovered as an observation by the dashboard.
export function finiteBrowserMetric(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined
}

// This guards record shape only. Every measurement and population still
// needs independent admission; TypeScript declarations do not validate JSON.
export function browserMetricRecord(value: unknown): BrowserDeviceLiveMetrics | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as BrowserDeviceLiveMetrics : undefined
}

export function formatBrowserSampleCount(value: unknown): string {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? `${value} samples` : 'Sample count unavailable'
}

export function validBrowserSamples(value: unknown): boolean {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function scoreThreshold(value: number, good: number, poor: number): number {
  if (value <= good) return 100
  if (value >= poor) return 0
  return Math.max(0, Math.round(100 - ((value - good) / (poor - good)) * 100))
}

function severityFromScore(score: number): BrowserUXSeverity {
  if (score >= 85) return 'good'
  if (score >= 65) return 'watch'
  return 'poor'
}

// Each required component has a fixed, nonnegative weight of one. Missing
// components never receive a score and never renormalize the other weights.
const browserUXComponents = [
  { key: 'avg_lcp_ms', label: 'LCP', good: 2500, poor: 4000, suffix: ' ms', digits: 1 },
  { key: 'avg_inp_ms', label: 'INP', good: 200, poor: 500, suffix: ' ms', digits: 1 },
  { key: 'avg_cls_score', label: 'CLS', good: 0.1, poor: 0.25, suffix: '', digits: 3 },
  { key: 'avg_long_task_count', label: 'Long tasks', good: 1, poor: 5, suffix: '', digits: 1 },
  { key: 'avg_blocking_resources', label: 'Blocking resources', good: 2, poor: 10, suffix: '', digits: 1 },
  { key: 'avg_browser_process_cpu_pct', label: 'Browser CPU', good: 65, poor: 90, suffix: '%', digits: 1 },
  { key: 'avg_browser_process_mem_mb', label: 'Browser RSS', good: 200, poor: 300, suffix: ' MB', digits: 1 },
] as const

function assessBrowserUX(summary: BrowserUXMetricSource): BrowserUXInsight {
  const record = browserMetricRecord(summary)
  const samplesValid = validBrowserSamples(record?.samples)
  const sampleReason = !record ? 'Malformed summary: expected an object.' : samplesValid ? undefined : 'Sample count must be a positive safe integer.'
  const signals: BrowserUXSignal[] = browserUXComponents.map(component => {
    const raw = record?.[component.key]
    const value = finiteBrowserMetric(raw)
    const status = !record ? 'invalid' : raw === undefined ? 'missing' : value === undefined ? 'invalid' : 'observed'
    const score = value !== undefined && samplesValid ? scoreThreshold(value, component.good, component.poor) : null
    const reason = !record ? sampleReason : status === 'missing' ? 'Value was not captured in this summary.'
      : status === 'invalid' ? 'Value must be finite and nonnegative.'
        : sampleReason
    return {
      label: component.label,
      value: value === undefined ? MISSING_VALUE : `${value.toFixed(component.digits)}${component.suffix}`,
      severity: score === null ? 'unscored' : severityFromScore(score),
      detail: `Legacy threshold: ${component.good}${component.suffix} to ${component.poor}${component.suffix}; weight 1/7.`,
      status,
      score,
      weight: 1,
      contribution: score === null ? null : score / browserUXComponents.length,
      reason,
    }
  })
  const observed = signals.filter(signal => signal.status === 'observed').length
  const score = samplesValid && observed === signals.length
    ? Math.round(signals.reduce((sum, signal) => sum + (signal.score ?? 0), 0) / signals.length)
    : null
  const severity = score === null ? 'unscored' : severityFromScore(score)
  const weakCount = signals.filter(signal => signal.score !== null && signal.severity !== 'good').length
  return {
    version: BROWSER_UX_MODEL_VERSION,
    score,
    severity,
    label: score === null ? 'Unscored' : severity === 'good' ? 'Within legacy thresholds' : severity === 'watch' ? 'Watch' : 'Poor',
    summary: score === null
      ? `Overall estimate unavailable: ${observed}/${signals.length} required fields available.${sampleReason ? ` ${sampleReason}` : ''}${weakCount ? ` ${weakCount} observed component${weakCount === 1 ? '' : 's'} need attention.` : ''}`
      : weakCount ? `${weakCount} observed component${weakCount === 1 ? '' : 's'} need attention.` : 'Observed components are within the legacy thresholds.',
    applicability: BROWSER_UX_APPLICABILITY,
    signals,
    coverage: { observed, required: signals.length },
    sampleReason,
    rankedDevices: 0,
    unrankedDevices: 0,
    unrankedDeviceReasons: [],
  }
}

export function buildBrowserUXInsight(
  summary?: BrowserUXMetricSource,
  devices?: Record<string, BrowserDeviceLiveMetrics>,
): BrowserUXInsight | null {
  if (summary === undefined) return null
  const insight = assessBrowserUX(summary)
  const deviceEntries = Object.entries(devices ?? {}).map(([profile, metrics]) => {
    const assessment = assessBrowserUX(metrics)
    const unavailable = assessment?.signals.filter(signal => signal.status !== 'observed') ?? []
    return {
      profile,
      score: assessment?.score ?? null,
      reason: [assessment?.sampleReason, unavailable.length ? `Required fields unavailable: ${unavailable.map(signal => `${signal.label} (${signal.status})`).join(', ')}.` : undefined].filter(Boolean).join(' ') || 'Device summary is unavailable.',
    }
  })
  const ranked = deviceEntries.filter((entry): entry is { profile: string; score: number; reason: string } => entry.score !== null)
    .sort((left, right) => left.score - right.score || left.profile.localeCompare(right.profile))
  return {
    ...insight,
    worstDevice: ranked[0]?.profile,
    rankedDevices: ranked.length,
    unrankedDevices: deviceEntries.length - ranked.length,
    unrankedDeviceReasons: deviceEntries.filter(entry => entry.score === null).map(({ profile, reason }) => ({ profile, reason })),
  }
}

// Reproduce the previously published arithmetic only when its version is
// explicitly requested. Current views must never use this unavailable-as-100
// recipe to assess collection or metric availability.
export function reproduceLegacyBrowserUXScore(
  summary: BrowserUXMetricSource | undefined,
  version: 'legacy-canary-v1',
): { version: 'legacy-canary-v1'; score: number | null } {
  if (version !== 'legacy-canary-v1') throw new Error('Unsupported legacy scoring version')
  if (!summary || !summary.samples) return { version, score: null }
  const scores = browserUXComponents.map(component => {
    const value = summary[component.key]
    return typeof value === 'number' && Number.isFinite(value) ? scoreThreshold(value, component.good, component.poor) : 100
  })
  return { version, score: Math.round(scores.reduce((sum, value) => sum + value, 0) / scores.length) }
}

export function getBrowserBreakdownSegmentLabel(breakdown: BrowserBreakdown): string {
  if (breakdown === 'overall') return 'Overview'
  if (breakdown === 'platform') return 'Platform'
  if (breakdown === 'engine') return 'Engine'
  return 'Device'
}

type BrowserBreakdownMetric = 'avg_lcp_ms' | 'avg_inp_ms' | 'avg_cls_score'

function cohortAverage(entries: BrowserDeviceLiveMetrics[], key: BrowserBreakdownMetric): number | undefined {
  let samples = 0
  let mean = 0
  for (const metrics of entries) {
    const record = browserMetricRecord(metrics)
    if (!record) return undefined
    // LCP averages over a cohort's page loads, the rest over its samples. A
    // cohort that loaded no page (a single-page app's steps) has no LCP to
    // add; an older summary without the count keeps its LCP.
    if (key === 'avg_lcp_ms' && record.document_loads === 0 && record.avg_lcp_ms === undefined && validBrowserSamples(record.samples)) continue
    const value = finiteBrowserMetric(record[key])
    if (!validBrowserSamples(record.samples) || value === undefined) return undefined
    const weight = key === 'avg_lcp_ms' && validBrowserSamples(record.document_loads) ? record.document_loads! : record.samples
    const nextSamples = samples + weight
    if (!Number.isSafeInteger(nextSamples)) return undefined
    // Weight before adding so a valid large finite value cannot overflow just
    // because its sample count is large. Never drop an unavailable cohort.
    mean += (value - mean) * (weight / nextSamples)
    if (finiteBrowserMetric(mean) === undefined) return undefined
    samples = nextSamples
  }
  return samples > 0 ? mean : undefined
}

function orderBreakdownData(left: BrowserBreakdownDatum, right: BrowserBreakdownDatum): number {
  const labelOrder = left.segment.localeCompare(right.segment)
  if (labelOrder) return labelOrder
  const leftKey = left.cohortKey ?? ''
  const rightKey = right.cohortKey ?? ''
  return leftKey < rightKey ? -1 : leftKey > rightKey ? 1 : 0
}

function deriveBreakdownData(devices: Record<string, BrowserDeviceLiveMetrics> | undefined, kind: 'engine' | 'platform'): BrowserBreakdownDatum[] {
  const grouped = new Map<string, { segment: string; entries: BrowserDeviceLiveMetrics[]; unavailable: boolean }>()
  for (const [profile, value] of Object.entries(devices ?? {})) {
    const metrics = browserMetricRecord(value)
    const identity = kind === 'engine' ? metrics?.browser_engine : metrics?.device_platform
    if (!metrics || typeof identity !== 'string' || identity.length === 0) {
      const cohortKey = JSON.stringify([kind, 'unavailable', profile])
      grouped.set(cohortKey, { segment: `${kind === 'engine' ? 'Engine' : 'Platform'} unavailable (${profile})`, entries: [value], unavailable: true })
      continue
    }
    // Exact identity tuples are separate from friendly labels. Neither case
    // aliases nor a label resembling an unavailable row may merge cohorts.
    const cohortKey = JSON.stringify([kind, identity])
    const current = grouped.get(cohortKey) ?? { segment: kind === 'engine' ? formatBrowserEngineLabel(identity) : formatDevicePlatformLabel(identity), entries: [], unavailable: false }
    current.entries.push(metrics)
    grouped.set(cohortKey, current)
  }
  return Array.from(grouped, ([cohortKey, { segment, entries, unavailable }]) => ({
    segment,
    cohortKey,
    lcp: unavailable ? undefined : cohortAverage(entries, 'avg_lcp_ms'),
    inp: unavailable ? undefined : cohortAverage(entries, 'avg_inp_ms'),
    cls: unavailable ? undefined : cohortAverage(entries, 'avg_cls_score'),
  })).sort(orderBreakdownData)
}

function observedMetric(metrics: Pick<BrowserLiveMetrics, 'samples' | BrowserBreakdownMetric>, key: BrowserBreakdownMetric): number | undefined {
  const record = browserMetricRecord(metrics)
  return record && validBrowserSamples(record.samples) ? finiteBrowserMetric(record[key]) : undefined
}

export function buildBrowserBreakdownData(breakdown: BrowserBreakdown, source: BrowserBreakdownSource): BrowserBreakdownDatum[] {
  if (breakdown === 'overall') {
    return source.summary !== undefined
      ? [{
          segment: 'Overall',
          cohortKey: JSON.stringify(['overall']),
          lcp: observedMetric(source.summary, 'avg_lcp_ms'),
          inp: observedMetric(source.summary, 'avg_inp_ms'),
          cls: observedMetric(source.summary, 'avg_cls_score'),
        }]
      : []
  }

  if (breakdown === 'platform') {
    const archivedEntries = Object.entries(source.platforms ?? {})
    if (archivedEntries.length > 0) {
      return archivedEntries
        .map(([identity, value]) => ({
          segment: formatDevicePlatformLabel(browserMetricRecord(value)?.device_platform ?? identity),
          cohortKey: JSON.stringify(['platform', identity]),
          lcp: observedMetric(value, 'avg_lcp_ms'),
          inp: observedMetric(value, 'avg_inp_ms'),
          cls: observedMetric(value, 'avg_cls_score'),
        }))
        .sort(orderBreakdownData)
    }
    return deriveBreakdownData(source.devices, 'platform')
  }

  if (breakdown === 'engine') {
    const archivedEntries = Object.entries(source.engines ?? {})
    if (archivedEntries.length > 0) {
      return archivedEntries
        .map(([identity, value]) => ({
          segment: formatBrowserEngineLabel(browserMetricRecord(value)?.browser_engine ?? identity),
          cohortKey: JSON.stringify(['engine', identity]),
          lcp: observedMetric(value, 'avg_lcp_ms'),
          inp: observedMetric(value, 'avg_inp_ms'),
          cls: observedMetric(value, 'avg_cls_score'),
        }))
        .sort(orderBreakdownData)
    }
    return deriveBreakdownData(source.devices, 'engine')
  }

  return Object.entries(source.devices ?? {})
    .map(([profile, metrics]) => ({
      segment: profile,
      cohortKey: JSON.stringify(['device', profile]),
      lcp: observedMetric(metrics, 'avg_lcp_ms'),
      inp: observedMetric(metrics, 'avg_inp_ms'),
      cls: observedMetric(metrics, 'avg_cls_score'),
    }))
    .sort(orderBreakdownData)
}


export function buildComparisonBrowserBreakdownData(
  breakdown: BrowserBreakdown,
  runs: Array<{ id: string; source: BrowserBreakdownSource }>,
): BrowserBreakdownDatum[] {
  const cohorts = new Map<string, BrowserBreakdownDatum>()
  for (const { id, source } of runs) {
    for (const row of buildBrowserBreakdownData(breakdown, source)) {
      const cohortKey = row.cohortKey!
      const current = cohorts.get(cohortKey) ?? { segment: row.segment, cohortKey }
      current[id] = row.lcp
      current[`${id}_inp`] = row.inp
      cohorts.set(cohortKey, current)
    }
  }
  return Array.from(cohorts.values()).sort(orderBreakdownData)
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
