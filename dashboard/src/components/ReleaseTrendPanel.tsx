import { useCallback, useEffect, useState } from 'react'

import type { ReleaseTrend } from '../types'

interface ReleaseTrendPanelProps {
  releaseId: string
  getReleaseTrend: (id: string) => Promise<ReleaseTrend>
}

function formatSignedMs(value: number): string {
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(0)}ms`
}

function formatSignedPct(value: number): string {
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(1)}%`
}

// ReleaseTrendPanel surfaces the multi-run trend analysis (run-over-run deltas
// and regression flags) from GET /api/v1/releases/{id}/trend — the value the raw
// CSV export lacks.
export function ReleaseTrendPanel({ releaseId, getReleaseTrend }: ReleaseTrendPanelProps) {
  const [trend, setTrend] = useState<ReleaseTrend | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setTrend(await getReleaseTrend(releaseId))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load release trend.')
    } finally {
      setLoading(false)
    }
  }, [getReleaseTrend, releaseId])

  useEffect(() => {
    void load()
  }, [load])

  if (loading) {
    return <div className="release-trend-panel cell-muted" data-testid="release-trend-panel">Loading release trend…</div>
  }
  if (error) {
    return <div className="release-trend-panel cell-muted" data-testid="release-trend-panel">{error}</div>
  }
  if (!trend || trend.run_count === 0) {
    return <div className="release-trend-panel cell-muted" data-testid="release-trend-panel">No archived runs yet for this release.</div>
  }

  return (
    <div className="release-trend-panel" data-testid="release-trend-panel">
      <div className="archive-section-header archive-subsection-header">
        <div>
          <h3 className="chart-title">Release Trend Analysis</h3>
          <p className="archive-section-copy">
            {trend.run_count} run{trend.run_count === 1 ? '' : 's'} · avg response {formatSignedPct(trend.summary.avg_response_trend_pct)} · error rate {formatSignedPct(trend.summary.error_pct_trend)} across the series.
          </p>
        </div>
        {trend.summary.latest_regressed && (
          <span className="status-badge status-failed" data-testid="release-trend-latest-regressed">Latest run regressed</span>
        )}
      </div>
      <table className="tx-table">
        <thead>
          <tr>
            <th>Run</th>
            <th>Avg (ms)</th>
            <th>Δ Avg</th>
            <th>Error %</th>
            <th>SLA</th>
            <th>Regressions</th>
          </tr>
        </thead>
        <tbody>
          {trend.points.map(point => (
            <tr key={point.run_id} className={point.regressions && point.regressions.length > 0 ? 'error-cell' : ''}>
              <td className="tx-name">{point.run_name || point.run_id}</td>
              <td>{point.avg_response_ms.toFixed(1)}</td>
              <td>{point.deltas ? formatSignedMs(point.deltas.avg_response_ms_delta) : '—'}</td>
              <td>{point.error_pct.toFixed(2)}%</td>
              <td>{point.sla_breached ? 'breached' : 'ok'}</td>
              <td>{point.regressions && point.regressions.length > 0 ? point.regressions.join(', ') : '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
