import type { LoadImpactRow } from './loadImpactModel'

export type LoadImpactSortDirection = 'asc' | 'desc'
export type LoadImpactSortState = {
  key: string
  direction: LoadImpactSortDirection
}

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
