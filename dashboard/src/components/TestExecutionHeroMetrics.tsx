import type { LiveMetrics, RunArchiveReport } from '../types'
import { livePopulation, liveRunCounts, liveRunErrorPercent, liveThroughputPopulation } from '../utils/liveErrorRate'
import type { ExecutionChartSummary } from './testExecutionMetricsModel'

interface Props {
  archiveReport: RunArchiveReport | null
  summary: ExecutionChartSummary | null
  metrics?: LiveMetrics
  /** A goal-oriented run's throughput stays TPS, the unit of its targets. */
  scenarioMode?: string
}

export default function TestExecutionHeroMetrics({ archiveReport, summary, metrics, scenarioMode }: Props) {
  const archivedAverageTps = (() => {
    const points = archiveReport?.summary_timeline?.filter(point => Number.isFinite(point.tps)) || []
    if (points.length === 0) return undefined
    return points.reduce((total, point) => total + point.tps, 0) / points.length
  })()
  const named = archiveReport?.metric_populations?.transaction
  const archived = Boolean(archiveReport)
  // A live run that sends requests is counted by its requests, as Command
  // Center and the run state count it; a script without transactions would
  // otherwise lead with 0.0 TPS and a 100% error rate.
  const requests = !archived && livePopulation(metrics) === 'requests'
  const requestThroughput = requests && liveThroughputPopulation({ scenario_mode: scenarioMode }, 'requests') === 'requests'
  const latency = archived ? named?.avg_response_ms : summary?.avgLatency ?? metrics?.avg_response_ms
  const errorPct = archived ? named?.error_pct : requests ? liveRunErrorPercent(metrics, 'requests') : summary?.avgError ?? metrics?.error_pct
  const count = archived ? named?.transactions : requests ? liveRunCounts(metrics, 'requests')?.attempts : metrics?.total_transactions
  const throughput = requestThroughput ? summary?.avgRequestRate ?? metrics?.request_rate ?? 0 : summary?.avgTps ?? archivedAverageTps ?? metrics?.tps ?? 0
  return (
    <div className="hero-metrics">
      <div className="hero-card">
        <span className="hero-value">{archiveReport?.overview.peak_active_vusers ?? summary?.maxVusers ?? metrics?.active_vusers ?? 0}</span>
        <span className="hero-label">Max VUsers</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{throughput.toFixed(1)}</span>
        <span className="hero-label">{requestThroughput ? 'Requests/s' : 'Successful transaction TPS'}</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{latency === undefined ? 'Unknown' : latency.toFixed(0)}</span>
        <span className="hero-label">Named transaction latency (ms)</span>
      </div>
      <div className="hero-card">
        <span className="hero-value error-value">
          {errorPct === undefined ? 'Unknown' : `${errorPct.toFixed(2)}%`}
        </span>
        <span className="hero-label">{requests ? 'Request error rate (run to date)' : 'Named transaction error rate'}</span>
      </div>
      <div className="hero-card">
        <span className="hero-value">{count ?? 'Unknown'}</span>
        <span className="hero-label">{requests ? 'Requests (run to date)' : 'Named transactions'}</span>
      </div>
      {archiveReport?.metric_populations?.action && <div className="hero-card"><span className="hero-value">{archiveReport.metric_populations.action.average_rate_per_second?.toFixed(3) ?? 'Unknown'}</span><span className="hero-label">Requests / actions per second</span></div>}
    </div>
  )
}
