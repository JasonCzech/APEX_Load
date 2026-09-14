import { GroupLiveMetrics, TestSummary } from '../types'
import { formatTimeCompact } from '../utils/timeUtils'

interface Props {
  test: TestSummary
  groupMetrics: GroupLiveMetrics[]
  remainingSeconds: number
}

function metricValue(value: number | undefined, suffix = '', digits = 1): string {
  if (value === undefined || !Number.isFinite(value)) return 'No observation'
  return `${value.toFixed(digits)}${suffix}`
}

function lifecycleLabel(test: TestSummary): string {
  if (test.status === 'RUNNING') return 'Running'
  if (test.status === 'PAUSED') return 'Paused'
  if (test.status === 'STOPPING') return 'Stopping'
  return test.status.replace(/_/g, ' ')
}

/** A read-only orientation card. Command Center remains the single run-control surface. */
export default function TestExecutionRunState({ test, groupMetrics, remainingSeconds }: Props) {
  const remaining = remainingSeconds > 0 ? formatTimeCompact(remainingSeconds) : 'Not available'
  return (
    <section className="advanced-run-state" aria-label="Run state">
      <header className="advanced-section-header">
        <div>
          <h3>Run state</h3>
          <p>Observed behavior against the planned workload. Make run changes in Command Center.</p>
        </div>
        <span className={`advanced-state-badge ${test.status.toLowerCase()}`}>{lifecycleLabel(test)}</span>
      </header>
      <dl className="advanced-fact-grid">
        <div><dt>Planned load</dt><dd>{test.total_vusers} VUsers</dd></div>
        <div><dt>Observed load</dt><dd>{test.live_metrics?.active_vusers ?? 'No observation'}</dd></div>
        <div><dt>Remaining</dt><dd>{remaining}</dd></div>
        <div><dt>Last telemetry</dt><dd>{test.live_metrics ? 'Live sample received' : 'No live sample'}</dd></div>
      </dl>
      <div className="advanced-group-table-wrap">
        <table className="advanced-group-table">
          <thead><tr><th>Group</th><th>Observed / planned</th><th>TPS</th><th>P95</th><th>Error rate</th></tr></thead>
          <tbody>
            {groupMetrics.map(group => (
              <tr key={group.name}>
                <th scope="row">{group.name}</th>
                <td>{group.vusers ?? '—'} / {test.groups?.find(item => item.name === group.name)?.vusers ?? '—'} VUsers</td>
                <td>{metricValue(group.tps)}</td>
                <td>{metricValue(group.p95_ms, ' ms')}</td>
                <td>{group.transactions > 0 ? metricValue((group.errors / group.transactions) * 100, '%', 2) : '0.00%'}</td>
              </tr>
            ))}
            {groupMetrics.length === 0 && <tr><td colSpan={5}>No group observations have arrived yet.</td></tr>}
          </tbody>
        </table>
      </div>
    </section>
  )
}
