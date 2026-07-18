interface StatTileProps {
  title: string
  value?: number
  unit?: string
  error?: string
  compact?: boolean
  sparkline?: number[]
  tone?: 'normal' | 'warning' | 'critical' | 'unknown'
  baselineValue?: number
}

function formatStatValue(value: number): string {
  if (!Number.isFinite(value)) return '—'
  const abs = Math.abs(value)
  if (abs !== 0 && (abs >= 1_000_000 || abs < 0.001)) {
    return value.toExponential(2)
  }
  // Trim to at most 3 significant fractional digits without trailing zeros.
  const rounded = Math.round(value * 1000) / 1000
  return rounded.toLocaleString(undefined, { maximumFractionDigits: 3 })
}

// StatTile renders a single-value imported dashboard panel (Grafana stat/gauge). It is
// deliberately presentational and dependency-free — no Recharts — so the Command
// Center, Run Report, and legacy archive can all import the same component without
// pulling a charting library into a lazy route.
export default function StatTile({ title, value, unit, error, compact, sparkline = [], tone, baselineValue }: StatTileProps) {
  const hasValue = typeof value === 'number' && Number.isFinite(value)
  const finite = sparkline.filter(Number.isFinite)
  const min = finite.length ? Math.min(...finite) : 0
  const max = finite.length ? Math.max(...finite) : 0
  const span = Math.max(max - min, 1)
  const points = finite.map((sample, index) => `${finite.length === 1 ? 50 : index / (finite.length - 1) * 100},${24 - ((sample - min) / span * 22 + 1)}`).join(' ')
  return (
    <div className={`stat-tile${compact ? ' stat-tile-compact' : ''}${tone ? ` stat-tile-${tone}` : ''}`} role="group" aria-label={`${title} tile`}>
      <div className="stat-tile-title">{title}</div>
      {error ? (
        <div className="stat-tile-error" role="status">{error}</div>
      ) : (
        <div className="stat-tile-value">
          <span className="stat-tile-number">{hasValue ? formatStatValue(value as number) : '—'}</span>
          {unit ? <span className="stat-tile-unit">{unit}</span> : null}
        </div>
      )}
      {!error && points && <svg className="stat-tile-sparkline" viewBox="0 0 100 26" preserveAspectRatio="none" aria-hidden="true"><polyline points={points} fill="none" vectorEffect="non-scaling-stroke" /></svg>}
      {!error && typeof baselineValue === 'number' && Number.isFinite(baselineValue) && <div className="stat-tile-title">Baseline {formatStatValue(baselineValue)}{unit ? ` ${unit}` : ''} · Δ {hasValue ? formatStatValue((value as number) - baselineValue) : '—'}</div>}
    </div>
  )
}
