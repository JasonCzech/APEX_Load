import type {
  LoadComparisonPoint,
  LoadComparisonLatency,
  RunArchiveReport,
  TestSummary,
} from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

export type AnalysisMetric = 'average_ms' | 'p50_ms' | 'p95_ms' | 'p99_ms'
export type LatencyOutcome = 'all' | 'successful' | 'failed'
export const analysisRuleVersion = 'dashboard-comparison-v1'
export const metricLabels: Record<AnalysisMetric, string> = {
  average_ms: 'Mean',
  p50_ms: 'P50',
  p95_ms: 'P95',
  p99_ms: 'P99',
}

export function finite(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}
export function displayNumber(value: unknown, digits = 1): string {
  return finite(value)
    ? value.toLocaleString(undefined, {
        maximumFractionDigits: digits,
        minimumFractionDigits: digits,
      })
    : 'Unavailable'
}
export function signedChange(
  current: number | undefined,
  baseline: number | undefined,
  unit = 'ms',
): string {
  if (!finite(current) || !finite(baseline)) return 'Unavailable'
  const delta = current - baseline
  return `${delta > 0 ? '+' : ''}${displayNumber(delta)} ${unit}${baseline !== 0 ? ` (${delta > 0 ? '+' : ''}${displayNumber((delta / baseline) * 100)}%)` : ' (baseline is zero)'}`
}
export function validLoadPoint(point?: LoadComparisonPoint): boolean {
  return Boolean(
    point &&
      point.transactions > 0 &&
      point.errors >= 0 &&
      point.errors <= point.transactions &&
      (!point.evidence_status || point.evidence_status === 'observed'),
  )
}
export function loadLatency(
  point: LoadComparisonPoint | undefined,
  metric: AnalysisMetric,
  outcome: LatencyOutcome,
): number | undefined {
  if (!validLoadPoint(point) || !point) return undefined
  const distribution: LoadComparisonLatency | undefined =
    outcome === 'successful'
      ? point.successful_latency
      : outcome === 'failed'
        ? point.failed_latency
        : point.latency
  if (distribution && distribution.samples > 0) return distribution[metric]
  // A stored mean is usable without a distribution; scalar percentiles cannot
  // be pooled into a new population percentile.
  return outcome === 'all' && metric === 'average_ms'
    ? point.avg_response_ms
    : undefined
}

function stableJSON(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(',')}]`
  if (value !== null && typeof value === 'object')
    return `{${Object.entries(value)
      .filter(([, item]) => item !== undefined)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, item]) => `${JSON.stringify(key)}:${stableJSON(item)}`)
      .join(',')}}`
  return JSON.stringify(value)
}
function workloadSignature(
  report: RunArchiveReport | null | undefined,
  exactLoad: boolean,
): string | undefined {
  const groups = report?.manifest?.provenance?.groups
  if (!groups?.length) return undefined
  const total = groups.reduce((sum, group) => sum + group.vusers, 0)
  // Runtime timestamps/state and display names are outcomes, not test conditions.
  return stableJSON(
    groups
      .map((group) => ({
        name: group.name,
        script_id: group.script_id,
        protocol: group.protocol,
        role: group.role || 'load',
        load: exactLoad
          ? group.vusers
          : total > 0
            ? group.vusers / total
            : undefined,
        ramp_up: group.ramp_up,
        ramp_down: group.ramp_down,
        start_offset: group.start_offset,
        stages: group.stages,
        iterations: group.iterations,
        iteration_override: group.iteration_override,
        runtime_settings: group.runtime_settings,
        initialization: group.initialization,
        assignment: group.assignment,
        continue_on_error: group.continue_on_error?.effective,
      }))
      .sort((a, b) => a.name.localeCompare(b.name)),
  )
}

export interface ComparisonEligibility {
  status: 'Comparable' | 'Not comparable' | 'Unverified'
  reasons: string[]
}
export function comparisonEligibility(
  candidate?: TestSummary,
  baseline?: TestSummary,
  report?: RunArchiveReport | null,
  baselineReport?: RunArchiveReport | null,
  matchObservedLoad = true,
): ComparisonEligibility {
  if (!candidate || !baseline)
    return {
      status: 'Unverified',
      reasons: ['Select a baseline and candidate run.'],
    }
  const a = report?.manifest?.provenance
  const b = baselineReport?.manifest?.provenance
  const ac = report?.change_attribution
  const bc = baselineReport?.change_attribution
  const checks: Array<[string, unknown, unknown]> = [
    ['Application', candidate.application_id, baseline.application_id],
    ['Project', candidate.project_id, baseline.project_id],
    [
      'Scenario',
      a?.scenario_id || candidate.scenario_id,
      b?.scenario_id || baseline.scenario_id,
    ],
    ['Scenario revision', ac?.scenario_revision, bc?.scenario_revision],
    [
      'Script revisions',
      ac?.script_revisions?.length
        ? [...ac.script_revisions].sort().join('|')
        : undefined,
      bc?.script_revisions?.length
        ? [...bc.script_revisions].sort().join('|')
        : undefined,
    ],
    ['Environment', ac?.environment, bc?.environment],
    ['Data revision', ac?.data_revision, bc?.data_revision],
    [
      'Duration',
      parseDurationToSeconds(a?.duration || candidate.duration) || undefined,
      parseDurationToSeconds(b?.duration || baseline.duration) || undefined,
    ],
    [
      'Workload and pacing',
      workloadSignature(report, matchObservedLoad),
      workloadSignature(baselineReport, matchObservedLoad),
    ],
    [
      'Runtime settings',
      a?.runtime_settings ? stableJSON(a.runtime_settings) : undefined,
      b?.runtime_settings ? stableJSON(b.runtime_settings) : undefined,
    ],
  ]
  if (matchObservedLoad)
    checks.push([
      'Achieved peak load',
      finite(report?.overview?.peak_active_vusers) && report.overview.peak_active_vusers >= 0 ? report.overview.peak_active_vusers : undefined,
      finite(baselineReport?.overview?.peak_active_vusers) && baselineReport.overview.peak_active_vusers >= 0 ? baselineReport.overview.peak_active_vusers : undefined,
    ])
  const missing = checks.filter(
    ([, l, r]) => l == null || l === '' || r == null || r === '',
  )
  const different = checks.filter(
    ([, l, r]) =>
      l != null && l !== '' && r != null && r !== '' && l !== r,
  )
  const reasons = different.map(([label]) => `${label} differs.`)
  if (missing.length)
    reasons.push(
      `Unverified: ${missing.map(([label]) => label.toLowerCase()).join(', ')}.`,
    )
  const completeness = [
    report?.completeness?.score_pct,
    baselineReport?.completeness?.score_pct,
  ]
  if (completeness.some((value) => !finite(value) || value < 95))
    reasons.push(
      'Archive coverage is incomplete or unverified (95% required for a qualified comparison).',
    )
  return {
    status: different.length
      ? 'Not comparable'
      : reasons.length
        ? 'Unverified'
        : 'Comparable',
    reasons,
  }
}

// A spreadsheet must not interpret operator-controlled names as formulas.
export function analysisCSV(rows: Array<Array<unknown>>): string {
  return rows
    .map((row) =>
      row
        .map((value) => {
          let text = value === undefined || value === null ? '' : String(value)
          if (typeof value !== 'number' && /^[\s]*[=+@-]/.test(text))
            text = `'${text}`
          return `"${text.replace(/"/g, '""')}"`
        })
        .join(','),
    )
    .join('\r\n')
}

export function numericInput(value: string): number | undefined {
  if (!value.trim()) return undefined
  const result = Number(value)
  return finite(result) && result >= 0 ? result : undefined
}

export interface AnalysisReportContext {
  compare?: string
  transaction?: string
  population?: 'action' | 'transaction' | 'unknown'
}
