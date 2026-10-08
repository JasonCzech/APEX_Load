import { useLiveObservation } from '../hooks/useLiveObservation'
import { errorRateBasisLabel, groupRunCounts, groupRunErrorPercent } from '../utils/liveErrorRate'
import LiveObservationNotice from './LiveObservationNotice'
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
  const terminal = ['COMPLETE', 'FAILED', 'ABORTED', 'STOPPED'].includes(test.status)
  const observation = useLiveObservation(test.status, test.live_metrics)
  const historical = observation === 'historical'
  const retained = historical || observation === 'stale'
  const observedAt = test.live_metrics?.timestamp ? Date.parse(test.live_metrics.timestamp) : NaN
  const sampleTime = Number.isFinite(observedAt) ? new Date(observedAt).toLocaleString() : 'Time unavailable'
  const remaining = terminal ? 'Run ended' : remainingSeconds > 0 ? formatTimeCompact(remainingSeconds) : 'Not available'
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
        <div><dt>{retained ? 'Last observed load' : 'Observed load'}</dt><dd>{test.live_metrics?.active_vusers ?? 'No observation'}</dd></div>
        {test.termination_mode === 'iterations' ? <><div><dt>Completed / expected iterations</dt><dd>{test.live_metrics?.completed_iterations ?? 0} / {(test.groups || []).reduce((sum, group) => sum + group.vusers * (group.iterations || 0), 0)}</dd></div><div><dt>Elapsed</dt><dd>{test.started_at ? formatTimeCompact(Math.max(0, ((test.ended_at ? Date.parse(test.ended_at) : Date.now()) - Date.parse(test.started_at)) / 1000)) : 'Not started'}</dd></div></> : <div><dt>Remaining</dt><dd>{remaining}</dd></div>}
        <div><dt>Last telemetry</dt><dd>{test.live_metrics ? `${historical ? 'Historical sample' : observation === 'stale' ? 'Stale sample' : 'Last sample'} · ${sampleTime}` : 'No sample available'}</dd></div>
      </dl>
      <LiveObservationNotice state={observation} />
      <div className="advanced-group-table-wrap">
        <table className="advanced-group-table">
          <thead><tr><th>Group</th><th>{retained ? 'Last observed / planned' : 'Observed / planned'}</th><th>TPS</th><th>P95</th><th>Error rate ({errorRateBasisLabel(groupMetrics.some(group => groupRunCounts(group)?.basis === 'window') ? 'window' : 'run')})</th></tr></thead>
          <tbody>
            {groupMetrics.map(group => (
              <tr key={group.name}>
                <th scope="row">{group.name}</th>
                <td>{group.vusers ?? '—'} / {test.groups?.find(item => item.name === group.name)?.vusers ?? '—'} VUsers</td>
                <td>{metricValue(group.tps)}</td>
                <td>{metricValue(group.p95_ms, ' ms')}</td>
                <td>{metricValue(groupRunErrorPercent(group), '%', 2)}</td>
              </tr>
            ))}
            {groupMetrics.length === 0 && <tr><td colSpan={5}>No group observations have arrived yet.</td></tr>}
          </tbody>
        </table>
      </div>
    </section>
  )
}
