import type { ReportFinding, RunArchiveReport } from '../../types'
import { formatFixed } from '../../utils/numberFormat'

/**
 * Above-the-fold answer for a release manager when the run has no acceptance
 * criteria: how it compares with the release's previous run, the top risks,
 * and how complete the evidence is. It is not a pass/fail verdict.
 */
export default function ReleaseReadinessSummary({ report, findings }: { report: RunArchiveReport | null; findings?: ReportFinding[] }) {
  if (!report || (report.verdict && report.verdict.status !== 'Not assessed')) return null
  const trend = report.release_trend
  const risks = (findings || []).filter(finding => finding.severity !== 'info').slice(0, 3)
  const coverage = report.completeness?.score_pct
  const signed = (value: number) => `${value > 0 ? '+' : ''}${formatFixed(value, 1)}`
  const trendSentence = !trend
    ? 'No release trend is available for this run.'
    : !trend.has_baseline || !trend.baseline
      ? `This is the first comparable run in ${trend.release_name || 'this release'}; there is nothing earlier to compare against yet.`
      : trend.regressed
        ? `Regressed against ${trend.baseline.run_name}: ${(trend.regressions || []).join('; ') || 'see the release trend'}.`
        : `No regression against ${trend.baseline.run_name}${trend.deltas ? ` (average response ${signed(trend.deltas.avg_response_pct_delta)}%, error rate ${signed(trend.deltas.error_pct_delta)} points)` : ''}.`
  return <section className="run-report-panel release-readiness" aria-label="Release readiness">
    <h2>Release readiness</h2>
    <p className="run-report-muted">No acceptance criteria are set for this run, so there is no pass/fail verdict. Set SLAs on the scenario to get one. Until then:</p>
    <ul>
      <li><strong>Release trend:</strong> {trendSentence}</li>
      <li><strong>Top risks:</strong> {risks.length ? risks.map(risk => risk.title + (risk.observed ? ` — ${risk.observed}` : '')).join(' · ') : 'None flagged.'}</li>
      {coverage !== undefined && <li><strong>Evidence coverage:</strong> {formatFixed(coverage, 0)}% <span className="run-report-muted">— the share of expected archive evidence that was captured; below 100% means some charts or tables may be incomplete.</span></li>}
    </ul>
  </section>
}
