import { forwardRef, type CSSProperties, type MouseEventHandler, useEffect, useMemo, useState } from 'react'

import type { MetricsHistoryPoint } from '../types'
import LoadImpact3DHeatmap from './LoadImpact3DHeatmap'
import {
  LOAD_IMPACT_METRICS,
  buildLoadImpactModel,
  filterLoadImpactModel,
  formatLoadImpactCellTitle,
  formatLoadImpactValue,
  loadImpactCellKey,
} from './loadImpactModel'
import type { LoadImpactBucket, LoadImpactCell, LoadImpactRow } from './loadImpactModel'
import { METRIC_SORT_KEY, sortLoadImpactRows, type LoadImpactSortState } from './loadImpactHeatmapUtils'
import './LoadImpactHeatmap.css'

interface Props {
  points: MetricsHistoryPoint[]
  title?: string
  subtitle?: string
  loading?: boolean
  error?: string
  emptyMessage?: string
  variant?: 'standalone' | 'graph-card'
  className?: string
  dataChartId?: string
  onContextMenu?: MouseEventHandler<HTMLElement>
  onMouseEnter?: MouseEventHandler<HTMLElement>
  onMouseLeave?: MouseEventHandler<HTMLElement>
}

function formatPointTimestamp(point: MetricsHistoryPoint) {
  if (typeof point.timestampMs === 'number') {
    return new Date(point.timestampMs).toLocaleString()
  }
  return point.time || 'n/a'
}

function relatedValue(point: MetricsHistoryPoint, key: keyof MetricsHistoryPoint, unit?: string) {
  const value = point[key]
  return typeof value === 'number' && Number.isFinite(value) ? formatLoadImpactValue(value, unit) : 'n/a'
}

function metricSampleCount(row: LoadImpactRow) {
  return row.cells.reduce((sum, cell) => sum + cell.sampleCount, 0)
}

function loadPointDescriptor(bucket: LoadImpactBucket) {
  return `${bucket.exact ? 'Exact load point' : 'Load range'} · ${bucket.sampleCount} total sample${bucket.sampleCount === 1 ? '' : 's'}`
}

function bucketSortKey(bucketId: string) {
  return `bucket:${bucketId}`
}

const LoadImpactHeatmap = forwardRef<HTMLElement, Props>(function LoadImpactHeatmap({
  points,
  title = 'Load Impact',
  subtitle = 'Performance and resource behavior grouped by active VUser load.',
  loading = false,
  error = '',
  emptyMessage = 'Load impact appears after metrics include active VUsers and performance or resource samples.',
  variant = 'standalone',
  className = '',
  dataChartId,
  onContextMenu,
  onMouseEnter,
  onMouseLeave,
}, ref) {
  const [selectedCellKey, setSelectedCellKey] = useState<string | null>(null)
  const [viewMode, setViewMode] = useState<'2d' | '3d'>('2d')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [selectedMetricIds, setSelectedMetricIds] = useState<string[] | null>(null)
  const [selectedBucketIds, setSelectedBucketIds] = useState<string[] | null>(null)
  const [sortState, setSortState] = useState<LoadImpactSortState | null>(null)
  const [highlightedAxis, setHighlightedAxis] = useState<{ metricId: string; bucketId: string } | null>(null)
  const model = useMemo(() => buildLoadImpactModel(points), [points])
  const availableMetricIds = useMemo(() => model.rows.map(row => row.metric.id), [model.rows])
  const availableBucketIds = useMemo(() => model.buckets.map(bucket => bucket.id), [model.buckets])
  const effectiveMetricIds = selectedMetricIds ?? availableMetricIds
  const effectiveBucketIds = selectedBucketIds ?? availableBucketIds
  const displayModel = useMemo(
    () => filterLoadImpactModel(model, effectiveMetricIds, effectiveBucketIds),
    [model, effectiveMetricIds, effectiveBucketIds],
  )
  const sortedRows = useMemo(
    () => sortLoadImpactRows(displayModel.rows, sortState),
    [displayModel.rows, sortState],
  )
  const selectedCell = useMemo(() => {
    if (!selectedCellKey) return null
    for (const row of displayModel.rows) {
      const cell = row.cells.find(candidate => loadImpactCellKey(candidate) === selectedCellKey)
      if (cell) return cell
    }
    return null
  }, [displayModel.rows, selectedCellKey])
  const availableMetricIdSet = useMemo(() => new Set(availableMetricIds), [availableMetricIds])
  const unavailableMetricCount = LOAD_IMPACT_METRICS.filter(metric => !availableMetricIdSet.has(metric.id)).length

  useEffect(() => {
    if (!drawerOpen) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setDrawerOpen(false)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [drawerOpen])

  const resetConfiguration = () => {
    setSelectedMetricIds(null)
    setSelectedBucketIds(null)
    setSelectedCellKey(null)
  }

  const selectAllConfiguration = () => {
    setSelectedMetricIds(availableMetricIds)
    setSelectedBucketIds(availableBucketIds)
  }

  const clearConfiguration = () => {
    setSelectedMetricIds([])
    setSelectedBucketIds([])
    setSelectedCellKey(null)
  }

  const toggleSort = (key: string) => {
    setSortState(prev => prev?.key === key
      ? { key, direction: prev.direction === 'asc' ? 'desc' : 'asc' }
      : { key, direction: key === METRIC_SORT_KEY ? 'asc' : 'desc' })
  }

  const sortIndicator = (key: string) => {
    if (sortState?.key !== key) return '↕'
    return sortState.direction === 'asc' ? '↑' : '↓'
  }

  const ariaSort = (key: string): 'ascending' | 'descending' | 'none' => {
    if (sortState?.key !== key) return 'none'
    return sortState.direction === 'asc' ? 'ascending' : 'descending'
  }

  const toggleMetric = (metricId: string) => {
    setSelectedMetricIds(prev => {
      const current = prev ?? availableMetricIds
      return current.includes(metricId)
        ? current.filter(id => id !== metricId)
        : [...current, metricId]
    })
    setSelectedCellKey(null)
  }

  const toggleBucket = (bucketId: string) => {
    setSelectedBucketIds(prev => {
      const current = prev ?? availableBucketIds
      return current.includes(bucketId)
        ? current.filter(id => id !== bucketId)
        : [...current, bucketId]
    })
    setSelectedCellKey(null)
  }

  const highlightCellAxis = (cell: LoadImpactCell) => {
    setHighlightedAxis({ metricId: cell.metric.id, bucketId: cell.bucket.id })
  }

  const clearHighlightedAxis = () => {
    setHighlightedAxis(null)
  }

  const renderMetricOption = (row: LoadImpactRow) => {
    const checked = effectiveMetricIds.includes(row.metric.id)
    return (
      <label key={row.metric.id} className="load-impact-graph-option">
        <input
          type="checkbox"
          value={row.metric.id}
          checked={checked}
          onChange={() => toggleMetric(row.metric.id)}
        />
        <span>
          <strong>{row.metric.label}</strong>
          <small>{row.metric.category} · {metricSampleCount(row)} samples{row.metric.unit ? ` · ${row.metric.unit}` : ''}</small>
        </span>
      </label>
    )
  }

  const renderBucketOption = (bucket: LoadImpactBucket) => {
    const checked = effectiveBucketIds.includes(bucket.id)
    return (
      <label key={bucket.id} className="load-impact-graph-option">
        <input
          type="checkbox"
          value={bucket.id}
          checked={checked}
          onChange={() => toggleBucket(bucket.id)}
        />
        <span>
          <strong>{bucket.label}</strong>
          <small>{loadPointDescriptor(bucket)}</small>
        </span>
      </label>
    )
  }

  const rootClassName = [
    'load-impact-card',
    variant === 'graph-card' ? 'load-impact-card-graph' : '',
    className,
  ].filter(Boolean).join(' ')

  return (
    <section
      ref={ref}
      className={rootClassName}
      data-chart-id={dataChartId}
      aria-label={title}
      onContextMenu={onContextMenu}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
    >
      <div className="load-impact-header">
        <div>
          <h3 className="chart-title">{title}</h3>
          <p className="load-impact-copy">{subtitle}</p>
        </div>
        <div className="load-impact-header-actions">
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={() => setDrawerOpen(true)}
            disabled={loading || model.rows.length === 0 || model.buckets.length === 0}
            aria-expanded={drawerOpen}
          >
            Configure Map
          </button>
          {!loading && model.rows.length > 0 && model.buckets.length > 0 && (
            <div className="load-impact-view-toggle" role="tablist" aria-label="Load Impact view mode">
              <button
                type="button"
                role="tab"
                aria-selected={viewMode === '2d'}
                className={`btn-chip ${viewMode === '2d' ? 'active' : ''}`}
                onClick={() => setViewMode('2d')}
              >
                2D
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={viewMode === '3d'}
                className={`btn-chip ${viewMode === '3d' ? 'active' : ''}`}
                onClick={() => setViewMode('3d')}
              >
                3D
              </button>
            </div>
          )}
          <div className="load-impact-badges">
            {loading && <span className="archive-chip">Loading</span>}
            {!loading && model.pointCount > 0 && <span className="archive-chip">{model.pointCount} samples</span>}
            {!loading && model.rows.length > 0 && <span className="archive-chip">{displayModel.rows.length}/{model.rows.length} metrics</span>}
            {!loading && model.buckets.length > 0 && <span className="archive-chip">{displayModel.buckets.length}/{model.buckets.length} loads</span>}
          </div>
        </div>
      </div>

      {error && <p className="scenario-script-status error">{error}</p>}

      {loading ? (
        <div className="load-impact-state" role="status" aria-live="polite">
          Loading load impact samples...
        </div>
      ) : model.rows.length === 0 || model.buckets.length === 0 ? (
        <div className="load-impact-state">
          {emptyMessage}
        </div>
      ) : displayModel.rows.length === 0 || displayModel.buckets.length === 0 ? (
        <div className="load-impact-state">
          No heatmap cells selected. Open Configure Map to add metrics and VUser load points.
        </div>
      ) : (
        <>
          {viewMode === '3d' ? (
            <LoadImpact3DHeatmap
              model={displayModel}
              selectedCellKey={selectedCellKey}
              onSelectedCellKeyChange={setSelectedCellKey}
            />
          ) : (
            <div className="load-impact-table-wrap">
              <table className="load-impact-table">
                <thead>
                  <tr>
                    <th scope="col" aria-sort={ariaSort(METRIC_SORT_KEY)}>
                      <button
                        type="button"
                        className="load-impact-sort-header"
                        onClick={() => toggleSort(METRIC_SORT_KEY)}
                      >
                        <span>Metric</span>
                        <span className="load-impact-sort-icon" aria-hidden="true">{sortIndicator(METRIC_SORT_KEY)}</span>
                      </button>
                    </th>
                    {displayModel.buckets.map(bucket => {
                      const columnHighlighted = highlightedAxis?.bucketId === bucket.id
                      return (
                        <th
                          key={bucket.id}
                          scope="col"
                          aria-sort={ariaSort(bucketSortKey(bucket.id))}
                          className={columnHighlighted ? 'load-impact-axis-highlighted' : undefined}
                          data-axis-active={columnHighlighted ? 'true' : undefined}
                        >
                          <button
                            type="button"
                            className="load-impact-sort-header"
                            onClick={() => toggleSort(bucketSortKey(bucket.id))}
                          >
                            <span>
                              <span>{bucket.label}</span>
                              <small>{bucket.sampleCount} total</small>
                            </span>
                            <span className="load-impact-sort-icon" aria-hidden="true">{sortIndicator(bucketSortKey(bucket.id))}</span>
                          </button>
                        </th>
                      )
                    })}
                  </tr>
                </thead>
                <tbody>
                  {sortedRows.map(row => {
                    const rowHighlighted = highlightedAxis?.metricId === row.metric.id
                    return (
                      <tr key={row.metric.id} className={rowHighlighted ? 'load-impact-row-highlighted' : undefined}>
                        <th
                          scope="row"
                          className={rowHighlighted ? 'load-impact-axis-highlighted' : undefined}
                          data-axis-active={rowHighlighted ? 'true' : undefined}
                        >
                          <span>{row.metric.label}</span>
                          {row.metric.unit && <small>{row.metric.unit}</small>}
                        </th>
                        {row.cells.map(cell => {
                          const selected = selectedCellKey === loadImpactCellKey(cell)
                          const alpha = cell.sampleCount > 0 ? 0.12 + cell.intensity * 0.68 : 0
                          const title = formatLoadImpactCellTitle(cell)
                          return (
                            <td
                              key={cell.bucket.id}
                              className={[
                                highlightedAxis?.metricId === cell.metric.id ? 'load-impact-cell-row-context' : '',
                                highlightedAxis?.bucketId === cell.bucket.id ? 'load-impact-cell-column-context' : '',
                              ].filter(Boolean).join(' ') || undefined}
                              onMouseEnter={() => highlightCellAxis(cell)}
                              onMouseLeave={clearHighlightedAxis}
                            >
                              <button
                                type="button"
                                className={`load-impact-cell${selected ? ' selected' : ''}`}
                                disabled={cell.sampleCount === 0}
                                title={title}
                                aria-label={`${cell.metric.label} at ${cell.bucket.label}: ${title}`}
                                onClick={() => setSelectedCellKey(loadImpactCellKey(cell))}
                                onFocus={() => highlightCellAxis(cell)}
                                onBlur={clearHighlightedAxis}
                                style={{
                                  '--load-impact-alpha': alpha,
                                } as CSSProperties}
                              >
                                <strong>{formatLoadImpactValue(cell.average, cell.metric.unit)}</strong>
                                <span>{cell.sampleCount}</span>
                              </button>
                            </td>
                          )
                        })}
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}

          {selectedCell && selectedCell.sampleCount > 0 && (
            <div className="load-impact-detail">
              <div className="load-impact-detail-header">
                <div>
                  <h4>{selectedCell.metric.label} at {selectedCell.bucket.label}</h4>
                  <p>
                    Avg {formatLoadImpactValue(selectedCell.average, selectedCell.metric.unit)} · Min {formatLoadImpactValue(selectedCell.min, selectedCell.metric.unit)} · Max {formatLoadImpactValue(selectedCell.max, selectedCell.metric.unit)}
                  </p>
                </div>
                <span className="archive-chip">{selectedCell.sampleCount} data points</span>
              </div>
              <div className="load-impact-detail-table-wrap">
                <table className="load-impact-detail-table">
                  <thead>
                    <tr>
                      <th>Timestamp</th>
                      <th>VUsers</th>
                      <th>{selectedCell.metric.label}</th>
                      <th>ART</th>
                      <th>TPS</th>
                      <th>P95</th>
                      <th>Error %</th>
                      <th>CPU</th>
                      <th>Memory</th>
                      <th>Network</th>
                      <th>Disk</th>
                    </tr>
                  </thead>
                  <tbody>
                    {selectedCell.points.map((point, index) => (
                      <tr key={`${point.timestampMs ?? point.time}-${index}`}>
                        <td className="tx-name">{formatPointTimestamp(point.source)}</td>
                        <td>{point.vusers}</td>
                        <td>{formatLoadImpactValue(point.value, selectedCell.metric.unit)}</td>
                        <td>{relatedValue(point.source, 'avgMs', 'ms')}</td>
                        <td>{relatedValue(point.source, 'tps')}</td>
                        <td>{relatedValue(point.source, 'p95', 'ms')}</td>
                        <td>{relatedValue(point.source, 'errorPct', '%')}</td>
                        <td>{relatedValue(point.source, 'cpuProcess', '%')}</td>
                        <td>{relatedValue(point.source, 'memProcess', 'MB')}</td>
                        <td>{relatedValue(point.source, 'netSent', 'KB/s')} / {relatedValue(point.source, 'netRecv', 'KB/s')}</td>
                        <td>{relatedValue(point.source, 'diskRead', 'KB/s')} / {relatedValue(point.source, 'diskWrite', 'KB/s')}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}

      {drawerOpen && (
        <div className="load-impact-graph-drawer-layer">
          <button
            type="button"
            className="load-impact-graph-backdrop"
            aria-label="Close heatmap configuration"
            onClick={() => setDrawerOpen(false)}
          />
          <aside className="load-impact-graph-drawer" role="dialog" aria-modal="true" aria-label="Load impact heatmap configuration">
            <div className="load-impact-graph-drawer-header">
              <div>
                <h3>Heatmap Configuration</h3>
                <p>Choose which metrics and VUser load points are displayed in this heatmap.</p>
              </div>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setDrawerOpen(false)}>Close</button>
            </div>
            <div className="load-impact-graph-drawer-actions">
              <button type="button" className="btn btn-secondary btn-sm" onClick={resetConfiguration}>
                Reset
              </button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={selectAllConfiguration}>
                Add All
              </button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={clearConfiguration}>
                Remove All
              </button>
            </div>
            <div className="load-impact-graph-option-list">
              <p className="load-impact-graph-drawer-note">
                Showing {displayModel.rows.length} of {model.rows.length} metrics across {displayModel.buckets.length} of {model.buckets.length} VUser load points.
              </p>
              <span className="results-compare-label">Performance Metrics</span>
              {model.rows.filter(row => row.metric.category === 'performance').map(renderMetricOption)}
              <span className="results-compare-label">Resource Metrics</span>
              {model.rows.filter(row => row.metric.category === 'resource').map(renderMetricOption)}
              <span className="results-compare-label">VUser Load Points</span>
              {model.buckets.map(renderBucketOption)}
              {unavailableMetricCount > 0 && (
                <p className="load-impact-graph-drawer-note">
                  {unavailableMetricCount} metric{unavailableMetricCount === 1 ? '' : 's'} without samples are hidden from this selector.
                </p>
              )}
            </div>
          </aside>
        </div>
      )}
    </section>
  )
})

export default LoadImpactHeatmap
