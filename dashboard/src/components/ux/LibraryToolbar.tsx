import { type ReactNode } from 'react'
import './ux.css'

/** Search stays first; optional filters remain mounted so their state survives disclosure. */
export default function LibraryToolbar({ search, filters, actions, children, filtersActive = false, label = 'Library controls' }: { search?: ReactNode; filters?: ReactNode; actions?: ReactNode; children?: ReactNode; label?: string; filtersActive?: boolean }) {
  return <section className="ux-library-toolbar" aria-label={label}>
    <div className="ux-library-toolbar-main">{search}{children}<div className="ux-library-toolbar-actions">{actions}</div></div>
    {filters && <details className="ux-library-filters"><summary>Filters{filtersActive ? ' · Active' : ''}</summary><div className="ux-library-filter-fields">{filters}</div></details>}
  </section>
}
