import { useId, useState, type ReactNode } from 'react'
import './ux.css'

/**
 * Search stays first; optional filters remain mounted so their state survives
 * disclosure. Given a filterCount, the filters fold behind a Filters toggle in
 * the toolbar's one row (open whenever one gets set), so a library's
 * list starts near the top.
 */
export default function LibraryToolbar({ search, filters, actions, children, filtersActive = false, filterCount, label = 'Library controls' }: { search?: ReactNode; filters?: ReactNode; actions?: ReactNode; children?: ReactNode; label?: string; filtersActive?: boolean; filterCount?: number }) {
  const foldable = filterCount !== undefined
  const [filtersOpen, setFiltersOpen] = useState(() => (filterCount ?? 0) > 0)
  // Filters set from outside the fold (restored for a project, say) open it,
  // so nothing narrows the list out of sight.
  const [countSeen, setCountSeen] = useState(filterCount ?? 0)
  if ((filterCount ?? 0) !== countSeen) {
    setCountSeen(filterCount ?? 0)
    if (countSeen === 0 && (filterCount ?? 0) > 0) setFiltersOpen(true)
  }
  const filtersId = useId()
  const showFilters = !foldable || filtersOpen
  return <section className="ux-library-toolbar" aria-label={label} data-filters-active={filtersActive || undefined}>
    <div className="ux-library-toolbar-main">
      {search}{children}
      {filters && foldable && <button type="button" className="btn btn-secondary ux-library-filters-toggle" aria-expanded={filtersOpen} aria-controls={filtersId} onClick={() => setFiltersOpen(open => !open)}>
        Filters{filterCount ? <><span className="ux-library-filters-count" aria-hidden="true">{filterCount}</span><span className="sr-only">, {filterCount} set</span></> : null}
      </button>}
      <div className="ux-library-toolbar-actions">{actions}</div>
    </div>
    {filters && <div id={filtersId} className="ux-library-filter-fields" hidden={!showFilters}>{filters}</div>}
  </section>
}
