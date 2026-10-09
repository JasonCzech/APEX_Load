import { useId, useMemo } from 'react'

import type { TransactionEvent, TransactionResponseTimeSLAResult } from '../types'
import { formatFixed } from '../utils/numberFormat'

interface Props {
  rows: TransactionResponseTimeSLAResult[]
  title?: string
  selectedGroup?: string
  selectedTransaction?: string
  onSelect?: (row: TransactionResponseTimeSLAResult) => void
  drilldownTransactions?: TransactionEvent[]
  loadingTransactions?: boolean
}

const statusRank: Record<TransactionResponseTimeSLAResult['status'], number> = {
  fail: 0,
  not_evaluated: 1,
  pass: 2,
}

function statusLabel(status: TransactionResponseTimeSLAResult['status']): string {
  if (status === 'not_evaluated') return 'Not evaluated'
  return status === 'fail' ? 'Fail' : 'Pass'
}

export default function TransactionResponseTimeSLATable({
  rows,
  title = 'Transaction SLA compliance',
  selectedGroup,
  selectedTransaction,
  onSelect,
  drilldownTransactions = [],
  loadingTransactions,
}: Props) {
  const titleId = useId()
  const orderedRows = useMemo(() => [...rows].sort((left, right) =>
    statusRank[left.status] - statusRank[right.status] ||
    right.missed_pct - left.missed_pct ||
    left.group.localeCompare(right.group) ||
    left.transaction.localeCompare(right.transaction),
  ), [rows])

  if (rows.length === 0) return null

  return (
    <section className="tx-section transaction-sla-compliance" aria-labelledby={titleId}>
      <div className="transaction-sla-compliance-heading">
        <div>
          <h3 className="chart-title" id={titleId}>{title}</h3>
          <p>Cumulative successful named business transactions; technical failures are shown separately and excluded from compliance.</p>
        </div>
        <span>95% required</span>
      </div>
      <div className="tx-table-scroll">
        <table className="tx-table">
          <thead><tr><th>Group</th><th>Transaction</th><th>Threshold</th><th>Eligible successes</th><th>Excluded errors</th><th>Missed</th><th>Missed %</th><th>Status</th></tr></thead>
          <tbody>
            {orderedRows.map(row => {
              const selected = selectedGroup === row.group && selectedTransaction === row.transaction
              const transactionLabel = row.transaction || 'No named transaction observed'
              return (
                <tr
                  key={`${row.group}\u0000${row.transaction}`}
                  className={selected ? 'selected' : ''}
                >
                  <td>{row.group}</td>
                  <td className="tx-name">
                    {onSelect && row.transaction ? (
                      <button
                        type="button"
                        className="transaction-sla-select"
                        aria-label={`Show archived samples for ${row.transaction} in ${row.group}`}
                        aria-pressed={selected}
                        onClick={() => onSelect(row)}
                      >
                        {transactionLabel}
                      </button>
                    ) : transactionLabel}
                  </td>
                  <td>{row.threshold_ms.toLocaleString()} ms</td>
                  <td>{row.eligible_success_count.toLocaleString()}</td>
                  <td>{row.excluded_error_count.toLocaleString()}</td>
                  <td>{row.missed_count.toLocaleString()}</td>
                  <td>{formatFixed(row.missed_pct, 2)}%</td>
                  <td><span className={`transaction-sla-status transaction-sla-status-${row.status}`} aria-label={`SLA status: ${statusLabel(row.status)}`}>{statusLabel(row.status)}</span></td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {(selectedGroup || selectedTransaction) && (
        <div className="run-report-drilldown">
          <h3>{selectedGroup} / {selectedTransaction || 'No named transaction observed'}</h3>
          {loadingTransactions ? (
            <p role="status">Loading drill-down…</p>
          ) : selectedTransaction && drilldownTransactions.length > 0 ? (
            <ul>
              {drilldownTransactions.slice(0, 5).map((event, index) => (
                <li key={`${event.timestamp}-${event.duration_ms}-${index}`}>
                  <span>{new Date(event.timestamp).toLocaleTimeString()}</span>
                  <strong>{event.duration_ms.toLocaleString()} ms</strong>
                  <em>{event.success ? 'ok' : event.error_message || 'error'}</em>
                </li>
              ))}
            </ul>
          ) : (
            <p>No matching archived samples are available.</p>
          )}
        </div>
      )}
    </section>
  )
}
