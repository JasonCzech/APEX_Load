import { useState } from 'react'

import { useLibraryFilter } from './useLibraryFilter'

const PAGE_SIZES = ['10', '25', 'all'] as const

/**
 * Page through a library table. With a persist scope the rows-per-page choice
 * survives a reload like the library's filters; the page itself restarts
 * whenever the listed rows change.
 */
export function useLibraryPagination(total: number, resetKey: string, persistScope: string | null = null) {
  const [position, setPosition] = useState({ key: resetKey, page: 0 })
  const [storedPageSize, setStoredPageSize] = useLibraryFilter<(typeof PAGE_SIZES)[number]>(persistScope, 'pageSize', '10', PAGE_SIZES)
  const pageSize: 10 | 25 | 'all' = storedPageSize === 'all' ? 'all' : storedPageSize === '25' ? 25 : 10
  if (position.key !== resetKey) setPosition({ key: resetKey, page: 0 })
  const pageCount = pageSize === 'all' ? 1 : Math.max(1, Math.ceil(total / pageSize))
  const page = position.key === resetKey ? Math.min(position.page, pageCount - 1) : 0
  const start = pageSize === 'all' ? 0 : page * pageSize
  return {
    total, page, pageCount, pageSize, start,
    end: pageSize === 'all' ? total : Math.min(total, start + pageSize),
    setPage: (next: number) => setPosition({ key: resetKey, page: Math.max(0, Math.min(next, pageCount - 1)) }),
    setPageSize: (next: 10 | 25 | 'all') => { setStoredPageSize(String(next) as (typeof PAGE_SIZES)[number]); setPosition({ key: resetKey, page: 0 }) },
  }
}
