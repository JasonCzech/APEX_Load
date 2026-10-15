import SeriesChartExport from "../SeriesChartExport"
import { useId, useMemo, useReducer, useState } from 'react'
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceArea,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import type { GoalConfig, MetricsHistoryPoint } from '../../types'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from '../chartTooltipTheme'
import {
  transactionChartSeriesKey,
  transactionChartSeriesNames,
  transactionChartSeriesPresentation,
} from '../transactionChartSeries'
import { buildBreachWindows } from './runReportModel'
import { timelineOverlayBands, type TimelineOverlay, type TimelineOverlayBand } from './timelineOverlays'
import { initialTimelineZoomState, timelineZoomReducer } from './timelineZoomModel'
import {
  DEFAULT_TIMELINE_GRAPHS,
  graphHasData,
  graphSupportsTransactions,
  MAX_TIMELINE_GRAPHS,
  MAX_TIMELINE_TRANSACTIONS,
  TIMELINE_GRAPHS,
  timelineMetricKeys,
  type TimelineGraph,
} from './timelineGraphModel'

/** An operator note placed on the run timeline. */
export interface TimelineAnnotation { id: string; label: string; timestamp: string }

// The axis is categorical (sample labels), so a note sits on its nearest sample.
function annotationMarkers(annotations: TimelineAnnotation[] | undefined, data: MetricsHistoryPoint[]) {
  const sampled = data.filter(point => point.timestampMs !== undefined)
  if (!annotations?.length || !sampled.length) return []
  return annotations.flatMap(annotation => {
    const at = Date.parse(annotation.timestamp)
    if (!Number.isFinite(at)) return []
    const first = sampled[0].timestampMs!
    const last = sampled[sampled.length - 1].timestampMs!
    // Notes outside the visible window are not pinned to its edge.
    if (at < first - 5_000 || at > last + 5_000) return []
    const nearest = sampled.reduce((best, point) => Math.abs(point.timestampMs! - at) < Math.abs(best.timestampMs! - at) ? point : best)
    return [{ id: annotation.id, label: annotation.label, x: nearest.time }]
  })
}

interface Props {
  data: MetricsHistoryPoint[]
  annotations?: TimelineAnnotation[]
  // overlays are another analysis's spans and instants (root cause) drawn on
  // every lane; onOverlaySelect opens one.
  overlays?: TimelineOverlay[]
  onOverlaySelect?: (overlay: TimelineOverlay) => void
  goalConfig?: GoalConfig
  loading?: boolean
  error?: string
  showBaseline?: boolean
  runId?: string
  baselineId?: string
}

const seriesColor = (index: number) => `var(--chart-${(index % 6) + 1})`

const OVERLAY_COLORS: Record<TimelineOverlay['tone'], string> = { danger: 'var(--danger)', warning: 'var(--warning)', accent: 'var(--accent)', muted: 'var(--text-muted)' }

function LaneChart({
  lane,
  data,
  metricKeys,
  goalConfig,
  breachWindows,
  showBaseline,
  transactions,
  onRemove,
  runId,
  baselineId,
  annotations,
  overlayBands = [],
}: {
  lane: TimelineGraph
  annotations?: TimelineAnnotation[]
  overlayBands?: TimelineOverlayBand[]
  data: MetricsHistoryPoint[]
  metricKeys: Set<string>
  goalConfig?: GoalConfig
  breachWindows: ReturnType<typeof buildBreachWindows>
  showBaseline?: boolean
  transactions?: string[]
  onRemove: () => void
  runId?: string
  baselineId?: string
}) {
  const transactionScoped = transactions !== undefined && graphSupportsTransactions(lane)
  const series = lane.series.flatMap((item, index) => {
    if (transactionScoped) {
      if (!item.transactionMetric) return []
      return transactions.flatMap((name) => {
        const key = transactionChartSeriesKey(name, item.transactionMetric!)
        if (!metricKeys.has(key)) return []
        const presentation = transactionChartSeriesPresentation(name)
        return [
          {
            key,
            name: `${name} · ${item.name}`,
            color: presentation.color,
            dash: index > 0 ? '5 3' : presentation.strokeDasharray,
          },
        ]
      })
    }
    const result = metricKeys.has(item.key)
      ? [
          {
            key: item.key,
            name: item.name,
            color: seriesColor(index),
            dash: item.key === 'targetTps' ? '4 4' : undefined,
          },
        ]
      : []
    if (showBaseline && item.baselineKey && metricKeys.has(item.baselineKey))
      result.push({ key: item.baselineKey, name: `Baseline · ${item.name}`, color: seriesColor(index), dash: '5 4' })
    return result
  })
  // Whole-run thresholds and bands are not transaction-specific evidence.
  const sla = transactionScoped
    ? undefined
    : lane.id === 'latency' || lane.id === 'p95'
      ? goalConfig?.p95_latency_ms
      : lane.id === 'errors'
        ? goalConfig?.max_error_pct
        : undefined
  return (
    <div className="run-report-timeline-lane" data-lane={lane.id}>
      <div className="run-report-lane-label">
        <strong>{lane.title}</strong>
        <span>
          {lane.unit} · {transactionScoped ? 'Selected transactions' : 'Whole run'}
        </span>
        <button
          className="btn btn-sm btn-ghost"
          type="button"
          aria-label={`Remove ${lane.title} graph`}
          onClick={onRemove}
        >
          Remove
        </button>
      </div>
      <div className="run-report-lane-chart">
        {series.length === 0 ? (
          <p className="run-report-timeline-empty">
            {transactionScoped
              ? 'No recorded values for the selected transactions in this window.'
              : 'No recorded values in this window.'}
          </p>
        ) : (
          <>
            <SeriesChartExport id={`timeline-${lane.id}`} title={lane.title} rows={data} axes={[{ key: "time", label: "Run time", value: point => point.time }, { key: "utc", label: "Captured UTC timestamp (when retained)", value: point => point.timestampMs === undefined ? undefined : new Date(point.timestampMs).toISOString() }]} series={series.map(item => ({ key: item.key, label: item.name, unit: lane.unit }))} context={{ run_id: runId, baseline_id: showBaseline && !transactionScoped ? baselineId : undefined, scope: transactionScoped ? transactions : "Whole run", baseline_visible: showBaseline && !transactionScoped, sla, breach_windows: transactionScoped ? undefined : breachWindows }}><ResponsiveContainer width="100%" height={138}>
              <ComposedChart data={data} syncId="metrics" margin={{ top: 14, right: 24, bottom: 8, left: 4 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} minTickGap={28} />
                <YAxis stroke="var(--text-muted)" fontSize={11} width={48} domain={[0, 'auto']} />
                <Tooltip
                  formatter={(value, name) => [
                    typeof value === 'number' && Number.isFinite(value)
                      ? `${value.toFixed(lane.unit === '%' ? 2 : 1)} ${lane.unit}`
                      : 'n/a',
                    String(name),
                  ]}
                  contentStyle={chartTooltipContentStyle}
                  labelStyle={chartTooltipLabelStyle}
                />
                {transactions === undefined &&
                  breachWindows.map((window) => (
                    <ReferenceArea
                      key={`${window.start}-${window.end}`}
                      x1={window.start}
                      x2={window.end}
                      fill="var(--danger)"
                      fillOpacity={0.08}
                      strokeOpacity={0}
                    />
                  ))}
                {overlayBands.map(({ overlay, x1, x2 }) => x2 !== undefined && x2 !== x1 ? (
                  <ReferenceArea
                    key={`overlay-${overlay.id}`}
                    x1={x1}
                    x2={x2}
                    fill={OVERLAY_COLORS[overlay.tone]}
                    fillOpacity={overlay.kind === 'validity' ? 0.12 : 0.1}
                    stroke={OVERLAY_COLORS[overlay.tone]}
                    strokeOpacity={0.35}
                    strokeDasharray={overlay.kind === 'validity' ? '1 3' : undefined}
                  />
                ) : (
                  <ReferenceLine key={`overlay-${overlay.id}`} x={x1} stroke={OVERLAY_COLORS[overlay.tone]} strokeDasharray={overlay.kind === 'phase' ? '5 4' : undefined} />
                ))}
                {annotationMarkers(annotations, data).map((marker) => (
                  <ReferenceLine
                    key={`annotation-${marker.id}`}
                    x={marker.x}
                    stroke="var(--accent-text)"
                    strokeDasharray="2 3"
                    label={{ value: marker.label, position: 'insideTopLeft', fill: 'var(--text-secondary)', fontSize: 10 }}
                  />
                ))}
                {typeof sla === 'number' && (
                  <ReferenceLine
                    y={sla}
                    stroke="var(--danger)"
                    strokeDasharray="4 3"
                    label={{
                      value: lane.id === 'errors' ? 'Error SLA' : 'P95 SLA',
                      fill: 'var(--danger)',
                      fontSize: 11,
                    }}
                  />
                )}
                {series.map((item) =>
                  item.key === 'vusers' ? (
                    <Area
                      key={item.key}
                      type="monotone"
                      dataKey={item.key}
                      name={item.name}
                      stroke={item.color}
                      fill={item.color}
                      fillOpacity={0.18}
                      isAnimationActive={false}
                    />
                  ) : (
                    <Line
                      key={item.key}
                      type="monotone"
                      dataKey={item.key}
                      name={item.name}
                      stroke={item.color}
                      strokeDasharray={item.dash}
                      strokeWidth={2}
                      dot={false}
                      isAnimationActive={false}
                    />
                  ),
                )}
              </ComposedChart>
            </ResponsiveContainer></SeriesChartExport>
            <div className="run-report-timeline-legend" aria-label={`${lane.title} series`}>
              {series.map((item) => (
                <span key={item.key}>
                  <i style={{ borderColor: item.color, borderTopStyle: item.dash ? 'dashed' : 'solid' }} />
                  {item.name}
                </span>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  )
}

export default function SynchronizedTimeline({ data, annotations, overlays, onOverlaySelect, goalConfig, loading, error, showBaseline, runId, baselineId }: Props) {
  const controlsId = useId()
  const [zoomState, dispatchZoom] = useReducer(timelineZoomReducer, initialTimelineZoomState)
  const [visibleLanes, setVisibleLanes] = useState(() => new Set(DEFAULT_TIMELINE_GRAPHS))
  const [visibleTransactions, setVisibleTransactions] = useState<Set<string>>(() => new Set())
  const [selectedOnly, setSelectedOnly] = useState(false)
  const [panel, setPanel] = useState<'graphs' | 'transactions' | null>(null)
  const [graphSearch, setGraphSearch] = useState('')
  const [transactionSearch, setTransactionSearch] = useState('')
  const startIndex = Math.min(zoomState.window?.startIndex ?? 0, Math.max(0, data.length - 1))
  const endIndex = Math.min(zoomState.window?.endIndex ?? Math.max(0, data.length - 1), Math.max(0, data.length - 1))
  const visibleData = useMemo(() => data.slice(startIndex, endIndex + 1), [data, startIndex, endIndex])
  const metricKeys = useMemo(() => timelineMetricKeys(data), [data])
  const visibleMetricKeys = useMemo(() => timelineMetricKeys(visibleData), [visibleData])
  const breachWindows = useMemo(
    () => buildBreachWindows(visibleData, goalConfig?.p95_latency_ms, goalConfig?.max_error_pct),
    [goalConfig?.max_error_pct, goalConfig?.p95_latency_ms, visibleData],
  )
  const overlayBands = useMemo(() => timelineOverlayBands(overlays, visibleData), [overlays, visibleData])
  const transactionNames = useMemo(() => transactionChartSeriesNames(data), [data])
  const selectedTransactions = useMemo(
    () => transactionNames.filter((name) => visibleTransactions.has(name)),
    [transactionNames, visibleTransactions],
  )
  const filteredTransactions = transactionNames.filter((name) =>
    name.toLocaleLowerCase().includes(transactionSearch.trim().toLocaleLowerCase()),
  )
  const filteredGraphs = TIMELINE_GRAPHS.filter((graph) =>
    `${graph.title} ${graph.group} ${graph.series.map((series) => series.name).join(' ')}`
      .toLocaleLowerCase()
      .includes(graphSearch.trim().toLocaleLowerCase()),
  )
  const toggleGraph = (id: string) =>
    setVisibleLanes((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else if (next.size < MAX_TIMELINE_GRAPHS) next.add(id)
      return next
    })
  const toggleTransaction = (name: string) =>
    setVisibleTransactions((current) => {
      const next = new Set(transactionNames.filter((transaction) => current.has(transaction)))
      if (next.has(name)) next.delete(name)
      else if (next.size < MAX_TIMELINE_TRANSACTIONS) next.add(name)
      return next
    })

  if (loading)
    return (
      <section className="run-report-panel run-report-timeline">
        <p role="status">Loading timeline…</p>
      </section>
    )
  if (error)
    return (
      <section className="run-report-panel run-report-timeline">
        <p className="run-report-error">{error}</p>
      </section>
    )
  if (data.length === 0)
    return (
      <section className="run-report-panel run-report-timeline">
        <p>No interval summary history is available for this run.</p>
      </section>
    )

  return (
    <section className="run-report-panel run-report-timeline" aria-label="Synchronized run timeline">
      <div className="run-report-section-heading">
        <div>
          <h2>Synchronized timeline</h2>
          <p>Compare performance and resource signals on a shared time window.</p>
        </div>
        {zoomState.window && (
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => dispatchZoom({ type: 'reset' })}>
            Reset zoom
          </button>
        )}
      </div>
      <div className="run-report-timeline-toolbar" aria-label="Timeline display controls">
        <button
          className="btn btn-sm btn-secondary"
          type="button"
          aria-expanded={panel === 'graphs'}
          aria-controls={`${controlsId}-graphs`}
          onClick={() => setPanel(panel === 'graphs' ? null : 'graphs')}
        >
          Graphs{' '}
          <span className="run-report-timeline-count">
            {visibleLanes.size} / {MAX_TIMELINE_GRAPHS}
          </span>{' '}
          <span aria-hidden="true">⌄</span>
        </button>
        <button
          className="btn btn-sm btn-secondary"
          type="button"
          aria-expanded={panel === 'transactions'}
          aria-controls={`${controlsId}-transactions`}
          onClick={() => setPanel(panel === 'transactions' ? null : 'transactions')}
        >
          Transactions{' '}
          <span className="run-report-timeline-count">
            {selectedOnly ? `${selectedTransactions.length} selected` : 'Whole run'}
          </span>{' '}
          <span aria-hidden="true">⌄</span>
        </button>
        <span className="run-report-muted">Shared hover &amp; zoom</span>
      </div>
      {panel === 'graphs' && (
        <div
          id={`${controlsId}-graphs`}
          className="run-report-timeline-picker"
          role="region"
          aria-label="Graph catalog"
        >
          <div className="run-report-timeline-picker-heading">
            <div>
              <h3>Choose graphs</h3>
              <p className="run-report-muted" role="status">
                {visibleLanes.size} of {MAX_TIMELINE_GRAPHS} displayed.{' '}
                {visibleLanes.size === MAX_TIMELINE_GRAPHS
                  ? 'Remove a graph to add another.'
                  : 'Choose up to six to keep the timeline readable.'}
              </p>
            </div>
            <button
              type="button"
              className="btn btn-sm btn-ghost"
              onClick={() => setVisibleLanes(new Set(DEFAULT_TIMELINE_GRAPHS))}
            >
              Restore defaults
            </button>
          </div>
          <label className="run-report-timeline-search">
            Search graphs
            <input
              type="search" autoComplete="off"
              value={graphSearch}
              onChange={(event) => setGraphSearch(event.target.value)}
              placeholder="Search by name or metric"
            />
          </label>
          <div className="run-report-timeline-catalog">
            {(['Performance', 'Resources', 'Diagnostics'] as const).map((group) => {
              const graphs = filteredGraphs.filter((graph) => graph.group === group)
              return (
                graphs.length > 0 && (
                  <fieldset key={group}>
                    <legend>{group}</legend>
                    {graphs.map((graph) => {
                      const checked = visibleLanes.has(graph.id)
                      const available = graphHasData(
                        graph,
                        metricKeys,
                        selectedOnly && selectedTransactions.length > 0 ? selectedTransactions : undefined,
                      )
                      return (
                        <label key={graph.id} className="run-report-timeline-option">
                          <input
                            type="checkbox"
                            aria-label={graph.title}
                            checked={checked}
                            disabled={!checked && (!available || visibleLanes.size >= MAX_TIMELINE_GRAPHS)}
                            onChange={() => toggleGraph(graph.id)}
                          />
                          <span>
                            {graph.title}
                            <small>
                              {!available
                                ? 'No recorded data for this scope'
                                : graphSupportsTransactions(graph)
                                  ? `${graph.unit} · Transaction filter supported`
                                  : `${graph.unit} · Whole run`}
                            </small>
                          </span>
                        </label>
                      )
                    })}
                  </fieldset>
                )
              )
            })}
            {filteredGraphs.length === 0 && <p className="run-report-muted">No graphs match your search.</p>}
          </div>
        </div>
      )}
      {panel === 'transactions' && (
        <div
          id={`${controlsId}-transactions`}
          className="run-report-timeline-picker"
          role="region"
          aria-label="Transaction filter"
        >
          <div className="run-report-timeline-picker-heading">
            <div>
              <h3>Transaction scope</h3>
              <p className="run-report-muted">
                Filter throughput, response times, and error rate. Load, resources, and diagnostics remain whole-run
                context.
              </p>
            </div>
          </div>
          <label className="run-report-timeline-search">
            Display
            <select
              value={selectedOnly ? 'selected' : 'all'}
              onChange={(event) => setSelectedOnly(event.target.value === 'selected')}
            >
              <option value="all">Whole run</option>
              <option value="selected" disabled={transactionNames.length === 0}>
                Selected transactions only
              </option>
            </select>
          </label>
          {transactionNames.length === 0 ? (
            <p className="run-report-muted">
              This run has no recorded transaction-level timeline data. Whole-run graphs are still available.
            </p>
          ) : (
            selectedOnly && (
              <>
                <label className="run-report-timeline-search">
                  Search transactions
                  <input
                    type="search" autoComplete="off"
                    value={transactionSearch}
                    onChange={(event) => setTransactionSearch(event.target.value)}
                    placeholder="Find a transaction"
                  />
                </label>
                <div className="run-report-timeline-picker-heading">
                  <span className="run-report-muted" role="status">
                    {selectedTransactions.length} / {MAX_TIMELINE_TRANSACTIONS} selected. Whole-run performance lines,
                    baselines, and SLA bands are hidden.
                  </span>
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost"
                    onClick={() => setVisibleTransactions(new Set())}
                  >
                    Clear selection
                  </button>
                </div>
                <div className="run-report-transaction-options" aria-label="Transaction selection">
                  {filteredTransactions.map((name) => (
                    <label key={name} className="run-report-timeline-option">
                      <input
                        type="checkbox"
                        checked={visibleTransactions.has(name)}
                        disabled={
                          !visibleTransactions.has(name) && selectedTransactions.length >= MAX_TIMELINE_TRANSACTIONS
                        }
                        onChange={() => toggleTransaction(name)}
                      />
                      <span>{name}</span>
                    </label>
                  ))}
                  {filteredTransactions.length === 0 && (
                    <p className="run-report-muted">No transactions match your search.</p>
                  )}
                </div>
              </>
            )
          )}
        </div>
      )}
      {selectedOnly && (
        <p className="run-report-timeline-scope" role="status">
          {selectedTransactions.length
            ? `Showing only: ${selectedTransactions.join(', ')}. Load, resources, and diagnostics show the whole run.`
            : 'No transactions selected. Choose transactions in the filter to display their performance.'}
        </p>
      )}
      {data.length > 1 && (
        <div className="run-report-timeline-window" aria-label="Timeline window">
          <label>
            Window start: {data[startIndex]?.time}
            <input
              type="range"
              aria-label="Timeline window start"
              min={0}
              max={endIndex}
              value={startIndex}
              aria-valuetext={data[startIndex]?.time}
              onChange={(event) =>
                dispatchZoom({
                  type: 'set-window',
                  startIndex: Number(event.target.value),
                  endIndex,
                  pointCount: data.length,
                })
              }
            />
          </label>
          <label>
            Window end: {data[endIndex]?.time}
            <input
              type="range"
              aria-label="Timeline window end"
              min={startIndex}
              max={data.length - 1}
              value={endIndex}
              aria-valuetext={data[endIndex]?.time}
              onChange={(event) =>
                dispatchZoom({
                  type: 'set-window',
                  startIndex,
                  endIndex: Number(event.target.value),
                  pointCount: data.length,
                })
              }
            />
          </label>
        </div>
      )}
      <div className="run-report-timeline-stack">
        {TIMELINE_GRAPHS.filter((lane) => visibleLanes.has(lane.id)).map((lane) => (
          <LaneChart
            key={lane.id}
            lane={lane}
            data={visibleData}
            metricKeys={visibleMetricKeys}
            goalConfig={goalConfig}
            breachWindows={breachWindows}
            showBaseline={showBaseline}
            transactions={selectedOnly ? selectedTransactions : undefined}
            onRemove={() => toggleGraph(lane.id)}
            runId={runId}
            baselineId={baselineId}
            annotations={annotations}
            overlayBands={overlayBands}
          />
        ))}
        {visibleLanes.size === 0 && (
          <p className="run-report-muted">No graphs selected. Open Graphs to choose what to display.</p>
        )}
      </div>
      {overlayBands.length > 0 && (
        <ul className="run-report-timeline-overlays" aria-label="Root-cause markers">
          {overlayBands.map(({ overlay }) => (
            <li key={overlay.id} data-kind={overlay.kind}>
              <i style={{ borderColor: OVERLAY_COLORS[overlay.tone], borderTopStyle: overlay.kind === 'phase' ? 'dashed' : 'solid' }} aria-hidden="true" />
              {onOverlaySelect && overlay.ref
                ? <button type="button" className="btn btn-sm btn-ghost" onClick={() => onOverlaySelect(overlay)}>{overlay.label}</button>
                : <span>{overlay.label}</span>}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
