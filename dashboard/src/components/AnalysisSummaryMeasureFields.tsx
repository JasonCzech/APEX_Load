import type { AnalysisColumn, AnalysisMeasure } from '../types/analysis'
import { analysisProtectedIdentityColumn } from '../utils/analysisCredentialColumn'

/** Statistic, source window and optional sample columns of a summary measure. */
export default function AnalysisSummaryMeasureFields({ measure, columns, statistics, onChange }: { measure: AnalysisMeasure; columns: AnalysisColumn[]; statistics: string[]; onChange: (values: Partial<AnalysisMeasure>) => void }) {
  const columnSelect = (label: string, value: string[] | undefined, set: (path: string[] | undefined) => void) => <label>{label}<select value={JSON.stringify(value ?? [])} onChange={e => { const path = JSON.parse(e.target.value) as string[]; set(path.length ? path : undefined) }}>
    <option value="[]">Not provided</option>
    {columns.filter(c => JSON.stringify(c.path_components) !== JSON.stringify(measure.value)).map(c => <option key={c.path} disabled={analysisProtectedIdentityColumn(c.path_components)} value={JSON.stringify(c.path_components)}>{c.path}{analysisProtectedIdentityColumn(c.path_components) ? ' (protected)' : ''}</option>)}
  </select></label>
  return <>
    <label>Statistic reported<select value={measure.statistic ?? ''} onChange={e => onChange({ statistic: e.target.value || undefined })}><option value="">Choose…</option>{statistics.map(v => <option key={v}>{v}</option>)}</select></label>
    <label>Source window (seconds)<input type="number" min={1} max={86400} step={1} value={measure.window_seconds ?? ''} onChange={e => onChange({ window_seconds: e.target.value === '' ? undefined : Number(e.target.value) })} /></label>
    {columnSelect('Sample count column', measure.sample_count_column, path => onChange(path ? { sample_count_column: path } : { sample_count_column: undefined, sample_sum_column: undefined }))}
    {measure.sample_count_column?.length ? columnSelect('Sample sum column', measure.sample_sum_column, path => onChange({ sample_sum_column: path })) : null}
  </>
}
