import { Fragment, useMemo, useState, type ReactNode } from 'react'
import type { DebugCompletionSnapshot } from './debugCompletionModel'
import { debugTransactionRows, type DebugTransactionRow } from './debugTransactionModel'
import { metricHeader } from '../metricLabels'
import { formatMs } from './runReport/runReportModel'
import { formatDateTime } from '../utils/dateTime'

const phaseLabel = { init: 'Init', action: 'Actions', teardown: 'Teardown' }
export default function DebugTransactionTable({ snapshot, onSelectEvent }: { snapshot: DebugCompletionSnapshot; onSelectEvent: (index: number) => void }) {
  const rows = useMemo(() => debugTransactionRows(snapshot), [snapshot])
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const renderRows = (items: DebugTransactionRow[], depth = 0): ReactNode => items.map(row => {
    const open = expanded.has(row.key)
    return <Fragment key={row.key}>
      <tr>
        <th scope="row"><div className="debug-transaction-name" style={{ paddingInlineStart: depth * 20 }}>
          {row.children.length > 0 && <button type="button" className="btn btn-sm btn-ghost" aria-expanded={open} aria-label={`${open ? 'Collapse' : 'Expand'} ${row.name}`} onClick={() => setExpanded(previous => { const next = new Set(previous); if (next.has(row.key)) next.delete(row.key); else next.add(row.key); return next })}>{open ? '▾' : '▸'}</button>}
          <span>{row.name}<small>{row.kind === 'container' ? 'Control flow' : row.kind === 'subtransaction' ? 'Subtransaction' : row.kind === 'execution' ? 'Execution' : row.kind === 'transaction' ? 'Transaction' : 'Page / step'}{row.executions > 1 ? ` · ${row.executions} executions` : ''}</small></span>
        </div></th>
        <td>{phaseLabel[row.phase]}</td>
        <td><span className={`debug-transaction-status ${row.status === 'Passed' ? 'passed' : row.status === 'Failed' ? 'failed' : 'muted'}`}>{row.status}</span>{row.incomplete && row.status !== 'Not observed' && <small>Incomplete evidence</small>}</td>
        <td>{row.startedAt ? <time dateTime={row.startedAt}>{formatDateTime(row.startedAt, { milliseconds: true })}</time> : '—'}</td>
        <td>{row.completedAt ? <time dateTime={row.completedAt}>{formatDateTime(row.completedAt, { milliseconds: true })}</time> : '—'}</td>
        <td>{row.durationMs == null ? '—' : formatMs(row.durationMs)}</td>
        <td>{row.eventIndex != null && <button type="button" className="btn btn-sm btn-secondary" onClick={() => onSelectEvent(row.eventIndex!)}>Inspect<span className="sr-only"> {row.name}</span></button>}</td>
      </tr>
      {open && renderRows(row.children, depth + 1)}
    </Fragment>
  })
  return <section aria-label="Transaction details" className="debug-transaction-section">
    <h3>Transactions</h3>
    {rows.length ? <>
      <p>Expand transactions for subtransactions, pages, and repeated executions. Group durations are elapsed spans (including pauses and think time); step durations are recorded measurements. Missing completion timing is left blank.</p>
      <div className="debug-transaction-table-wrap" tabIndex={0} role="region" aria-label="Transaction timings"><table className="debug-transaction-table"><caption className="sr-only">Transactions from the completed debug run</caption><thead><tr><th scope="col">Transaction / page</th><th scope="col">Phase</th><th scope="col">Status</th><th scope="col">Start time</th><th scope="col">End time</th><th scope="col">{metricHeader('duration')}</th><th scope="col">Evidence</th></tr></thead><tbody>{renderRows(rows)}</tbody></table></div>
    </> : <p>{snapshot.transactions ? 'No transactions were defined in this run’s script.' : 'Transaction definitions were not retained for this run. Debug again to capture the transaction breakdown.'}</p>}
  </section>
}
