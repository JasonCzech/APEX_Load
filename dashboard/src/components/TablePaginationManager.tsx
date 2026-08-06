import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

type TablePageLimit = 10 | 25 | 'all'
type TablePageState = {
  limit: TablePageLimit
  pageIndex: number
}

type ManagedTable = {
  id: number
  table: HTMLTableElement
  container: HTMLDivElement
  totalRows: number
}

type PortalEntry = Pick<ManagedTable, 'id' | 'container' | 'totalRows'>

const DEFAULT_TABLE_PAGE_LIMIT = 10
const DEFAULT_TABLE_PAGE_STATE: TablePageState = { limit: DEFAULT_TABLE_PAGE_LIMIT, pageIndex: 0 }

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

function maxPageIndex(totalRows: number, limit: TablePageLimit) {
  if (limit === 'all') return 0
  return Math.max(0, Math.ceil(totalRows / limit) - 1)
}

function normalizePageState(state: TablePageState | undefined, totalRows: number): TablePageState {
  const current = state || DEFAULT_TABLE_PAGE_STATE
  const pageIndex = Math.min(Math.max(0, current.pageIndex), maxPageIndex(totalRows, current.limit))
  return { ...current, pageIndex }
}

function pageBounds(totalRows: number, state: TablePageState) {
  if (totalRows === 0) return { startIndex: 0, endIndex: 0 }
  if (state.limit === 'all') return { startIndex: 0, endIndex: totalRows }
  const startIndex = Math.min(state.pageIndex * state.limit, totalRows)
  return { startIndex, endIndex: Math.min(startIndex + state.limit, totalRows) }
}

function applyTablePageState(table: HTMLTableElement, state: TablePageState) {
  const rows = getTableRows(table)
  const { startIndex, endIndex } = pageBounds(rows.length, normalizePageState(state, rows.length))
  rows.forEach((row, index) => {
    const hiddenByManager = index < startIndex || index >= endIndex
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
  const pageStatesRef = useRef<Record<number, TablePageState>>({})
  const [pageStates, setPageStates] = useState<Record<number, TablePageState>>({})
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
    delete pageStatesRef.current[id]
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
        pageStatesRef.current[id] = normalizePageState(pageStatesRef.current[id], managed.totalRows)
      } else {
        managed.totalRows = getTableRows(table).length
        pageStatesRef.current[id] = normalizePageState(pageStatesRef.current[id], managed.totalRows)
      }

      applyTablePageState(table, pageStatesRef.current[id] || DEFAULT_TABLE_PAGE_STATE)
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
    pageStatesRef.current = { ...pageStates }
    managedTablesRef.current.forEach(managed => {
      applyTablePageState(managed.table, normalizePageState(pageStates[managed.id], managed.totalRows))
    })
  }, [pageStates])

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
        const state = normalizePageState(pageStates[id], totalRows)
        const { startIndex, endIndex } = pageBounds(totalRows, state)
        const canGoPrevious = state.limit !== 'all' && state.pageIndex > 0
        const canGoNext = state.limit !== 'all' && state.pageIndex < maxPageIndex(totalRows, state.limit)
        return createPortal(
          <div className="table-pagination-controls" aria-label="Table row display options">
            <span className="table-pagination-summary">
              {totalRows > 0 ? `Showing ${startIndex + 1}-${endIndex} of ${totalRows}` : 'No rows'}
            </span>
            <label className="table-pagination-select-label">
              Rows
              <select
                aria-label="Rows per table"
                className="table-pagination-select"
                value={formatLimit(state.limit)}
                onChange={(event) => {
                  const nextValue = event.target.value === 'All' ? 'all' : Number(event.target.value) as 10 | 25
                  setPageStates(current => ({ ...current, [id]: { limit: nextValue, pageIndex: 0 } }))
                }}
              >
                <option value="10">10</option>
                <option value="25">25</option>
                <option value="All">All</option>
              </select>
            </label>
            <div className="table-pagination-actions" aria-label="Table page navigation">
              <button
                type="button"
                className="btn btn-sm btn-secondary table-pagination-button"
                disabled={!canGoPrevious}
                onClick={() => {
                  setPageStates(current => {
                    const currentState = normalizePageState(current[id], totalRows)
                    return { ...current, [id]: { ...currentState, pageIndex: Math.max(0, currentState.pageIndex - 1) } }
                  })
                }}
              >
                Previous
              </button>
              <button
                type="button"
                className="btn btn-sm btn-secondary table-pagination-button"
                disabled={!canGoNext}
                onClick={() => {
                  setPageStates(current => {
                    const currentState = normalizePageState(current[id], totalRows)
                    return { ...current, [id]: { ...currentState, pageIndex: Math.min(maxPageIndex(totalRows, currentState.limit), currentState.pageIndex + 1) } }
                  })
                }}
              >
                Next
              </button>
            </div>
          </div>,
          container,
        )
      })}
    </>
  )
}
