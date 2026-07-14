import { SortIndicator as SortIcon } from './SortIndicator'
import { SortState } from './accessTypes'

export function SortHeader<T extends string>({
  label,
  field,
  sort,
  onSort,
  numeric = false,
}: {
  label: string
  field: T
  sort: SortState<T>
  onSort: (field: T) => void
  numeric?: boolean
}) {
  const active = sort.field === field
  return (
    <th
      className={`sortable ${numeric ? 'num' : ''}`.trim()}
      aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}
    >
      <button type="button" onClick={() => onSort(field)}>
        {label}
        <SortIcon active={active} direction={sort.direction} />
      </button>
    </th>
  )
}
