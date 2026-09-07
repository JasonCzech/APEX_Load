import { RunArchiveReport } from '../types'

interface SummaryMetrics {
  avgTps: number
  avgLatency: number
  avgError: number
  maxVusers: number
}

interface LiveMetricsSnapshot {
  active_vusers?: number
  tps?: number
  avg_response_ms?: number
  error_pct?: number
  total_transactions?: number
}

interface Props {
  archiveReport: RunArchiveReport | null
  summary: SummaryMetrics | null
  metrics?: LiveMetricsSnapshot
}

export default function TestExecutionHeroMetrics({ archiveReport, summary, metrics }: Props) {
  const archivedAverageTps = (() => {
    const points = archiveReport?.summary_timeline?.filter(point => Number.isFinite(point.tps)) || []
    if (points.length === 0) return undefined
    return points.reduce((total, point) => total + point.tps, 0) / points.length
  })()
  const named = archiveReport?.metric_populations?.transaction
  const archived = Boolean(archiveReport)
  const latency = archived ? named?.avg_response_ms : summary?.avgLatency ?? metrics?.avg_response_ms
  const errorPct = archived ? named?.error_pct : summary?.avgError ?? metrics?.error_pct
  const count = archived ? named?.transactions : metrics?.total_transactions
  return (
    <div className="hero-metrics">
      <div className="hero-card">
        <span className="hero-value">{archiveReport?.overview.peak_active_vusers ?? summary?.maxVusers ?? metrics?.active_vusers ?? 0}</span>
        <span className="hero-label">Max VUsers</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{(summary?.avgTps ?? archivedAverageTps ?? metrics?.tps ?? 0).toFixed(1)}</span>
        <span className="hero-label">Successful transaction TPS</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{latency === undefined ? 'Unknown' : latency.toFixed(0)}</span>
        <span className="hero-label">Named transaction latency (ms)</span>
      </div>
      <div className="hero-card">
        <span className="hero-value error-value">
          {errorPct === undefined ? 'Unknown' : `${errorPct.toFixed(2)}%`}
        </span>
        <span className="hero-label">Named transaction error rate</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{count ?? 'Unknown'}</span>
        <span className="hero-label">Named transactions</span>
      </div>
      {archiveReport?.metric_populations?.action && <div className="hero-card"><span className="hero-value">{archiveReport.metric_populations.action.average_rate_per_second?.toFixed(3) ?? 'Unknown'}</span><span className="hero-label">Requests / actions per second</span></div>}
    </div>
  )
}
