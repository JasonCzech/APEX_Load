import { useLibraryPagination } from './useLibraryPagination'
import './LibraryPagination.css'

export default function LibraryPagination({ label, pagination: p }: { label: string; pagination: ReturnType<typeof useLibraryPagination> }) {
  // An empty table explains itself; a "0–0 of 0" pager with dead buttons adds nothing.
  if (p.total === 0) return null
  return <nav className="library-pagination" aria-label={`${label} pages`}>
    <span role="status">Showing {p.total ? p.start + 1 : 0}–{p.end} of {p.total}</span>
    <label>Rows <select aria-label={`${label} rows per page`} value={p.pageSize} onChange={event => p.setPageSize(event.target.value === 'all' ? 'all' : Number(event.target.value) as 10 | 25)}>
      <option value="10">10</option><option value="25">25</option><option value="all">All</option>
    </select></label>
    <button type="button" className="btn btn-secondary btn-sm" disabled={p.page === 0} onClick={() => p.setPage(p.page - 1)}>Previous</button>
    <span>Page {p.page + 1} of {p.pageCount}</span>
    <button type="button" className="btn btn-secondary btn-sm" disabled={p.page + 1 >= p.pageCount} onClick={() => p.setPage(p.page + 1)}>Next</button>
  </nav>
}
