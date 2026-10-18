import { useMemo } from 'react'
import { Area, CartesianGrid, ComposedChart, Line, ReferenceArea, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { loadTimeTicks, loadValueTicks } from './loadPlanSteps'
import { formatTrafficProfileAxisTime } from './trafficProfilePreviewModel'
import './LoadPlanChart.css'

export interface LoadPlanSeries {
  key: string
  label: string
  color: string
}

export interface LoadPlanObservedPoint {
  second: number
  vusers: number
}

interface Props {
  /** The plan, one row per step, keyed by elapsed `second`. */
  points: Array<Record<string, number>>
  totalSeconds: number
  /** Groups whose planned VUsers stack into the load. */
  groups: LoadPlanSeries[]
  showVUsers?: boolean
  /** Planned TPS targets; the last one is the total target. */
  tps?: LoadPlanSeries[]
  phases?: { preBaselineSeconds: number; steadySeconds: number; postBaselineSeconds: number }
  /** VUsers the run actually had, by elapsed second. */
  observed?: LoadPlanObservedPoint[]
  /** Where the run is now, for a live run. */
  nowSecond?: number
  height?: number
  /** What the chart shows, for its accessible name. */
  label: string
  compact?: boolean
}

const OBSERVED_KEY = 'observedVusers'
// Stable defaults, so a chart without them keeps its merged rows between renders.
const NO_SERIES: LoadPlanSeries[] = []
const NO_SAMPLES: LoadPlanObservedPoint[] = []
const TOTAL_KEY = '__plannedVusers'

type Row = Record<string, number | undefined>

// The plan and the observed samples on one time axis. A sample between two
// plan steps carries the step before it, which is what a staircase means.
function mergeRows(points: Array<Record<string, number>>, groups: LoadPlanSeries[], observed: LoadPlanObservedPoint[]): Row[] {
  const plan = [...points].sort((left, right) => left.second - right.second)
  const samples = [...observed].filter(sample => Number.isFinite(sample.second) && sample.second >= 0).sort((left, right) => left.second - right.second)
  const withTotal = (row: Record<string, number>): Row => ({ ...row, [TOTAL_KEY]: groups.reduce((sum, group) => sum + (Number(row[group.key]) || 0), 0) })
  const rows: Row[] = []
  let previous: Row | undefined
  let planIndex = 0
  let sampleIndex = 0
  while (planIndex < plan.length || sampleIndex < samples.length) {
    const step = plan[planIndex]
    const sample = samples[sampleIndex]
    if (step && (!sample || step.second <= sample.second)) {
      previous = withTotal(step)
      rows.push(sample && sample.second === step.second ? { ...previous, [OBSERVED_KEY]: sample.vusers } : previous)
      if (sample && sample.second === step.second) sampleIndex += 1
      planIndex += 1
    } else if (sample) {
      rows.push({ ...(previous ?? {}), second: sample.second, [OBSERVED_KEY]: sample.vusers })
      sampleIndex += 1
    }
  }
  return rows
}

function formatValue(value: number) {
  return value.toLocaleString(undefined, { maximumFractionDigits: 2 })
}

function LoadPlanTooltip({ active, payload, groups, tps, showVUsers }: {
  active?: boolean
  payload?: Array<{ payload?: Row }>
  groups: LoadPlanSeries[]
  tps: LoadPlanSeries[]
  showVUsers: boolean
}) {
  const row = payload?.[0]?.payload
  if (!active || !row) return null
  const observed = row[OBSERVED_KEY]
  return (
    <div className="load-plan-tooltip">
      <strong>{formatTrafficProfileAxisTime(Number(row.second) || 0)}</strong>
      {showVUsers && <span className="load-plan-tooltip-total">Planned: {formatValue(Number(row[TOTAL_KEY]) || 0)} VUsers</span>}
      {showVUsers && groups.length > 1 && groups.map(group => (
        <span key={group.key}><i style={{ background: group.color }} />{group.label}: {formatValue(Number(row[group.key]) || 0)}</span>
      ))}
      {observed !== undefined && <span className="load-plan-tooltip-total"><i className="line" />Observed: {formatValue(observed)} VUsers</span>}
      {tps.map(series => row[series.key] !== undefined && (
        <span key={series.key}><i className="line dashed" style={{ borderColor: series.color }} />{series.label}: {formatValue(Number(row[series.key]) || 0)}</span>
      ))}
    </div>
  )
}

/**
 * The one picture of a load plan: the engine's staircase on elapsed time,
 * with round ticks, the same in the scenario builder, on New Test, in
 * Command Center and in the run's results. Groups stack (the top edge is the
 * total, which the tooltip states); a run's observed VUsers are laid over it.
 */
export default function LoadPlanChart({ points, totalSeconds, groups, showVUsers = true, tps = NO_SERIES, phases, observed = NO_SAMPLES, nowSecond, height = 220, label, compact = false }: Props) {
  const rows = useMemo(() => mergeRows(points, groups, observed), [groups, observed, points])
  const plannedPeak = rows.reduce((peak, row) => Math.max(peak, Number(row[TOTAL_KEY]) || 0), 0)
  const observedPeak = observed.reduce((peak, sample) => Math.max(peak, sample.vusers), 0)
  const lastObserved = observed.reduce((latest, sample) => Math.max(latest, sample.second), 0)
  const currentSecond = nowSecond !== undefined && Number.isFinite(nowSecond) ? Math.max(0, nowSecond) : undefined
  const xMax = Math.max(totalSeconds, lastObserved, currentSecond ?? 0, 1)
  const timeTicks = loadTimeTicks(xMax, compact ? 5 : 7)
  const valueTicks = loadValueTicks(Math.max(plannedPeak, observedPeak))
  // Without VUsers to draw, the groups' TPS targets stack (the top edge is the
  // total target, drawn as its line), so equal targets do not hide each other.
  const stackedTps = !showVUsers && tps.length > 1 ? tps.slice(0, -1) : NO_SERIES
  const lineTps = stackedTps.length ? tps.slice(-1) : tps
  const tpsPeak = rows.reduce((peak, row) => Math.max(peak, ...tps.map(series => Number(row[series.key]) || 0)), 0)
  // TPS gets round ticks too, as VUsers do.
  const tpsTicks = loadValueTicks(tpsPeak)
  const primaryAxis = showVUsers ? 'vusers' : 'tps'
  const summary = `${label}: ${showVUsers ? `planned peak ${formatValue(plannedPeak)} VUsers` : 'planned TPS targets'} over ${formatTrafficProfileAxisTime(totalSeconds)}${observed.length ? `; observed peak ${formatValue(observedPeak)} VUsers` : ''}`
  const axisLabel = (value: string, axis: 'x' | 'left' | 'right') => compact ? undefined : axis === 'x'
    ? { value, position: 'insideBottom' as const, offset: -4, fill: 'var(--text-muted)', fontSize: 10 }
    : { value, angle: axis === 'left' ? -90 : 90, position: axis === 'left' ? 'insideLeft' as const : 'insideRight' as const, fill: 'var(--text-muted)', fontSize: 10 }
  return (
    <div className={`load-plan-chart${compact ? ' compact' : ''}`}>
      <div className="load-plan-chart-plot" role="img" aria-label={summary}>
        <ResponsiveContainer width="100%" height={height}>
          <ComposedChart data={rows} margin={{ top: 8, right: tps.length && showVUsers ? 4 : 12, left: 0, bottom: compact ? 0 : 12 }} accessibilityLayer={false}>
            <CartesianGrid stroke="var(--chart-grid, var(--border))" strokeDasharray="3 3" vertical={false} />
            <XAxis
              dataKey="second"
              type="number"
              domain={[0, timeTicks[timeTicks.length - 1] >= xMax ? timeTicks[timeTicks.length - 1] : xMax]}
              ticks={timeTicks}
              tickFormatter={value => formatTrafficProfileAxisTime(Number(value))}
              stroke="var(--text-muted)"
              fontSize={10}
              tickLine={false}
              height={compact ? 22 : 36}
              label={axisLabel('Elapsed time', 'x')}
            />
            {showVUsers && <YAxis
              yAxisId="vusers"
              domain={[0, valueTicks[valueTicks.length - 1]]}
              ticks={valueTicks}
              allowDecimals={false}
              stroke="var(--text-muted)"
              fontSize={10}
              tickLine={false}
              width={compact ? 32 : 48}
              label={axisLabel('VUsers', 'left')}
            />}
            {tps.length > 0 && <YAxis
              yAxisId="tps"
              orientation={showVUsers ? 'right' : 'left'}
              domain={[0, tpsTicks[tpsTicks.length - 1]]}
              ticks={tpsTicks}
              stroke="var(--warning-text, var(--warning))"
              fontSize={10}
              tickLine={false}
              width={compact ? 32 : 48}
              label={axisLabel('TPS', showVUsers ? 'right' : 'left')}
            />}
            <Tooltip content={<LoadPlanTooltip groups={groups} tps={tps} showVUsers={showVUsers} />} isAnimationActive={false} />
            {phases && (showVUsers || tps.length > 0) && <>
              {phases.preBaselineSeconds > 0 && <ReferenceArea yAxisId={primaryAxis} x1={0} x2={phases.preBaselineSeconds} fill="var(--warning)" fillOpacity={0.06} strokeOpacity={0} />}
              <ReferenceArea yAxisId={primaryAxis} x1={phases.preBaselineSeconds} x2={phases.preBaselineSeconds + phases.steadySeconds} fill="var(--accent)" fillOpacity={0.04} strokeOpacity={0} />
              {phases.postBaselineSeconds > 0 && <ReferenceArea yAxisId={primaryAxis} x1={totalSeconds - phases.postBaselineSeconds} x2={totalSeconds} fill="var(--warning)" fillOpacity={0.06} strokeOpacity={0} />}
            </>}
            {showVUsers && groups.map(group => (
              <Area
                key={group.key}
                yAxisId="vusers"
                type="stepAfter"
                dataKey={group.key}
                name={group.label}
                stackId="planned-vusers"
                stroke={group.color}
                fill={group.color}
                fillOpacity={0.24}
                strokeWidth={1.5}
                connectNulls
                isAnimationActive={false}
              />
            ))}
            {stackedTps.map(series => (
              <Area
                key={series.key}
                yAxisId="tps"
                type="stepAfter"
                dataKey={series.key}
                name={series.label}
                stackId="planned-tps"
                stroke={series.color}
                fill={series.color}
                fillOpacity={0.24}
                strokeWidth={1.5}
                connectNulls
                isAnimationActive={false}
              />
            ))}
            {currentSecond !== undefined && currentSecond > 0 && (showVUsers || tps.length > 0) && <ReferenceArea
              className="load-plan-elapsed-area"
              yAxisId={primaryAxis}
              x1={0}
              x2={currentSecond}
              fill="#000"
              fillOpacity={0.22}
              strokeOpacity={0}
              pointerEvents="none"
            />}
            {lineTps.map((series, index) => (
              <Line
                key={series.key}
                yAxisId="tps"
                type="stepAfter"
                dataKey={series.key}
                name={series.label}
                stroke={series.color}
                strokeWidth={index === lineTps.length - 1 ? 2.5 : 1.5}
                strokeDasharray="6 5"
                dot={false}
                connectNulls
                isAnimationActive={false}
              />
            ))}
            {showVUsers && observed.length > 0 && <Line
              yAxisId="vusers"
              type="linear"
              dataKey={OBSERVED_KEY}
              name="Observed VUsers"
              stroke="var(--text-primary)"
              strokeWidth={2}
              dot={false}
              connectNulls
              isAnimationActive={false}
            />}
            {currentSecond !== undefined && <ReferenceLine className="load-plan-now" yAxisId={primaryAxis} x={currentSecond} stroke="var(--warning)" strokeDasharray="4 4" ifOverflow="extendDomain" />}
          </ComposedChart>
        </ResponsiveContainer>
      </div>
      <div className="load-plan-legend">
        {showVUsers && groups.map(group => <span key={group.key}><i style={{ background: group.color }} />{group.label}</span>)}
        {showVUsers && observed.length > 0 && <span><i className="line" />Observed VUsers</span>}
        {stackedTps.map(series => <span key={series.key}><i style={{ background: series.color }} />{series.label}</span>)}
        {lineTps.map(series => <span key={series.key}><i className="line dashed" style={{ borderColor: series.color }} />{series.label}</span>)}
        {currentSecond !== undefined && <span><i className="line dashed now" />Now</span>}
      </div>
    </div>
  )
}
