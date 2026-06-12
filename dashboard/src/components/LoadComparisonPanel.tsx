import { useCallback, useEffect, useMemo, useState } from 'react'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type { LoadComparisonOption, LoadComparisonRequestOptions, LoadComparisonResponse, LoadComparisonScope } from '../types'
import { chartTooltipContentStyle } from './chartTooltipTheme'

export interface LoadComparisonTestOption {
  id: string
  name: string
  releaseId?: string
  releaseName?: string
  completedAt?: string
  vusers?: number
  transactionCount?: number
}

interface LoadComparisonPanelProps {
  applicationId?: string
  getLoadComparison: (applicationId: string, scope?: LoadComparisonScope, key?: string, options?: LoadComparisonRequestOptions) => Promise<LoadComparisonResponse>
  testOptions?: LoadComparisonTestOption[]
  selectedTestIds?: string[]
}

type LoadComparisonMetric = 'tps' | 'avg_response_ms'
type ChartRow = Record<string, string | number | undefined>
type LoadComparisonSummaryCard = {
  label: string
  value: string
}

const releaseSeriesColors = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--accent)',
  'var(--warning)',
  'var(--success)',
  'var(--danger)',
]

function formatNumber(value: number | undefined, digits = 1): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return value.toLocaleString(undefined, { maximumFractionDigits: digits, minimumFractionDigits: digits })
}

function formatCompactCount(value: number | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '0'
  return value.toLocaleString()
}

function formatMetricValue(value: number, metric: LoadComparisonMetric): string {
  return metric === 'tps' ? `${formatNumber(value, 1)} TPS` : `${formatNumber(value, 0)} ms`
}

function optionMeta(option: LoadComparisonOption | undefined): string {
  if (!option) return ''
  const releaseLabel = `${option.release_count} release${option.release_count === 1 ? '' : 's'}`
  const runLabel = `${option.run_count} run${option.run_count === 1 ? '' : 's'}`
  const txLabel = `${formatCompactCount(option.transaction_count)} tx`
  return [releaseLabel, runLabel, txLabel].join(' · ')
}

function scopeLabel(scope: LoadComparisonScope): string {
  return scope === 'group' ? 'Groups' : 'Transactions'
}

function emptyLoadComparison(applicationId: string, scope: LoadComparisonScope, selectedKey: string): LoadComparisonResponse {
  return {
    application_id: applicationId,
    scope,
    selected_key: selectedKey || undefined,
    options: [],
    load_levels: [],
    releases: [],
  }
}

function chartRows(comparison: LoadComparisonResponse, metric: LoadComparisonMetric): ChartRow[] {
  return comparison.load_levels.map(level => {
    const row: ChartRow = { load: level.label }
    comparison.releases.forEach((release, index) => {
      const point = release.points.find(candidate => candidate.load_level_id === level.id)
      row[`release_${index}`] = point ? point[metric] : undefined
    })
    return row
  })
}

function uniqueRunCount(comparison: LoadComparisonResponse): number {
  const byReleaseRuns = comparison.releases.reduce((sum, release) => sum + release.run_count, 0)
  if (byReleaseRuns > 0) return byReleaseRuns
  return comparison.releases.reduce((sum, release) => (
    sum + Math.max(0, ...release.points.map(point => point.run_count || 0))
  ), 0)
}

function weightedAverageResponseMs(comparison: LoadComparisonResponse): number | undefined {
  let total = 0
  let weight = 0
  comparison.releases.forEach(release => {
    release.points.forEach(point => {
      if (Number.isFinite(point.avg_response_ms) && point.transactions > 0) {
        total += point.avg_response_ms * point.transactions
        weight += point.transactions
      }
    })
  })
  return weight > 0 ? total / weight : undefined
}

function chartSummaryCards(comparison: LoadComparisonResponse): LoadComparisonSummaryCard[] {
  const points = comparison.releases.flatMap(release => release.points)
  const transactionCount = points.reduce((sum, point) => sum + (Number.isFinite(point.transactions) ? point.transactions : 0), 0)
  const peakTPS = points.reduce((max, point) => Math.max(max, Number.isFinite(point.tps) ? point.tps : 0), 0)
  const errors = points.reduce((sum, point) => sum + (Number.isFinite(point.errors) ? point.errors : 0), 0)
  const errorPct = transactionCount > 0 ? (errors / transactionCount) * 100 : 0
  const avgART = weightedAverageResponseMs(comparison)

  return [
    { label: 'Graph Releases', value: formatCompactCount(comparison.releases.length) },
    { label: 'Graph Runs', value: formatCompactCount(uniqueRunCount(comparison)) },
    { label: 'Load Levels', value: formatCompactCount(comparison.load_levels.length) },
    { label: 'Transactions', value: formatCompactCount(transactionCount) },
    { label: 'Peak TPS', value: formatNumber(peakTPS, 1) },
    { label: 'Avg ART', value: `${formatNumber(avgART, 0)} ms` },
    { label: 'Errors', value: `${formatCompactCount(errors)} (${formatNumber(errorPct, 2)}%)` },
  ]
}

function LoadComparisonChart({ comparison, metric, title }: { comparison: LoadComparisonResponse; metric: LoadComparisonMetric; title: string }) {
  const rows = useMemo(() => chartRows(comparison, metric), [comparison, metric])
  const unit = metric === 'tps' ? 'TPS' : 'ms'

  return (
    <div className="load-comparison-chart" data-testid={`load-comparison-chart-${metric}`}>
      <div className="load-comparison-chart-header">
        <h4>{title}</h4>
        <span>{unit}</span>
      </div>
      <ResponsiveContainer width="100%" height={220}>
        <LineChart data={rows} margin={{ top: 8, right: 18, left: 4, bottom: 8 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
          <XAxis dataKey="load" stroke="var(--text-muted)" fontSize={11} tickMargin={8} />
          <YAxis stroke="var(--text-muted)" fontSize={11} width={52} tickFormatter={value => formatNumber(Number(value), metric === 'tps' ? 1 : 0)} />
          <Tooltip
            contentStyle={{ ...chartTooltipContentStyle, borderRadius: 8 }}
            formatter={(value, name) => [formatMetricValue(Number(value), metric), name]}
            labelFormatter={value => `Load ${value}`}
            isAnimationActive={false}
          />
          <Legend verticalAlign="bottom" height={34} wrapperStyle={{ fontSize: 11 }} />
          {comparison.releases.map((release, index) => (
            <Line
              key={release.release_id || index}
              type="monotone"
              dataKey={`release_${index}`}
              name={release.release_name || release.release_id}
              stroke={releaseSeriesColors[index % releaseSeriesColors.length]}
              strokeWidth={2}
              dot={{ r: 2 }}
              activeDot={{ r: 5 }}
              connectNulls
              isAnimationActive={false}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}

export function LoadComparisonPanel({ applicationId, getLoadComparison, testOptions = [], selectedTestIds }: LoadComparisonPanelProps) {
  const [scope, setScope] = useState<LoadComparisonScope>('transaction')
  const [selectedKeys, setSelectedKeys] = useState<Record<LoadComparisonScope, string>>({ transaction: '', group: '' })
  const [comparison, setComparison] = useState<LoadComparisonResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const requestedKey = selectedKeys[scope]
  const hasSelectedTestIdsProp = selectedTestIds !== undefined
  const selectedTestIdsKey = useMemo(() => selectedTestIds?.join('\u001f') ?? '', [selectedTestIds])
  const consideredTestIdList = useMemo(() => {
    if (testOptions.length === 0) return undefined
    const validIds = new Set(testOptions.map(option => option.id).filter(Boolean))
    if (!hasSelectedTestIdsProp) {
      return Array.from(validIds)
    }
    if (!selectedTestIdsKey) return []
    return selectedTestIdsKey.split('\u001f').filter(id => validIds.has(id))
  }, [hasSelectedTestIdsProp, selectedTestIdsKey, testOptions])
  const consideredTestIdsKey = consideredTestIdList?.join('\u001f') ?? ''
  const hasRunSelectionControls = testOptions.length > 0
  const noSelectedRuns = hasRunSelectionControls && consideredTestIdList?.length === 0

  const load = useCallback(async () => {
    if (!applicationId) {
      setComparison(null)
      return
    }
    if (noSelectedRuns) {
      setComparison(emptyLoadComparison(applicationId, scope, requestedKey))
      setLoading(false)
      setError('')
      return
    }
    setLoading(true)
    setError('')
    try {
      const testIds = hasRunSelectionControls ? consideredTestIdsKey.split('\u001f').filter(Boolean) : undefined
      const next = testIds
        ? await getLoadComparison(applicationId, scope, requestedKey, { testIds })
        : await getLoadComparison(applicationId, scope, requestedKey)
      setComparison(next)
    } catch (err) {
      setComparison(null)
      setError(err instanceof Error ? err.message : 'Failed to load comparison.')
    } finally {
      setLoading(false)
    }
  }, [applicationId, consideredTestIdsKey, getLoadComparison, hasRunSelectionControls, noSelectedRuns, requestedKey, scope])

  useEffect(() => {
    let cancelled = false
    const run = async () => {
      if (!applicationId) {
        setComparison(null)
        return
      }
      if (noSelectedRuns) {
        setComparison(emptyLoadComparison(applicationId, scope, requestedKey))
        setLoading(false)
        setError('')
        return
      }
      setLoading(true)
      setError('')
      try {
        const testIds = hasRunSelectionControls ? consideredTestIdsKey.split('\u001f').filter(Boolean) : undefined
        const next = testIds
          ? await getLoadComparison(applicationId, scope, requestedKey, { testIds })
          : await getLoadComparison(applicationId, scope, requestedKey)
        if (!cancelled) setComparison(next)
      } catch (err) {
        if (!cancelled) {
          setComparison(null)
          setError(err instanceof Error ? err.message : 'Failed to load comparison.')
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void run()
    return () => {
      cancelled = true
    }
  }, [applicationId, consideredTestIdsKey, getLoadComparison, hasRunSelectionControls, noSelectedRuns, requestedKey, scope])

  const selectedValue = requestedKey && comparison?.options.some(option => option.key === requestedKey)
    ? requestedKey
    : comparison?.selected_key || ''
  const selectedOption = comparison?.options.find(option => option.key === selectedValue)
  const hasOptions = Boolean(comparison && comparison.options.length > 0)
  const hasChartData = Boolean(comparison && comparison.load_levels.length > 0 && comparison.releases.some(release => release.points.length > 0))
  const summaryCards = comparison && hasChartData ? chartSummaryCards(comparison) : []

  const setSelectedScope = (nextScope: LoadComparisonScope) => {
    setScope(nextScope)
  }

  const setSelectedKey = (key: string) => {
    setSelectedKeys(current => ({ ...current, [scope]: key }))
  }

  const selectedRunCount = consideredTestIdList?.length ?? 0
  const selectedRunCountLabel = `${selectedRunCount} of ${testOptions.length} runs selected`

  return (
    <section className="load-comparison-panel" data-testid="load-comparison-panel">
      <div className="load-comparison-header">
        <div>
          <h3 className="dash-section-title">Load Comparison</h3>
          <p>TPS and ART by release at matching VUser load levels.</p>
        </div>
        {comparison && (
          <span className="dash-context-chip">
            {comparison.releases.length} release{comparison.releases.length === 1 ? '' : 's'}
          </span>
        )}
      </div>

      <div className="load-comparison-controls">
        <div className="time-range-selector load-comparison-scope-toggle" role="group" aria-label="Load comparison scope">
          {(['transaction', 'group'] as LoadComparisonScope[]).map(candidate => (
            <button
              key={candidate}
              className={`btn-chip ${scope === candidate ? 'active' : ''}`}
              type="button"
              onClick={() => setSelectedScope(candidate)}
              aria-pressed={scope === candidate}
            >
              {scopeLabel(candidate)}
            </button>
          ))}
        </div>
        <label className="load-comparison-select">
          <span>{scope === 'group' ? 'Group' : 'Transaction'}</span>
          <select
            value={selectedValue}
            onChange={event => setSelectedKey(event.currentTarget.value)}
            disabled={!hasOptions || loading}
            aria-label={`Select ${scope === 'group' ? 'group' : 'transaction'} for load comparison`}
          >
            {!hasOptions && <option value="">No {scopeLabel(scope).toLowerCase()}</option>}
            {comparison?.options.map(option => (
              <option key={option.key} value={option.key}>{option.label}</option>
            ))}
          </select>
        </label>
        {selectedOption && <span className="load-comparison-meta">{optionMeta(selectedOption)}</span>}
        {hasRunSelectionControls && <span className="load-comparison-run-count">{selectedRunCountLabel}</span>}
        {error && (
          <button type="button" className="btn btn-secondary btn-sm" onClick={load}>
            Retry
          </button>
        )}
      </div>

      {loading && !comparison ? (
        <div className="dash-empty small">
          <span>Loading load comparison...</span>
        </div>
      ) : noSelectedRuns ? (
        <div className="dash-empty small">
          <span>Select at least one archived run to compare</span>
        </div>
      ) : error ? (
        <div className="dash-empty small">
          <span>{error}</span>
        </div>
      ) : !comparison || !hasOptions ? (
        <div className="dash-empty small">
          <span>No archived {scopeLabel(scope).toLowerCase()} under load yet</span>
        </div>
      ) : !hasChartData ? (
        <div className="dash-empty small">
          <span>Selected {scope === 'group' ? 'group' : 'transaction'} has insufficient load-level data</span>
        </div>
      ) : (
        <>
          <div className="load-comparison-chart-grid">
            <LoadComparisonChart comparison={comparison} metric="tps" title="TPS Under Load" />
            <LoadComparisonChart comparison={comparison} metric="avg_response_ms" title="ART Under Load" />
          </div>
          <div className="load-comparison-summary-grid" aria-label="Load comparison graph summary">
            {summaryCards.map(card => (
              <div className="load-comparison-summary-card" key={card.label}>
                <span className="load-comparison-summary-value">{card.value}</span>
                <span className="load-comparison-summary-label">{card.label}</span>
              </div>
            ))}
          </div>
        </>
      )}
    </section>
  )
}
