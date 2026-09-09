import { reportEvidenceWarnings } from './reportEvidenceWarnings'
import { runOutcomeLabels } from './runOutcomeLabels'
import type { RunArchiveReport } from '../types'

export default function RunOutcomeSummary({ report, showPopulations = true }: { report: RunArchiveReport; showPopulations?: boolean }) {
  const manifest = report.manifest
  const warnings = reportEvidenceWarnings(report)
  const outcome = runOutcomeLabels(manifest)
  const computed = manifest?.test_id && report.outcome?.schema_version === 1 && report.outcome.execution_id === manifest.test_id ? report.outcome : undefined
  const assessment = computed && ({ passed: 'Passed', failed: 'Failed', inconclusive: 'Inconclusive', not_assessed: 'Not assessed', unavailable: 'Not available' })[computed.assessment.status]
  return <section className="run-outcome-summary" aria-label="Run outcome">
    <h3>Run outcome</h3>
    <dl>
      <div><dt>Execution</dt><dd>{outcome.execution}</dd></div>
      <div><dt>Workload completion</dt><dd>Planned {manifest?.provenance?.duration || 'Unknown'} · elapsed {outcome.elapsed}</dd></div>
      <div><dt>Acceptance assessment</dt><dd>{assessment || report.verdict?.status || 'Unknown'}</dd><dd>{computed?.assessment.explanation || report.verdict?.explanation || 'No acceptance assessment is available.'}</dd></div>
      <div><dt>Evidence coverage</dt><dd>{report.completeness?.score_pct === undefined ? 'Unknown' : `${report.completeness.score_pct}% of expected captured evidence`}</dd><dd>Archive coverage does not imply completion of the planned workload.</dd></div>
    </dl>
    {warnings.length > 0 && <div role="note" className="run-report-panel" aria-label="Evidence limitations"><h4>Evidence limitations — review before acceptance</h4><ul>{warnings.map(warning => <li key={warning}>{warning}</li>)}</ul></div>}
    {showPopulations && <RunMetricPopulations report={report} />}
  </section>
}

export function RunMetricPopulations({ report }: { report: RunArchiveReport }) {
  return <dl aria-label="Metric populations">
      {(['action', 'transaction', 'unknown'] as const).map(key => {
        const metrics = report.metric_populations?.[key]
        return <div key={key}><dt>{key === 'action' ? 'Requests / actions' : key === 'transaction' ? 'Named transactions' : 'Unclassified legacy records'}</dt><dd>{metrics?.transactions === 0 ? '0 records · no samples to evaluate' : metrics ? `${metrics.transactions} · ${metrics.errors} errors · p95 ${metrics.p95_ms?.toFixed(1) ?? 'Unknown'} ms · ${!Number.isFinite(metrics.average_rate_per_second) ? 'Elapsed-run average unavailable' : `${metrics.average_rate_per_second?.toFixed(3)}/s elapsed-run average`}` : 'Unknown'}</dd></div>
      })}
      <div><dt>Archive records</dt><dd>{report.overview?.transactions ?? 'Unavailable'}</dd></div>
    </dl>
}
