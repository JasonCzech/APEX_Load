import { useEffect, useMemo, useState } from 'react'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type { MetricsHistoryPoint } from '../types'
import {
  LOAD_IMPACT_METRICS,
  buildLoadImpactModel,
  formatLoadImpactValue,
} from './loadImpactModel'
import type { LoadImpactMetric, LoadImpactRow } from './loadImpactModel'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'

const DEFAULT_GRAPH_METRIC_IDS = ['avgMs', 'p95', 'tps', 'errorPct', 'cpuProcess', 'memProcess']
const GRAPH_COLORS = ['#2563eb', '#16a34a', '#f59e0b', '#dc2626', '#7c3aed', '#0891b2', '#db2777', '#4f46e5']
const GRAPH_AXIS_LABEL_STYLE = {
  fill: 'var(--text-muted)',
  fontSize: 11,
  fontWeight: 700,
}

interface Props {
  points: MetricsHistoryPoint[]
  title?: string
  subtitle?: string
  loading?: boolean
  error?: string
  emptyMessage?: string
}

interface GraphDatum {
  bucket: string
  average?: number
  min?: number
  max?: number
  samples: number
}

export function buildMetricGraphRows(points: MetricsHistoryPoint[], selectedMetricIds: string[]): LoadImpactRow[] {
  const selected = new Set(selectedMetricIds)
  return buildLoadImpactModel(points).rows.filter(row => selected.has(row.metric.id))
}

function metricSampleCount(row: LoadImpactRow) {
  return row.cells.reduce((sum, cell) => sum + cell.sampleCount, 0)
}

function graphDataForRow(row: LoadImpactRow): GraphDatum[] {
  return row.cells.map(cell => ({
    bucket: cell.bucket.label,
    average: cell.average,
    min: cell.min,
    max: cell.max,
    samples: cell.sampleCount,
  }))
}

function graphSummary(row: LoadImpactRow) {
  const populated = row.cells.filter(cell => cell.sampleCount > 0)
  if (populated.length === 0) return 'No samples'
  const values = populated
    .map(cell => cell.average)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
  if (values.length === 0) return `${metricSampleCount(row)} samples`
  const peak = Math.max(...values)
  return `${metricSampleCount(row)} samples · peak ${formatLoadImpactValue(peak, row.metric.unit)}`
}

function formatTooltipValue(value: unknown, metric: LoadImpactMetric) {
  return formatLoadImpactValue(typeof value === 'number' ? value : undefined, metric.unit)
}

function graphAxisLabel(value: string, position: 'bottom' | 'left') {
  if (position === 'bottom') {
    return {
      value,
      position: 'insideBottom',
      offset: -18,
      style: GRAPH_AXIS_LABEL_STYLE,
    }
  }
  return {
    value,
    angle: -90,
    position: 'left',
    offset: 0,
    style: { ...GRAPH_AXIS_LABEL_STYLE, textAnchor: 'middle' },
  }
}

function metricAxisLabel(metric: LoadImpactMetric) {
  return metric.unit ? `${metric.label} (${metric.unit})` : metric.label
}

function LoadMetricChart({ row, color }: { row: LoadImpactRow; color: string }) {
  const data = useMemo(() => graphDataForRow(row), [row])

  return (
    <div className="load-impact-graph-card">
      <div className="load-impact-graph-card-header">
        <div>
          <h4>{row.metric.label} Under Load</h4>
          <p>{graphSummary(row)}</p>
        </div>
        {row.metric.unit && <span className="archive-chip">{row.metric.unit}</span>}
      </div>
      <div className="load-impact-graph-canvas" data-testid={`load-impact-graph-${row.metric.id}`}>
        <ResponsiveContainer width="100%" height={220}>
          <LineChart data={data} margin={{ top: 10, right: 16, bottom: 8, left: 24 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis
              dataKey="bucket"
              stroke="var(--text-muted)"
              fontSize={11}
              height={46}
              tickMargin={8}
              interval={0}
              minTickGap={8}
              label={graphAxisLabel('VUser load', 'bottom')}
            />
            <YAxis
              stroke="var(--text-muted)"
              fontSize={11}
              width={92}
              tickFormatter={(value) => formatTooltipValue(value, row.metric)}
              label={graphAxisLabel(metricAxisLabel(row.metric), 'left')}
            />
            <Tooltip
              contentStyle={chartTooltipContentStyle}
              labelStyle={chartTooltipLabelStyle}
              labelFormatter={(label) => `Load: ${label}`}
              formatter={(value, name, item) => {
                const payload = (item as { payload?: GraphDatum } | undefined)?.payload
                const details = payload
                  ? `Average · ${payload.samples} sample${payload.samples === 1 ? '' : 's'}`
                  : String(name)
                return [formatTooltipValue(value, row.metric), details]
              }}
            />
            <Line
              type="monotone"
              dataKey="average"
              name={row.metric.label}
              stroke={color}
              strokeWidth={2.5}
              dot={{ r: 3 }}
              activeDot={{ r: 5 }}
              connectNulls
              isAnimationActive={false}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}

export default function LoadImpactMetricGraphs({
  points,
  title = 'Metrics Under Load',
  subtitle = 'Selected performance and resource metrics plotted by active VUser load.',
  loading = false,
  error = '',
  emptyMessage = 'Load graphs appear when archive samples include active VUser data.',
}: Props) {
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [selectedMetricIds, setSelectedMetricIds] = useState<string[]>(DEFAULT_GRAPH_METRIC_IDS)
  const model = useMemo(() => buildLoadImpactModel(points), [points])
  const availableRows = model.rows
  const selectedRows = useMemo(
    () => availableRows.filter(row => selectedMetricIds.includes(row.metric.id)),
    [availableRows, selectedMetricIds],
  )
  const availableMetricIds = new Set(availableRows.map(row => row.metric.id))
  const defaultAvailableMetricIds = DEFAULT_GRAPH_METRIC_IDS.filter(id => availableMetricIds.has(id))

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

  const toggleMetric = (metricId: string) => {
    setSelectedMetricIds(prev => (
      prev.includes(metricId)
        ? prev.filter(id => id !== metricId)
        : [...prev, metricId]
    ))
  }

  const renderMetricOption = (row: LoadImpactRow) => {
    const checked = selectedMetricIds.includes(row.metric.id)
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

  return (
    <section className="load-impact-card load-impact-graphs" aria-label={title}>
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
            disabled={loading || availableRows.length === 0}
            aria-expanded={drawerOpen}
          >
            Configure Graphs
          </button>
          <div className="load-impact-badges">
            {loading && <span className="archive-chip">Loading</span>}
            {!loading && selectedRows.length > 0 && <span className="archive-chip">{selectedRows.length} graph{selectedRows.length === 1 ? '' : 's'}</span>}
            {!loading && availableRows.length > 0 && <span className="archive-chip">{availableRows.length} metrics available</span>}
          </div>
        </div>
      </div>

      {error && <p className="scenario-script-status error">{error}</p>}

      {loading ? (
        <div className="load-impact-state" role="status" aria-live="polite">
          Loading load metric graphs...
        </div>
      ) : model.rows.length === 0 || model.buckets.length === 0 ? (
        <div className="load-impact-state">{emptyMessage}</div>
      ) : selectedRows.length === 0 ? (
        <div className="load-impact-state">
          No graphs selected. Open Configure Graphs to add metrics.
        </div>
      ) : (
        <div className="load-impact-graphs-grid">
          {selectedRows.map((row, index) => (
            <LoadMetricChart
              key={row.metric.id}
              row={row}
              color={GRAPH_COLORS[index % GRAPH_COLORS.length]}
            />
          ))}
        </div>
      )}

      {drawerOpen && (
        <div className="load-impact-graph-drawer-layer">
          <button
            type="button"
            className="load-impact-graph-backdrop"
            aria-label="Close graph configuration"
            onClick={() => setDrawerOpen(false)}
          />
          <aside className="load-impact-graph-drawer" role="dialog" aria-modal="true" aria-label="Load metric graph selector">
            <div className="load-impact-graph-drawer-header">
              <div>
                <h3>Graph Selection</h3>
                <p>Add or remove report graphs by metric.</p>
              </div>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setDrawerOpen(false)}>Close</button>
            </div>
            <div className="load-impact-graph-drawer-actions">
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={() => setSelectedMetricIds(defaultAvailableMetricIds)}
              >
                Reset
              </button>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={() => setSelectedMetricIds(availableRows.map(row => row.metric.id))}
              >
                Add All
              </button>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => setSelectedMetricIds([])}
              >
                Remove All
              </button>
            </div>
            <div className="load-impact-graph-option-list">
              <span className="results-compare-label">Performance</span>
              {availableRows.filter(row => row.metric.category === 'performance').map(renderMetricOption)}
              <span className="results-compare-label">Resources</span>
              {availableRows.filter(row => row.metric.category === 'resource').map(renderMetricOption)}
              {LOAD_IMPACT_METRICS.filter(metric => !availableMetricIds.has(metric.id)).length > 0 && (
                <p className="load-impact-graph-drawer-note">
                  Metrics without archive samples are hidden from this selector.
                </p>
              )}
            </div>
          </aside>
        </div>
      )}
    </section>
  )
}
