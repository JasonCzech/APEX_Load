// Filtering and sorting for Command Center's tables.

export interface CommandTableSort {
  key: string
  direction: 'asc' | 'desc'
}

// A column whose value can come in another unit sorts those rows by it among themselves.
// A transaction's rows, one for each group that ran it, sort by group.
const SORT_TIEBREAKERS: Record<string, string> = { memory: 'memoryMb', name: 'group' }

export function filterAndSortRows<T extends object>(rows: T[], filter: string, sort: CommandTableSort): T[] {
  const query = filter.trim().toLowerCase()
  const filtered = query
    ? rows.filter(row => rowMatchesFilter(row as Record<string, unknown>, query))
    : rows
  const tiebreaker = SORT_TIEBREAKERS[sort.key]
  return [...filtered].sort((left, right) => compareSortValues(
    (left as Record<string, unknown>)[sort.key],
    (right as Record<string, unknown>)[sort.key],
    sort.direction,
  ) || (tiebreaker ? compareSortValues((left as Record<string, unknown>)[tiebreaker], (right as Record<string, unknown>)[tiebreaker], sort.direction) : 0))
}

function rowMatchesFilter(row: Record<string, unknown>, query: string) {
  return Object.values(row).some(value => {
    if (value == null) return false
    if (Array.isArray(value)) return value.join(' ').toLowerCase().includes(query)
    return String(value).toLowerCase().includes(query)
  })
}

function compareSortValues(left: unknown, right: unknown, direction: CommandTableSort['direction']) {
  const leftNumber = typeof left === 'number' && Number.isFinite(left) ? left : Number.NaN
  const rightNumber = typeof right === 'number' && Number.isFinite(right) ? right : Number.NaN
  let result = 0
  if (Number.isFinite(leftNumber) || Number.isFinite(rightNumber)) {
    result = (Number.isFinite(leftNumber) ? leftNumber : -Infinity) - (Number.isFinite(rightNumber) ? rightNumber : -Infinity)
  } else {
    result = String(left ?? '').localeCompare(String(right ?? ''), undefined, { numeric: true, sensitivity: 'base' })
  }
  return direction === 'asc' ? result : -result
}
