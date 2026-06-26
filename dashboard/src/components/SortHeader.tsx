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
  return (
    <th className={`sortable ${numeric ? 'num' : ''}`.trim()}>
      <button type="button" onClick={() => onSort(field)}>
        {label}
        <SortIcon active={sort.field === field} direction={sort.direction} />
      </button>
    </th>
  )
}
