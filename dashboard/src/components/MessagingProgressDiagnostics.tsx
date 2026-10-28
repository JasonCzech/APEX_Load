import type { MessagingProgressSnapshot } from '../types'
import { capturedSequenceLag, checkpointTiming, positionText } from './messagingProgress'

export default function MessagingProgressDiagnostics({ snapshot }: { snapshot?: MessagingProgressSnapshot }) {
  if (!snapshot) return null
  return <details className="run-report-panel messaging-diagnostics-panel">
    <summary>Event Hubs processor progress</summary>
    <p className="run-report-muted">Cumulative evidence per ownership generation. Sequence backlog is as of source capture; transit time is unqualified. Ownership and both cursors must be observed within 30 seconds of that capture. Provider offsets are opaque values separate from sequence numbers.</p>
    <p>Latest snapshot {snapshot.captured_at} · Lifecycle {snapshot.lifecycle || 'Multiple sources'}</p>
    {snapshot.incomplete && <p role="note">Processor coverage incomplete; lag is unscored. {snapshot.unavailable_reason || 'Some evidence is missing.'} {snapshot.dropped_rows ? `At least ${snapshot.dropped_rows} rows omitted across sources.` : ''}</p>}
    {snapshot.rows.length === 0 && <p>No partition ownership observations captured.</p>}
    <div className="run-report-table-wrap"><table className="run-report-table"><thead><tr>{['Execution group', 'Source / subscription', 'Partition / generation / source capture', 'Ownership / inventory', 'Delivered / rejected / redelivered', 'Delivered sequence', 'Checkpoint sequence', 'Observed tail sequence', 'Delivered backlog', 'Checkpoint backlog', 'Checkpoint outcomes', 'Checkpoint timing'].map(label => <th key={label}>{label}</th>)}</tr></thead>
      <tbody>{snapshot.rows.map(row => <tr key={JSON.stringify([row.origin, row.group, row.source_id, row.subscription, row.session_id, row.partition, row.generation])}>
        <td>{row.group || 'Unavailable'}</td><td>{row.source} / {row.subscription}</td><td>{row.partition} / {row.generation} / {row.captured_at}</td><td>{row.ownership} / {row.inventory_complete ? `${row.expected_partitions} partitions confirmed` : 'Incomplete inventory'}</td>
        <td>{row.delivered} / {row.rejected} / {row.redelivered}</td><td>{positionText(row.delivered_position)}</td><td>{positionText(row.checkpoint_position)}</td><td>{positionText(row.tail_position)}</td><td>{capturedSequenceLag(snapshot, row, 'delivered')}</td><td>{capturedSequenceLag(snapshot, row, 'checkpoint')}</td>
        <td>{row.checkpoint.attempts} attempts; {row.checkpoint.committed} committed; {row.checkpoint.failed} failed; {row.checkpoint.fenced} fenced; {row.checkpoint.cancelled_before_write} cancelled before write; {row.checkpoint.unconfirmed} unconfirmed; {row.checkpoint.in_flight} in flight; {row.checkpoint.retry_attempts} retries</td><td>{checkpointTiming(row.checkpoint.duration)}</td>
      </tr>)}</tbody></table></div>
  </details>
}
