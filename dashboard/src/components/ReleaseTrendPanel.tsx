import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'

import type {
  Release,
  ReleaseTrend,
  ReleaseTrendPoint,
  RunArchiveActionStats,
  RunArchiveReport,
  TestSummary,
} from '../types'
import StatusBadge from './StatusBadge'

interface ReleaseTrendPanelProps {
  releaseId: string
  getReleaseTrend: (id: string) => Promise<ReleaseTrend>
  /** Releases the in-tab release filter can switch between. */
  releaseOptions?: Release[]
  onReleaseChange?: (release: Release) => void
  /** Run metadata (scenario association) keyed by run id. */
  runsById?: Record<string, TestSummary>
  /** Archive reports keyed by run id; supplies transaction-level detail. */
  reportsById?: Record<string, RunArchiveReport | null>
  /** Display names for scenario ids; falls back to the raw id when absent. */
  scenarioNames?: Record<string, string>
  onOpenRun?: (id: string) => void
  onOpenRunArchive?: (id: string) => void
}

const tableBottomGutterPx = 24
const tableMinimumViewportHeightPx = 80
const detailTransactionLimit = 12
const ALL = '__all__'
const UNASSIGNED_SCENARIO = '__unassigned__'

const EMPTY_RUNS: Record<string, TestSummary> = {}
const EMPTY_REPORTS: Record<string, RunArchiveReport | null> = {}
const EMPTY_SCENARIO_NAMES: Record<string, string> = {}
const EMPTY_RELEASES: Release[] = []

function formatSignedMs(value: number): string {
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(0)}ms`
}

function formatSignedPct(value: number): string {
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(1)}%`
}

function formatSignedPoints(value: number, digits = 2): string {
  const sign = value > 0 ? '+' : ''
  return `${sign}${value.toFixed(digits)}`
}

function formatOptional(value: number | undefined, digits = 1): string {
  return typeof value === 'number' && Number.isFinite(value) ? value.toFixed(digits) : '—'
}

function formatCount(value: number | undefined): string {
  return typeof value === 'number' && Number.isFinite(value) ? Math.round(value).toLocaleString() : '—'
}

function formatTimestamp(value?: string): string {
  if (!value) return '—'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? '—' : parsed.toLocaleString()
}

function useBottomBoundedTable(dependencySignature: string) {
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
  }, [dependencySignature])

  const style = maxHeight === null ? undefined : ({ '--dashboard-table-max-height': `${maxHeight}px` } as CSSProperties)
  return { ref, style }
}

// A row in the rendered series: the trend point plus the scenario association and
// (when a transaction filter is active) that transaction's stats for the run.
interface TrendRow {
  point: ReleaseTrendPoint
  scenarioId: string
  scenarioLabel: string
  transaction?: RunArchiveActionStats
  transactionDeltaMs?: number
}

// ReleaseTrendPanel surfaces the multi-run trend analysis (run-over-run deltas
// and regression flags) from GET /api/v1/releases/{id}/trend — the value the raw
// CSV export lacks. Release/scenario/transaction filters narrow the series, and
// selecting a run expands its archived detail below the table.
export function ReleaseTrendPanel({
  releaseId,
  getReleaseTrend,
  releaseOptions = EMPTY_RELEASES,
  onReleaseChange,
  runsById = EMPTY_RUNS,
  reportsById = EMPTY_REPORTS,
  scenarioNames = EMPTY_SCENARIO_NAMES,
  onOpenRun,
  onOpenRunArchive,
}: ReleaseTrendPanelProps) {
  const [trend, setTrend] = useState<ReleaseTrend | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [scenarioFilter, setScenarioFilter] = useState(ALL)
  const [transactionFilter, setTransactionFilter] = useState(ALL)
  const [selectedRunId, setSelectedRunId] = useState('')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    void getReleaseTrend(releaseId)
      .then(response => {
        if (!cancelled) setTrend(response)
      })
      .catch(err => {
        if (!cancelled) {
          setTrend(null)
          setError(err instanceof Error ? err.message : 'Failed to load release trend.')
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [getReleaseTrend, releaseId])

  // Filters and the expanded run are scoped to one release; switching releases
  // starts from a clean, unfiltered series.
  useEffect(() => {
    setScenarioFilter(ALL)
    setTransactionFilter(ALL)
    setSelectedRunId('')
  }, [releaseId])

  const points = useMemo(() => trend?.points ?? [], [trend])

  const scenarioIdForRun = useMemo(() => {
    const resolve = (runId: string) => runsById[runId]?.scenario_id?.trim() || UNASSIGNED_SCENARIO
    return resolve
  }, [runsById])

  const scenarioOptions = useMemo(() => {
    const seen = new Map<string, string>()
    points.forEach(point => {
      const scenarioId = scenarioIdForRun(point.run_id)
      if (seen.has(scenarioId)) return
      seen.set(scenarioId, scenarioId === UNASSIGNED_SCENARIO
        ? 'Unassigned scenario'
        : scenarioNames[scenarioId] || scenarioId)
    })
    return Array.from(seen.entries())
      .map(([id, label]) => ({ id, label }))
      .sort((left, right) => left.label.localeCompare(right.label))
  }, [points, scenarioIdForRun, scenarioNames])

  const transactionOptions = useMemo(() => {
    const names = new Set<string>()
    points.forEach(point => {
      const report = reportsById[point.run_id]
      if (!report?.by_action) return
      Object.keys(report.by_action).forEach(name => names.add(name))
    })
    return Array.from(names).sort((left, right) => left.localeCompare(right))
  }, [points, reportsById])

  // A stale filter (its scenario/transaction left the series after a data refresh)
  // would silently render an empty table, so fall back to "all".
  useEffect(() => {
    if (scenarioFilter !== ALL && !scenarioOptions.some(option => option.id === scenarioFilter)) {
      setScenarioFilter(ALL)
    }
  }, [scenarioFilter, scenarioOptions])

  useEffect(() => {
    if (transactionFilter !== ALL && !transactionOptions.includes(transactionFilter)) {
      setTransactionFilter(ALL)
    }
  }, [transactionFilter, transactionOptions])

  const rows = useMemo<TrendRow[]>(() => {
    const filtered: TrendRow[] = []
    points.forEach(point => {
      const scenarioId = scenarioIdForRun(point.run_id)
      if (scenarioFilter !== ALL && scenarioId !== scenarioFilter) return

      let transaction: RunArchiveActionStats | undefined
      if (transactionFilter !== ALL) {
        transaction = reportsById[point.run_id]?.by_action?.[transactionFilter]
        // A run that never executed the selected transaction has nothing to plot.
        if (!transaction) return
      }

      filtered.push({
        point,
        scenarioId,
        scenarioLabel: scenarioId === UNASSIGNED_SCENARIO
          ? '—'
          : scenarioNames[scenarioId] || scenarioId,
        transaction,
      })
    })

    // Run-over-run deltas are recomputed against the filtered series so the Δ column
    // always compares the two runs the user can actually see.
    return filtered.map((row, index) => {
      if (!row.transaction || index === 0) return row
      const previous = filtered[index - 1].transaction
      if (!previous) return row
      return { ...row, transactionDeltaMs: row.transaction.avg_response_ms - previous.avg_response_ms }
    })
  }, [points, reportsById, scenarioFilter, scenarioIdForRun, scenarioNames, transactionFilter])

  // Keep the expanded run in sync with the visible rows: a run filtered out of the
  // series must not keep a detail card open below the table.
  useEffect(() => {
    if (selectedRunId && !rows.some(row => row.point.run_id === selectedRunId)) {
      setSelectedRunId('')
    }
  }, [rows, selectedRunId])

  const selectedRow = useMemo(
    () => rows.find(row => row.point.run_id === selectedRunId),
    [rows, selectedRunId],
  )
  const selectedIndex = selectedRow ? rows.indexOf(selectedRow) : -1
  const selectedPreviousPoint = selectedIndex > 0 ? rows[selectedIndex - 1].point : undefined

  const tableBounds = useBottomBoundedTable(
    `${loading}:${error}:${rows.length}:${transactionFilter}:${Boolean(selectedRow)}`,
  )

  const releaseInOptions = releaseOptions.some(release => release.id === releaseId)
  const showReleaseFilter = Boolean(onReleaseChange) && releaseOptions.length > 0

  const filters = (
    <div className="release-trend-filters" role="group" aria-label="Release trend filters">
      {showReleaseFilter && (
        <label className="release-trend-filter">
          <span>Release</span>
          <select
            value={releaseInOptions ? releaseId : ''}
            onChange={event => {
              const next = releaseOptions.find(release => release.id === event.target.value)
              if (next) onReleaseChange?.(next)
            }}
          >
            {!releaseInOptions && <option value="">Current release</option>}
            {releaseOptions.map(release => (
              <option key={release.id} value={release.id}>{release.name}</option>
            ))}
          </select>
        </label>
      )}
      <label className="release-trend-filter">
        <span>Scenario</span>
        <select
          value={scenarioFilter}
          onChange={event => setScenarioFilter(event.target.value)}
          disabled={scenarioOptions.length === 0}
        >
          <option value={ALL}>All scenarios</option>
          {scenarioOptions.map(option => (
            <option key={option.id} value={option.id}>{option.label}</option>
          ))}
        </select>
      </label>
      <label className="release-trend-filter">
        <span>Transaction</span>
        <select
          value={transactionFilter}
          onChange={event => setTransactionFilter(event.target.value)}
          disabled={transactionOptions.length === 0}
        >
          <option value={ALL}>
            {transactionOptions.length === 0 ? 'No transaction data' : 'All transactions'}
          </option>
          {transactionOptions.map(name => (
            <option key={name} value={name}>{name}</option>
          ))}
        </select>
      </label>
    </div>
  )

  if (loading) {
    return <div className="release-trend-panel release-trend-note" data-testid="release-trend-panel">Loading release trend…</div>
  }
  if (error) {
    return <div className="release-trend-panel release-trend-note" data-testid="release-trend-panel">{error}</div>
  }
  if (!trend || trend.run_count === 0) {
    return (
      <div className="release-trend-panel" data-testid="release-trend-panel">
        {showReleaseFilter && filters}
        <div className="release-trend-note">No archived runs yet for this release.</div>
      </div>
    )
  }

  const transactionScoped = transactionFilter !== ALL

  return (
    <div className="release-trend-panel" data-testid="release-trend-panel">
      <div className="release-trend-header">
        <div>
          <h4 className="release-trend-title">Release Trend Analysis</h4>
          <p className="release-trend-subtitle">
            {trend.run_count} run{trend.run_count === 1 ? '' : 's'} · avg response {formatSignedPct(trend.summary.avg_response_trend_pct)} · error rate {formatSignedPct(trend.summary.error_pct_trend)} across the series.
            {rows.length !== points.length && ` Showing ${rows.length} of ${points.length} runs.`}
          </p>
        </div>
        {trend.summary.latest_regressed && (
          <span className="status-badge status-failed" data-testid="release-trend-latest-regressed">Latest run regressed</span>
        )}
      </div>
      {filters}
      {transactionScoped && (
        <p className="release-trend-scope-note" data-testid="release-trend-scope-note">
          Response and error columns are scoped to <strong>{transactionFilter}</strong>; runs without that transaction are hidden.
        </p>
      )}
      {rows.length === 0 ? (
        <div className="release-trend-note">No runs match the selected filters.</div>
      ) : (
        <div className="trend-table-wrap" ref={tableBounds.ref} style={tableBounds.style}>
          <table className="trend-table">
            <thead>
              <tr>
                <th>Run</th>
                <th>Scenario</th>
                <th>Completed</th>
                <th className="num">{transactionScoped ? 'Txn Avg (ms)' : 'Avg (ms)'}</th>
                <th className="num">Δ Avg</th>
                <th className="num">{transactionScoped ? 'Txn Error %' : 'Error %'}</th>
                <th className="num">{transactionScoped ? 'Txn Volume' : 'Transactions'}</th>
                <th className="num">SLA</th>
                <th>Regressions</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(row => {
                const { point } = row
                const regressed = Boolean(point.regressions && point.regressions.length > 0)
                const selected = point.run_id === selectedRunId
                const avgMs = row.transaction ? row.transaction.avg_response_ms : point.avg_response_ms
                const errorPct = row.transaction ? row.transaction.error_pct : point.error_pct
                const volume = row.transaction ? row.transaction.transactions : point.transactions
                const deltaMs = row.transaction ? row.transactionDeltaMs : point.deltas?.avg_response_ms_delta
                const classNames = [
                  regressed ? 'release-trend-row-regressed' : '',
                  selected ? 'release-trend-row-selected' : '',
                ].filter(Boolean).join(' ')
                return (
                  <tr
                    key={point.run_id}
                    className={classNames}
                    data-testid={`release-trend-row-${point.run_id}`}
                    role="button"
                    tabIndex={0}
                    aria-pressed={selected}
                    aria-label={`Show details for ${point.run_name || point.run_id}`}
                    onClick={() => setSelectedRunId(current => current === point.run_id ? '' : point.run_id)}
                    onKeyDown={event => {
                      if (event.key !== 'Enter' && event.key !== ' ') return
                      event.preventDefault()
                      setSelectedRunId(current => current === point.run_id ? '' : point.run_id)
                    }}
                  >
                    <td><span className="trend-run-name">{point.run_name || point.run_id}</span></td>
                    <td>{row.scenarioLabel}</td>
                    <td>{formatTimestamp(point.completed_at)}</td>
                    <td className="num">{formatOptional(avgMs)}</td>
                    <td className={`num ${typeof deltaMs === 'number' && deltaMs > 0 ? 'text-danger strong' : ''}`}>
                      {typeof deltaMs === 'number' ? formatSignedMs(deltaMs) : '—'}
                    </td>
                    <td className={`num ${errorPct > 0 ? 'text-danger' : ''}`}>{errorPct.toFixed(2)}%</td>
                    <td className="num">{formatCount(volume)}</td>
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
      )}
      <ReleaseTrendRunDetail
        row={selectedRow}
        previousPoint={selectedPreviousPoint}
        report={selectedRow ? reportsById[selectedRow.point.run_id] ?? null : null}
        run={selectedRow ? runsById[selectedRow.point.run_id] : undefined}
        transactionFilter={transactionScoped ? transactionFilter : ''}
        onOpenRun={onOpenRun}
        onOpenRunArchive={onOpenRunArchive}
      />
    </div>
  )
}

interface ReleaseTrendRunDetailProps {
  row?: TrendRow
  previousPoint?: ReleaseTrendPoint
  report: RunArchiveReport | null
  run?: TestSummary
  transactionFilter: string
  onOpenRun?: (id: string) => void
  onOpenRunArchive?: (id: string) => void
}

function ReleaseTrendRunDetail({
  row,
  previousPoint,
  report,
  run,
  transactionFilter,
  onOpenRun,
  onOpenRunArchive,
}: ReleaseTrendRunDetailProps) {
  const transactionRows = useMemo(() => {
    const byAction = report?.by_action
    if (!byAction) return []
    const entries = Object.entries(byAction)
    const scoped = transactionFilter
      ? entries.filter(([name]) => name === transactionFilter)
      : entries
    return scoped
      .sort((left, right) => right[1].transactions - left[1].transactions)
      .slice(0, detailTransactionLimit)
      .map(([name, stats]) => ({ name, stats, sla: report?.sla_rows?.[name] }))
  }, [report, transactionFilter])

  if (!row) {
    return (
      <div className="release-trend-detail is-empty" data-testid="release-trend-detail">
        <span className="release-trend-note">Select a run above to see its scenario, transaction, and resource detail.</span>
      </div>
    )
  }

  const { point } = row
  const deltas = point.deltas
  const overview = report?.overview
  const peaks = report?.resource_peaks

  return (
    <div className="release-trend-detail" data-testid="release-trend-detail">
      <div className="release-trend-detail-header">
        <div>
          <h5 className="release-trend-detail-title">{point.run_name || point.run_id}</h5>
          <p className="release-trend-detail-meta">
            {row.scenarioLabel !== '—' ? `${row.scenarioLabel} · ` : ''}
            Completed {formatTimestamp(point.completed_at)}
            {run?.total_vusers ? ` · ${run.total_vusers} VUsers` : ''}
            {run?.duration ? ` · ${run.duration}` : ''}
            {previousPoint ? ` · compared against ${previousPoint.run_name || previousPoint.run_id}` : ' · first run in the filtered series'}
          </p>
        </div>
        <div className="release-trend-detail-actions">
          <StatusBadge status={point.status} />
          {onOpenRun && (
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => onOpenRun(point.run_id)}>
              Open run
            </button>
          )}
          {onOpenRunArchive && (
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => onOpenRunArchive(point.run_id)}>
              Full report
            </button>
          )}
        </div>
      </div>

      <div className="release-trend-detail-stats">
        <DetailStat label="Transactions" value={formatCount(point.transactions)} />
        <DetailStat label="Error %" value={`${point.error_pct.toFixed(2)}%`} tone={point.error_pct > 0 ? 'danger' : 'success'} />
        <DetailStat label="Avg Response" value={`${formatOptional(point.avg_response_ms)} ms`} />
        <DetailStat label="P95" value={overview?.p95_ms === undefined ? '—' : `${formatOptional(overview.p95_ms)} ms`} />
        <DetailStat label="Peak TPS" value={formatOptional(point.peak_tps)} />
        <DetailStat label="Peak VUsers" value={formatCount(point.peak_active_vusers)} />
        <DetailStat label="Completeness" value={`${formatOptional(point.completeness_pct, 0)}%`} />
        <DetailStat
          label="SLA"
          value={point.sla_breached ? 'Breached' : 'OK'}
          tone={point.sla_breached ? 'danger' : 'success'}
        />
        {point.telemetry_status && <DetailStat label="Telemetry" value={point.telemetry_status} />}
        {peaks && <DetailStat label="Peak CPU" value={`${formatOptional(peaks.cpu_process_pct)}%`} />}
        {peaks && <DetailStat label="Peak Memory" value={`${formatOptional(peaks.mem_process_mb, 0)} MB`} />}
      </div>

      <div className="release-trend-detail-grid">
        <section className="release-trend-detail-block">
          <h6 className="release-trend-detail-subtitle">Change vs previous run</h6>
          {deltas ? (
            <ul className="release-trend-delta-list">
              <li>
                <span>Avg response</span>
                <strong className={deltas.avg_response_ms_delta > 0 ? 'text-danger' : 'text-success'}>
                  {formatSignedMs(deltas.avg_response_ms_delta)} ({formatSignedPct(deltas.avg_response_pct_delta)})
                </strong>
              </li>
              <li>
                <span>Error rate</span>
                <strong className={deltas.error_pct_delta > 0 ? 'text-danger' : 'text-success'}>
                  {formatSignedPoints(deltas.error_pct_delta)} pts
                </strong>
              </li>
              <li>
                <span>Peak TPS</span>
                <strong className={deltas.peak_tps_delta < 0 ? 'text-danger' : 'text-success'}>
                  {formatSignedPoints(deltas.peak_tps_delta, 1)}
                </strong>
              </li>
              <li>
                <span>Completeness</span>
                <strong className={deltas.completeness_delta < 0 ? 'text-danger' : 'text-success'}>
                  {formatSignedPoints(deltas.completeness_delta, 1)} pts
                </strong>
              </li>
            </ul>
          ) : (
            <p className="release-trend-note">No prior run in this release to compare against.</p>
          )}
          {point.regressions && point.regressions.length > 0 && (
            <ul className="release-trend-regression-list">
              {point.regressions.map(regression => (
                <li key={regression} className="text-danger">{regression}</li>
              ))}
            </ul>
          )}
        </section>

        <section className="release-trend-detail-block">
          <h6 className="release-trend-detail-subtitle">
            {transactionFilter ? `Transaction: ${transactionFilter}` : 'Top transactions'}
          </h6>
          {transactionRows.length === 0 ? (
            <p className="release-trend-note">
              {report
                ? 'This run has no per-transaction breakdown archived.'
                : 'Transaction detail is not available for this run.'}
            </p>
          ) : (
            <div className="release-trend-detail-table-wrap">
              {/* Short, already-scrollable list: the global table paginator would
                  add a toolbar for a handful of rows. */}
              <table className="trend-table release-trend-detail-table" data-table-pagination="off">
                <thead>
                  <tr>
                    <th>Transaction</th>
                    <th className="num">Volume</th>
                    <th className="num">Avg (ms)</th>
                    <th className="num">P95</th>
                    <th className="num">Error %</th>
                    <th>SLA</th>
                  </tr>
                </thead>
                <tbody>
                  {transactionRows.map(({ name, stats, sla }) => (
                    <tr key={name}>
                      <td>{name}</td>
                      <td className="num">{formatCount(stats.transactions)}</td>
                      <td className="num">{formatOptional(stats.avg_response_ms)}</td>
                      <td className="num">{formatOptional(stats.p95_ms)}</td>
                      <td className={`num ${stats.error_pct > 0 ? 'text-danger' : ''}`}>{stats.error_pct.toFixed(2)}%</td>
                      <td className={sla?.result_kind === 'fail' ? 'text-danger strong' : ''}>{sla?.result || '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>
    </div>
  )
}

function DetailStat({ label, value, tone = 'neutral' }: { label: string; value: string; tone?: 'neutral' | 'danger' | 'success' }) {
  return (
    <div className={`release-trend-detail-stat tone-${tone}`}>
      <span className="release-trend-detail-stat-value">{value}</span>
      <span className="release-trend-detail-stat-label">{label}</span>
    </div>
  )
}
