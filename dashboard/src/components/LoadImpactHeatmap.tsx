import TabList from './ux/TabList'
import DialogLayer from './ux/DialogLayer'
import { handleTabKey } from './tabKeyboard'
import { forwardRef, lazy, Suspense, type CSSProperties, type MouseEventHandler, useMemo, useRef, useState } from 'react'
import LazyLoadBoundary from './LazyLoadBoundary'

import type { MetricsHistoryPoint } from '../types'
import { useModalDialog } from '../hooks/useModalDialog'
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

const LoadImpact3DHeatmap = lazy(() => import('./LoadImpact3DHeatmap'))

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

function telemetryIntervalCount(count: number) {
  return `${count} telemetry interval${count === 1 ? '' : 's'}`
}

function loadPointDescriptor(bucket: LoadImpactBucket) {
  return `${bucket.exact ? 'Exact load point' : 'Load range'} · ${telemetryIntervalCount(bucket.sampleCount)}`
}

const OPERATOR_METRIC_IDS = ['avgMs', 'p95', 'tps', 'errorPct', 'cpuProcess', 'memProcess']
const DETAIL_COLUMNS: Array<{ key: keyof MetricsHistoryPoint; label: string; unit?: string }> = [
  { key: 'avgMs', label: 'ART', unit: 'ms' },
  { key: 'tps', label: 'TPS' },
  { key: 'p95', label: 'P95', unit: 'ms' },
  { key: 'errorPct', label: 'Error %', unit: '%' },
  { key: 'cpuProcess', label: 'CPU', unit: '%' },
  { key: 'memProcess', label: 'Memory', unit: 'MB' },
]

function detailColumns(cell: LoadImpactCell) {
  return DETAIL_COLUMNS.filter(column => column.key !== cell.metric.key)
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
  const drawerRef = useRef<HTMLElement | null>(null)
  const drawerCloseButtonRef = useRef<HTMLButtonElement | null>(null)
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

  useModalDialog({
    dialogRef: drawerRef,
    enabled: drawerOpen,
    initialFocusRef: drawerCloseButtonRef,
    onClose: () => setDrawerOpen(false),
  })

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

  const useOperatorMetrics = () => {
    setSelectedMetricIds(availableMetricIds.filter(id => OPERATOR_METRIC_IDS.includes(id)))
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
          <small>{row.metric.category} · {telemetryIntervalCount(metricSampleCount(row))}{row.metric.unit ? ` · ${row.metric.unit}` : ''}</small>
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
            <TabList className="load-impact-view-toggle" role="tablist" aria-label="Load Impact view mode" onKeyDown={handleTabKey}>
              <button
                type="button"
                role="tab"
                aria-selected={viewMode === '2d'}
                className={`btn-chip ${viewMode === '2d' ? 'active' : ''}`}
                onClick={() => setViewMode('2d')} tabIndex={(viewMode === '2d') ? 0 : -1}
              >
                2D
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={viewMode === '3d'}
                className={`btn-chip ${viewMode === '3d' ? 'active' : ''}`}
                onClick={() => setViewMode('3d')} tabIndex={(viewMode === '3d') ? 0 : -1}
              >
                3D
              </button>
            </TabList>
          )}
          <div className="load-impact-badges">
            {loading && <span className="archive-chip">Loading</span>}
            {!loading && model.pointCount > 0 && <span className="archive-chip">{telemetryIntervalCount(model.pointCount)}</span>}
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
          <div className="load-impact-context">
            <div className="load-impact-scale-legend">
              <span className="load-impact-scale-swatch low" aria-hidden="true" />
              <span>Lower relative value</span>
              <span className="load-impact-scale-swatch high" aria-hidden="true" />
              <span>Higher relative value</span>
            </div>
            <p>Each metric uses its own observed range. Color and 3D height compare load points within that metric; they do not indicate an SLA breach or compare units.</p>
            <p>Cells show averages of telemetry intervals. Resource metrics describe the load generators.</p>
            {displayModel.buckets.length === 1 && <p className="load-impact-context-callout">One observed load level: load sensitivity cannot be assessed from this run.</p>}
          </div>
          {viewMode === '3d' ? (
            <LazyLoadBoundary title="The 3D heatmap could not be loaded." className="load-impact-state lazy-load-error">
              <Suspense fallback={<div className="load-impact-state" role="status">Loading 3D view…</div>}>
                <LoadImpact3DHeatmap
                  model={displayModel}
                  selectedCellKey={selectedCellKey}
                  onSelectedCellKeyChange={setSelectedCellKey}
                />
              </Suspense>
            </LazyLoadBoundary>
          ) : (
            <div className="load-impact-table-wrap">
              <table className="load-impact-table" data-table-pagination="off">
                <thead>
                  <tr>
            <th scope="col" aria-label="Sort by metric" aria-sort={ariaSort(METRIC_SORT_KEY)}>
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
                aria-label={bucket.label}
                          key={bucket.id}
                          scope="col"
                          aria-sort="none"
                          className={columnHighlighted ? 'load-impact-axis-highlighted' : undefined}
                          data-axis-active={columnHighlighted ? 'true' : undefined}
                        >
                          <span>
                            <span>{bucket.label}</span>
                            <small>{telemetryIntervalCount(bucket.sampleCount)}</small>
                          </span>
                        </th>
                      )
                    })}
                  </tr>
                </thead>
                <tbody>
                  {sortedRows.map(row => {
                    const rowHighlighted = highlightedAxis?.metricId === row.metric.id
                    const hasVariation = row.minAverage !== undefined && row.maxAverage !== undefined && row.minAverage !== row.maxAverage
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
                          const isConstant = cell.sampleCount > 0 && !hasVariation
                          const alpha = cell.sampleCount > 0 ? hasVariation ? 0.12 + cell.intensity * 0.68 : 0.1 : 0
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
                                className={`load-impact-cell${selected ? ' selected' : ''}${isConstant ? ' load-impact-cell-constant' : ''}`}
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
                                <span>{cell.sampleCount === 0 ? 'No telemetry intervals' : isConstant ? `No variation · ${telemetryIntervalCount(cell.sampleCount)}` : telemetryIntervalCount(cell.sampleCount)}</span>
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
                    Mean interval {selectedCell.metric.label} {formatLoadImpactValue(selectedCell.average, selectedCell.metric.unit)} · Min {formatLoadImpactValue(selectedCell.min, selectedCell.metric.unit)} · Max {formatLoadImpactValue(selectedCell.max, selectedCell.metric.unit)}
                  </p>
                </div>
                <span className="archive-chip">{telemetryIntervalCount(selectedCell.sampleCount)}</span>
              </div>
              <div className="load-impact-detail-table-wrap">
                <table className="load-impact-detail-table" data-table-pagination="off">
                  <thead>
                    <tr>
                      <th>Timestamp</th>
                      <th>VUsers</th>
                      <th>{selectedCell.metric.label}</th>
                      {detailColumns(selectedCell).map(column => <th key={column.key}>{column.label}</th>)}
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
                        {detailColumns(selectedCell).map(column => (
                          <td key={column.key}>{relatedValue(point.source, column.key, column.unit)}</td>
                        ))}
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
        <DialogLayer><div className="load-impact-graph-drawer-layer">
          <button
            type="button"
            className="load-impact-graph-backdrop"
            aria-label="Close heatmap configuration"
            onClick={() => setDrawerOpen(false)}
          />
          <aside ref={drawerRef} className="load-impact-graph-drawer" role="dialog" aria-modal="true" aria-label="Load impact heatmap configuration">
            <div className="load-impact-graph-drawer-header">
              <div>
                <h3>Heatmap Configuration</h3>
                <p>Choose which metrics and VUser load points are displayed in this heatmap.</p>
              </div>
              <button ref={drawerCloseButtonRef} type="button" className="btn btn-secondary btn-sm" onClick={() => setDrawerOpen(false)}>Close</button>
            </div>
            <div className="load-impact-graph-drawer-actions">
              <button type="button" className="btn btn-secondary btn-sm" onClick={resetConfiguration}>
                Restore defaults
              </button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={useOperatorMetrics}>
                Key operator metrics
              </button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={selectAllConfiguration}>
                Select all
              </button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={clearConfiguration}>
                Clear selection
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
        </div></DialogLayer>
      )}
    </section>
  )
})

export default LoadImpactHeatmap
