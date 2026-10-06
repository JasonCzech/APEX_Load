import type { MonitorRuleResult } from '../types/runMonitoring'

export default function MonitorRuleResults({ results }: { results?: MonitorRuleResult[] }) {
  if (!results?.length) return null
  return <section aria-label="Monitoring performance results"><h3>Monitoring performance criteria</h3><p>Evaluated after settled collection. Averages use valid observation weights where supplied. Results do not infer causation.</p><div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Source / metric</th><th>Criterion</th><th>Observed</th><th>Coverage</th><th>Result</th></tr></thead><tbody>{results.map((result, index) => <tr key={`${result.rule.source_id}:${result.rule.metric}:${index}`}><td>{result.rule.source_id} · {result.rule.metric}</td><td>{result.rule.statistic} {result.rule.comparator} {result.rule.threshold} {result.rule.unit}</td><td>{result.observed === undefined ? 'Not evaluated' : `${result.observed.toLocaleString()} ${result.rule.unit}`}</td><td>{result.coverage_pct.toFixed(1)}% · {result.eligible_buckets} eligible observations</td><td>{result.status} · {result.message}</td></tr>)}</tbody></table></div></section>
}
