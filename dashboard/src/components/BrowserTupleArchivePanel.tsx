import './BrowserTupleArchivePanel.css'
import { Line, LineChart, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'
import ExportableChart from './ExportableChart'

interface Bucket { from: string; to: string; offset_seconds: number; count: number; value: number | null; reason?: string }
interface Tuple { id: string; label: string; cohort_digest: string; measurement: { metric: string; unit: string; algorithm: string; algorithm_sha256: string; engine: string; device_profile?: string; group?: string; region?: string; frame_scope: string; lifecycle_phase: string }; buckets: Bucket[] }
export interface BrowserTupleResult { version: number; tuples: Tuple[] }
interface Series { run_id: string; browser_tuples?: BrowserTupleResult; warnings: string[]; excluded?: Record<string, number> }
interface Comparison { tuple_id: string; status: string; reason?: string }

export default function BrowserTupleArchivePanel({ panel, unit, current, baseline, comparisons = [], warnings }: { panel: DashboardDefinitionPanel; unit: string; current: Series; baseline?: Series; comparisons?: Comparison[]; warnings: string[] }) {
  if (current.browser_tuples?.version !== 1 || baseline && baseline.browser_tuples?.version !== 1) return <p role="alert">Separate browser cohort results are unavailable from this server.</p>
  const currentTuples = current.browser_tuples.tuples
  const known = new Set(currentTuples.map(tuple => tuple.id))
  const baselineOnly = baseline?.browser_tuples?.tuples.filter(tuple => !known.has(tuple.id)) ?? []
  const tuples = [...currentTuples, ...baselineOnly]
  return <section className="browser-tuple-archive-panel" aria-label="Separate browser cohort panel">
    <p>{panel.query?.metric} · {panel.query?.aggregation} ({unit}). Each document counts once at its final measurement. Percentiles are bucket estimates. Different measurement tuples remain separate; missing observations are not zero.</p>
    <p>Current run: {current.run_id}{baseline && ` · Baseline run: ${baseline.run_id}`}. {panel.query?.start_seconds || 0}–{panel.query?.end_seconds}s · {panel.query?.bucket_seconds}s buckets.</p>
    {panel.query?.browser_inputs && <details><summary>Frozen archive inputs</summary><p>Query: {panel.query.browser_inputs.query_sha256}</p><p>Current publication: {panel.query.browser_inputs.current.manifest_sha256}</p>{panel.query.browser_inputs.baseline && <p>Baseline publication: {panel.query.browser_inputs.baseline.manifest_sha256}</p>}{panel.query.browser_inputs.selection_receipt_sha256 && <p>Selection receipt: {panel.query.browser_inputs.selection_receipt_sha256}</p>}{!!panel.query.browser_inputs.policy_pins?.length && <p>Policy pins: {panel.query.browser_inputs.policy_pins.map(pin => `${pin.kind} ${pin.id}@${pin.revision} (${pin.sha256})`).join(' · ')}</p>}<p>These inputs remain fixed when this panel is opened from another run. They do not establish baseline eligibility.</p></details>}
    {[...warnings, ...current.warnings, ...(baseline?.warnings ?? [])].map((warning, index) => <p role="status" key={index}>{warning}</p>)}
    {Object.entries(current.excluded ?? {}).map(([reason, count]) => <p key={reason}>Excluded current measurements: {reason.replace(/_/g, ' ')} ({count})</p>)}
    {Object.entries(baseline?.excluded ?? {}).map(([reason, count]) => <p key={reason}>Excluded baseline measurements: {reason.replace(/_/g, ' ')} ({count})</p>)}
    {!currentTuples.length && <p>No final eligible browser measurements match the current selection.</p>}
    {tuples.map(tuple => {
      const comparison = comparisons.find(item => item.tuple_id === tuple.id)
      const match = baseline?.browser_tuples?.tuples.find(item => item.id === tuple.id)
      const hasCurrent = known.has(tuple.id)
      const rows = tuple.buckets.map(bucket => {
        const other = match?.buckets.find(item => item.offset_seconds === bucket.offset_seconds)
        return { ...bucket, value: hasCurrent ? bucket.value : null, count: hasCurrent ? bucket.count : null, baselineValue: other?.value ?? null, baselineCount: other?.count ?? null }
      })
      return <section key={tuple.id} aria-label={tuple.label}>
        <h4>{tuple.label}</h4>{!hasCurrent && <p>Baseline-only tuple: {tuple.label}. Current evidence unavailable.</p>}<p>Tuple: {tuple.id} · Cohort: {tuple.cohort_digest}</p>
        {baseline && <p>Comparison: {comparison?.status === 'comparable' ? 'Compatible recorded workload/version for this tuple' : `Unavailable (${comparison?.reason?.replace(/_/g, ' ') || 'compatibility not established'})`}. This is a population comparison, not a baseline anomaly verdict.</p>}
        {panel.kind === 'stat' && <p>Latest: {rows[rows.length - 1]?.value ?? 'Missing'} {unit}</p>}
        <ExportableChart definitionId={`${panel.id}-${tuple.id}`} allowSVG={panel.kind === 'line'} title={`${panel.title} · ${tuple.label}`} description="Separate final browser document measurements; no pooled percentiles." rows={rows} columns={[
          ...(panel.query?.browser_inputs ? [
            { key: 'queryPin', label: 'Query SHA-256', value: () => panel.query!.browser_inputs!.query_sha256 },
            { key: 'currentPin', label: 'Current archive SHA-256', value: () => panel.query!.browser_inputs!.current.manifest_sha256 },
            { key: 'baselinePin', label: 'Baseline archive SHA-256', value: () => panel.query!.browser_inputs!.baseline?.manifest_sha256 ?? '' },
            ...(panel.query.browser_inputs.version === 2 ? [
              { key: 'selectionReceiptPin', label: 'Selection receipt SHA-256', value: () => panel.query!.browser_inputs!.selection_receipt_sha256 ?? '' },
              { key: 'policyPins', label: 'Policy pins', value: () => (panel.query!.browser_inputs!.policy_pins ?? []).map(pin => `${pin.kind}:${pin.id}@${pin.revision}:${pin.sha256}`).join(';') },
            ] : []),
          ] : []),
          { key: 'tuple', label: 'Tuple', value: () => tuple.id }, { key: 'cohort', label: 'Cohort', value: () => tuple.cohort_digest },
          { key: 'run', label: 'Current run', value: () => current.run_id }, { key: 'offset', label: 'Elapsed seconds', value: row => row.offset_seconds },
          { key: 'metric', label: 'Metric', value: () => tuple.measurement.metric }, { key: 'algorithm', label: 'Algorithm', value: () => tuple.measurement.algorithm_sha256 },
          { key: 'value', label: `Current (${unit})`, value: row => row.value ?? '' }, { key: 'count', label: 'Current final documents', value: row => row.count ?? '' },
          ...(baseline ? [{ key: 'baselineRun', label: 'Baseline run', value: () => baseline.run_id }, { key: 'baselineValue', label: `Baseline (${unit})`, value: (row: typeof rows[number]) => row.baselineValue ?? '' }, { key: 'baselineCount', label: 'Baseline final documents', value: (row: typeof rows[number]) => row.baselineCount ?? '' }, { key: 'compatibility', label: 'Comparison', value: () => comparison?.reason || comparison?.status || 'unavailable' }] : [])
        ]}>
          {panel.kind === 'line' && <ResponsiveContainer width="100%" height={240}><LineChart data={rows}><XAxis dataKey="offset_seconds" /><YAxis /><Tooltip /><Line name={`Current (${unit})`} dataKey="value" stroke="var(--chart-5)" connectNulls={false} dot={false} isAnimationActive={false} />{match && <Line name={`Baseline (${unit})`} dataKey="baselineValue" stroke="var(--chart-3)" connectNulls={false} dot={false} isAnimationActive={false} />}</LineChart></ResponsiveContainer>}
        </ExportableChart>
        <details><summary>Inspect tuple time buckets ({rows.length})</summary><div className="browser-tuple-table"><table><thead><tr><th>Elapsed seconds</th><th>Current ({unit})</th><th>Current final documents</th>{baseline && <><th>Baseline ({unit})</th><th>Baseline final documents</th></>}</tr></thead><tbody>{rows.map(row => <tr key={row.from}><td>{row.offset_seconds}</td><td>{row.value ?? 'Missing'}</td><td>{row.count ?? 'Missing'}</td>{baseline && <><td>{row.baselineValue ?? 'Missing'}</td><td>{row.baselineCount ?? 'Missing'}</td></>}</tr>)}</tbody></table></div></details>
      </section>
    })}
  </section>
}
