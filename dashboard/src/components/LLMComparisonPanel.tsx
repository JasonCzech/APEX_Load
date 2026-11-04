import type { LLMCompareResult } from '../types'
import { formatFixed } from '../utils/numberFormat'

export default function LLMComparisonPanel({ comparison }: { comparison?: LLMCompareResult }) {
  if (!comparison || !comparison.metrics || comparison.metrics.length === 0) return null

  return (
    <section aria-label="LLM A/B comparison">
      <h4>A/B model and engine comparison</h4>
      <p className="run-report-muted">
        Baseline {comparison.baseline_test_id}
        {comparison.baseline_model ? ` (${comparison.baseline_model})` : ''} vs candidate {comparison.candidate_test_id}
        {comparison.candidate_model ? ` (${comparison.candidate_model})` : ''}.
        {comparison.workload_digest_matched ? ' Workload digest matched.' : ' Workload digest mismatch.'}
      </p>
      {comparison.warnings && comparison.warnings.length > 0 && (
        <ul className="run-report-muted">
          {comparison.warnings.map(w => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      )}
      <div className="run-report-table-wrap" tabIndex={0} role="region" aria-label="LLM A/B comparison metrics">
        <table className="run-report-table">
          <thead>
            <tr>
              {['Metric', 'Baseline', 'Candidate', 'Delta', 'Delta %', '95% bootstrap CI', 'Direction'].map(label => (
                <th key={label} scope="col">
                  {label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {comparison.metrics.map(m => (
              <tr key={m.metric}>
                <th scope="row">{m.metric}</th>
                <td>{formatFixed(m.baseline, 2)}</td>
                <td>{formatFixed(m.candidate, 2)}</td>
                <td>{formatFixed(m.delta, 2)}</td>
                <td>{formatFixed(m.delta_pct, 2)}%</td>
                <td>
                  {m.ci95_low !== undefined && m.ci95_high !== undefined
                    ? `[${formatFixed(m.ci95_low, 2)}, ${formatFixed(m.ci95_high, 2)}]`
                    : '—'}
                </td>
                <td>{m.direction}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}
