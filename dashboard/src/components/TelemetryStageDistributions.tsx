import type { TelemetryStageDistribution } from '../types'

const STAGE_LABELS: Record<string, string> = {
  generator_processing: 'Generator processing',
  transport: 'Transport',
  controller_fanout: 'Controller fan-out',
  sample_age: 'Sample age (end to end)',
  browser_render: 'Browser render',
}

const EXCLUSION_LABELS: Record<string, string> = {
  clock_unqualified: 'clock not qualified',
  background_tab: 'background tab',
  disconnected: 'disconnected',
  stalled_render: 'stalled render',
  invalid: 'invalid',
  before_run: 'before the run',
  future_time: 'future time',
  missing_time: 'no time',
  unknown_state: 'unknown state',
}

function formatStageMs(value?: number): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  if (value < 2000) return `${value < 10 ? value.toFixed(1) : Math.round(value)} ms`
  return `${(value / 1000).toFixed(2)} s`
}

function describeStageExclusions(excluded?: Record<string, number>): string {
  if (!excluded) return ''
  return Object.entries(excluded)
    .filter(([, count]) => count > 0)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([reason, count]) => `${count} ${EXCLUSION_LABELS[reason] || reason.replace(/_/g, ' ')}`)
    .join(', ')
}

// Each stage's own sampled percentiles for the run so far. Stages are
// independent distributions: their percentiles are never added, and an
// unsampled stage shows no figures rather than zero.
export default function TelemetryStageDistributions({ stages }: { stages?: TelemetryStageDistribution[] }) {
  if (!stages || stages.length === 0) return null
  return (
    <div className="telemetry-stage-distributions">
      <h4 className="goal-status-title">Stage distributions (run so far)</h4>
      <div className="data-table" style={{ overflowX: 'auto' }}>
      <table aria-label="Telemetry stage distributions">
        <thead>
          <tr>
            <th scope="col">Stage</th>
            <th scope="col">Samples</th>
            <th scope="col">p50</th>
            <th scope="col">p95</th>
            <th scope="col">p99</th>
            <th scope="col">Max</th>
            <th scope="col">Notes</th>
          </tr>
        </thead>
        <tbody>
          {stages.map(stage => {
            const measured = stage.availability === 'measured'
            const notes = [
              stage.advisory ? 'advisory, not run health' : '',
              measured ? '' : 'not sampled',
              describeStageExclusions(stage.excluded) ? `excluded: ${describeStageExclusions(stage.excluded)}` : '',
            ].filter(Boolean).join(' · ')
            return (
              <tr key={stage.stage} data-stage={stage.stage}>
                <td><strong>{STAGE_LABELS[stage.stage] || stage.stage}</strong></td>
                <td>{stage.samples}</td>
                <td>{measured ? formatStageMs(stage.p50_ms) : '—'}</td>
                <td>{measured ? formatStageMs(stage.p95_ms) : '—'}</td>
                <td>{measured ? formatStageMs(stage.p99_ms) : '—'}</td>
                <td>{measured ? formatStageMs(stage.max_ms) : '—'}</td>
                <td>{notes}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
      </div>
      <p className="goal-status-subtitle">Each stage is its own distribution; percentiles are not summed across stages. Browser render comes from dashboard tabs that opted in, and only visible renders count.</p>
    </div>
  )
}
