import type { TelemetryAcceptanceCriterion, TelemetryAcceptanceVerdict as Verdict } from '../types'
import { formatAcceptancePercent } from './telemetryAcceptanceFormat'

function formatValue(value: number | undefined, unit: string): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return 'unknown'
  if (unit === 'ratio') return formatAcceptancePercent(value)
  if (unit === 'ms') return `${Math.round(value).toLocaleString()} ms`
  return `${Math.round(value).toLocaleString()} ${unit}`
}

function observedLabel(criterion: TelemetryAcceptanceCriterion): string {
  const observed = formatValue(criterion.observed, criterion.unit)
  if (criterion.observed === undefined) return observed
  if (criterion.bound === 'lower_bound') return `≥ ${observed}`
  return criterion.bound === 'upper_bound' ? `≤ ${observed}` : observed
}

function boundLabel(bound: string): string {
  return bound === 'partial' ? 'partial evidence' : bound.replace('_', ' ')
}

function modeLabel(mode: string): string {
  if (mode === 'fail_ci') return 'fail_ci — can fail an otherwise passing CI run'
  if (mode === 'unknown') return 'mode not retained — no CI gate applies'
  return 'advisory — never changes a CI outcome'
}

// The run's telemetry verdict under its pinned acceptance policy (C7),
// shown apart from the application verdict. A provisional verdict reads live
// evidence and can change until the run is finalized.
export default function TelemetryAcceptanceVerdict({ verdict }: { verdict?: Verdict }) {
  if (!verdict) return null
  const outcome = verdict.outcome || 'inconclusive'
  return <section className="goal-status-card telemetry-acceptance-card" aria-label="Telemetry acceptance">
    <h3>Telemetry acceptance{verdict.provisional ? ' (provisional)' : ''}</h3>
    <p>
      <span className={`telemetry-acceptance-outcome telemetry-acceptance-${outcome}`}>{outcome.toUpperCase()}</span>{' '}
      {verdict.policy ? <>under <strong>{verdict.policy.name}</strong> revision {verdict.policy.revision}</> : 'under the run’s pinned policy'} · {modeLabel(verdict.mode)}
    </p>
    <p>This verdict is separate from the application verdict. Unknown required evidence is inconclusive, never a pass.{verdict.provisional ? ' Live evidence: the finalized verdict is in the run report.' : ''}</p>
    {verdict.criteria.length > 0 && <div className="table-scroll"><table className="manifest-table" aria-label="Telemetry acceptance criteria">
      <thead><tr><th>Criterion</th><th>Result</th><th>Observed</th><th>Threshold</th><th>Basis</th></tr></thead>
      <tbody>{verdict.criteria.map(criterion => <tr key={criterion.criterion}>
        <td>{criterion.criterion}</td>
        <td>{criterion.outcome}{criterion.advisory ? ' (advisory, never decides)' : ''}</td>
        <td>{observedLabel(criterion)}{criterion.bound ? ` (${boundLabel(criterion.bound)})` : ''}</td>
        <td>{criterion.comparator} {formatValue(criterion.threshold, criterion.unit)}</td>
        <td>{criterion.numerator !== undefined && criterion.denominator !== undefined ? `${criterion.numerator.toLocaleString()} / ${criterion.denominator.toLocaleString()}` : criterion.denominator !== undefined ? `${criterion.denominator.toLocaleString()} samples` : '—'}</td>
      </tr>)}</tbody>
    </table></div>}
    {(verdict.reasons?.length ?? 0) > 0 && <ul aria-label="Telemetry acceptance reasons">{verdict.reasons?.map(reason => <li key={reason}>{reason}</li>)}</ul>}
    {(verdict.exclusions?.length ?? 0) > 0 && <details><summary>Not counted</summary><ul>{verdict.exclusions?.map(exclusion => <li key={exclusion}>{exclusion}</li>)}</ul></details>}
  </section>
}
