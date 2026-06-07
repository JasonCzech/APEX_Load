import { useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type DependencyList } from 'react'

import type { ReleaseTrend } from '../types'

interface ReleaseTrendPanelProps {
  releaseId: string
  getReleaseTrend: (id: string) => Promise<ReleaseTrend>
}

const tableBottomGutterPx = 24
const tableMinimumViewportHeightPx = 80

function formatSignedMs(value: number): string {
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(0)}ms`
}

function formatSignedPct(value: number): string {
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(1)}%`
}

function useBottomBoundedTable(dependencies: DependencyList) {
  const ref = useRef<HTMLDivElement | null>(null)
  const [maxHeight, setMaxHeight] = useState<number | null>(null)

  useLayoutEffect(() => {
    if (typeof window === 'undefined') return

    const updateMaxHeight = () => {
      const node = ref.current
      if (!node) return
      const rect = node.getBoundingClientRect()
      const availableHeight = Math.floor(window.innerHeight - rect.top - tableBottomGutterPx)
      const nextMaxHeight = Math.max(tableMinimumViewportHeightPx, availableHeight)
      setMaxHeight(current => current === nextMaxHeight ? current : nextMaxHeight)
    }

    updateMaxHeight()
    window.addEventListener('resize', updateMaxHeight)
    window.addEventListener('scroll', updateMaxHeight, { passive: true })

    return () => {
      window.removeEventListener('resize', updateMaxHeight)
      window.removeEventListener('scroll', updateMaxHeight)
    }
  }, dependencies)

  const style = maxHeight === null ? undefined : ({ '--dashboard-table-max-height': `${maxHeight}px` } as CSSProperties)
  return { ref, style }
}

// ReleaseTrendPanel surfaces the multi-run trend analysis (run-over-run deltas
// and regression flags) from GET /api/v1/releases/{id}/trend — the value the raw
// CSV export lacks.
export function ReleaseTrendPanel({ releaseId, getReleaseTrend }: ReleaseTrendPanelProps) {
  const [trend, setTrend] = useState<ReleaseTrend | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const tableBounds = useBottomBoundedTable([loading, error, trend?.points.length ?? 0])

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
    return <div className="release-trend-panel release-trend-note" data-testid="release-trend-panel">Loading release trend…</div>
  }
  if (error) {
    return <div className="release-trend-panel release-trend-note" data-testid="release-trend-panel">{error}</div>
  }
  if (!trend || trend.run_count === 0) {
    return <div className="release-trend-panel release-trend-note" data-testid="release-trend-panel">No archived runs yet for this release.</div>
  }

  return (
    <div className="release-trend-panel" data-testid="release-trend-panel">
      <div className="release-trend-header">
        <div>
          <h4 className="release-trend-title">Release Trend Analysis</h4>
          <p className="release-trend-subtitle">
            {trend.run_count} run{trend.run_count === 1 ? '' : 's'} · avg response {formatSignedPct(trend.summary.avg_response_trend_pct)} · error rate {formatSignedPct(trend.summary.error_pct_trend)} across the series.
          </p>
        </div>
        {trend.summary.latest_regressed && (
          <span className="status-badge status-failed" data-testid="release-trend-latest-regressed">Latest run regressed</span>
        )}
      </div>
      <div className="trend-table-wrap" ref={tableBounds.ref} style={tableBounds.style}>
        <table className="trend-table">
          <thead>
            <tr>
              <th>Run</th>
              <th className="num">Avg (ms)</th>
              <th className="num">Δ Avg</th>
              <th className="num">Error %</th>
              <th className="num">SLA</th>
              <th>Regressions</th>
            </tr>
          </thead>
          <tbody>
            {trend.points.map(point => {
              const regressed = Boolean(point.regressions && point.regressions.length > 0)
              return (
                <tr key={point.run_id} className={regressed ? 'release-trend-row-regressed' : ''}>
                  <td><span className="trend-run-name">{point.run_name || point.run_id}</span></td>
                  <td className="num">{point.avg_response_ms.toFixed(1)}</td>
                  <td className={`num ${point.deltas && point.deltas.avg_response_ms_delta > 0 ? 'text-danger strong' : ''}`}>
                    {point.deltas ? formatSignedMs(point.deltas.avg_response_ms_delta) : '—'}
                  </td>
                  <td className={`num ${point.error_pct > 0 ? 'text-danger' : ''}`}>{point.error_pct.toFixed(2)}%</td>
                  <td className={`num ${point.sla_breached ? 'text-danger strong' : 'text-success'}`}>
                    {point.sla_breached ? 'Breached' : 'OK'}
                  </td>
                  <td>{regressed ? point.regressions!.join(', ') : '—'}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
