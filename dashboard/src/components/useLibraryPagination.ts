import { useState } from 'react'

export function useLibraryPagination(total: number, resetKey: string) {
  const [position, setPosition] = useState({ key: resetKey, page: 0 })
  const [pageSize, setPageSize] = useState<10 | 25 | 'all'>(10)
  if (position.key !== resetKey) setPosition({ key: resetKey, page: 0 })
  const pageCount = pageSize === 'all' ? 1 : Math.max(1, Math.ceil(total / pageSize))
  const page = position.key === resetKey ? Math.min(position.page, pageCount - 1) : 0
  const start = pageSize === 'all' ? 0 : page * pageSize
  return {
    total, page, pageCount, pageSize, start,
    end: pageSize === 'all' ? total : Math.min(total, start + pageSize),
    setPage: (next: number) => setPosition({ key: resetKey, page: Math.max(0, Math.min(next, pageCount - 1)) }),
    setPageSize: (next: 10 | 25 | 'all') => { setPageSize(next); setPosition({ key: resetKey, page: 0 }) },
  }
}

