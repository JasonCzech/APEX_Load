import type { BrowserDocumentMetric, BrowserDocumentMetricSummary, BrowserDocumentSummaryMode, BrowserMetricDistribution } from '../../types/browserDocumentEvidence'
import { buildBrowserDocumentPanelModel } from '../browserDocumentEvidenceModel'

// Browser intelligence comparison (BI-4): a run's main-frame navigation
// documents side by side by engine, device profile and scenario group (the
// stand-in for a page: document evidence records no route). Each cell is the
// metric's p75 over final qualified documents, computed as the controller
// computes it (protocol.BrowserMetricDistribution.Quantile).

export const BROWSER_COMPARISON_METRICS: BrowserDocumentMetric[] = ['lcp', 'fcp', 'inp', 'cls', 'ttfb']

export interface BrowserComparisonCell {
  p75: number | null
  final: number
  unit: 'ms' | 'score'
}

export interface BrowserComparisonRow {
  key: string
  engine: string
  deviceProfile: string
  group: string
  region: string
  /** Set when one engine, device and group ran under more than one actual runtime cohort. */
  cohortLabel: string
  metrics: Partial<Record<BrowserDocumentMetric, BrowserComparisonCell>>
}

export interface BrowserComparison {
  state: 'absent' | 'ready' | 'invalid' | 'unsupported'
  issue: string | null
  rows: BrowserComparisonRow[]
  /** Metric tuples left out: child frames and back-forward cache restores. */
  otherScopes: number
  incomplete: boolean | null
}

/** The approximate q-quantile: the upper bound of the bucket holding it, capped by the observed maximum. */
export function browserDistributionQuantile(d: BrowserMetricDistribution, q: number): number | null {
  if (!(d.count > 0) || !(q >= 0 && q <= 1)) return null
  const rank = Math.max(1, Math.ceil(q * d.count))
  let seen = 0
  for (let index = 0; index < d.buckets.length; index += 1) {
    seen += d.buckets[index]
    if (seen >= rank) return Math.max(d.min, Math.min(0.000001 * Math.pow(1.2, index), d.max))
  }
  return d.max
}

function cohortKey(summary: BrowserDocumentMetricSummary): string {
  return JSON.stringify(summary.cohort ?? null)
}

export function buildBrowserComparison(summary: unknown, mode: BrowserDocumentSummaryMode): BrowserComparison {
  const model = buildBrowserDocumentPanelModel(summary, undefined, mode)
  if (model.state !== 'ready') return { state: model.state, issue: model.issue, rows: [], otherScopes: 0, incomplete: model.incomplete }
  const rows = new Map<string, BrowserComparisonRow>()
  const cohortsByTuple = new Map<string, string[]>()
  let otherScopes = 0
  for (const row of model.rows) {
    const metric = row.summary
    if (!metric) continue
    if (metric.frame_scope !== 'main_frame' || metric.lifecycle_phase !== 'navigation') {
      otherScopes += 1
      continue
    }
    const tuple = JSON.stringify([metric.engine, metric.device_profile, metric.group ?? '', metric.region ?? ''])
    const cohorts = cohortsByTuple.get(tuple) ?? []
    const cohort = cohortKey(metric)
    if (!cohorts.includes(cohort)) cohorts.push(cohort)
    cohortsByTuple.set(tuple, cohorts)
    const key = `${tuple}#${cohort}`
    const current = rows.get(key) ?? {
      key,
      engine: metric.engine,
      deviceProfile: metric.device_profile,
      group: metric.group ?? '',
      region: metric.region ?? '',
      cohortLabel: '',
      metrics: {},
    }
    current.metrics[metric.metric] = {
      p75: metric.eligible_final > 0 ? browserDistributionQuantile(metric.distribution, 0.75) : null,
      final: metric.eligible_final,
      unit: metric.unit,
    }
    rows.set(key, current)
  }
  const ordered = [...rows.values()].map(row => {
    const tuple = row.key.slice(0, row.key.indexOf('#'))
    const cohorts = cohortsByTuple.get(tuple) ?? []
    return cohorts.length > 1 ? { ...row, cohortLabel: `runtime cohort ${cohorts.indexOf(row.key.slice(row.key.indexOf('#') + 1)) + 1} of ${cohorts.length}` } : row
  })
  ordered.sort((a, b) => a.engine.localeCompare(b.engine) || a.deviceProfile.localeCompare(b.deviceProfile) || a.group.localeCompare(b.group) || a.region.localeCompare(b.region) || a.cohortLabel.localeCompare(b.cohortLabel))
  return { state: 'ready', issue: null, rows: ordered, otherScopes, incomplete: model.incomplete }
}
