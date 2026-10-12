import { useLiveObservation } from '../hooks/useLiveObservation'
import { errorRateBasisLabel, errorRateLabel, groupRunCounts, groupRunErrorPercent, livePopulation, liveRate, liveThroughputPopulation, throughputLabel } from '../utils/liveErrorRate'
import LiveObservationNotice from './LiveObservationNotice'
import { GroupLiveMetrics, TestSummary } from '../types'
import { formatRunClock } from '../utils/timeUtils'
import { formatFixed } from '../utils/numberFormat'
import { MISSING_VALUE } from './runReport/reportPresentation'
import { formatDateTime } from '../utils/dateTime'

interface Props {
  test: TestSummary
  groupMetrics: GroupLiveMetrics[]
  remainingSeconds: number
}

function metricValue(value: number | undefined, suffix = '', digits = 1): string {
  if (value === undefined || !Number.isFinite(value)) return MISSING_VALUE
  return `${formatFixed(value, digits)}${suffix}`
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
  const sampleTime = Number.isFinite(observedAt) ? formatDateTime(observedAt, { seconds: true }) : 'Time unavailable'
  // A live run is still producing data; only a finished run has a true gap.
  const pending = terminal || retained ? MISSING_VALUE : 'Awaiting data'
  const remaining = terminal ? 'Run ended' : remainingSeconds > 0 ? formatRunClock(remainingSeconds) : pending
  // As in Command Center: requests once the run has sent any.
  const population = livePopulation(test.live_metrics)
  const throughputPopulation = liveThroughputPopulation(test, population)
  const errorBasis = groupMetrics.some(group => groupRunCounts(group, population)?.basis === 'window') ? 'window' : 'run'
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
        <div><dt>{retained ? 'Last observed load' : 'Observed load'}</dt><dd>{test.live_metrics?.active_vusers ?? pending}</dd></div>
        {test.termination_mode === 'iterations' ? <><div><dt>Completed / expected iterations</dt><dd>{test.live_metrics?.completed_iterations ?? 0} / {(test.groups || []).reduce((sum, group) => sum + group.vusers * (group.iterations || 0), 0)}</dd></div><div><dt>Elapsed</dt><dd>{test.started_at ? formatRunClock(Math.max(0, ((test.ended_at ? Date.parse(test.ended_at) : Date.now()) - Date.parse(test.started_at)) / 1000)) : 'Not started'}</dd></div></> : <div><dt>Remaining</dt><dd>{remaining}</dd></div>}
        <div><dt>Last telemetry</dt><dd>{test.live_metrics ? `${historical ? 'Historical sample' : observation === 'stale' ? 'Stale sample' : 'Last sample'} · ${sampleTime}` : pending}</dd></div>
      </dl>
      <LiveObservationNotice state={observation} />
      <div className="advanced-group-table-wrap">
        <table className="advanced-group-table">
          <thead><tr><th>Group</th><th>{retained ? 'Last observed / planned' : 'Observed / planned'}</th><th>{throughputLabel(throughputPopulation)} (last sample)</th><th>P95</th><th>{errorRateLabel(population)} ({errorRateBasisLabel(errorBasis)})</th></tr></thead>
          <tbody>
            {groupMetrics.map(group => (
              <tr key={group.name}>
                <th scope="row">{group.name}</th>
                <td>{group.vusers ?? MISSING_VALUE} / {test.groups?.find(item => item.name === group.name)?.vusers ?? MISSING_VALUE} VUsers</td>
                <td>{metricValue(liveRate(group, throughputPopulation))}</td>
                <td>{metricValue(group.p95_ms, ' ms')}</td>
                <td>{metricValue(groupRunErrorPercent(group, population), '%', 2)}</td>
              </tr>
            ))}
            {groupMetrics.length === 0 && <tr><td colSpan={5}>No group observations have arrived yet.</td></tr>}
          </tbody>
        </table>
      </div>
    </section>
  )
}
