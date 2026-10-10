import type { TestSummary } from '../types'
import { formatTimeOfDay } from '../utils/dateTime'
import { runReference, summarizeAcceptance, type DashboardAttentionItem, type DashboardReports } from './dashboardLandingModel'

interface Props {
  compact?: boolean
  releaseName: string
  completed: TestSummary[]
  reports: DashboardReports
  activeCount: number
  queuedCount: number
  updatedAt: number | null
  stale: boolean
  attention: DashboardAttentionItem[]
  // attentionLoading says completed runs' reports are still loading, so
  // their attention items may not be listed yet.
  attentionLoading?: boolean
  onAttention: (item: DashboardAttentionItem) => void
  onDismissAttention: (item: DashboardAttentionItem) => void
  onQueue: () => void
  onResults: () => void
}

export default function DashboardLandingSummary({ compact = false, releaseName, completed, reports, activeCount, queuedCount, updatedAt, stale, attention, attentionLoading = false, onAttention, onDismissAttention, onQueue, onResults }: Props) {
  const counts = summarizeAcceptance(completed, reports)
  const archived = completed.filter(test => test.archive).length
  const latest = completed[0]
  const latestReport = latest ? reports[latest.id] : null
  const baseline = latestReport?.release_trend
  const latestReportLoading = Boolean(latest?.archive && !(latest.id in reports))
  const completeness = latestReport?.completeness?.score_pct
  const completenessLabel = typeof completeness === 'number' && Number.isFinite(completeness) ? `${completeness.toFixed(0)}%` : 'Unavailable'
  const baselineLabel = !latestReport
    ? 'Comparison baseline unavailable without a loaded report.'
    : baseline?.has_baseline && baseline.baseline
      ? `Recorded baseline: ${baseline.baseline.run_name || baseline.baseline.run_id}. ${baseline.regressed ? `Regressions: ${baseline.regressions?.join(', ') || 'review release analysis'}.` : 'No regression flagged by the recorded comparison.'}`
      : 'No recorded comparison baseline for the latest run.'
  const attentionRow = (item: DashboardAttentionItem) => (
    <div key={item.id} className={`dashboard-attention-item tone-${item.tone}`}>
      <button type="button" className="dashboard-attention-link" onClick={() => onAttention(item)}>
        <strong>{item.title}</strong><span>{item.detail}</span>
      </button>
      <button type="button" className="dashboard-attention-dismiss" onClick={() => onDismissAttention(item)} aria-label={`Dismiss ${item.title}`} title="Dismiss">
        <span aria-hidden="true">×</span>
      </button>
    </div>
  )
  const body = (
    <section className="dashboard-landing-summary" aria-label="Release readiness and attention">
      <div className="dashboard-readiness">
        <div className="dashboard-summary-heading"><h2>{releaseName} · Release quality</h2><button type="button" className="btn btn-secondary btn-sm" onClick={onResults}>Review results</button></div>
        <p>Execution completion does not establish performance acceptance.</p>
        <dl className="dashboard-acceptance-counts" aria-label="Acceptance outcomes">
          {Object.entries(counts).filter(([label, count]) => label !== 'Loading' || count > 0).map(([label, count]) => <div key={label}><dt>{label === 'Pass' ? 'Passed' : label === 'Fail' ? 'Failed' : label === 'Unavailable' ? 'Assessment unavailable' : label}</dt><dd>{count}</dd></div>)}
        </dl>
        <p>Archive evidence available for <strong>{archived} of {completed.length}</strong> completed runs (including failed and aborted).</p>
        <p>{latest ? `Latest completed run: ${latest.name} · ${runReference(latest)}` : 'No completed runs in this release.'}</p>
        {latest && <p>{latestReportLoading ? 'Loading latest report completeness and comparison baseline…' : `Latest report completeness: ${completenessLabel} · ${baselineLabel}`}</p>}
        <div className="dashboard-workload-state"><span>{activeCount ? `${activeCount} active (running or paused)` : 'Idle — no active runs'}</span>{queuedCount > 0 && <button type="button" className="btn btn-secondary btn-sm" onClick={onQueue}>View queued tests ({queuedCount})</button>}</div>
        <p className="dashboard-freshness">Run data: {stale ? 'Stale · ' : ''}{updatedAt ? `last updated ${formatTimeOfDay(updatedAt)}` : 'No successful update yet'}</p>
      </div>
      <div className="dashboard-priority" aria-label="Needs attention">
        <h2>Needs attention <span>({attention.length})</span></h2>
        {attentionLoading && <p role="status">Checking completed runs' reports…</p>}
        {attention.length === 0 ? !attentionLoading && <p>No current attention items. Acceptance and evidence coverage are shown separately.</p> : <>
          {attention.slice(0, 2).map(attentionRow)}
          {attention.length > 2 && <details><summary>View all attention items ({attention.length})</summary>{attention.slice(2).map(attentionRow)}</details>}
        </>}
      </div>
    </section>
  )
  return compact ? <details className="analysis-readiness"><summary>{releaseName} · {archived}/{completed.length} archives · {counts['Fail'] || 0} acceptance failures · {attention.length} attention items · Expand release context</summary>{body}</details> : body
}
