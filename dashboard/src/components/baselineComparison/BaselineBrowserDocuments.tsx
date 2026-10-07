import { number, type BrowserDocumentComparison, type RunIdentity } from './model'

const reasons: Record<string, string> = {
  algorithm_mismatch: 'Different measurement algorithm',
  unit_mismatch: 'Different unit',
  lifecycle_phase_mismatch: 'Different lifecycle phase',
  frame_scope_mismatch: 'Different frame scope',
  engine_mismatch: 'Different browser engine',
  device_profile_mismatch: 'Different device profile',
  cohort_mismatch: 'Different actual runtime cohort',
  baseline_unmatched: 'No baseline tuple',
  current_unmatched: 'No current tuple',
  insufficient_final_documents: 'No qualified final documents',
}
const tuple = (row: BrowserDocumentComparison) => [row.engine, `profile ${row.device_profile || 'not recorded'}`, row.frame_scope === 'main_frame' ? 'main frame' : 'child frame', row.lifecycle_phase === 'navigation' ? 'navigation' : 'BFCache restore', row.group && `group ${row.group}`, row.region && `region ${row.region}`, row.cohort && `cohort ${row.cohort}`].filter(Boolean).join(' · ')
const value = (v: number | undefined, unit: string) => v === undefined ? 'Unavailable' : `${number(v)} ${unit}`

export default function BaselineBrowserDocuments({ rows, baselines, softNavigation }: { rows?: BrowserDocumentComparison[]; baselines: RunIdentity[]; softNavigation?: string }) {
  if (!rows?.length) return null
  return <section className="run-report-panel" aria-label="Browser document comparison"><h2>Browser documents</h2>
    <p>p75 of qualified final document measurements, one per document lifecycle metric. Tuples are compared only when algorithm, unit, lifecycle phase, frame scope, engine, device profile and actual runtime cohort all match. These rows are not part of the regression verdict, and legacy action summaries are not compared here.</p>
    <p>Soft (same-document) navigations: {softNavigation || 'unsupported'} — they are not measured as navigations.</p>
    <div className="bc-table-scroll"><table><thead><tr><th scope="col">Metric tuple</th><th scope="col">Baseline</th><th scope="col">Current p75</th><th scope="col">Baseline p75</th><th scope="col">Comparison</th></tr></thead><tbody>
      {rows.map((row, i) => <tr key={i}><th scope="row">{row.metric.toUpperCase()} ({row.unit})<small className="bc-document-tuple">{tuple(row)}</small></th><td>{baselines.find(run => run.id === row.baseline_id)?.name || row.baseline_id}</td>
        <td>{value(row.current_p75, row.unit)}<small className="bc-document-tuple">{row.current_final.toLocaleString('en-US')} final documents</small></td>
        <td>{value(row.baseline_p75, row.unit)}<small className="bc-document-tuple">{row.baseline_final.toLocaleString('en-US')} final documents</small></td>
        <td>{row.comparable && row.delta_p75 !== undefined ? `${row.delta_p75 > 0 ? '+' : ''}${number(row.delta_p75)} ${row.unit}` : `Not compared: ${reasons[row.reason || ''] || row.reason || 'unavailable'}`}</td></tr>)}
    </tbody></table></div>
  </section>
}
