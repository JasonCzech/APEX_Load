import { useMemo } from 'react'
import { CartesianGrid, ComposedChart, Line, ReferenceArea, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type { MetricsHistoryPoint } from '../../types'
import type { RcaValidityWindow } from '../../types/rca'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from '../chartTooltipTheme'
import {
  formatRcaDuration,
  formatRcaEffect,
  formatRcaTime,
  rcaAnomalyWindow,
  rcaSeriesLabel,
  rcaTimelineRows,
  rcaValidityLabel,
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

const clock = (value: number) => new Date(value).toISOString().slice(11, 19)

// RcaTimeline draws one hypothesis on the run's timeline: its symptom and
// cause anomaly windows, each onset's uncertainty band, the load phases and
// the spans the run was not valid, over the run's throughput and p95
// latency. The list below the chart says the same in text.
export default function RcaTimeline({ history, anomalies, phases, phasesInferred, validity, window, loading }: Props) {
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
      {rows.length > 0 && domain && (
        <ResponsiveContainer width="100%" height={220}>
          <ComposedChart data={rows} margin={{ top: 18, right: 16, bottom: 8, left: 4 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis dataKey="t" type="number" scale="time" domain={domain} allowDataOverflow tickFormatter={clock} stroke="var(--text-muted)" fontSize={11} minTickGap={28} />
            <YAxis yAxisId="tps" stroke="var(--text-muted)" fontSize={11} width={44} domain={[0, 'auto']} />
            <YAxis yAxisId="ms" orientation="right" stroke="var(--text-muted)" fontSize={11} width={52} domain={[0, 'auto']} />
            {anomalies.map(({ anomaly, role }) => {
              const w = rcaAnomalyWindow(anomaly)
              const onset = Date.parse(anomaly.onset)
              return [
                <ReferenceArea key={`${anomaly.id}-window`} yAxisId="tps" x1={onset} x2={w.end} ifOverflow="hidden"
                  fill={role === 'symptom' ? 'var(--danger)' : 'var(--warning)'} fillOpacity={0.14} />,
                anomaly.onset_uncertainty_ns > 0 && <ReferenceArea key={`${anomaly.id}-band`} yAxisId="tps" x1={w.start} x2={onset + anomaly.onset_uncertainty_ns / 1e6} ifOverflow="hidden"
                  fill="var(--accent)" fillOpacity={0.12} stroke="var(--accent)" strokeOpacity={0.35} strokeDasharray="2 3" />,
                <ReferenceLine key={`${anomaly.id}-onset`} yAxisId="tps" x={onset} ifOverflow="hidden" stroke={role === 'symptom' ? 'var(--danger)' : 'var(--warning)'} strokeWidth={1.5} />,
              ]
            })}
            {concerns.map(c => <ReferenceArea key={`validity-${c.start}`} yAxisId="tps" x1={c.start} x2={c.end} ifOverflow="hidden" fill="var(--text-muted)" fillOpacity={0.18} stroke="var(--text-muted)" strokeDasharray="1 3" />)}
            {steps.map((step, i) => <ReferenceLine key={`phase-${i}`} yAxisId="tps" x={step.at} ifOverflow="hidden" stroke="var(--text-muted)" strokeDasharray="5 4" />)}
            <Line yAxisId="tps" type="monotone" dataKey="tps" name="Throughput (TPS)" stroke="var(--chart-1)" dot={false} isAnimationActive={false} connectNulls />
            <Line yAxisId="ms" type="monotone" dataKey="p95" name="p95 latency (ms)" stroke="var(--chart-2)" dot={false} isAnimationActive={false} connectNulls />
            <Tooltip labelFormatter={value => formatRcaTime(Number(value))} contentStyle={chartTooltipContentStyle} labelStyle={chartTooltipLabelStyle} />
          </ComposedChart>
        </ResponsiveContainer>
      )}
      <figcaption>
        <ul className="rca-timeline-markers">
          {anomalies.map(({ anomaly, role }) => (
            <li key={anomaly.id} data-role={role}>
              <span className="rca-marker" aria-hidden="true" />
              <strong>{role === 'symptom' ? 'Symptom' : 'Cause'}:</strong> {rcaSeriesLabel(anomaly.series)} {anomaly.direction === 'decrease' ? 'fell' : 'rose'} {formatRcaEffect(anomaly)} from {formatRcaTime(anomaly.onset)}
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
