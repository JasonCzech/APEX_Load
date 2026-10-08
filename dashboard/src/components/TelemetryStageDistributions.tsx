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

// STAGE_ADOPTION_SLACK_MS is how long after the run's start sampling may
// begin before the panel says the distributions cover only part of the run.
export const STAGE_ADOPTION_SLACK_MS = 60_000

// stageCoverageNote describes distributions this controller began sampling
// well after the run started (it adopted the run after a failover or
// restart), or ones it never sampled.
export function stageCoverageNote(stages: TelemetryStageDistribution[], startedAt?: string): string {
  if (stages.some(stage => stage.reason === 'recorder_lost')) {
    return 'This controller did not sample the run (it restarted or took over after the run); stage distributions are unavailable.'
  }
  const from = Date.parse(stages.find(stage => stage.sampled_from)?.sampled_from || '')
  const started = Date.parse(startedAt || '')
  if (!Number.isFinite(from) || !Number.isFinite(started) || from - started <= STAGE_ADOPTION_SLACK_MS) return ''
  return `Sampled from ${new Date(from).toLocaleTimeString()}: this controller took over the run then, so earlier publications are not included.`
}

// Each stage's own sampled percentiles for the run so far. Stages are
// independent distributions: their percentiles are never added, and an
// unsampled stage shows no figures rather than zero.
export default function TelemetryStageDistributions({ stages, startedAt }: { stages?: TelemetryStageDistribution[]; startedAt?: string }) {
  if (!stages || stages.length === 0) return null
  const coverage = stageCoverageNote(stages, startedAt)
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
      {coverage && <p className="goal-status-subtitle" role="note">{coverage}</p>}
      <p className="goal-status-subtitle">Each stage is its own distribution; percentiles are not summed across stages. Browser render comes from dashboard tabs that opted in, and only visible renders count.</p>
    </div>
  )
}
