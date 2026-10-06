import { useState } from 'react'
import type { AnalysisColumn, AnalysisHistogram, AnalysisMeasure } from '../types/analysis'
import { analysisProtectedIdentityColumn } from '../utils/analysisCredentialColumn'
import { parseAnalysisHistogramBounds } from '../utils/analysisSummary'

/** Bounds, count conventions, window and source columns of a histogram measure. */
export default function AnalysisHistogramMeasureFields({ measure, columns, onChange }: { measure: AnalysisMeasure; columns: AnalysisColumn[]; onChange: (values: Partial<AnalysisMeasure>) => void }) {
  const h = measure.histogram as AnalysisHistogram
  // The bounds text stays as typed; the mapping holds its parsed bounds.
  const [boundsText, setBoundsText] = useState(() => h.bounds.join(', '))
  const set = (values: Partial<AnalysisHistogram>) => {
    const next = { ...h, ...values }
    if (next.temporality !== 'delta') delete next.window_seconds
    onChange({ histogram: next })
  }
  const long = !!h.bound_column?.length
  const choices = columns.filter(c => JSON.stringify(c.path_components) !== JSON.stringify(measure.value))
  const columnSelect = (label: string, value: string[] | undefined, pick: (path: string[]) => void, required = false) => <label>{label}<select value={JSON.stringify(value ?? [])} onChange={e => pick(JSON.parse(e.target.value) as string[])}>
    {!required && <option value="[]">Choose a column</option>}
    {choices.map(c => <option key={c.path} disabled={analysisProtectedIdentityColumn(c.path_components)} value={JSON.stringify(c.path_components)}>{c.path}{analysisProtectedIdentityColumn(c.path_components) ? ' (protected)' : ''}</option>)}
  </select></label>
  return <>
    <label>Upper bounds, in the measure unit<input value={boundsText} placeholder="0.005, 0.01, 0.1, 1, +Inf" onChange={e => { setBoundsText(e.target.value); const bounds = parseAnalysisHistogramBounds(e.target.value); set(long ? { bounds } : { bounds, count_columns: Array.from({ length: Math.max(0, bounds.length - 1) }, (_, i) => h.count_columns?.[i] ?? []) }) }} /></label>
    <label>Bucket counts<select value={h.bucket_counts} onChange={e => set({ bucket_counts: e.target.value })}><option value="per_bin">Each bin counts only its own range</option><option value="le_cumulative">Each bound counts everything below it (Prometheus le)</option></select></label>
    <label>Counting<select value={h.temporality} onChange={e => set(e.target.value === 'delta' ? { temporality: 'delta', window_seconds: h.window_seconds ?? 60 } : { temporality: 'cumulative' })}><option value="delta">Each row counts one window</option><option value="cumulative">Running totals since process start</option></select></label>
    {h.temporality === 'delta' && <label>Window (seconds)<input type="number" min={1} max={86400} step={1} value={h.window_seconds ?? ''} onChange={e => set({ window_seconds: e.target.value === '' ? undefined : Number(e.target.value) })} /></label>}
    <label>Table shape<select value={long ? 'long' : 'wide'} onChange={e => set(e.target.value === 'long' ? { bound_column: choices[0]?.path_components ?? [], count_columns: undefined } : { bound_column: undefined, count_columns: Array.from({ length: Math.max(0, h.bounds.length - 1) }, () => []) })}><option value="wide">One count column per bound</option><option value="long">One row per bound</option></select></label>
    {long
      ? <>{columnSelect('Bound column', h.bound_column, path => set({ bound_column: path }), true)}<p>This column holds each row's count for its bound.</p></>
      : <>{h.bounds.slice(0, -1).map((bound, i) => <span key={i}>{columnSelect(`Count for ≤ ${bound}`, h.count_columns?.[i], path => set({ count_columns: (h.count_columns ?? []).map((existing, j) => j === i ? path : existing) }))}</span>)}<p>This column holds the count for the last bound ({h.bounds[h.bounds.length - 1] ?? '—'}).</p></>}
  </>
}
