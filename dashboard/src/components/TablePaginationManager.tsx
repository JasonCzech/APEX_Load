import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

type TablePageLimit = 10 | 25 | 'all'

type ManagedTable = {
  id: number
  table: HTMLTableElement
  container: HTMLDivElement
  totalRows: number
}

type PortalEntry = Pick<ManagedTable, 'id' | 'container' | 'totalRows'>

const DEFAULT_TABLE_PAGE_LIMIT = 10

function getTableRows(table: HTMLTableElement): HTMLTableRowElement[] {
  if (table.tBodies.length === 0) return []
  return Array.from(table.tBodies).flatMap(body => Array.from(body.rows))
}

function shouldManageTable(table: HTMLTableElement): boolean {
  if (table.dataset.tablePagination === 'off') return false
  if (table.closest('[data-table-pagination="off"]')) return false
  if (table.closest('.table-pagination-toolbar')) return false
  return getTableRows(table).length > DEFAULT_TABLE_PAGE_LIMIT
}

function getToolbarInsertionTarget(table: HTMLTableElement): HTMLElement {
  const wrapper = table.closest('.data-table')
  if (wrapper instanceof HTMLElement) return wrapper
  return table
}

function formatLimit(limit: TablePageLimit) {
  return limit === 'all' ? 'All' : String(limit)
}

function applyTableLimit(table: HTMLTableElement, limit: TablePageLimit) {
  const rows = getTableRows(table)
  rows.forEach((row, index) => {
    const hiddenByManager = limit !== 'all' && index >= limit
    row.hidden = hiddenByManager
    if (hiddenByManager) {
      row.dataset.tablePaginationHidden = 'true'
    } else {
      delete row.dataset.tablePaginationHidden
    }
  })
}

export default function TablePaginationManager() {
  const nextIdRef = useRef(1)
  const managedTablesRef = useRef(new Map<number, ManagedTable>())
  const tableIdsRef = useRef(new WeakMap<HTMLTableElement, number>())
  const limitsRef = useRef<Record<number, TablePageLimit>>({})
  const [limits, setLimits] = useState<Record<number, TablePageLimit>>({})
  const [portals, setPortals] = useState<PortalEntry[]>([])

  const cleanupManagedTable = useCallback((id: number) => {
    const managed = managedTablesRef.current.get(id)
    if (!managed) return
    getTableRows(managed.table).forEach(row => {
      if (row.dataset.tablePaginationHidden === 'true') {
        row.hidden = false
        delete row.dataset.tablePaginationHidden
      }
    })
    managed.container.remove()
    managedTablesRef.current.delete(id)
    delete limitsRef.current[id]
  }, [])

  const scanTables = useCallback(() => {
    const seenIds = new Set<number>()
    document.querySelectorAll('table').forEach(tableNode => {
      if (tableNode.tagName.toLowerCase() !== 'table') return
      const table = tableNode as HTMLTableElement

      let id = tableIdsRef.current.get(table)
      if (!shouldManageTable(table)) {
        if (id) cleanupManagedTable(id)
        return
      }

      if (!id) {
        id = nextIdRef.current
        nextIdRef.current += 1
        tableIdsRef.current.set(table, id)
      }
      seenIds.add(id)

      let managed = managedTablesRef.current.get(id)
      if (!managed) {
        const insertionTarget = getToolbarInsertionTarget(table)
        const container = document.createElement('div')
        container.className = 'table-pagination-toolbar'
        container.dataset.tablePaginationToolbar = 'true'
        insertionTarget.parentElement?.insertBefore(container, insertionTarget)
        managed = { id, table, container, totalRows: getTableRows(table).length }
        managedTablesRef.current.set(id, managed)
        limitsRef.current[id] = limitsRef.current[id] || DEFAULT_TABLE_PAGE_LIMIT
      } else {
        managed.totalRows = getTableRows(table).length
      }

      applyTableLimit(table, limitsRef.current[id] || DEFAULT_TABLE_PAGE_LIMIT)
    })

    Array.from(managedTablesRef.current.keys()).forEach(id => {
      const managed = managedTablesRef.current.get(id)
      if (!managed || !seenIds.has(id) || !document.body.contains(managed.table)) {
        cleanupManagedTable(id)
      }
    })

    const nextPortals = Array.from(managedTablesRef.current.values()).map(({ id, container, totalRows }) => ({ id, container, totalRows }))
    setPortals(current => {
      if (
        current.length === nextPortals.length &&
        current.every((entry, index) => (
          entry.id === nextPortals[index].id &&
          entry.container === nextPortals[index].container &&
          entry.totalRows === nextPortals[index].totalRows
        ))
      ) {
        return current
      }
      return nextPortals
    })
  }, [cleanupManagedTable])

  useEffect(() => {
    limitsRef.current = limits
    managedTablesRef.current.forEach(managed => {
      applyTableLimit(managed.table, limits[managed.id] || DEFAULT_TABLE_PAGE_LIMIT)
    })
  }, [limits])

  useEffect(() => {
    scanTables()
    const managedTables = managedTablesRef.current
    let rafId = 0
    const debouncedScan = (mutations: MutationRecord[]) => {
      // Skip mutations caused by our own toolbar insertions or row visibility toggling.
      const isOwnMutation = mutations.every(m => {
        const target = m.target as HTMLElement
        if (target.dataset?.tablePaginationToolbar === 'true') return true
        if (target.closest?.('[data-table-pagination-toolbar="true"]')) return true
        // Row hiding via applyTableLimit sets row.hidden which fires a childList mutation
        // on the tbody. Detect it by checking if all added/removed nodes are hidden pagination rows.
        if (m.type === 'attributes' && m.attributeName === 'hidden') return true
        return false
      })
      if (isOwnMutation) return
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(scanTables)
    }
    const observer = new MutationObserver(debouncedScan)
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['hidden'] })
    return () => {
      cancelAnimationFrame(rafId)
      observer.disconnect()
      Array.from(managedTables.keys()).forEach(cleanupManagedTable)
    }
  }, [cleanupManagedTable, scanTables])

  return (
    <>
      {portals.map(({ id, container, totalRows }) => {
        const value = limits[id] || DEFAULT_TABLE_PAGE_LIMIT
        const visibleRows = value === 'all' ? totalRows : Math.min(value, totalRows)
        return createPortal(
          <div className="table-pagination-controls" aria-label="Table row display options">
            <span className="table-pagination-summary">
              Showing {visibleRows} of {totalRows}
            </span>
            <label className="table-pagination-select-label">
              Rows
              <select
                aria-label="Rows per table"
                className="table-pagination-select"
                value={formatLimit(value)}
                onChange={(event) => {
                  const nextValue = event.target.value === 'All' ? 'all' : Number(event.target.value) as 10 | 25
                  setLimits(current => ({ ...current, [id]: nextValue }))
                }}
              >
                <option value="10">10</option>
                <option value="25">25</option>
                <option value="All">All</option>
              </select>
            </label>
          </div>,
          container,
        )
      })}
    </>
  )
}
