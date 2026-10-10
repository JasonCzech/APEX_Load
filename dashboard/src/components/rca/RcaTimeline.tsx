import { useId, useMemo } from 'react'
import { CartesianGrid, ComposedChart, Line, ReferenceArea, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type { MetricsHistoryPoint } from '../../types'
import type { RcaValidityWindow } from '../../types/rca'
import { formatTimeOfDay } from '../../utils/dateTime'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from '../chartTooltipTheme'
import {
  formatRcaDuration,
  formatRcaEffect,
  formatRcaTime,
  rcaAnomalyWindow,
  rcaSeriesLabel,
  rcaTimelineRows,
  rcaValidityLabel,
  type RcaAnomalyRole,
  type RcaHypothesisAnomaly,
  type RcaPhaseMarker,
  type RcaWindow,
} from './rcaModel'

interface Props {
  history: MetricsHistoryPoint[]
  anomalies: RcaHypothesisAnomaly[]
  // phases are the run's load-phase transitions; phasesInferred says they
  // were inferred from steady load levels because none were archived.
  phases: RcaPhaseMarker[]
  phasesInferred?: boolean
  // validity is the run's suspect and invalid spans.
  validity: RcaValidityWindow[]
  window: RcaWindow | null
  loading?: boolean
}

const clock = (value: number) => formatTimeOfDay(value)

// The timeline's measures, one panel each on a shared time axis, so neither
// is read against the other's scale.
const MEASURES = [
  { key: 'tps', title: 'Throughput (TPS)', color: 'var(--chart-1)' },
  { key: 'p95', title: 'P95 latency (ms)', color: 'var(--chart-2)' },
] as const

// A symptom is the hypothesis's effect, a cause what its chain cites, and a
// change one of the run's anomalies that no selected hypothesis cites.
const roleColor: Record<RcaAnomalyRole, string> = { symptom: 'var(--danger)', cause: 'var(--warning)', anomaly: 'var(--accent)' }
const roleLabel: Record<RcaAnomalyRole, string> = { symptom: 'Symptom', cause: 'Cause', anomaly: 'Change' }

// RcaTimeline draws one hypothesis on the run's timeline: its symptom and
// cause anomaly windows, each onset's uncertainty band, the load phases and
// the spans the run was not valid, over the run's throughput and P95
// latency, each in its own titled panel. The list below the chart says the
// same in text.
export default function RcaTimeline({ history, anomalies, phases, phasesInferred, validity, window, loading }: Props) {
  const syncId = useId()
  const rows = useMemo(() => rcaTimelineRows(history, window), [history, window])
  const domain = useMemo<[number, number] | null>(() => {
    if (window) return [window.start, window.end]
    const times = [...rows.map(row => row.t), ...anomalies.flatMap(({ anomaly }) => { const w = rcaAnomalyWindow(anomaly); return [w.start, w.end] })].filter(Number.isFinite)
    return times.length ? [Math.min(...times), Math.max(...times)] : null
  }, [anomalies, rows, window])
  const steps = phases.filter(step => !domain || (step.at >= domain[0] && step.at <= domain[1]))
  const concerns = validity.flatMap(w => {
    const start = Date.parse(w.start)
    const end = Date.parse(w.end)
    return Number.isFinite(start) && Number.isFinite(end) && (!domain || (end >= domain[0] && start <= domain[1])) ? [{ start, end, verdict: w.verdict }] : []
  })
  return (
    <figure className="rca-timeline" aria-label="Hypothesis timeline">
      {loading && <p role="status">Loading the run timeline…</p>}
      {!loading && rows.length === 0 && <p className="run-report-muted">No archived intervals fall in this window; the markers below still apply.</p>}
      {rows.length > 0 && domain && MEASURES.map((measure, index) => (
        <div key={measure.key} className="rca-timeline-panel">
          <p className="rca-timeline-axis" aria-hidden="true">{measure.title}</p>
          <ResponsiveContainer width="100%" height={index === MEASURES.length - 1 ? 140 : 118}>
            <ComposedChart data={rows} syncId={syncId} margin={{ top: 6, right: 16, bottom: 4, left: 4 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
              <XAxis dataKey="t" type="number" scale="time" domain={domain} allowDataOverflow tickFormatter={clock} stroke="var(--text-muted)" fontSize={11} minTickGap={36} hide={index !== MEASURES.length - 1} />
              <YAxis stroke="var(--text-muted)" fontSize={11} width={52} domain={[0, 'auto']} />
              {anomalies.map(({ anomaly, role }) => {
                const w = rcaAnomalyWindow(anomaly)
                const onset = Date.parse(anomaly.onset)
                return [
                  <ReferenceArea key={`${anomaly.id}-window`} x1={onset} x2={w.end} ifOverflow="hidden"
                    fill={roleColor[role]} fillOpacity={0.14} />,
                  anomaly.onset_uncertainty_ns > 0 && <ReferenceArea key={`${anomaly.id}-band`} x1={w.start} x2={onset + anomaly.onset_uncertainty_ns / 1e6} ifOverflow="hidden"
                    fill="var(--accent)" fillOpacity={0.12} stroke="var(--accent)" strokeOpacity={0.35} strokeDasharray="2 3" />,
                  <ReferenceLine key={`${anomaly.id}-onset`} x={onset} ifOverflow="hidden" stroke={roleColor[role]} strokeWidth={1.5} />,
                ]
              })}
              {concerns.map(c => <ReferenceArea key={`validity-${c.start}`} x1={c.start} x2={c.end} ifOverflow="hidden" fill="var(--text-muted)" fillOpacity={0.18} stroke="var(--text-muted)" strokeDasharray="1 3" />)}
              {steps.map((step, i) => <ReferenceLine key={`phase-${i}`} x={step.at} ifOverflow="hidden" stroke="var(--text-muted)" strokeDasharray="5 4" />)}
              <Line type="monotone" dataKey={measure.key} name={measure.title} stroke={measure.color} dot={false} isAnimationActive={false} connectNulls />
              <Tooltip labelFormatter={value => formatRcaTime(Number(value))} contentStyle={chartTooltipContentStyle} labelStyle={chartTooltipLabelStyle} />
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      ))}
      <figcaption>
        <ul className="rca-timeline-markers">
          {anomalies.map(({ anomaly, role }) => (
            <li key={anomaly.id} data-role={role}>
              <span className="rca-marker" aria-hidden="true" />
              <strong>{roleLabel[role]}:</strong> {rcaSeriesLabel(anomaly.series)} {anomaly.direction === 'decrease' ? 'fell' : 'rose'} {formatRcaEffect(anomaly)} from {formatRcaTime(anomaly.onset)}
              {anomaly.onset_uncertainty_ns > 0 && <> ± {formatRcaDuration(anomaly.onset_uncertainty_ns)}</>}
              {anomaly.recovered ? <> and recovered by {formatRcaTime(anomaly.end)}</> : <> and had not recovered by {formatRcaTime(anomaly.end)}</>}
            </li>
          ))}
          {concerns.map(c => <li key={`validity-${c.start}`} data-role="validity"><span className="rca-marker" aria-hidden="true" />{rcaValidityLabel(c.verdict)} from {formatRcaTime(c.start)} to {formatRcaTime(c.end)}: symptoms here may come from the load generators</li>)}
          {steps.map((step, i) => <li key={`phase-${i}`} data-role="phase"><span className="rca-marker" aria-hidden="true" />{step.label} at {formatRcaTime(step.at)}</li>)}
        </ul>
        {phasesInferred && steps.length > 0 && <p className="run-report-muted">This run archived no load-level events; the load steps are inferred from its steady load levels.</p>}
      </figcaption>
    </figure>
  )
}
