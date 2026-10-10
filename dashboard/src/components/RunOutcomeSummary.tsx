import { reportEvidenceWarnings } from './reportEvidenceWarnings'
import { runOutcomeLabels } from './runOutcomeLabels'
import type { RunArchiveReport } from '../types'
import type { ReportRootCauseSummary } from '../types/rca'
import { rcaSummaryLine, rcaTone, rcaValidityLabel, type RcaSummaryAction } from './rca/rcaModel'
import { formatMs, formatNumber } from './runReport/runReportModel'
import { formatDurationDisplay } from '../utils/timeUtils'
import { ratioText, usedHTTP3, versionMixHeadline } from './runReport/httpVersionMixModel'
import './rca/RootCause.css'

interface Props {
  report: RunArchiveReport
  showPopulations?: boolean
  // showRootCause adds the run's validity and root cause; an exported
  // report's preview has its own root-cause section instead.
  showRootCause?: boolean
  // rootCauseSummary replaces the report's summary with a newer copy (an
  // analysis that completed since the report loaded).
  rootCauseSummary?: ReportRootCauseSummary
  // onRootCause opens the root cause for the cell's action.
  onRootCause?: (action: RcaSummaryAction) => void
}

export default function RunOutcomeSummary({ report, showPopulations = true, showRootCause = true, rootCauseSummary, onRootCause }: Props) {
  const manifest = report.manifest
  const warnings = reportEvidenceWarnings(report)
  const outcome = runOutcomeLabels(manifest)
  const computed = manifest?.test_id && report.outcome?.schema_version === 1 && report.outcome.execution_id === manifest.test_id ? report.outcome : undefined
  const assessment = computed && ({ passed: 'Passed', failed: 'Failed', inconclusive: 'Inconclusive', not_assessed: 'Not assessed', unavailable: 'Not available' })[computed.assessment.status]
  // A project that does not present root cause shows only a judged
  // validity here; Diagnostics tells analysts how to turn root cause on.
  const summary = rootCauseSummary || report.root_cause_summary
  const rootCause = showRootCause && summary?.state !== 'not_enabled' ? rcaSummaryLine(summary) : null
  // Unknown validity (no generator statistics) is common and says nothing
  // about the run, so only a judged verdict is shown.
  const validity = showRootCause && report.validity?.verdict && report.validity.verdict !== 'unknown' ? report.validity.verdict : undefined
  // The version mix leads only when HTTP/3 was in play.
  const versionMix = report.network_phases?.version_mix
  const showVersions = usedHTTP3(versionMix)
  return <section className="run-outcome-summary" aria-label="Run outcome">
    <h3>Run outcome</h3>
    <dl>
      <div><dt>Execution</dt><dd>{outcome.execution}</dd></div>
      <div><dt>Workload completion</dt><dd>Planned {formatDurationDisplay(manifest?.provenance?.duration) || 'Unknown'} · elapsed {outcome.elapsed}</dd></div>
      <div><dt>Acceptance assessment</dt><dd>{assessment || report.verdict?.status || 'Unknown'}</dd><dd>{computed?.assessment.explanation || report.verdict?.explanation || 'No acceptance assessment is available.'}</dd></div>
      <div><dt>Evidence coverage</dt><dd>{report.completeness?.score_pct === undefined ? 'Unknown' : `${report.completeness.score_pct}% of expected captured evidence`}</dd><dd>Archive coverage does not imply completion of the planned workload.</dd></div>
      {showVersions && versionMix && <div className="run-outcome-versions">
        <dt>HTTP versions</dt>
        <dd>{versionMixHeadline(versionMix) || 'No HTTP version was observed'}</dd>
        {(versionMix.upgrade_eligible ?? 0) > 0 && <dd>Fell back to TCP: {ratioText(versionMix.fallback_ratio, 'upgrade-eligible auto+h3 actions')}</dd>}
        {(versionMix.quic_attempted ?? 0) > 0 && <dd>QUIC failures: {ratioText(versionMix.quic_failure_ratio, 'QUIC attempts')}</dd>}
        {versionMix.finding && <dd role="note">Protocol Mix: above the expected ratio. See Diagnostics.</dd>}
      </div>}
      {(rootCause || validity) && <div className="run-outcome-root-cause" data-tone={rootCause?.tone}>
        <dt>{rootCause ? 'Root cause' : 'Run validity'}</dt>
        {validity && <dd><span className={`rca-badge rca-badge-${rcaTone(validity)}`}>{rcaValidityLabel(validity)}</span></dd>}
        {/* A role on the dd itself would replace its definition semantics. */}
        {rootCause && <dd>{rootCause.pending ? <span role="status">{rootCause.text}</span> : rootCause.text}</dd>}
        {rootCause && onRootCause && rootCause.action !== 'none' && <dd><button type="button" className="btn btn-sm btn-secondary" onClick={() => onRootCause(rootCause.action)}>{rootCause.actionLabel}</button></dd>}
        <dd className="run-report-muted">Generated from archived evidence.</dd>
      </div>}
    </dl>
    {warnings.length > 0 && <div role="note" className="run-report-panel" aria-label="Evidence limitations"><h4>Evidence limitations — review before acceptance</h4><ul>{warnings.map(warning => <li key={warning}>{warning}</li>)}</ul></div>}
    {showPopulations && <RunMetricPopulations report={report} />}
  </section>
}

export function RunMetricPopulations({ report }: { report: RunArchiveReport }) {
  return <dl aria-label="Metric populations">
      {(['action', 'transaction', 'unknown'] as const).map(key => {
        const metrics = report.metric_populations?.[key]
        return <div key={key}><dt>{key === 'action' ? 'Requests / actions' : key === 'transaction' ? 'Named transactions' : 'Unclassified legacy records'}</dt><dd>{metrics?.transactions === 0 ? '0 records · no samples to evaluate' : metrics ? `${formatNumber(metrics.transactions, 0)} · ${formatNumber(metrics.errors, 0)} ${metrics.errors === 1 ? 'error' : 'errors'} · P95 ${formatMs(metrics.p95_ms)} · ${!Number.isFinite(metrics.average_rate_per_second) ? 'Elapsed-run average unavailable' : `${formatNumber(metrics.average_rate_per_second, 1)}/s elapsed-run average`}` : 'Unknown'}</dd></div>
      })}
      <div><dt>Archive records</dt><dd>{report.overview?.transactions ?? 'Unavailable'}</dd></div>
    </dl>
}
