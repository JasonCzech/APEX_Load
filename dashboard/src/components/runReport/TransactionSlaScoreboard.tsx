import { useMemo, useRef, useState } from 'react'
import { useVirtualizer, type VirtualItem } from '@tanstack/react-virtual'

import type { RunArchiveSLARow, TransactionEvent } from '../../types'
import { formatMs, formatMsValue, formatNumber, formatPct } from './runReportModel'
import { MISSING_VALUE } from './reportPresentation'
import { formatDateTime } from '../../utils/dateTime'
import PercentileQualityMark from '../PercentileQualityMark'

type SortKey = 'name' | 'volume' | 'p50' | 'p90' | 'p95' | 'p99' | 'error' | 'result'
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
  // No SLA, no result: the same mark the SLA column shows.
  return MISSING_VALUE
}

function sortValue(row: RunArchiveSLARow, key: SortKey) {
  switch (key) {
    case 'volume': return row.volume
    case 'p50': return row.p50_ms ?? -1
    case 'p90': return row.p90_ms ?? -1
    case 'p95': return row.p95_ms
    case 'p99': return row.p99_ms ?? -1
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

function SortableTh({ label, sortKey, activeKey, direction, onSort }: {
  label: string
  sortKey: SortKey
  activeKey: SortKey
  direction: SortDir
  onSort: (key: SortKey) => void
}) {
  const active = activeKey === sortKey
  return (
    <th aria-sort={active ? (direction === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button className="run-report-table-sort" type="button" onClick={() => onSort(sortKey)}>
        {label}<span aria-hidden="true">{active ? (direction === 'asc' ? ' ↑' : ' ↓') : ''}</span>
      </button>
    </th>
  )
}

function ScoreboardRow({ row, selected, onSelect }: { row: RunArchiveSLARow; selected?: boolean; onSelect?: (action: string) => void }) {
  return (
    <tr className={selected ? 'selected' : ''} onClick={() => onSelect?.(row.name)}>
      <td>{row.name}</td>
      <td>{formatNumber(row.volume, 0)}</td>
      <td>{formatMsValue(row.min_ms)}</td>
      <td><PercentileQualityMark quality={row.percentile_quality} value={row.p50_ms} />{formatMsValue(row.p50_ms)}</td>
      <td><PercentileQualityMark quality={row.percentile_quality} value={row.p90_ms} />{formatMsValue(row.p90_ms)}</td>
      <td><PercentileQualityMark quality={row.percentile_quality} value={row.p95_ms} />{formatMsValue(row.p95_ms)}</td>
      <td><PercentileQualityMark quality={row.percentile_quality} value={row.p99_ms} />{formatMsValue(row.p99_ms)}</td>
      <td>{formatMsValue(row.max_ms)}</td>
      <td>{row.has_sla ? formatMsValue(row.sla_ms) : MISSING_VALUE}</td>
      <td>{formatPct(row.error_pct)}</td>
      {/* Only a judged result is a status pill; no SLA is plain text, as in the SLA column. */}
      <td>{row.result_kind === 'na' ? <span title="No SLA">{MISSING_VALUE}</span> : <span className={`run-report-sla-pill run-report-sla-${row.result_kind}`}>{resultLabel(row.result_kind)}</span>}</td>
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

  if (rows.length === 0) return <section className="run-report-panel"><h2>Transaction SLA scoreboard</h2><p>No named transaction samples are available for an SLA scorecard. Request / action evidence remains available below.</p></section>

  return (
    <section className="run-report-panel run-report-scoreboard">
      <div className="run-report-section-heading">
        <div>
          <h2>Transaction SLA scoreboard</h2>
          <p>{filteredRows.length} of {rows.length} transactions shown</p>
        </div>
        <div className="run-report-table-controls">
          <input aria-label="Filter transactions" value={filter} onChange={event => setFilter(event.currentTarget.value)} placeholder="Filter transaction" />
          <select aria-label="Filter by SLA result" value={resultFilter} onChange={event => setResultFilter(event.currentTarget.value as (typeof RESULT_FILTERS)[number])}>
            {RESULT_FILTERS.map(value => <option key={value} value={value}>{value === 'all' ? 'All results' : value === 'na' ? 'No SLA' : resultLabel(value)}</option>)}
          </select>
        </div>
      </div>
      <div className="run-report-table-wrap" ref={tableWrapRef}>
        <table className="run-report-table">
          <thead>
            <tr>
              <SortableTh label="Transaction" sortKey="name" activeKey={sortKey} direction={sortDir} onSort={sort} />
              <SortableTh label="Count" sortKey="volume" activeKey={sortKey} direction={sortDir} onSort={sort} />
              <th>Min (ms)</th>
              <SortableTh label="P50 (ms)" sortKey="p50" activeKey={sortKey} direction={sortDir} onSort={sort} />
              <SortableTh label="P90 (ms)" sortKey="p90" activeKey={sortKey} direction={sortDir} onSort={sort} />
              <SortableTh label="P95 (ms)" sortKey="p95" activeKey={sortKey} direction={sortDir} onSort={sort} />
              <SortableTh label="P99 (ms)" sortKey="p99" activeKey={sortKey} direction={sortDir} onSort={sort} />
              <th>Max (ms)</th>
              <th>SLA (ms)</th>
              <SortableTh label="Error rate" sortKey="error" activeKey={sortKey} direction={sortDir} onSort={sort} />
              <SortableTh label="Result" sortKey="result" activeKey={sortKey} direction={sortDir} onSort={sort} />
            </tr>
          </thead>
          <tbody>
            {virtualizeRows && virtualPaddingTop > 0 && <tr aria-hidden="true"><td aria-hidden="true" colSpan={11} style={{ height: virtualPaddingTop }} /></tr>}
            {rowsToRender.map(({ row }) => (
              <ScoreboardRow key={row.name} row={row} selected={selectedAction === row.name} onSelect={onSelectAction} />
            ))}
            {virtualizeRows && virtualPaddingBottom > 0 && <tr aria-hidden="true"><td aria-hidden="true" colSpan={11} style={{ height: virtualPaddingBottom }} /></tr>}
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
                <td>{formatNumber(row.volume, 0)}</td>
                <td>{row.result_kind === 'na' ? 'No SLA' : resultLabel(row.result_kind)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="run-report-drilldown">
        <h3>{selectedAction || 'Select a transaction'}</h3>
        {loadingTransactions ? (
          <p role="status">Loading drill-down…</p>
        ) : selectedAction && drilldownTransactions.length > 0 ? (
          <ul>
            {drilldownTransactions.slice(0, 5).map((event, index) => (
              <li key={`${event.timestamp}-${event.duration_ms}-${index}`}>
                <span>{formatDateTime(event.timestamp, { seconds: true })}</span>
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
