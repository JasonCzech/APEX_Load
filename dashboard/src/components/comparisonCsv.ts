import type { RunArchiveActionStats, RunArchiveReport, TestSummary } from '../types'

export interface ComparisonCsvCard {
  test: TestSummary
  report?: RunArchiveReport | null
}

const HEADERS = [
  'action', 'run_ref', 'run_name', 'is_baseline',
  'transactions', 'errors', 'error_pct', 'avg_ms', 'p50_ms', 'p90_ms', 'p95_ms', 'p99_ms', 'peak_tps',
  'avg_ms_delta_vs_baseline', 'p95_ms_delta_vs_baseline', 'error_pct_delta_vs_baseline',
]

function actionStats(report?: RunArchiveReport | null): Record<string, RunArchiveActionStats> {
  return (report?.metric_populations ? report.by_request : report?.by_action) || {}
}

function cell(value: unknown): string {
  if (value === undefined || value === null || (typeof value === 'number' && !Number.isFinite(value))) return ''
  let text = typeof value === 'number' ? String(Math.round(value * 1000) / 1000) : String(value)
  // Action and run names are operator-controlled: a spreadsheet must not run
  // them as formulas (negative numbers are values, not text, and stay as-is).
  // eslint-disable-next-line no-control-regex
  if (typeof value !== 'number' && /^[\s\u0000-\u001f]*[=+@-]/.test(text)) text = `'${text}`
  return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
}

function delta(value: number | undefined, baseline: number | undefined) {
  return value === undefined || baseline === undefined ? undefined : value - baseline
}

/** The on-screen run comparison as CSV: one row per action and run, first card is the baseline. */
export function buildComparisonActionCsv(cards: ComparisonCsvCard[]): string {
  const [baseline] = cards
  const baselineStats = actionStats(baseline?.report)
  const actions = Array.from(new Set(cards.flatMap(card => Object.keys(actionStats(card.report))))).sort()
  const rows = [HEADERS.join(',')]
  for (const action of actions) {
    const base = baselineStats[action]
    cards.forEach((card, index) => {
      const stats = actionStats(card.report)[action]
      rows.push([
        action, card.test.ref_key || card.test.id, card.test.name, index === 0 ? 'true' : 'false',
        stats?.transactions, stats?.errors, stats?.error_pct, stats?.avg_response_ms, stats?.p50_ms, stats?.p90_ms, stats?.p95_ms, stats?.p99_ms, stats?.peak_tps,
        index === 0 ? undefined : delta(stats?.avg_response_ms, base?.avg_response_ms),
        index === 0 ? undefined : delta(stats?.p95_ms, base?.p95_ms),
        index === 0 ? undefined : delta(stats?.error_pct, base?.error_pct),
      ].map(cell).join(','))
    })
  }
  return rows.join('\n') + '\n'
}

export function comparisonCsvFilename(cards: ComparisonCsvCard[]): string {
  const refs = cards.map(card => (card.test.ref_key || card.test.id).replace(/[^A-Za-z0-9_-]+/g, '_'))
  return `run-comparison-${refs.join('-vs-')}.csv`
}
