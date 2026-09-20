import { AnalysisSavedViews } from './AnalysisSavedViews'
import { useEffect, useMemo, useState } from 'react'
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type {
  Release,
  ReleaseTrend,
  RunArchiveReport,
  TestSummary,
} from '../types'
import { downloadTextFile } from '../utils/fileUtils'
import {
  analysisCSV,
  analysisRuleVersion,
  displayNumber,
  finite,
  metricLabels,
  signedChange,
  type AnalysisMetric,
  type AnalysisReportContext,
} from './dashboardAnalysisModel'
import {
  compareTrendMeasurements,
  trendMeasurement,
  trendStats,
  validTrendStats,
  type TrendPopulation,
} from './releaseTrendModel'
import { chartTooltipContentStyle } from './chartTooltipTheme'
import StatusBadge from './StatusBadge'

interface Props {
  releaseId: string
  applicationId?: string
  onOpenAnalysisReport?: (id: string, context: AnalysisReportContext) => void
  getReleaseTrend: (id: string) => Promise<ReleaseTrend>
  releaseOptions?: Release[]
  onReleaseChange?: (release: Release) => void
  runsById?: Record<string, TestSummary>
  reportsById?: Record<string, RunArchiveReport | null>
  scenarioNames?: Record<string, string>
  onOpenRun?: (id: string) => void
  onOpenRunArchive?: (id: string) => void
  onExportRelease?: () => void
}
const emptyRuns: Record<string, TestSummary> = {}
const emptyReports: Record<string, RunArchiveReport | null> = {}
const emptyNames: Record<string, string> = {}
const emptyReleases: Release[] = []
const ALL = '__all__'

export function ReleaseTrendPanel({
  releaseId,
  applicationId,
  onOpenAnalysisReport,
  getReleaseTrend,
  releaseOptions = emptyReleases,
  onReleaseChange,
  runsById = emptyRuns,
  reportsById = emptyReports,
  scenarioNames = emptyNames,
  onOpenRun,
  onOpenRunArchive,
  onExportRelease,
}: Props) {
  const [trends, setTrends] = useState<ReleaseTrend[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [mode, setMode] = useState('within')
  const [otherRelease, setOtherRelease] = useState('')
  const [scenario, setScenario] = useState(ALL)
  const [transaction, setTransaction] = useState('')
  const [population, setPopulation] = useState<TrendPopulation>('transaction')
  const [metric, setMetric] = useState<AnalysisMetric>('p95_ms')
  const [baselineMode, setBaselineMode] = useState('previous')
  const [pinnedId, setPinnedId] = useState('')
  const [selectedRunId, setSelectedRunId] = useState('')
  const [latencyRule, setLatencyRule] = useState(10)
  const [errorRule, setErrorRule] = useState(1)
  const [sort, setSort] = useState('time')
  const [detailSort, setDetailSort] = useState('regression')
  const [exportError, setExportError] = useState('')
  const otherId = releaseOptions.some(
    (release) => release.id === otherRelease && release.id !== releaseId,
  )
    ? otherRelease
    : releaseOptions.find((release) => release.id !== releaseId)?.id

  useEffect(() => {
    let active = true
    setLoading(true)
    setError('')
    setTrends([])
    setSelectedRunId('')
    const ids =
      mode === 'across' && otherId ? [releaseId, otherId] : [releaseId]
    void Promise.all(ids.map((id) => getReleaseTrend(id)))
      .then((result) => {
        if (active) setTrends(result)
      })
      .catch((cause) => {
        if (active)
          setError(
            cause instanceof Error
              ? cause.message
              : 'Could not load release trend.',
          )
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [releaseId, otherId, mode, getReleaseTrend, retry])

  const points = useMemo(
    () =>
      [
        ...new Map(
          trends
            .flatMap((trend) => trend.points)
            .map((point) => [point.run_id, point]),
        ).values(),
      ].sort(
        (a, b) =>
          Date.parse(a.completed_at) - Date.parse(b.completed_at) ||
          a.run_id.localeCompare(b.run_id),
      ),
    [trends],
  )
  const scenarios = [
    ...new Set(
      points.map(
        (point) => runsById[point.run_id]?.scenario_id || '__unassigned__',
      ),
    ),
  ]
  const effectiveScenario = scenarios.includes(scenario) ? scenario : ALL
  const sourcePoints = points.filter(
    (point) =>
      effectiveScenario === ALL ||
      (runsById[point.run_id]?.scenario_id || '__unassigned__') ===
        effectiveScenario,
  )
  const transactionOptions = [
    ...new Set(
      sourcePoints.flatMap((point) => {
        const report = reportsById[point.run_id]
        const stats =
          population === 'transaction'
            ? report?.by_transaction
            : population === 'action'
              ? report?.by_request
              : !report?.metric_populations
                ? report?.by_action
                : undefined
        return Object.keys(stats ?? {})
      }),
    ),
  ].sort()
  const effectiveTransaction = transactionOptions.includes(transaction)
    ? transaction
    : ''
  const measurements = sourcePoints
    .map((point) =>
      trendMeasurement(
        point,
        reportsById[point.run_id],
        population,
        effectiveTransaction,
        metric,
      ),
    )
    .filter((row) => !effectiveTransaction || row.stats)
  const eligiblePinned = pinnedId
    ? measurements.find((row) => row.point.run_id === pinnedId)
    : measurements.find((row) => runsById[row.point.run_id]?.baseline_pinned) ??
      measurements[0]
  const analysis = measurements.map((row, index) => {
    const recordedId =
      runsById[row.point.run_id]?.comparison_baseline?.source_test_id
    const baseline =
      baselineMode === 'pinned'
        ? eligiblePinned
        : baselineMode === 'recorded'
          ? measurements.find((item) => item.point.run_id === recordedId)
          : [...measurements.slice(0, index)].reverse().find((previous) => {
              if (!validTrendStats(previous.stats)) return false
              return (
                compareTrendMeasurements(row, previous, runsById, reportsById, {
                  latencyPct: latencyRule,
                  errorPoints: errorRule,
                }).eligibility.status !== 'Not comparable'
              )
            })
    return {
      row,
      comparison: compareTrendMeasurements(
        row,
        baseline,
        runsById,
        reportsById,
        { latencyPct: latencyRule, errorPoints: errorRule },
      ),
    }
  })
  const sorted = [...analysis].sort((a, b) =>
    sort === 'regression'
      ? b.comparison.regressions.length - a.comparison.regressions.length ||
        (b.comparison.latencyPct ?? -Infinity) -
          (a.comparison.latencyPct ?? -Infinity)
      : sort === 'latency'
        ? (b.row.latency ?? -Infinity) - (a.row.latency ?? -Infinity)
        : 0,
  )
  const selected = analysis.find(
    (item) => item.row.point.run_id === selectedRunId,
  )
  const latest = analysis[analysis.length - 1]
  const report = selected ? reportsById[selected.row.point.run_id] : undefined
  const baselineReport = selected?.comparison.baseline
    ? reportsById[selected.comparison.baseline.point.run_id]
    : undefined
  const run = selected ? runsById[selected.row.point.run_id] : undefined
  const statSet =
    population === 'transaction'
      ? report?.by_transaction
      : population === 'action'
        ? report?.by_request
        : !report?.metric_populations
          ? report?.by_action
          : undefined
  const transactionRows = Object.entries(statSet ?? {})
    .filter(([name]) => !effectiveTransaction || name === effectiveTransaction)
    .map(([name, stats]) => {
      const baselineStats = trendStats(baselineReport, population, name)
      const current =
        metric === 'average_ms' ? stats.avg_response_ms : stats[metric]
      const prior =
        metric === 'average_ms'
          ? baselineStats?.avg_response_ms
          : baselineStats?.[metric]
      return {
        name,
        stats,
        current,
        prior,
        delta:
          selected?.comparison.eligibility.status !== 'Not comparable' &&
          finite(current) &&
          finite(prior)
            ? current - prior
            : undefined,
        sla:
          population === 'transaction' ? report?.sla_rows?.[name] : undefined,
      }
    })
    .sort((a, b) =>
      detailSort === 'volume'
        ? b.stats.transactions - a.stats.transactions
        : detailSort === 'slowest'
          ? (b.current ?? -Infinity) - (a.current ?? -Infinity)
          : Number(b.sla?.result_kind === 'fail') -
              Number(a.sla?.result_kind === 'fail') ||
            (b.delta ?? -Infinity) - (a.delta ?? -Infinity),
    )
  const chartRows = analysis.map(({ row }) => ({
    run: row.point.run_id,
    time: Date.parse(row.point.completed_at),
    name: row.point.run_name,
    latency: row.latency,
    throughput: row.successfulRate,
    errors: validTrendStats(row.stats) ? row.stats.error_pct : undefined,
    load: row.point.peak_active_vusers,
  }))
  const rowLabel = (id: string) => {
    const point = points.find((item) => item.run_id === id)
    return `${point?.run_name || id} · ${runsById[id]?.ref_key || id}`
  }
  const acceptance = (id: string) =>
    reportsById[id]?.verdict?.status || 'Not assessed'
  const reset = () => {
    setScenario(ALL)
    setTransaction('')
    setPopulation('transaction')
    setMetric('p95_ms')
    setBaselineMode('previous')
    setPinnedId('')
    setSort('time')
    setSelectedRunId('')
    setLatencyRule(10)
    setErrorRule(1)
    setMode('within')
    setDetailSort('regression')
  }
  const exportView = async () => {
    setExportError('')
    const rows: unknown[][] = [
      ['APEX Load filtered trend', analysisRuleVersion],
      ['Releases', ...trends.map((trend) => trend.release_id)],
      ['Population', population],
      ['Scenario', effectiveScenario],
      ['Transaction', effectiveTransaction || 'All'],
      ['Statistic', metric],
      ['Window', 'Entire recorded run'],
      ['Baseline policy', baselineMode],
      ['Latency regression %', latencyRule],
      ['Error regression percentage points', errorRule],
      [
        'Run ID',
        'Run name',
        'Release',
        'Completed',
        'Baseline ID',
        'Latency ms',
        'Latency delta ms',
        'Latency delta %',
        'Successful / s',
        'Error %',
        'Error delta pp',
        'Samples',
        'Observed peak VUsers',
        'Acceptance',
        'Eligibility',
        'Reasons',
        'Regression rules triggered',
      ],
    ]
    analysis.forEach(({ row, comparison }) =>
      rows.push([
        row.point.run_id,
        row.point.run_name,
        runsById[row.point.run_id]?.release_id,
        row.point.completed_at,
        comparison.baseline?.point.run_id,
        row.latency,
        comparison.latencyDelta,
        comparison.latencyPct,
        row.successfulRate,
        validTrendStats(row.stats) ? row.stats.error_pct : undefined,
        comparison.errorDelta,
        row.stats?.transactions,
        row.point.peak_active_vusers,
        acceptance(row.point.run_id),
        comparison.eligibility.status,
        comparison.reason || comparison.eligibility.reasons.join('; '),
        comparison.regressions.join('; '),
      ]),
    )
    try {
      await downloadTextFile(
        analysisCSV(rows),
        'apexload-filtered-release-trend.csv',
        'text/csv;charset=utf-8',
      )
    } catch (cause) {
      setExportError(
        cause instanceof Error ? cause.message : 'Could not export trend.',
      )
    }
  }

  return (
    <section
      className="release-trend-panel"
      data-testid="release-trend-panel"
      aria-busy={loading}
    >
      <div className="release-trend-header">
        <div>
          <h3 className="release-trend-title">Release Trend Analysis</h3>
          <p>
            Track changes against a visible baseline under comparable test
            conditions.
          </p>
        </div>
        <div className="analysis-actions">
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={() => void exportView()}
            disabled={loading || !analysis.length}
          >
            Export this analysis
          </button>
          {onExportRelease && (
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={onExportRelease}
            >
              Export complete release CSV
            </button>
          )}
        </div>
      </div>
      <div
        className="release-trend-filters analysis-controls"
        role="group"
        aria-label="Release trend filters"
      >
        <label className="release-trend-filter">
          <span>Trend scope</span>
          <select
            value={mode}
            onChange={(event) => setMode(event.target.value)}
          >
            <option value="within">Runs within this release</option>
            <option value="across">Compare releases</option>
          </select>
        </label>
        {onReleaseChange && releaseOptions.length > 0 && (
          <label className="release-trend-filter">
            <span>Release</span>
            <select
              value={releaseId}
              onChange={(event) => {
                const release = releaseOptions.find(
                  (item) => item.id === event.target.value,
                )
                if (release) {
                  reset()
                  onReleaseChange(release)
                }
              }}
            >
              {releaseOptions.map((release) => (
                <option key={release.id} value={release.id}>
                  {release.name}
                </option>
              ))}
            </select>
          </label>
        )}
        {mode === 'across' && (
          <label className="release-trend-filter">
            <span>Comparison release</span>
            <select
              value={otherId ?? ''}
              onChange={(event) => setOtherRelease(event.target.value)}
            >
              {!otherId && <option value="">Another release required</option>}
              {releaseOptions
                .filter((release) => release.id !== releaseId)
                .map((release) => (
                  <option key={release.id} value={release.id}>
                    {release.name}
                  </option>
                ))}
            </select>
          </label>
        )}
        <label className="release-trend-filter">
          <span>Population</span>
          <select
            value={population}
            onChange={(event) => {
              setPopulation(event.target.value as TrendPopulation)
              setTransaction('')
            }}
          >
            <option value="transaction">Named transactions</option>
            <option value="action">Requests / actions</option>
            <option value="unknown">Legacy / unclassified</option>
          </select>
        </label>
        <label className="release-trend-filter">
          <span>Scenario</span>
          <select
            value={effectiveScenario}
            onChange={(event) => setScenario(event.target.value)}
          >
            <option value={ALL}>All scenarios</option>
            {scenarios.map((id) => (
              <option value={id} key={id}>
                {id === '__unassigned__'
                  ? 'Unassigned scenario'
                  : scenarioNames[id] || `Scenario ${id}`}
              </option>
            ))}
          </select>
        </label>
        <label className="release-trend-filter">
          <span>Transaction</span>
          <select
            value={effectiveTransaction}
            onChange={(event) => setTransaction(event.target.value)}
            disabled={!transactionOptions.length}
          >
            <option value="">
              {transactionOptions.length
                ? 'All in selected population'
                : 'No transaction data'}
            </option>
            {transactionOptions.map((name) => (
              <option value={name} key={name}>
                {name}
              </option>
            ))}
          </select>
        </label>
        <label className="release-trend-filter">
          <span>Latency statistic</span>
          <select
            value={metric}
            onChange={(event) =>
              setMetric(event.target.value as AnalysisMetric)
            }
          >
            {Object.entries(metricLabels).map(([value, label]) => (
              <option value={value} key={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <button
          className="btn btn-secondary btn-sm"
          type="button"
          onClick={reset}
        >
          Reset analysis
        </button>
      </div>
      <AnalysisSavedViews
        storageKey={`apexload.analysis.trend.v1.${applicationId || releaseId}`}
        values={{
          mode,
          otherRelease: otherId || '',
          releaseId,
          scenario: effectiveScenario,
          transaction: effectiveTransaction,
          population,
          metric,
          baselineMode,
          pinnedId: eligiblePinned?.point.run_id || '',
          latencyRule: String(latencyRule),
          errorRule: String(errorRule),
          sort,
          rules: analysisRuleVersion,
        }}
        onRestore={(values) => {
          const release = releaseOptions.find(
            (item) => item.id === values.releaseId,
          )
          if (release) onReleaseChange?.(release)
          setMode(values.mode === 'across' ? 'across' : 'within')
          setOtherRelease(values.otherRelease || '')
          setScenario(values.scenario || ALL)
          setTransaction(values.transaction || '')
          setPopulation(
            values.population === 'action' || values.population === 'unknown'
              ? values.population
              : 'transaction',
          )
          setMetric(
            Object.prototype.hasOwnProperty.call(metricLabels, values.metric)
              ? (values.metric as AnalysisMetric)
              : 'p95_ms',
          )
          setBaselineMode(
            values.baselineMode === 'pinned' ||
              values.baselineMode === 'recorded'
              ? values.baselineMode
              : 'previous',
          )
          setPinnedId(values.pinnedId || '')
          const latency = Number(values.latencyRule)
          const errors = Number(values.errorRule)
          setLatencyRule(
            Number.isFinite(latency) && latency >= 0 ? latency : 10,
          )
          setErrorRule(Number.isFinite(errors) && errors >= 0 ? errors : 1)
          setSort(
            values.sort === 'latency' || values.sort === 'regression'
              ? values.sort
              : 'time',
          )
        }}
      />
      <div className="analysis-controls">
        <label>
          Baseline policy
          <select
            value={baselineMode}
            onChange={(event) => setBaselineMode(event.target.value)}
          >
            <option value="previous">Previous eligible visible run</option>
            <option value="pinned">Choose a fixed baseline</option>
            <option value="recorded">Recorded run baseline</option>
          </select>
        </label>
        {baselineMode === 'pinned' && (
          <label>
            Fixed baseline
            <select
              value={eligiblePinned?.point.run_id ?? ''}
              onChange={(event) => setPinnedId(event.target.value)}
            >
              {!eligiblePinned && (
                <option value="">Selected baseline has no matching evidence</option>
              )}
              {measurements.map((row) => (
                <option value={row.point.run_id} key={row.point.run_id}>
                  {rowLabel(row.point.run_id)}
                  {runsById[row.point.run_id]?.baseline_pinned
                    ? ' · pinned'
                    : ''}
                </option>
              ))}
            </select>
          </label>
        )}
        <label>
          Table order
          <select
            value={sort}
            onChange={(event) => setSort(event.target.value)}
          >
            <option value="time">Completion time</option>
            <option value="regression">Largest regressions</option>
            <option value="latency">Slowest latency</option>
          </select>
        </label>
      </div>
      {baselineMode === 'pinned' && pinnedId && !eligiblePinned && (
        <p role="status">The fixed baseline has no matching evidence for these filters. Change the filters or explicitly choose another baseline to compare.</p>
      )}
      <details className="analysis-settings">
        <summary>Regression rules · {analysisRuleVersion}</summary>
        <div className="analysis-controls">
          <label>
            Latency increase (%)
            <input
              type="number"
              min="0"
              step="1"
              value={latencyRule}
              onChange={(event) => {
                const value = Number(event.target.value)
                if (finite(value) && value >= 0) setLatencyRule(value)
              }}
            />
          </label>
          <label>
            Error increase (percentage points)
            <input
              type="number"
              min="0"
              max="100"
              step="0.1"
              value={errorRule}
              onChange={(event) => {
                const value = Number(event.target.value)
                if (finite(value) && value >= 0 && value <= 100)
                  setErrorRule(value)
              }}
            />
          </label>
        </div>
        <p>
          Flags require a change greater than the selected rule. These are
          exploratory rules, not statistical significance tests or changes to
          stored acceptance. Missing provenance or fewer than 100 samples makes
          a comparison unverified.
        </p>
      </details>
      {loading ? (
        <p role="status">Loading release trend…</p>
      ) : error ? (
        <p role="alert">
          {error}{' '}
          <button type="button" onClick={() => setRetry((value) => value + 1)}>
            Retry
          </button>
        </p>
      ) : !points.length ? (
        <p>No archived runs yet for this release.</p>
      ) : (
        <>
          <p className="release-trend-subtitle">
            Showing {analysis.length} of {points.length} runs · Entire recorded
            run · {effectiveTransaction || 'All selected-population attempts'}.
            Chart, summary, deltas, and export use these filters.
          </p>
          {effectiveTransaction && (
            <p
              className="release-trend-scope-note"
              data-testid="release-trend-scope-note"
            >
              Response and error columns are scoped to{' '}
              <strong>{effectiveTransaction}</strong>; runs without that
              transaction are hidden.
            </p>
          )}
          {analysis.some((item) => !item.row.stats?.transactions) && (
            <p className="analysis-warning">
              Some runs have no samples in this population. Request / action
              samples in Load Comparison do not establish named transaction
              attempts. Choose Requests / actions to inspect protocol evidence,
              or open the full report to check instrumentation.
            </p>
          )}
          {analysis.length < 2 && (
            <p className="analysis-warning">
              A trend needs at least two comparable runs with matching
              population samples.
            </p>
          )}
          <div className="analysis-context" role="status">
            Latest visible run:{' '}
            {latest ? rowLabel(latest.row.point.run_id) : 'No matching runs'} ·{' '}
            {latest?.comparison.reason ||
              (latest?.comparison.regressions.length
                ? `${latest.comparison.eligibility.status === 'Comparable' ? 'Regressed' : 'Potential regression (unverified)'}: ${latest.comparison.regressions.join('; ')}`
                : 'No selected regression rule exceeded; acceptance is separate.')}
            {latest?.comparison.baseline && (
              <span>
                {' '}
                · Baseline: {rowLabel(latest.comparison.baseline.point.run_id)}
              </span>
            )}
          </div>
          <div className="analysis-trend-charts">
            {(
              [
                {
                  key: 'latency',
                  label: `${metricLabels[metric]} latency (ms)`,
                },
                { key: 'throughput', label: 'Successful throughput (/s)' },
                { key: 'errors', label: 'Errors (%)' },
                { key: 'load', label: 'Observed peak active VUsers' },
              ] as const
            ).map((chart, index) => (
              <div className="load-comparison-chart" key={chart.key}>
                <h4>{chart.label}</h4>
                <ResponsiveContainer width="100%" height={180}>
                  <LineChart
                    data={chartRows}
                    syncId="release-analysis"
                    margin={{ top: 8, right: 20, left: 0, bottom: 8 }}
                  >
                    <CartesianGrid
                      stroke="var(--border)"
                      strokeDasharray="3 3"
                    />
                    <XAxis
                      dataKey="time"
                      type="number"
                      domain={['dataMin', 'dataMax']}
                      tickFormatter={(value) =>
                        new Date(Number(value)).toLocaleString(undefined, {
                          month: 'short',
                          day: 'numeric',
                          hour: '2-digit',
                          minute: '2-digit',
                        })
                      }
                      minTickGap={40}
                      tickCount={3}
                      stroke="var(--text-secondary)"
                    />
                    <YAxis stroke="var(--text-secondary)" width={55} />
                    <Tooltip
                      contentStyle={chartTooltipContentStyle}
                      labelFormatter={(value) =>
                        new Date(Number(value)).toLocaleString()
                      }
                      formatter={(value) => displayNumber(Number(value))}
                    />
                    {latest?.comparison.baseline && (
                      <ReferenceLine
                        x={Date.parse(
                          latest.comparison.baseline.point.completed_at,
                        )}
                        stroke="var(--text-secondary)"
                        strokeDasharray="4 4"
                        label={{
                          value: 'Latest baseline',
                          position: 'insideTopLeft',
                          fill: 'var(--text-secondary)',
                          fontSize: 10,
                        }}
                      />
                    )}
                    <Line
                      dataKey={chart.key}
                      name={chart.label}
                      stroke={`var(--chart-${index + 1})`}
                      type="linear"
                      dot={{ r: 4 }}
                      connectNulls={false}
                      isAnimationActive={false}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            ))}
          </div>
          <div
            className="analysis-table-wrap"
            role="region"
            aria-label="Release trend data"
            tabIndex={0}
          >
            <table className="trend-table" data-table-pagination="off">
              <caption>Run evidence and baseline comparisons</caption>
              <thead>
                <tr>
                  <th>Run / release</th>
                  <th>Comparison outcome</th>
                  <th>{metricLabels[metric]} (ms)</th>
                  <th>Δ latency</th>
                  <th>Error % / Δ pp</th>
                  <th>Successful / s</th>
                  <th>Samples</th>
                  <th>Acceptance</th>
                  <th>Baseline</th>
                </tr>
              </thead>
              <tbody>
                {sorted.map(({ row, comparison }) => (
                  <tr
                    key={row.point.run_id}
                    data-testid={`release-trend-row-${row.point.run_id}`}
                    className={
                      selectedRunId === row.point.run_id
                        ? 'release-trend-row-selected'
                        : comparison.regressions.length
                          ? 'release-trend-row-regressed'
                          : ''
                    }
                  >
                    <td>
                      <button
                        type="button"
                        className="analysis-run-button"
                        aria-expanded={selectedRunId === row.point.run_id}
                        aria-controls="release-trend-run-detail"
                        onClick={() =>
                          setSelectedRunId((id) =>
                            id === row.point.run_id ? '' : row.point.run_id,
                          )
                        }
                      >
                        {row.point.run_name || row.point.run_id}
                      </button>
                      <small className="analysis-id">
                        {runsById[row.point.run_id]?.ref_key ||
                          row.point.run_id}{' '}
                        ·{' '}
                        {releaseOptions.find(
                          (release) =>
                            release.id ===
                            runsById[row.point.run_id]?.release_id,
                        )?.name || runsById[row.point.run_id]?.release_id}
                      </small>
                      <small className="analysis-id">
                        {new Date(row.point.completed_at).toLocaleString()} ·{' '}
                        {scenarioNames[
                          runsById[row.point.run_id]?.scenario_id || ''
                        ] ||
                          runsById[row.point.run_id]?.scenario_id ||
                          'Unassigned scenario'}
                      </small>
                    </td>
                    <td>
                      {comparison.reason ||
                        (comparison.regressions.length
                          ? `${comparison.eligibility.status === 'Comparable' ? 'Regression' : 'Potential regression'}: ${comparison.regressions.join('; ')}`
                          : 'No rule exceeded')}
                      <small className="analysis-id">
                        {comparison.eligibility.status}
                      </small>
                    </td>
                    <td>
                      {validTrendStats(row.stats)
                        ? displayNumber(row.latency)
                        : 'No samples'}
                    </td>
                    <td>
                      {finite(comparison.latencyDelta)
                        ? signedChange(
                            row.latency,
                            comparison.baseline?.latency,
                          )
                        : '—'}
                    </td>
                    <td>
                      {validTrendStats(row.stats)
                        ? displayNumber(row.stats.error_pct, 2)
                        : 'No samples'}
                      <small className="analysis-id">
                        {finite(comparison.errorDelta)
                          ? `${comparison.errorDelta > 0 ? '+' : ''}${displayNumber(comparison.errorDelta, 2)} pp`
                          : '—'}
                      </small>
                    </td>
                    <td>{displayNumber(row.successfulRate)}</td>
                    <td>{row.stats?.transactions ?? 0}</td>
                    <td>{acceptance(row.point.run_id)}</td>
                    <td>
                      {comparison.baseline
                        ? rowLabel(comparison.baseline.point.run_id)
                        : 'None'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div
            id="release-trend-run-detail"
            className="release-trend-detail"
            data-testid="release-trend-detail"
          >
            {!selected ? (
              <p>
                Select a run above to see its scenario, transaction, and
                resource detail.
              </p>
            ) : (
              <>
                <div className="release-trend-detail-header">
                  <div>
                    <h4>{rowLabel(selectedRunId)}</h4>
                    <p>
                      {run?.total_vusers ?? 'Unknown'} planned VUsers ·{' '}
                      {selected.row.point.peak_active_vusers} observed peak
                      VUsers · {run?.duration || 'Unknown duration'}
                    </p>
                    <p>
                      Baseline:{' '}
                      {selected.comparison.baseline
                        ? rowLabel(selected.comparison.baseline.point.run_id)
                        : 'None'}{' '}
                      · {selected.comparison.eligibility.status}
                    </p>
                  </div>
                  <div className="analysis-actions">
                    <StatusBadge status={selected.row.point.status} />
                    {onOpenRun && (
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => onOpenRun(selectedRunId)}
                      >
                        Open run
                      </button>
                    )}
                    {(onOpenAnalysisReport || onOpenRunArchive) && (
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() =>
                          onOpenAnalysisReport
                            ? onOpenAnalysisReport(selectedRunId, {
                                compare:
                                  selected.comparison.baseline?.point.run_id,
                                transaction: effectiveTransaction || undefined,
                                population,
                              })
                            : onOpenRunArchive?.(selectedRunId)
                        }
                      >
                        Full report
                      </button>
                    )}
                  </div>
                </div>
                <p className="analysis-context">
                  {effectiveTransaction || 'All selected-population attempts'} ·{' '}
                  {selected.row.stats?.transactions ?? 0} samples ·{' '}
                  {metricLabels[metric]}: {displayNumber(selected.row.latency)}{' '}
                  ms · Acceptance: {acceptance(selectedRunId)} · Completeness:{' '}
                  {displayNumber(selected.row.point.completeness_pct, 0)}%
                </p>
                <h5>Change vs selected baseline</h5>
                {selected.comparison.reason ? (
                  <p>{selected.comparison.reason}</p>
                ) : (
                  <p>
                    {signedChange(
                      selected.row.latency,
                      selected.comparison.baseline?.latency,
                    )}{' '}
                    · Error change{' '}
                    {displayNumber(selected.comparison.errorDelta, 2)} pp ·
                    Successful throughput change{' '}
                    {displayNumber(selected.comparison.throughputDelta)} /s
                  </p>
                )}
                <ul>
                  {selected.comparison.eligibility.reasons.map((reason) => (
                    <li key={reason}>{reason}</li>
                  ))}
                  {selected.comparison.regressions.map((reason) => (
                    <li key={reason}>{reason}</li>
                  ))}
                </ul>
                <details>
                  <summary>Run context and telemetry evidence</summary>
                  <p>
                    Scenario revision:{' '}
                    {report?.change_attribution?.scenario_revision ||
                      'Unverified'}{' '}
                    · Environment:{' '}
                    {report?.change_attribution?.environment || 'Unverified'} ·
                    Data revision:{' '}
                    {report?.change_attribution?.data_revision || 'Unverified'}
                  </p>
                  <p>
                    Script revisions:{' '}
                    {report?.change_attribution?.script_revisions?.join(', ') ||
                      'Unverified'}
                  </p>
                  <p>
                    Telemetry status:{' '}
                    {selected.row.point.telemetry_status || 'Unavailable'} ·
                    Recorded process peak CPU:{' '}
                    {displayNumber(report?.resource_peaks?.cpu_process_pct)}% ·
                    Recorded process peak memory:{' '}
                    {displayNumber(report?.resource_peaks?.mem_process_mb)} MB.
                  </p>
                  <p>
                    Process resource peaks do not establish system-under-test
                    utilization or time correlation. Use the full report’s
                    resource timeline and telemetry evidence to investigate the
                    same interval.
                  </p>
                </details>
                <div className="analysis-controls">
                  <label>
                    Transaction ranking
                    <select
                      value={detailSort}
                      onChange={(event) => setDetailSort(event.target.value)}
                    >
                      <option value="regression">
                        Acceptance breaches and degradation
                      </option>
                      <option value="slowest">Slowest</option>
                      <option value="volume">Most traffic</option>
                    </select>
                  </label>
                </div>
                {!transactionRows.length ? (
                  <p>
                    This run has no per-transaction breakdown archived for this
                    population.
                  </p>
                ) : (
                  <div
                    className="analysis-table-wrap"
                    tabIndex={0}
                    role="region"
                    aria-label="Transaction investigation"
                  >
                    <table className="trend-table" data-table-pagination="off">
                      <caption>
                        Transaction investigation · {transactionRows.length}{' '}
                        entries
                      </caption>
                      <thead>
                        <tr>
                          <th>Transaction</th>
                          <th>Samples</th>
                          <th>{metricLabels[metric]} (ms)</th>
                          <th>Δ vs baseline</th>
                          <th>Error %</th>
                          <th>Acceptance</th>
                        </tr>
                      </thead>
                      <tbody>
                        {transactionRows.map((item) => (
                          <tr key={item.name}>
                            <td>
                              <button
                                className="analysis-run-button"
                                type="button"
                                onClick={() => setTransaction(item.name)}
                              >
                                {item.name}
                              </button>
                            </td>
                            <td>{item.stats.transactions}</td>
                            <td>{displayNumber(item.current)}</td>
                            <td>
                              {selected.comparison.eligibility.status ===
                              'Not comparable'
                                ? 'Not comparable'
                                : signedChange(item.current, item.prior)}
                            </td>
                            <td>
                              {validTrendStats(item.stats)
                                ? displayNumber(item.stats.error_pct, 2)
                                : 'No samples'}
                            </td>
                            <td>{item.sla?.result || 'Not assessed'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </>
            )}
          </div>
        </>
      )}
      {exportError && <p role="alert">{exportError}</p>}
    </section>
  )
}
