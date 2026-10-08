import type { RcaBrowserBaseline, RcaBrowserBaselineResult } from '../../types/rca'
import { formatNumber } from '../runReport/runReportModel'
import { rcaStatusLabel } from './rcaModel'

function formatValue(value: number | undefined, unit: string): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return unit === 'ms' ? `${formatNumber(value, 0)} ms` : formatNumber(value, 3)
}

function resultLabel(result: RcaBrowserBaselineResult): string {
  if (result.state === 'anomaly') return `${result.direction === 'decrease' ? 'Lower' : 'Higher'} than baseline`
  if (result.state === 'within_range') return 'Within baseline range'
  return `Unscored: ${rcaStatusLabel(result.reason || 'unknown')}`
}

// RcaBrowserBaseline lists browser_baseline.v1's comparison of each browser
// document cohort with the same cohort of the baseline run at the same load.
// It is advisory: application SLAs own CI and stop decisions.
export default function RcaBrowserBaselineSection({ baseline }: { baseline?: RcaBrowserBaseline }) {
  if (!baseline) return null
  const results = baseline.results || []
  const anomalies = results.filter(result => result.state === 'anomaly').length
  const excluded = Object.entries(baseline.excluded || {}).filter(([, count]) => count > 0)
  return (
    <details className="rca-browser-baseline">
      <summary>Browser baseline ({anomalies} {anomalies === 1 ? 'anomaly' : 'anomalies'})</summary>
      <p className="run-report-muted">
        {baseline.version} compares each browser document cohort (metric, engine, profile and the actual runtime cohort) with the same cohort of
        {baseline.baseline?.test_id ? ` baseline run ${baseline.baseline.test_id}` : ' a baseline run'} at the same load: at least {baseline.params.min_baseline_buckets} baseline buckets,
        |z| ≥ {baseline.params.sigma} and an effect of at least max({baseline.params.timing_floor_ms} ms, {Math.round(baseline.params.timing_floor_fraction * 100)}% of the baseline) for timings or {baseline.params.cls_floor} for layout shift. Advisory only.
      </p>
      {(baseline.limitations || []).map((limitation, i) => <p key={i} className="run-report-muted" role="note">{rcaStatusLabel(limitation.code)}{limitation.detail ? `: ${limitation.detail}` : ''}</p>)}
      {results.length > 0 && <table className="rca-table" aria-label="Browser baseline results">
        <thead><tr><th scope="col">Cohort</th><th scope="col">Level</th><th scope="col">Result</th><th scope="col">Current (p75)</th><th scope="col">Expected range</th><th scope="col">Buckets (samples)</th></tr></thead>
        <tbody>{results.map(result => <tr key={result.id}>
          <th scope="row" title={result.series}>{result.metric.toUpperCase()}</th>
          <td>{result.level}</td>
          <td>{resultLabel(result)}{result.detail ? ` — ${result.detail}` : ''}{result.z ? ` (z ${formatNumber(result.z, 1)})` : ''}{result.caveats?.length ? ` · ${result.caveats.map(rcaStatusLabel).join(', ')}` : ''}</td>
          <td>{formatValue(result.current, result.unit)}</td>
          <td>{result.expected_high !== undefined ? `${formatValue(result.expected_low ?? 0, result.unit)} – ${formatValue(result.expected_high, result.unit)}` : '—'}</td>
          <td>{result.current_buckets} ({result.current_samples}) vs {result.baseline_buckets} ({result.baseline_samples})</td>
        </tr>)}</tbody>
      </table>}
      {excluded.length > 0 && <p className="run-report-muted">Excluded: {excluded.map(([reason, count]) => `${count} ${rcaStatusLabel(reason).toLowerCase()} bucket${count === 1 ? '' : 's'}`).join(', ')}.</p>}
    </details>
  )
}
