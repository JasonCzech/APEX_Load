import { useMemo, useRef, useState } from 'react'
import { useVirtualizer, type VirtualItem } from '@tanstack/react-virtual'

import type { RunArchiveSLARow, TransactionEvent } from '../../types'
import { formatMs, formatNumber, formatPct } from './runReportModel'

type SortKey = 'name' | 'volume' | 'p95' | 'error' | 'result'
type SortDir = 'asc' | 'desc'

interface Props {
  rows: RunArchiveSLARow[]
  selectedAction?: string
  drilldownTransactions?: TransactionEvent[]
  loadingTransactions?: boolean
  onSelectAction?: (action: string) => void
}

const RESULT_FILTERS = ['all', 'fail', 'warn', 'pass', 'na'] as const

function resultLabel(kind: string) {
  if (kind === 'fail') return 'Fail'
  if (kind === 'warn') return 'Warn'
  if (kind === 'pass') return 'Pass'
  return 'n/a'
}

function sortValue(row: RunArchiveSLARow, key: SortKey) {
  switch (key) {
    case 'volume': return row.volume
    case 'p95': return row.p95_ms
    case 'error': return row.error_pct
    case 'result': return row.result_kind
    case 'name':
    default:
      return row.name.toLowerCase()
  }
}

function compareRows(a: RunArchiveSLARow, b: RunArchiveSLARow, key: SortKey, dir: SortDir) {
  const left = sortValue(a, key)
  const right = sortValue(b, key)
  const factor = dir === 'asc' ? 1 : -1
  if (typeof left === 'number' && typeof right === 'number') return factor * (left - right)
  return factor * String(left).localeCompare(String(right))
}

function HeaderButton({ label, sortKey, activeKey, direction, onSort }: {
  label: string
  sortKey: SortKey
  activeKey: SortKey
  direction: SortDir
  onSort: (key: SortKey) => void
}) {
  return (
    <button className="run-report-table-sort" type="button" onClick={() => onSort(sortKey)}>
      {label}{activeKey === sortKey ? (direction === 'asc' ? ' ↑' : ' ↓') : ''}
    </button>
  )
}

function ScoreboardRow({ row, selected, onSelect }: { row: RunArchiveSLARow; selected?: boolean; onSelect?: (action: string) => void }) {
  return (
    <tr className={selected ? 'selected' : ''} onClick={() => onSelect?.(row.name)}>
      <td>{row.name}</td>
      <td>{formatNumber(row.volume, 0)}</td>
      <td>{formatMs(row.p95_ms)}</td>
      <td>{row.has_sla ? formatMs(row.sla_ms) : '—'}</td>
      <td>{formatPct(row.error_pct)}</td>
      <td><span className={`run-report-sla-pill run-report-sla-${row.result_kind}`}>{resultLabel(row.result_kind)}</span></td>
    </tr>
  )
}

export default function TransactionSlaScoreboard({
  rows,
  selectedAction,
  drilldownTransactions = [],
  loadingTransactions,
  onSelectAction,
}: Props) {
  const [filter, setFilter] = useState('')
  const [resultFilter, setResultFilter] = useState<(typeof RESULT_FILTERS)[number]>('all')
  const [sortKey, setSortKey] = useState<SortKey>('result')
  const [sortDir, setSortDir] = useState<SortDir>('desc')
  const tableWrapRef = useRef<HTMLDivElement | null>(null)

  const filteredRows = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return rows
      .filter(row => resultFilter === 'all' || row.result_kind === resultFilter)
      .filter(row => !needle || row.name.toLowerCase().includes(needle) || row.result.toLowerCase().includes(needle))
      .sort((a, b) => compareRows(a, b, sortKey, sortDir))
  }, [filter, resultFilter, rows, sortDir, sortKey])

  const virtualizeRows = filteredRows.length > 80
  const rowVirtualizer = useVirtualizer({
    count: filteredRows.length,
    getScrollElement: () => tableWrapRef.current,
    estimateSize: () => 44,
    overscan: 10,
  })
  const virtualRows = rowVirtualizer.getVirtualItems()
  const virtualTotalSize = rowVirtualizer.getTotalSize()
  const virtualPaddingTop = virtualRows.length > 0 ? virtualRows[0].start : 0
  const virtualPaddingBottom = virtualRows.length > 0 ? virtualTotalSize - virtualRows[virtualRows.length - 1].end : 0
  const rowsToRender = virtualizeRows
    ? virtualRows.map(virtualRow => ({ row: filteredRows[virtualRow.index], virtualItem: virtualRow }))
    : filteredRows.map(row => ({ row, virtualItem: null as VirtualItem | null }))

  const sort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir(current => current === 'asc' ? 'desc' : 'asc')
      return
    }
    setSortKey(key)
    setSortDir(key === 'name' ? 'asc' : 'desc')
  }

  if (rows.length === 0) return <section className="run-report-panel"><h2>Transaction SLA Scoreboard</h2><p>No named transaction samples are available for an SLA scorecard. Request / action evidence remains available below.</p></section>

  return (
    <section className="run-report-panel run-report-scoreboard">
      <div className="run-report-section-heading">
        <div>
          <h2>Transaction SLA Scoreboard</h2>
          <p>{filteredRows.length} of {rows.length} transactions shown</p>
        </div>
        <div className="run-report-table-controls">
          <input aria-label="Filter transactions" value={filter} onChange={event => setFilter(event.currentTarget.value)} placeholder="Filter transaction" />
          <select aria-label="Filter by SLA result" value={resultFilter} onChange={event => setResultFilter(event.currentTarget.value as (typeof RESULT_FILTERS)[number])}>
            {RESULT_FILTERS.map(value => <option key={value} value={value}>{value === 'all' ? 'All results' : resultLabel(value)}</option>)}
          </select>
        </div>
      </div>
      <div className="run-report-table-wrap" ref={tableWrapRef}>
        <table className="run-report-table">
          <thead>
            <tr>
              <th><HeaderButton label="Transaction" sortKey="name" activeKey={sortKey} direction={sortDir} onSort={sort} /></th>
              <th><HeaderButton label="Volume" sortKey="volume" activeKey={sortKey} direction={sortDir} onSort={sort} /></th>
              <th><HeaderButton label="P95" sortKey="p95" activeKey={sortKey} direction={sortDir} onSort={sort} /></th>
              <th>SLA</th>
              <th><HeaderButton label="Errors" sortKey="error" activeKey={sortKey} direction={sortDir} onSort={sort} /></th>
              <th><HeaderButton label="Result" sortKey="result" activeKey={sortKey} direction={sortDir} onSort={sort} /></th>
            </tr>
          </thead>
          <tbody>
            {virtualizeRows && virtualPaddingTop > 0 && <tr aria-hidden="true"><td aria-hidden="true" colSpan={6} style={{ height: virtualPaddingTop }} /></tr>}
            {rowsToRender.map(({ row }) => (
              <ScoreboardRow key={row.name} row={row} selected={selectedAction === row.name} onSelect={onSelectAction} />
            ))}
            {virtualizeRows && virtualPaddingBottom > 0 && <tr aria-hidden="true"><td aria-hidden="true" colSpan={6} style={{ height: virtualPaddingBottom }} /></tr>}
          </tbody>
        </table>
      </div>
      {/*
        The screen-reader mirror renders every filtered row, so it is only worth
        it for small sets: when the visible table is virtualized (large set) this
        full hidden table would rebuild O(n) DOM nodes on every filter keystroke,
        negating the virtualization it sits beside. For large sets the virtualized
        table's own rows carry the accessible content.
      */}
      {!virtualizeRows && (
        <table className="sr-only" aria-label="Complete transaction SLA scoreboard">
          <tbody>
            {filteredRows.map(row => (
              <tr key={row.name}>
                <td>{row.name}</td>
                <td>{row.volume}</td>
                <td>{row.result_kind}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="run-report-drilldown">
        <h3>{selectedAction || 'Select a transaction'}</h3>
        {loadingTransactions ? (
          <p role="status">Loading drill-down...</p>
        ) : selectedAction && drilldownTransactions.length > 0 ? (
          <ul>
            {drilldownTransactions.slice(0, 5).map((event, index) => (
              <li key={`${event.timestamp}-${event.duration_ms}-${index}`}>
                <span>{new Date(event.timestamp).toLocaleTimeString()}</span>
                <strong>{formatMs(event.duration_ms)}</strong>
                <em>{event.success ? 'ok' : event.error_message || 'error'}</em>
              </li>
            ))}
          </ul>
        ) : (
          <p>Drill-down samples appear here after selecting a transaction.</p>
        )}
      </div>
    </section>
  )
}
