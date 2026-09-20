import { AnalysisSavedViews } from './AnalysisSavedViews'
import { useEffect, useMemo, useState } from 'react'
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type {
  LoadComparisonPoint,
  LoadComparisonRequestOptions,
  LoadComparisonResponse,
  LoadComparisonScope,
  RunArchiveReport,
  TestSummary,
} from '../types'
import { downloadTextFile } from '../utils/fileUtils'
import { chartTooltipContentStyle } from './chartTooltipTheme'
import {
  analysisCSV,
  analysisRuleVersion,
  comparisonEligibility,
  displayNumber,
  loadLatency,
  metricLabels,
  numericInput,
  signedChange,
  validLoadPoint,
  type AnalysisMetric,
  type LatencyOutcome,
} from './dashboardAnalysisModel'

export interface LoadComparisonTestOption {
  id: string
  name: string
  releaseId?: string
  releaseName?: string
  completedAt?: string
  vusers?: number
  transactionCount?: number
}
interface Props {
  applicationId?: string
  getLoadComparison: (
    applicationId: string,
    scope?: LoadComparisonScope,
    key?: string,
    options?: LoadComparisonRequestOptions,
  ) => Promise<LoadComparisonResponse>
  testOptions?: LoadComparisonTestOption[]
  selectedTestIds?: string[]
  onSelectedTestIdsChange?: (ids: string[]) => void
  runsById?: Record<string, TestSummary>
  reportsById?: Record<string, RunArchiveReport | null>
}
const emptyRuns: Record<string, TestSummary> = {}
const emptyReports: Record<string, RunArchiveReport | null> = {}
const colors = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
]
const populations = {
  action: 'Requests / actions',
  transaction: 'Named transactions',
  unknown: 'Legacy / unclassified',
}
type Population = keyof typeof populations

export function LoadComparisonPanel({
  applicationId,
  getLoadComparison,
  testOptions = [],
  selectedTestIds,
  onSelectedTestIdsChange,
  runsById = emptyRuns,
  reportsById = emptyReports,
}: Props) {
  const [scope, setScope] = useState<LoadComparisonScope>('transaction')
  const [population, setPopulation] = useState<Population>('action')
  const [key, setKey] = useState('')
  const [comparison, setComparison] = useState<LoadComparisonResponse | null>(
    null,
  )
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const [mode, setMode] = useState<'run' | 'release'>('run')
  const [baselineKey, setBaselineKey] = useState('')
  const [candidateKey, setCandidateKey] = useState('')
  const [metric, setMetric] = useState<AnalysisMetric>('p95_ms')
  const [outcome, setOutcome] = useState<LatencyOutcome>('all')
  const [windowMode, setWindowMode] = useState('all')
  const [start, setStart] = useState('0')
  const [end, setEnd] = useState('')
  const [window, setWindow] = useState<{
    startSeconds?: number
    endSeconds?: number
  }>({})
  const [windowError, setWindowError] = useState('')
  const [latencyLimit, setLatencyLimit] = useState('')
  const [errorLimit, setErrorLimit] = useState('')
  const [throughputLimit, setThroughputLimit] = useState('')
  const [minimumSeconds, setMinimumSeconds] = useState('60')
  const [exportError, setExportError] = useState('')
  const validIds = new Set(testOptions.map((option) => option.id))
  const selectedIds = testOptions.length
    ? (selectedTestIds ?? testOptions.map((option) => option.id)).filter((id) =>
        validIds.has(id),
      )
    : selectedTestIds
  const idsKey = selectedIds?.join('\u001f')
  const noneSelected = selectedIds?.length === 0

  useEffect(() => {
    let active = true
    setComparison(null)
    setError('')
    if (!applicationId || noneSelected) {
      setLoading(false)
      return
    }
    setLoading(true)
    const options: LoadComparisonRequestOptions = {
      ...window,
      ...(scope === 'transaction' ? { population } : {}),
      ...(idsKey !== undefined ? { testIds: idsKey.split('\u001f') } : {}),
    }
    void getLoadComparison(applicationId, scope, key, options)
      .then((result) => {
        if (active) setComparison(result)
      })
      .catch((cause) => {
        if (active)
          setError(
            cause instanceof Error
              ? cause.message
              : 'Could not load comparison.',
          )
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [
    applicationId,
    getLoadComparison,
    scope,
    population,
    key,
    idsKey,
    noneSelected,
    window,
    retry,
  ])

  const levels = comparison?.load_levels ?? []
  const series =
    mode === 'run' && comparison?.run_series
      ? comparison.run_series
      : (comparison?.releases ?? [])
  const effectiveMode =
    mode === 'run' && comparison?.run_series ? 'run' : 'release'
  const idFor = (item: (typeof series)[number]) =>
    item.run_id || item.release_id
  const labelFor = (item: (typeof series)[number]) =>
    item.run_id
      ? `${item.run_name || item.run_id} · ${item.run_id} · ${item.release_name}`
      : `${item.release_name} · ${item.run_count} runs`
  const baseline =
    series.find((item) => idFor(item) === baselineKey) ?? series[0]
  const candidate =
    series.find(
      (item) =>
        idFor(item) === candidateKey &&
        idFor(item) !== (baseline && idFor(baseline)),
    ) ??
    [...series]
      .reverse()
      .find((item) => !baseline || idFor(item) !== idFor(baseline))
  const plotted = [baseline, candidate].filter(
    (item): item is (typeof series)[number] => Boolean(item),
  )
  const latencyMax = numericInput(latencyLimit)
  const errorsMax = numericInput(errorLimit)
  const throughputMin = numericInput(throughputLimit)
  const secondsMin = numericInput(minimumSeconds) ?? 60
  const hasLimits =
    latencyMax !== undefined &&
    errorsMax !== undefined &&
    errorsMax <= 100 &&
    secondsMin > 0 &&
    throughputMin !== undefined
  const points = candidate?.points ?? []
  const meetsLimits = (point: LoadComparisonPoint) => {
    const latency = loadLatency(point, metric, outcome)
    return (
      hasLimits &&
      effectiveMode === 'run' &&
      !point.duration_estimated &&
      validLoadPoint(point) &&
      latency !== undefined &&
      point.successful_rate !== undefined &&
      latency <= latencyMax! &&
      point.error_pct <= errorsMax! &&
      point.successful_rate >= throughputMin! &&
      (point.observed_seconds ?? 0) >= secondsMin
    )
  }
  const highest = [...levels]
    .reverse()
    .find((level) =>
      points.some(
        (point) => point.load_level_id === level.id && meetsLimits(point),
      ),
    )
  const chartRows = levels.map((level) => {
    const row: Record<string, number | string | undefined> = {
      load: (level.min_vusers + level.max_vusers) / 2,
      label: level.label,
    }
    plotted.forEach((item, index) => {
      const point = item.points.find((p) => p.load_level_id === level.id)
      row[`latency_${index}`] = loadLatency(point, metric, outcome)
      row[`throughput_${index}`] = validLoadPoint(point)
        ? point?.successful_rate
        : undefined
      row[`errors_${index}`] = validLoadPoint(point)
        ? point?.error_pct
        : undefined
    })
    return row
  })
  const eligibility = useMemo(() => {
    if (!baseline || !candidate)
      return {
        status: 'Unverified',
        reasons: ['Select two runs or releases to establish a comparison.'],
      }
    const baselineIds = baseline.run_id
      ? [baseline.run_id]
      : [...new Set(baseline.points.flatMap((point) => point.run_ids ?? []))]
    const candidateIds = candidate.run_id
      ? [candidate.run_id]
      : [...new Set(candidate.points.flatMap((point) => point.run_ids ?? []))]
    if (!baselineIds.length || !candidateIds.length)
      return {
        status: 'Unverified',
        reasons: ['Run provenance is unavailable for this series.'],
      }
    const checks = [...baselineIds.slice(1), ...candidateIds].map((id) =>
      comparisonEligibility(
        runsById[id],
        runsById[baselineIds[0]],
        reportsById[id],
        reportsById[baselineIds[0]],
        false,
      ),
    )
    return {
      status: checks.some((check) => check.status === 'Not comparable')
        ? 'Not comparable'
        : checks.every((check) => check.status === 'Comparable')
          ? 'Comparable'
          : 'Unverified',
      reasons: [...new Set(checks.flatMap((check) => check.reasons))],
    }
  }, [baseline, candidate, runsById, reportsById])
  const repeatSpread = (releaseId: string, levelId: string) => {
    const values = (comparison?.run_series ?? [])
      .filter((item) => item.release_id === releaseId)
      .map((item) =>
        loadLatency(
          item.points.find((point) => point.load_level_id === levelId),
          metric,
          outcome,
        ),
      )
      .filter(
        (value): value is number =>
          value !== undefined && Number.isFinite(value),
      )
    return values.length
      ? `${displayNumber(Math.min(...values))}–${displayNumber(Math.max(...values))} ms across ${values.length} measured repeats (range, not a confidence interval)`
      : 'Repeat spread unavailable'
  }
  const selectedOption = comparison?.options.find(
    (option) => option.key === comparison.selected_key,
  )
  const sourceNote =
    scope === 'group'
      ? 'Stored group summaries. Percentiles and successful throughput require event-level evidence and are unavailable here.'
      : population === 'transaction'
        ? 'Named transaction attempts only; protocol requests are excluded.'
        : population === 'action'
          ? 'Request / action attempts only; enclosing business transactions are excluded.'
          : 'Legacy archive samples with unverified population. Do not use these to qualify a business-transaction regression.'

  const exportComparison = async () => {
    if (!comparison) return
    setExportError('')
    const rows: unknown[][] = [
      ['APEX Load comparison', analysisRuleVersion],
      ['Application', applicationId],
      ['Population', scope === 'group' ? 'group summaries' : population],
      ['Series view', effectiveMode],
      ['Series', comparison.selected_key],
      ['Window start seconds', window.startSeconds ?? 0],
      ['Window end seconds', window.endSeconds ?? 'Entire recorded run'],
      ['Interval policy', 'Complete recorded intervals; no interpolation'],
      ['Baseline', baseline && idFor(baseline)],
      ['Candidate', candidate && idFor(candidate)],
      ['Eligibility', eligibility.status, ...eligibility.reasons],
      ['Selected run IDs', ...(selectedIds ?? [])],
      ['Latency metric / outcome', metric, outcome],
      ['Analysis limits', latencyMax, errorsMax, throughputMin, secondsMin],
      ...(comparison.warnings?.map((warning) => ['Warning', warning]) ?? []),
      ...(comparison.skipped_runs?.map((run) => [
        'Excluded run',
        run.test_id,
        run.reason,
      ]) ?? []),
      [
        'Release',
        'Run',
        'Load min VUsers',
        'Load max VUsers',
        'Latency ms',
        'Successful / s',
        'Attempt / s',
        'Error %',
        'Attempts',
        'Errors',
        'Run count',
        'Interval count',
        'Observed seconds',
        'Evidence',
        'Source runs',
        'Latency delta ms',
        'Successful rate delta /s',
        'Error delta pp',
        'Duration estimated',
        'Repeat latency range',
      ],
    ]
    plotted.forEach((item) =>
      levels.forEach((level) => {
        const point = item.points.find((p) => p.load_level_id === level.id)
        const base = baseline?.points.find((p) => p.load_level_id === level.id)
        const latency = loadLatency(point, metric, outcome)
        const baseLatency = loadLatency(base, metric, outcome)
        rows.push([
          item.release_id,
          item.run_id,
          level.min_vusers,
          level.max_vusers,
          loadLatency(point, metric, outcome),
          point?.successful_rate,
          point?.tps,
          validLoadPoint(point) ? point?.error_pct : undefined,
          point?.transactions,
          point?.errors,
          point?.run_count,
          point?.sample_count,
          point?.observed_seconds,
          point?.evidence_status ?? 'No measurement',
          point?.run_ids?.join('; '),
          latency !== undefined && baseLatency !== undefined
            ? latency - baseLatency
            : undefined,
          point?.successful_rate !== undefined &&
          base?.successful_rate !== undefined
            ? point.successful_rate - base.successful_rate
            : undefined,
          validLoadPoint(point) && validLoadPoint(base)
            ? point!.error_pct - base!.error_pct
            : undefined,
          point?.duration_estimated ?? false,
          effectiveMode === 'release'
            ? repeatSpread(item.release_id, level.id)
            : undefined,
        ])
      }),
    )
    try {
      await downloadTextFile(
        analysisCSV(rows),
        'apexload-load-comparison.csv',
        'text/csv;charset=utf-8',
      )
    } catch (cause) {
      setExportError(
        cause instanceof Error ? cause.message : 'Could not export comparison.',
      )
    }
  }
  const reset = () => {
    setKey('')
    setBaselineKey('')
    setCandidateKey('')
    setScope('transaction')
    setPopulation('action')
    setMetric('p95_ms')
    setOutcome('all')
    setWindowMode('all')
    setWindow({})
    setStart('0')
    setEnd('')
    setWindowError('')
    setMode('run')
    setLatencyLimit('')
    setErrorLimit('')
    setThroughputLimit('')
    setMinimumSeconds('60')
  }

  return (
    <section
      className="load-comparison-panel"
      data-testid="load-comparison-panel"
      aria-busy={loading}
    >
      <div className="load-comparison-header">
        <div>
          <h3 className="dash-section-title">Load Comparison</h3>
          <p>
            Compare latency, successful throughput, and errors at observed load.
          </p>
        </div>
        <button
          className="btn btn-secondary btn-sm"
          type="button"
          disabled={!comparison || loading}
          onClick={() => void exportComparison()}
        >
          Export this comparison
        </button>
      </div>
      <div className="load-comparison-controls">
        <div
          role="group"
          aria-label="Load comparison scope"
          className="time-range-selector load-comparison-scope-toggle"
        >
          {(['transaction', 'group'] as const).map((value) => (
            <button
              className={`btn-chip ${scope === value ? 'active' : ''}`}
              type="button"
              key={value}
              aria-pressed={scope === value}
              onClick={() => {
                setScope(value)
                if (value === 'group') {
                  setMetric('average_ms')
                  setOutcome('all')
                }
                setKey('')
              }}
            >
              {value === 'group' ? 'Groups' : 'Transactions'}
            </button>
          ))}
        </div>
        {scope === 'transaction' && (
          <label className="load-comparison-select">
            <span>Metric population</span>
            <select
              value={population}
              onChange={(event) => {
                setPopulation(event.target.value as Population)
                setKey('')
              }}
            >
              {Object.entries(populations).map(([value, label]) => (
                <option value={value} key={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="load-comparison-select">
          <span>
            {scope === 'group'
              ? 'Group'
              : population === 'action'
                ? 'Request / action'
                : 'Transaction'}
          </span>
          <select
            aria-label={`Select ${scope === 'group' ? 'group' : 'transaction'} for load comparison`}
            value={comparison?.selected_key ?? ''}
            disabled={!comparison?.options.length || loading}
            onChange={(event) => setKey(event.target.value)}
          >
            {!comparison?.options.length && (
              <option value="">No matching evidence</option>
            )}
            {comparison?.options.map((option) => (
              <option value={option.key} key={option.key}>
                {option.label}
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
        {selectedIds && (
          <span className="load-comparison-run-count">
            {selectedIds.length} of {testOptions.length} runs selected
          </span>
        )}
      </div>
      <p
        className="dashboard-context-note"
        aria-label="Comparison metric population"
      >
        Population: {sourceNote} Charts cover the selected series and runs; the
        archive table below shows whole-run totals across stored populations.
      </p>
      <AnalysisSavedViews
        storageKey={`apexload.analysis.load.v1.${applicationId}`}
        values={{
          scope,
          population,
          key: comparison?.selected_key || key,
          mode,
          baseline: baseline ? idFor(baseline) : '',
          candidate: candidate ? idFor(candidate) : '',
          metric,
          outcome,
          start: String(window.startSeconds ?? 0),
          end: window.endSeconds === undefined ? '' : String(window.endSeconds),
          latencyLimit,
          errorLimit,
          throughputLimit,
          minimumSeconds,
          runs: JSON.stringify(selectedIds ?? []),
          rules: analysisRuleVersion,
        }}
        onRestore={(values) => {
          setScope(values.scope === 'group' ? 'group' : 'transaction')
          setPopulation(
            values.population === 'transaction' ||
              values.population === 'unknown'
              ? values.population
              : 'action',
          )
          setKey(values.key || '')
          setMode(values.mode === 'release' ? 'release' : 'run')
          setBaselineKey(values.baseline || '')
          setCandidateKey(values.candidate || '')
          setMetric(
            Object.prototype.hasOwnProperty.call(metricLabels, values.metric)
              ? (values.metric as AnalysisMetric)
              : 'p95_ms',
          )
          setOutcome(
            values.outcome === 'successful' || values.outcome === 'failed'
              ? values.outcome
              : 'all',
          )
          const a = numericInput(values.start || '0') ?? 0
          const b = numericInput(values.end || '')
          const custom = b !== undefined && b > a
          setWindow(custom ? { startSeconds: a, endSeconds: b } : {})
          setWindowMode(custom ? 'custom' : 'all')
          setStart(String(a))
          setEnd(custom ? String(b) : '')
          setWindowError('')
          setLatencyLimit(values.latencyLimit || '')
          setErrorLimit(values.errorLimit || '')
          setThroughputLimit(values.throughputLimit || '')
          setMinimumSeconds(values.minimumSeconds || '60')
          try {
            const ids: unknown = JSON.parse(values.runs || '[]')
            if (Array.isArray(ids) && ids.every((id) => typeof id === 'string'))
              onSelectedTestIdsChange?.(ids.filter((id) => validIds.has(id)))
          } catch {
            /* Preserve current selection for malformed saved values. */
          }
        }}
      />
      <details className="analysis-settings">
        <summary>Measurement window and analysis limits</summary>
        <div className="analysis-controls">
          <label>
            Measurement window
            <select
              value={windowMode}
              onChange={(event) => {
                setWindowMode(event.target.value)
                if (event.target.value === 'all') {
                  setWindow({})
                  setWindowError('')
                }
              }}
            >
              <option value="all">Entire recorded run</option>
              <option value="custom">Selected steady-state interval</option>
            </select>
          </label>
          {windowMode === 'custom' && (
            <>
              <label>
                Start after run start (s)
                <input
                  type="number"
                  min="0"
                  value={start}
                  onChange={(event) => setStart(event.target.value)}
                />
              </label>
              <label>
                End after run start (s)
                <input
                  type="number"
                  min="0"
                  value={end}
                  onChange={(event) => setEnd(event.target.value)}
                />
              </label>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={() => {
                  const a = numericInput(start)
                  const b = numericInput(end)
                  if (a === undefined || b === undefined || b <= a) {
                    setWindowError(
                      'Enter an end greater than the start, in seconds.',
                    )
                    return
                  }
                  setWindowError('')
                  setWindow({ startSeconds: a, endSeconds: b })
                }}
              >
                Apply window
              </button>
            </>
          )}
          <label>
            Maximum latency (ms)
            <input
              type="number"
              min="0"
              value={latencyLimit}
              onChange={(event) => setLatencyLimit(event.target.value)}
            />
          </label>
          <label>
            Maximum errors (%)
            <input
              type="number"
              min="0"
              max="100"
              step="0.1"
              value={errorLimit}
              onChange={(event) => setErrorLimit(event.target.value)}
            />
          </label>
          <label>
            Minimum successful / s
            <input
              type="number"
              min="0"
              value={throughputLimit}
              onChange={(event) => setThroughputLimit(event.target.value)}
            />
          </label>
          <label>
            Minimum seconds per run / load
            <input
              type="number"
              min="1"
              value={minimumSeconds}
              onChange={(event) => setMinimumSeconds(event.target.value)}
            />
          </label>
        </div>
        <p className="dashboard-context-note">
          Select a known steady-state interval; this view does not infer one.
          Only complete recorded intervals are included. Analysis limits are
          exploratory and do not change stored acceptance criteria. Percentiles
          merge recorded event distributions; they are never averages of run
          percentiles.
        </p>
        {windowError && <p role="alert">{windowError}</p>}
      </details>
      <p className="analysis-context">
        Applied window:{' '}
        {window.endSeconds !== undefined
          ? `${window.startSeconds}–${window.endSeconds}s after run start`
          : 'Entire recorded run'}{' '}
        · {selectedOption?.sample_count ?? 0} source records
      </p>
      {!!comparison?.warnings?.length && (
        <div className="analysis-warning" role="status">
          {comparison.warnings.join(' ')}
          <ul>
            {comparison.skipped_runs?.map((run) => (
              <li key={run.test_id}>
                {run.test_id}: {run.reason}
              </li>
            ))}
          </ul>
        </div>
      )}
      {loading ? (
        <p role="status">Loading load comparison…</p>
      ) : error ? (
        <div role="alert">
          {error}{' '}
          <button type="button" onClick={() => setRetry((value) => value + 1)}>
            Retry
          </button>
        </div>
      ) : noneSelected ? (
        <p>Select at least one archived run to compare</p>
      ) : !series.length ? (
        <p>
          No matching archived evidence. Try another population or measurement
          window, or select runs below.
        </p>
      ) : (
        <>
          <div className="analysis-controls">
            <label>
              Series view
              <select
                value={effectiveMode}
                onChange={(event) => {
                  setMode(event.target.value as 'run' | 'release')
                  setBaselineKey('')
                  setCandidateKey('')
                }}
              >
                <option value="run" disabled={!comparison?.run_series}>
                  Individual runs
                </option>
                <option value="release">Release summaries</option>
              </select>
            </label>
            <label>
              Baseline
              <select
                value={baseline ? idFor(baseline) : ''}
                onChange={(event) => setBaselineKey(event.target.value)}
              >
                {series.map((item) => (
                  <option value={idFor(item)} key={idFor(item)}>
                    {labelFor(item)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Candidate
              <select
                value={candidate ? idFor(candidate) : ''}
                onChange={(event) => setCandidateKey(event.target.value)}
              >
                {!candidate && (
                  <option value="">Another series required</option>
                )}
                {series
                  .filter(
                    (item) => !baseline || idFor(item) !== idFor(baseline),
                  )
                  .map((item) => (
                    <option value={idFor(item)} key={idFor(item)}>
                      {labelFor(item)}
                    </option>
                  ))}
              </select>
            </label>
            <label>
              Latency statistic
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
            <label>
              Latency outcomes
              <select
                value={outcome}
                onChange={(event) =>
                  setOutcome(event.target.value as LatencyOutcome)
                }
              >
                <option value="all">All attempts</option>
                <option value="successful">Successful only</option>
                <option value="failed">Failed only</option>
              </select>
            </label>
          </div>
          {((baselineKey &&
            !series.some((item) => idFor(item) === baselineKey)) ||
            (candidateKey &&
              !series.some((item) => idFor(item) === candidateKey))) && (
            <p role="status" className="analysis-warning">
              A previously selected run is unavailable in this evidence set.
              Review the displayed baseline and candidate before interpreting
              changes.
            </p>
          )}
          <details className="analysis-qualification">
            <summary>Comparison: {eligibility.status}</summary>
            <ul>
              {eligibility.reasons.map((reason) => (
                <li key={reason}>{reason}</li>
              ))}
            </ul>
            <p>
              Same observed load does not establish equivalent arrival rates or
              workload mix. Differences remain exploratory until provenance is
              verified.
            </p>
          </details>
          {levels.length < 2 && (
            <p className="analysis-warning">
              One load level measured. A point comparison cannot establish a
              scaling curve or capacity limit.
            </p>
          )}
          {!candidate && (
            <p className="analysis-warning">
              One run or release is available. Select another comparable run;
              one point cannot establish a trend.
            </p>
          )}
          {effectiveMode === 'release' && (
            <p className="dashboard-context-note">
              Release summaries pool selected repeats. Switch to Individual runs
              to inspect variability; pooled counts are not statistical
              confidence.
            </p>
          )}
          {chartRows.every(
            (row) => row.latency_0 === undefined && row.latency_1 === undefined,
          ) && (
            <p className="analysis-warning">
              No latency evidence for this statistic and outcome. Try Mean / All
              attempts for older archives, or inspect the missing-sample reasons
              below.
            </p>
          )}
          <div className="load-comparison-chart-grid">
            {(
              [
                {
                  key: 'latency',
                  title: `${metricLabels[metric]} response time`,
                  unit: 'ms',
                  limit: latencyMax,
                },
                {
                  key: 'throughput',
                  title: 'Successful throughput',
                  unit: '/s',
                  limit: throughputMin,
                },
                {
                  key: 'errors',
                  title: 'Error rate',
                  unit: '%',
                  limit: errorsMax,
                },
              ] as const
            ).map((chart) => (
              <div
                className="load-comparison-chart"
                key={chart.key}
                data-testid={`load-comparison-chart-${chart.key}`}
              >
                <h4>
                  {chart.title} ({chart.unit})
                </h4>
                <ResponsiveContainer width="100%" height={240}>
                  <LineChart
                    data={chartRows}
                    margin={{ top: 16, right: 20, bottom: 12, left: 0 }}
                    syncId="load-analysis"
                  >
                    <CartesianGrid
                      stroke="var(--border)"
                      strokeDasharray="3 3"
                    />
                    <XAxis
                      dataKey="load"
                      type="number"
                      domain={['dataMin', 'dataMax']}
                      stroke="var(--text-secondary)"
                      label={{
                        value: 'Observed active VUsers',
                        position: 'insideBottom',
                        offset: -8,
                      }}
                    />
                    <YAxis
                      stroke="var(--text-secondary)"
                      width={55}
                      domain={[0, 'auto']}
                    />
                    <Tooltip
                      contentStyle={chartTooltipContentStyle}
                      labelFormatter={(value) =>
                        `Observed load: ${value} VUsers`
                      }
                      formatter={(value) => displayNumber(Number(value))}
                    />
                    <Legend wrapperStyle={{ paddingTop: 16 }} />
                    {chart.limit !== undefined && (
                      <ReferenceLine
                        y={chart.limit}
                        stroke="var(--warning)"
                        strokeDasharray="6 4"
                        label="Analysis limit"
                      />
                    )}
                    {plotted.map((item, index) => (
                      <Line
                        key={idFor(item)}
                        name={`${index === 0 ? 'Baseline' : 'Candidate'} · ${item.run_name || item.release_name}`}
                        dataKey={`${chart.key}_${index}`}
                        stroke={colors[index]}
                        strokeDasharray={index ? '6 3' : undefined}
                        type="linear"
                        dot={{ r: 4 }}
                        connectNulls={false}
                        isAnimationActive={false}
                      />
                    ))}
                  </LineChart>
                </ResponsiveContainer>
              </div>
            ))}
          </div>
          <p className="dashboard-context-note">
            Load uses the recorded group concurrency when available, otherwise
            total active VUsers. Bucketed loads are plotted at their midpoint;
            exact ranges are listed below. Missing measurements remain gaps.
          </p>
          <p className="analysis-context">
            Highest tested candidate load meeting analysis limits:{' '}
            <strong>
              {effectiveMode === 'release'
                ? 'Select an individual candidate run to assess duration and limits'
                : !hasLimits
                  ? 'Set all three analysis limits'
                  : highest
                    ? highest.label
                    : 'Not established'}
            </strong>
            . This is not an extrapolated capacity or release acceptance
            verdict.
          </p>
          <div
            className="analysis-table-wrap"
            tabIndex={0}
            role="region"
            aria-label="Load comparison data"
          >
            <table className="trend-table" data-table-pagination="off">
              <caption>Measured values and changes at matching load</caption>
              <thead>
                <tr>
                  <th>Observed load</th>
                  <th>Series</th>
                  <th>{metricLabels[metric]} (ms)</th>
                  <th>Δ latency vs baseline</th>
                  <th>Successful / s</th>
                  <th>Δ successful / s</th>
                  <th>Error %</th>
                  <th>Δ error (pp)</th>
                  <th>Samples / runs</th>
                  <th>Evidence</th>
                </tr>
              </thead>
              <tbody>
                {levels.flatMap((level) =>
                  plotted.map((item, index) => {
                    const point = item.points.find(
                      (p) => p.load_level_id === level.id,
                    )
                    const basePoint = baseline?.points.find(
                      (p) => p.load_level_id === level.id,
                    )
                    return (
                      <tr key={`${level.id}-${idFor(item)}`}>
                        <td>
                          {level.label}
                          {!level.exact && ' (bucket)'}
                        </td>
                        <td>
                          {index === 0 ? 'Baseline' : 'Candidate'}:{' '}
                          {item.run_name || item.release_name}
                          <small className="analysis-id">{idFor(item)}</small>
                        </td>
                        <td>
                          {displayNumber(loadLatency(point, metric, outcome))}
                        </td>
                        <td>
                          {index
                            ? signedChange(
                                loadLatency(point, metric, outcome),
                                loadLatency(basePoint, metric, outcome),
                              )
                            : 'Baseline'}
                        </td>
                        <td>{displayNumber(point?.successful_rate)}</td>
                        <td>
                          {index
                            ? signedChange(
                                point?.successful_rate,
                                basePoint?.successful_rate,
                                '/s',
                              )
                            : '—'}
                        </td>
                        <td>
                          {validLoadPoint(point)
                            ? displayNumber(point?.error_pct, 2)
                            : 'Unavailable'}
                        </td>
                        <td>
                          {index &&
                          validLoadPoint(point) &&
                          validLoadPoint(basePoint)
                            ? signedChange(
                                point?.error_pct,
                                basePoint?.error_pct,
                                'pp',
                              )
                            : '—'}
                        </td>
                        <td>
                          {point
                            ? `${point.transactions} / ${point.run_count}`
                            : 'No samples'}
                        </td>
                        <td>
                          {point && validLoadPoint(point)
                            ? point.observed_seconds === undefined
                              ? 'Measurement duration unavailable'
                              : `${displayNumber(point.observed_seconds)} ${point.duration_estimated ? 'estimated' : 'recorded'} seconds`
                            : point
                              ? `${point.errors} errors; matching attempt count unavailable`
                              : 'Not measured'}
                          {effectiveMode === 'release' && (
                            <small className="analysis-id">
                              {repeatSpread(item.release_id, level.id)}
                              <br />
                              Source runs:{' '}
                              {point?.run_ids?.join(', ') || 'Unavailable'}
                            </small>
                          )}
                          {point && point.transactions < 100 && (
                            <small className="analysis-id">
                              Limited samples
                            </small>
                          )}
                        </td>
                      </tr>
                    )
                  }),
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
      {exportError && <p role="alert">{exportError}</p>}
    </section>
  )
}
