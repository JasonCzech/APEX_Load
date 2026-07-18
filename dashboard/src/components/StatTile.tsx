interface StatTileProps {
  title: string
  value?: number
  unit?: string
  error?: string
  compact?: boolean
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
export default function StatTile({ title, value, unit, error, compact }: StatTileProps) {
  const hasValue = typeof value === 'number' && Number.isFinite(value)
  return (
    <div className={`stat-tile${compact ? ' stat-tile-compact' : ''}`} role="group" aria-label={`${title} tile`}>
      <div className="stat-tile-title">{title}</div>
      {error ? (
        <div className="stat-tile-error" role="status">{error}</div>
      ) : (
        <div className="stat-tile-value">
          <span className="stat-tile-number">{hasValue ? formatStatValue(value as number) : '—'}</span>
          {unit ? <span className="stat-tile-unit">{unit}</span> : null}
        </div>
      )}
    </div>
  )
}
