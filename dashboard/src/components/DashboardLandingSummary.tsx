import type { TestSummary } from '../types'
import { runReference, summarizeAcceptance, type DashboardAttentionItem, type DashboardReports } from './dashboardLandingModel'

interface Props {
  releaseName: string
  completed: TestSummary[]
  reports: DashboardReports
  activeCount: number
  queuedCount: number
  updatedAt: number | null
  stale: boolean
  attention: DashboardAttentionItem[]
  onAttention: (item: DashboardAttentionItem) => void
  onQueue: () => void
  onResults: () => void
}

export default function DashboardLandingSummary({ releaseName, completed, reports, activeCount, queuedCount, updatedAt, stale, attention, onAttention, onQueue, onResults }: Props) {
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
  const attentionButton = (item: DashboardAttentionItem, index: number) => (
    <button type="button" key={`${item.testId || item.destination}-${index}`} className={`dashboard-attention-item tone-${item.tone}`} onClick={() => onAttention(item)}>
      <strong>{item.title}</strong><span>{item.detail}</span>
    </button>
  )
  return (
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
        <p className="dashboard-freshness">Run data: {stale ? 'Stale · ' : ''}{updatedAt ? `last updated ${new Date(updatedAt).toLocaleTimeString()}` : 'No successful update yet'}</p>
      </div>
      <div className="dashboard-priority" aria-label="Needs attention">
        <h2>Needs attention <span>({attention.length})</span></h2>
        {attention.length === 0 ? <p>No current attention items. Acceptance and evidence coverage are shown separately.</p> : <>
          {attention.slice(0, 2).map(attentionButton)}
          {attention.length > 2 && <details><summary>View all attention items ({attention.length})</summary>{attention.slice(2).map(attentionButton)}</details>}
        </>}
      </div>
    </section>
  )
}
