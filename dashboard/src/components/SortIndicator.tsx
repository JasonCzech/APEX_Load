export type SortDirection = 'asc' | 'desc'

interface SortIndicatorProps {
  active: boolean
  direction?: SortDirection
}

// SortIndicator renders the ↑/↓/↕ glyph shown in sortable table headers.
// It consolidates the copies that were previously duplicated across table
// components (TestHub, ScriptsLibraryTable, AccessPage, ResultsPage, …).
export function SortIndicator({ active, direction }: SortIndicatorProps) {
  const glyph = active ? (direction === 'asc' ? '↑' : '↓') : '↕'
  const label = active ? (direction === 'asc' ? 'sorted ascending' : 'sorted descending') : 'not sorted'
  return (
    <span className={active ? 'sort-icon active' : 'sort-icon'} aria-label={label}>
      {glyph}
    </span>
  )
}

export default SortIndicator
