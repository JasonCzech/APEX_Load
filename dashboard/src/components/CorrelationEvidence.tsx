import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'

import type { AnalysisPoint } from '../types/analysis'
import { formatAnalysisTimeTick } from '../utils/analysisTime'
import './CorrelationEvidence.css'

// Lagged correlation evidence (B6). Each series keeps its own semantics:
// unit, kind, source semantics, population and the statistic or conversion
// that produced its buckets. The investigation chart gives each series its
// own labelled axis, so values in different units are never read as directly
// comparable; the scores are associations, not causes.

export interface CorrelationScore {
  lag_seconds: number
  pairs: number
  expected_buckets: number
  coverage: number
  coefficient?: number
  // Why no coefficient: insufficient_pairs or constant_series.
  unavailable?: string
}

export interface CorrelationSide {
  metric: string
  unit?: string
  kind?: string
  input_kind?: string
  population?: string
  statistic: string
  conversion?: string
  points: number
  valid: number
  excluded?: Record<string, number>
}

export interface CorrelationSemantics {
  left: CorrelationSide
  right: CorrelationSide
  same_units: boolean
  lag_convention: string
}

const UNAVAILABLE: Record<string, string> = {
  insufficient_pairs: 'Fewer than three paired buckets',
  constant_series: 'A series did not vary across the paired buckets',
}

const CONVERSION: Record<string, string> = {
  counter_rate: 'counter converted to a per-second rate',
  declared_summary_statistic: 'declared summary statistic (not raw observations)',
  observation_percentile: 'percentile of raw observations',
}

const EXCLUSION: Record<string, string> = {
  stale: 'stale',
  clock_skewed: 'clock skewed',
  dropped: 'dropped',
  missing: 'missing',
  counter_reset: 'counter reset',
  duplicate_counter_instant: 'duplicate counter instant',
}

function describeExclusions(excluded?: Record<string, number>) {
  const entries = Object.entries(excluded || {}).filter(([, count]) => count > 0).sort(([a], [b]) => a.localeCompare(b))
  return entries.length ? entries.map(([reason, count]) => `${count} ${EXCLUSION[reason] || reason.replace(/_/g, ' ')}`).join(', ') : 'none'
}

function SideSemantics({ label, side }: { label: string; side: CorrelationSide }) {
  return (
    <div className="correlation-side" data-side={label}>
      <h4>{label}: {side.metric}</h4>
      <dl>
        <dt>Unit</dt><dd>{side.unit || 'Undeclared'}</dd>
        <dt>Values</dt><dd>{side.kind || 'Unknown'}{side.input_kind && side.input_kind !== side.kind ? ` from ${side.input_kind}` : ''}</dd>
        <dt>Statistic</dt><dd>{side.statistic}{side.conversion ? ` · ${CONVERSION[side.conversion] || side.conversion}` : ''}</dd>
        {side.population && <><dt>Population</dt><dd>{side.population}</dd></>}
        <dt>Buckets</dt><dd>{side.valid} valid of {side.points}</dd>
        <dt>Excluded</dt><dd>{describeExclusions(side.excluded)}</dd>
      </dl>
    </div>
  )
}

export default function CorrelationEvidence({ scores, semantics, left, right }: { scores: CorrelationScore[]; semantics?: CorrelationSemantics; left: AnalysisPoint[]; right: AnalysisPoint[] }) {
  const rows = new Map<string, { timestamp: string; left?: number; right?: number }>()
  for (const [side, points] of [['left', left], ['right', right]] as const) {
    for (const point of points) {
      const row = rows.get(point.timestamp_utc) ?? { timestamp: point.timestamp_utc }
      row[side] = point.quality === 'valid' ? point.value : undefined
      rows.set(point.timestamp_utc, row)
    }
  }
  const data = [...rows.values()].sort((a, b) => a.timestamp.localeCompare(b.timestamp))
  const leftUnit = semantics?.left.unit || left[0]?.unit
  const rightUnit = semantics?.right.unit || right[0]?.unit
  return (
    <div className="correlation-evidence" aria-label="Correlation evidence">
      {semantics && (
        <div className="correlation-semantics">
          <SideSemantics label="Dataset series" side={semantics.left} />
          <SideSemantics label="Comparison series" side={semantics.right} />
          {!semantics.same_units && <p role="note">The series have different units or kinds: they are associated, never compared value for value.</p>}
        </div>
      )}
      {data.length > 0 && (
        <div className="analysis-chart" role="img" aria-label={`Correlation investigation chart: ${leftUnit || 'dataset'} on the left axis, ${rightUnit || 'comparison'} on the right axis`}>
          <ResponsiveContainer width="100%" height={260}>
            <LineChart data={data} margin={{ top: 8, right: 16, bottom: 8, left: 8 }}>
              <XAxis dataKey="timestamp" tickFormatter={formatAnalysisTimeTick} tick={{ fontSize: 11 }} interval="preserveStartEnd" minTickGap={20} />
              <YAxis yAxisId="left" orientation="left" label={{ value: leftUnit || 'dataset', angle: -90, position: 'insideLeft', fontSize: 11 }} tick={{ fontSize: 11 }} />
              <YAxis yAxisId="right" orientation="right" label={{ value: rightUnit || 'comparison', angle: 90, position: 'insideRight', fontSize: 11 }} tick={{ fontSize: 11 }} />
              <Tooltip />
              <Line yAxisId="left" isAnimationActive={false} type="linear" dataKey="left" name={semantics?.left.metric || 'Dataset series'} stroke="var(--accent)" dot={false} connectNulls={false} />
              <Line yAxisId="right" isAnimationActive={false} type="linear" dataKey="right" name={semantics?.right.metric || 'Comparison series'} stroke="var(--warning, #bd7800)" dot={false} connectNulls={false} />
            </LineChart>
          </ResponsiveContainer>
        </div>
      )}
      <table aria-label="Lagged correlation">
        <caption>{semantics?.lag_convention || 'Positive lag means the comparison series follows the dataset.'}</caption>
        <thead><tr><th>Lag (seconds)</th><th>Association</th><th>Pairs</th><th>Coverage</th></tr></thead>
        <tbody>
          {scores.map(score => (
            <tr key={score.lag_seconds}>
              <td>{score.lag_seconds}</td>
              <td>{score.coefficient == null ? (UNAVAILABLE[score.unavailable || ''] || 'Insufficient variation or evidence') : score.coefficient.toFixed(3)}</td>
              <td>{score.pairs} / {score.expected_buckets}</td>
              <td>{(100 * score.coverage).toFixed(1)}%</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
