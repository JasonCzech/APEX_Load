import type { MetricsHistoryPoint } from '../types'

export type TransactionChartMetric = 'tps' | 'avgMs' | 'errorPct' | 'p50' | 'p90' | 'p95' | 'p99'

// Archive interval summaries are compacted to this cardinality. Keep the UI on
// the same bound so malformed or forward-version payloads cannot create an
// unbounded number of Recharts series.
export const MAX_TRANSACTION_CHART_SERIES = 512

const TRANSACTION_SERIES_PREFIX = 'transaction:'
const TRANSACTION_SERIES_METRICS: TransactionChartMetric[] = ['tps', 'avgMs', 'errorPct', 'p50', 'p90', 'p95', 'p99']
const TRANSACTION_SERIES_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
  'var(--chart-6)',
] as const
// These patterns intentionally differ from the group-series patterns so a
// reused finite-palette color does not make a transaction and group identical.
const TRANSACTION_SERIES_DASH_PATTERNS = ['10 3', '4 2 1 2', '7 2 2 2', '1 2', '12 3 2 3'] as const

// Transaction names are encoded because imported and recorded actions may
// contain punctuation that would otherwise collide with group-derived keys.
export function transactionChartSeriesKey(name: string, metric: TransactionChartMetric) {
  return `${TRANSACTION_SERIES_PREFIX}${encodeURIComponent(name)}:${metric}`
}

// One group's rate of a transaction, kept beside the run-wide series so a
// group's row in the Transactions table averages its own rate. The prefix
// keeps it out of the transaction charts.
export function groupTransactionTpsSeriesKey(group: string, transaction: string) {
  return `group-transaction:${encodeURIComponent(group)}:${encodeURIComponent(transaction)}:tps`
}

export function transactionChartSeriesName(key: string): string | undefined {
  if (!key.startsWith(TRANSACTION_SERIES_PREFIX)) return undefined
  const metric = TRANSACTION_SERIES_METRICS.find(candidate => key.endsWith(`:${candidate}`))
  if (!metric) return undefined
  const encodedName = key.slice(TRANSACTION_SERIES_PREFIX.length, -(metric.length + 1))
  if (!encodedName) return undefined
  try {
    return decodeURIComponent(encodedName)
  } catch {
    return undefined
  }
}

export function isTransactionChartSeriesKey(key: string) {
  return transactionChartSeriesName(key) !== undefined
}

function stableTransactionNameHash(name: string) {
  let hash = 2166136261
  for (let index = 0; index < name.length; index += 1) {
    hash ^= name.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return hash >>> 0
}

export function transactionChartSeriesPresentation(name: string) {
  const hash = stableTransactionNameHash(name)
  return {
    color: TRANSACTION_SERIES_COLORS[hash % TRANSACTION_SERIES_COLORS.length],
    strokeDasharray: TRANSACTION_SERIES_DASH_PATTERNS[Math.floor(hash / TRANSACTION_SERIES_COLORS.length) % TRANSACTION_SERIES_DASH_PATTERNS.length],
  }
}

export function omitTransactionChartSeries(point: MetricsHistoryPoint): MetricsHistoryPoint {
  const transactionKeys = Object.keys(point).filter(key => key.startsWith(TRANSACTION_SERIES_PREFIX))
  if (transactionKeys.length === 0) return point

  const scopedPoint = { ...point }
  transactionKeys.forEach(key => {
    delete scopedPoint[key]
  })
  return scopedPoint
}

// transactionChartSeriesNames lists the transactions present in the data. Pass a
// metric to list only the transactions that actually reported it — percentile
// graphs must not advertise a line for a transaction whose source (e.g. an
// archived interval summary) carries no percentiles.
export function transactionChartSeriesNames(
  data: ReadonlyArray<MetricsHistoryPoint>,
  metric?: TransactionChartMetric,
) {
  const names = new Set<string>()
  data.forEach(point => {
    Object.keys(point).forEach(key => {
      if (metric !== undefined) {
        if (!key.endsWith(`:${metric}`)) return
        const value = point[key]
        if (typeof value !== 'number' || !Number.isFinite(value)) return
      }
      const name = transactionChartSeriesName(key)
      if (name !== undefined) names.add(name)
    })
  })
  return Array.from(names)
    .sort((left, right) => left.localeCompare(right))
    .slice(0, MAX_TRANSACTION_CHART_SERIES)
}
