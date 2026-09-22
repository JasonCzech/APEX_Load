import type { LoadImpactPoint, LoadImpactRow } from './loadImpactModel'

export type LoadImpactSortDirection = 'asc' | 'desc'
export type LoadImpactSortState = {
  key: string
  direction: LoadImpactSortDirection
}

export type LoadImpactDetailSortKey =
  | 'timestamp'
  | 'vusers'
  | 'metric'
  | 'net'
  | 'disk'
  | string

export const METRIC_SORT_KEY = 'metric'

function compareMaybeNumber(left: number | undefined, right: number | undefined, direction: LoadImpactSortDirection) {
  const leftMissing = left === undefined || !Number.isFinite(left)
  const rightMissing = right === undefined || !Number.isFinite(right)
  if (leftMissing && rightMissing) return 0
  if (leftMissing) return 1
  if (rightMissing) return -1
  return direction === 'asc' ? left - right : right - left
}

export function sortLoadImpactRows(rows: LoadImpactRow[], sort: LoadImpactSortState | null): LoadImpactRow[] {
  if (!sort) return rows

  return [...rows].sort((left, right) => {
    const fallback = left.metric.label.localeCompare(right.metric.label)
    if (sort.key === METRIC_SORT_KEY) {
      const multiplier = sort.direction === 'asc' ? 1 : -1
      return fallback * multiplier
    }

    const bucketId = sort.key.replace(/^bucket:/, '')
    const leftCell = left.cells.find(cell => cell.bucket.id === bucketId)
    const rightCell = right.cells.find(cell => cell.bucket.id === bucketId)
    const valueComparison = compareMaybeNumber(leftCell?.average, rightCell?.average, sort.direction)
    if (valueComparison !== 0) return valueComparison
    return fallback
  })
}

function detailPointValue(point: LoadImpactPoint, key: LoadImpactDetailSortKey): number | string | undefined {
  if (key === 'timestamp') {
    if (typeof point.timestampMs === 'number' && Number.isFinite(point.timestampMs)) return point.timestampMs
    const parsed = Date.parse(point.time)
    return Number.isFinite(parsed) ? parsed : point.time
  }
  if (key === 'vusers') return point.vusers
  if (key === 'metric') return point.value
  if (key === 'net') {
    const sent = point.source.netSent
    const received = point.source.netRecv
    return typeof sent === 'number' && Number.isFinite(sent) && typeof received === 'number' && Number.isFinite(received)
      ? sent + received
      : undefined
  }
  if (key === 'disk') {
    const read = point.source.diskRead
    const write = point.source.diskWrite
    return typeof read === 'number' && Number.isFinite(read) && typeof write === 'number' && Number.isFinite(write)
      ? read + write
      : undefined
  }

  const value = point.source[key as keyof typeof point.source]
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

// sortLoadImpactDetailPoints is intentionally based on raw point values, rather
// than their localized display strings, so numeric and chronological sorts stay
// correct regardless of browser locale or the selected metric's unit.
export function sortLoadImpactDetailPoints(
  points: LoadImpactPoint[],
  sort: LoadImpactSortState | null,
): LoadImpactPoint[] {
  if (!sort) return points

  return [...points].sort((left, right) => {
    const leftValue = detailPointValue(left, sort.key)
    const rightValue = detailPointValue(right, sort.key)
    const leftMissing = leftValue === undefined
    const rightMissing = rightValue === undefined
    if (leftMissing && rightMissing) return 0
    if (leftMissing) return 1
    if (rightMissing) return -1

    const comparison = typeof leftValue === 'string' || typeof rightValue === 'string'
      ? String(leftValue).localeCompare(String(rightValue), undefined, { numeric: true })
      : leftValue - rightValue
    return sort.direction === 'asc' ? comparison : -comparison
  })
}
