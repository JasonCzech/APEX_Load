import type { RunArchiveReport } from '../types'

export default function RunOutcomeSummary({ report, showPopulations = true }: { report: RunArchiveReport; showPopulations?: boolean }) {
  const manifest = report.manifest
  const execution: Record<string, string> = { workload_completed: 'Finished', operator_stopped: 'Stopped by operator', stopped_early: 'Stopped early', aborted: 'Aborted', failed: 'Failed' }
  const start = manifest.provenance?.started_at
  const elapsed = start && manifest.completed_at ? (Date.parse(manifest.completed_at) - Date.parse(start)) / 1000 : undefined
  return <section className="run-outcome-summary" aria-label="Run outcome">
    <h3>Run outcome</h3>
    <dl>
      <div><dt>Execution</dt><dd>{execution[manifest.termination_reason || ''] || 'Unknown termination reason'}</dd></div>
      <div><dt>Workload completion</dt><dd>Planned {manifest.provenance?.duration || 'Unknown'} · elapsed {elapsed !== undefined && Number.isFinite(elapsed) ? `${Math.round(elapsed)}s` : 'Unknown'}</dd></div>
      <div><dt>Acceptance assessment</dt><dd>{report.verdict?.status || 'Unknown'}</dd><dd>{report.verdict?.explanation || 'No acceptance assessment is available.'}</dd></div>
      <div><dt>Evidence coverage</dt><dd>{report.completeness?.score_pct === undefined ? 'Unknown' : `${report.completeness.score_pct}% of expected captured evidence`}</dd><dd>Archive coverage does not imply completion of the planned workload.</dd></div>
    </dl>
    {showPopulations && <RunMetricPopulations report={report} />}
  </section>
}

export function RunMetricPopulations({ report }: { report: RunArchiveReport }) {
  return <dl aria-label="Metric populations">
      {(['action', 'transaction', 'unknown'] as const).map(key => {
        const metrics = report.metric_populations?.[key]
        return <div key={key}><dt>{key === 'action' ? 'Requests / actions' : key === 'transaction' ? 'Named transactions' : 'Unclassified legacy records'}</dt><dd>{metrics?.transactions === 0 ? '0 records · no samples to evaluate' : metrics ? `${metrics.transactions} · ${metrics.errors} errors · p95 ${metrics.p95_ms?.toFixed(1) ?? 'Unknown'} ms · ${metrics.average_rate_per_second?.toFixed(3) ?? 'Unknown'}/s elapsed-run average` : 'Unknown'}</dd></div>
      })}
      <div><dt>Archive records</dt><dd>{report.overview.transactions}</dd></div>
    </dl>
}
