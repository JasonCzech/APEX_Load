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
  return (
    <div className="hero-metrics">
      <div className="hero-card">
        <span className="hero-value">{archiveReport?.overview.peak_active_vusers ?? summary?.maxVusers ?? metrics?.active_vusers ?? 0}</span>
        <span className="hero-label">Max VUsers</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{(summary?.avgTps ?? archivedAverageTps ?? metrics?.tps ?? 0).toFixed(1)}</span>
        <span className="hero-label">Avg TPS</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{(archiveReport?.overview.avg_response_ms ?? summary?.avgLatency ?? metrics?.avg_response_ms ?? 0).toFixed(0)}</span>
        <span className="hero-label">Avg Latency (ms)</span>
      </div>
      <div className="hero-card">
        <span className="hero-value error-value">
          {(archiveReport?.overview.error_pct ?? summary?.avgError ?? metrics?.error_pct ?? 0).toFixed(2)}%
        </span>
        <span className="hero-label">Avg Error Rate</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{archiveReport?.overview.transactions ?? metrics?.total_transactions ?? 0}</span>
        <span className="hero-label">Total Tx</span>
      </div>
    </div>
  )
}
