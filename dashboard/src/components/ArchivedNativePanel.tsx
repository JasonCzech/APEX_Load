import { nativePopulationMetrics } from './nativeAnalyticsModel'
import { useEffect, useState } from 'react'
import { Line, LineChart, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import { request } from '../hooks/apiClient'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'
import ExportableChart from './ExportableChart'
import TransactionBucketInspector from './TransactionBucketInspector'

interface Bucket { from: string; to: string; offset_seconds: number; count: number; value: number | null }
interface Series { run_id: string; buckets: Bucket[]; warnings: string[]; identities: string[] }
interface Result { current: Series; baseline?: Series; comparable: boolean; warnings: string[]; added: string[]; removed: string[] }

export default function ArchivedNativePanel({ panel, testId }: { panel: DashboardDefinitionPanel; testId: string }) {
  const [revision, setRevision] = useState(0)
  const encoded = JSON.stringify(panel.query)
  const [result, setResult] = useState<Result | null>(null)
  const [error, setError] = useState('')
  const [selection, setSelection] = useState<{ run: string; bucket: Bucket } | null>(null)
  useEffect(() => {
    const abort = new AbortController()
    setResult(null); setError(''); setSelection(null)
    request<Result>(`/tests/${encodeURIComponent(testId)}/archive/native-series`, { method: 'POST', body: encoded, signal: abort.signal, timeoutMs: 35000 })
      .then(value => { if (!abort.signal.aborted) setResult(value) })
      .catch(reason => { if (!abort.signal.aborted) setError(reason instanceof Error ? reason.message : 'Archive query failed') })
    return () => abort.abort()
  }, [testId, encoded, revision])
  if (error) return <div><p role="alert">Archive panel unavailable: {error}</p><button onClick={() => setRevision(revision + 1)}>Retry archive panel</button></div>
  if (!result) return <p role="status">Loading archived observations…</p>
  const unit = nativePopulationMetrics[panel.query?.metric || '']?.unit || 'observations'
  const rows = result.current.buckets.map((bucket, i) => ({ ...bucket, baseline: result.baseline?.buckets[i]?.value ?? null }))
  const select = (run: string, bucket: Bucket) => { setSelection({ run, bucket }) }
  return <section aria-label="Filtered archive panel">
    <p>{panel.query?.population} observations · {panel.query?.metric} · {panel.query?.aggregation} ({unit}). Elapsed wall time; gaps remain missing. Latency percentiles use nearest-rank raw observations.</p>
    <p>Selection: {panel.query?.filters?.action || 'All identities'}{panel.query?.property_name ? ` · ${panel.query.property_name}=${panel.query.property_value}` : ''} · {panel.query?.start_seconds || 0}–{panel.query?.end_seconds}s · {panel.query?.bucket_seconds}s buckets{panel.query?.comparison_run_id ? ` · Comparison run: ${panel.query.comparison_run_id}` : ''}</p>
    {[...result.warnings, ...result.current.warnings, ...(result.baseline?.warnings || [])].map((warning, i) => <p role="status" key={i}>{warning}</p>)}
    {result.baseline && <p>Comparison: {result.comparable ? 'Compatible recorded workload/version' : 'Compatibility not established'} · Added identities: {result.added.join(', ') || 'none'} · Removed identities: {result.removed.join(', ') || 'none'}</p>}
    {panel.kind === 'stat' && <p>Latest: {rows[rows.length - 1]?.value ?? 'Missing'} {unit}{result.baseline && ` · Baseline: ${rows[rows.length - 1]?.baseline ?? 'Missing'} ${unit}`}</p>}
    <ExportableChart definitionId={panel.id} allowSVG={panel.kind === 'line'} title={panel.title} description="Filtered archived raw observations" rows={rows} columns={[{ key: 'offset', label: 'Elapsed seconds', value: row => row.offset_seconds }, { key: 'value', label: `Current (${unit})`, value: row => row.value ?? '' }, ...(result.baseline ? [{ key: 'baseline', label: `Baseline (${unit})`, value: (row: typeof rows[number]) => row.baseline ?? '' }] : [])]}>
      {panel.kind === 'line' && <ResponsiveContainer width="100%" height={240}><LineChart data={rows} onClick={state => { const index = Number(state.activeTooltipIndex); if (state.activeTooltipIndex != null && Number.isInteger(index) && rows[index]?.count) select(result.current.run_id, rows[index]) }}><XAxis dataKey="offset_seconds" /><YAxis /><Tooltip /><Line name={`Current (${unit})`} dataKey="value" stroke="#38bdf8" connectNulls={false} dot={false} isAnimationActive={false} />{result.baseline && <Line name={`Baseline (${unit})`} dataKey="baseline" stroke="#f59e0b" connectNulls={false} dot={false} isAnimationActive={false} />}</LineChart></ResponsiveContainer>}
    </ExportableChart>
    <details><summary>Inspect time buckets ({rows.length})</summary><table><thead><tr><th>Elapsed seconds</th><th>Current ({unit})</th>{result.baseline && <th>Baseline ({unit})</th>}<th>Raw observations</th></tr></thead><tbody>{rows.map((row, i) => <tr key={row.from}><td>{row.offset_seconds}</td><td>{row.value ?? 'Missing'}</td>{result.baseline && <td>{row.baseline ?? 'Missing'}</td>}<td><button disabled={!row.count} onClick={() => select(result.current.run_id, row)}>Inspect current bucket {row.offset_seconds}s ({row.count})</button>{result.baseline && <button disabled={!result.baseline.buckets[i]?.count} onClick={() => select(result.baseline!.run_id, result.baseline!.buckets[i])}>Inspect baseline bucket {row.offset_seconds}s ({result.baseline.buckets[i]?.count || 0})</button>}</td></tr>)}</tbody></table></details>
    {selection && panel.query && <TransactionBucketInspector key={`${selection.run}-${selection.bucket.from}`} query={panel.query} selection={{ run: selection.run, from: selection.bucket.from, to: selection.bucket.to }} onClose={() => setSelection(null)} />}
  </section>
}
