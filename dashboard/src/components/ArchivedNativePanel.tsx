import { browserDocumentPanelMetrics, nativeByteMetrics, nativePopulationMetrics, nativeJavaHTTPMetrics } from './nativeAnalyticsModel'
import { useEffect, useState } from 'react'
import { Line, LineChart, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import { request } from '../hooks/apiClient'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'
import ExportableChart from './ExportableChart'
import TransactionBucketInspector from './TransactionBucketInspector'

interface Bucket { from: string; to: string; offset_seconds: number; count: number; value: number | null; reason?: string }
interface Series { run_id: string; unit?: string; java_http_compression?: string; buckets: Bucket[]; warnings: string[]; identities: string[] }
interface Result { current: Series; baseline?: Series; comparable: boolean; warnings: string[]; added: string[]; removed: string[]; derivation?: { operation: string } }

// missingLabel names why a bucket has no value, when the server says.
const missingLabel = (bucket?: Pick<Bucket, 'reason'>) => bucket?.reason ? `Missing (${bucket.reason.replace(/_/g, ' ')})` : 'Missing'

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
  const metric = panel.query?.metric || ''
  // Browser document panels read final document measurements: their
  // percentiles are bucket estimates and their buckets hold documents, not
  // transactions, so the transaction inspector does not apply.
  const documents = panel.query?.population === 'browser_document'
  // Message panels count received messages: no latency basis and no
  // transaction inspector.
  const messages = panel.query?.population === 'message'
  const javaHTTP = panel.query?.population === 'java_http_action'
  // Byte panels read HTTP actions' body bytes from raw observations.
  const bytes = panel.query?.population === 'byte'
  const inspectable = !documents && !messages && !javaHTTP && !bytes
  // A derived series carries its own unit; its baseline already entered the
  // derivation, so the raw baseline is not plotted beside it.
  const derived = result.derivation?.operation
  const unit = result.current.unit || nativePopulationMetrics[metric]?.unit || browserDocumentPanelMetrics[metric]?.unit || nativeJavaHTTPMetrics[metric]?.unit || nativeByteMetrics[metric]?.unit || 'observations'
  const baseline = derived ? undefined : result.baseline
  // Byte and Java panels count their own measured observations and say why a
  // bucket is unscored, in the table and the export.
  const measured = javaHTTP || bytes
  const basis = bytes ? 'HTTP body bytes on the wire: compressed bodies count compressed, embedded resources are included, and a response served from the cache receives nothing. Headers and framing are not counted. Each request counts in the bucket where it started. Sums and rates need complete buckets; percentiles use nearest-rank raw requests. Requests archived without byte evidence are unavailable.' : javaHTTP ? 'Representation measurements use their own observed counts; paired ratios divide sums from the same samples. Percentiles use nearest-rank raw measurements. Raw transaction drill-down is unavailable for this measurement population.' : documents ? 'Each document counts once at its final measurement; percentiles are bucket estimates (the upper bound of the histogram bucket holding them).' : messages ? 'Each received message counts once.' : 'Latency percentiles use nearest-rank raw observations.'
  // A bucket without a value or a server reason had nothing to measure.
  const unmeasured = javaHTTP ? 'measurement_unavailable' : bytes ? 'no_requests' : undefined
  const rows = result.current.buckets.map((bucket, i) => ({ ...bucket, reason: bucket.reason || (bucket.value == null ? unmeasured : undefined), baseline: baseline?.buckets[i]?.value ?? null, baselineCount: baseline?.buckets[i]?.count, baselineReason: baseline?.buckets[i]?.reason || (baseline?.buckets[i] && baseline.buckets[i].value == null ? unmeasured : undefined) }))
  const select = (run: string, bucket: Bucket) => { setSelection({ run, bucket }) }
  return <section aria-label="Filtered archive panel">
    <p>{javaHTTP ? 'Java action representations' : bytes ? 'HTTP requests' : documents ? 'browser documents' : messages ? 'received messages' : `${panel.query?.population} observations`} · {panel.query?.metric} · {panel.query?.aggregation}{derived ? ` · ${derived.replace(/_/g, ' ')}` : ''} ({unit}). Elapsed wall time; gaps remain missing. {basis}</p>
    {javaHTTP && <p>Prepared-body compression: current {result.current.java_http_compression || 'Unavailable'}{baseline && ` · baseline ${baseline.java_http_compression || 'Unavailable'}`}.</p>}
    <p>Selection: {panel.query?.filters?.action || 'All identities'}{panel.query?.property_name ? ` · ${panel.query.property_name}=${panel.query.property_value}` : ''} · {panel.query?.start_seconds || 0}–{panel.query?.end_seconds}s · {panel.query?.bucket_seconds}s buckets{panel.query?.comparison_run_id ? ` · Comparison run: ${panel.query.comparison_run_id}` : ''}</p>
    {[...result.warnings, ...result.current.warnings, ...(result.baseline?.warnings || [])].map((warning, i) => <p role="status" key={i}>{warning}</p>)}
    {result.baseline && <p>Comparison: {result.comparable ? 'Compatible recorded workload/version' : 'Compatibility not established'} · Added identities: {result.added.join(', ') || 'none'} · Removed identities: {result.removed.join(', ') || 'none'}</p>}
    {panel.kind === 'stat' && <p>Latest: {rows[rows.length - 1]?.value ?? missingLabel(rows[rows.length - 1])} {unit}{baseline && ` · Baseline: ${rows[rows.length - 1]?.baseline ?? 'Missing'} ${unit}`}</p>}
    <ExportableChart definitionId={panel.id} allowSVG={panel.kind === 'line'} title={panel.title} description={javaHTTP ? `Java representation measurements. ${basis}` : "Filtered archived raw observations"} rows={rows} columns={[{ key: 'offset', label: 'Elapsed seconds', value: row => row.offset_seconds }, { key: 'value', label: `Current (${unit})`, value: row => row.value ?? '' }, ...(baseline ? [{ key: 'baseline', label: `Baseline (${unit})`, value: (row: typeof rows[number]) => row.baseline ?? '' }] : []), ...(measured ? [{ key: 'count', label: 'Measured observations', value: (row: typeof rows[number]) => row.count }, { key: 'reason', label: 'Availability', value: (row: typeof rows[number]) => row.reason || 'measured' }, ...(javaHTTP ? [{ key: 'compression', label: 'Current prepared-body compression', value: () => result.current.java_http_compression || 'Unavailable' }] : []), ...(baseline ? [{ key: 'baselineCount', label: 'Baseline measured observations', value: (row: typeof rows[number]) => row.baselineCount ?? '' }, { key: 'baselineReason', label: 'Baseline availability', value: (row: typeof rows[number]) => row.baselineReason || 'measured' }, ...(javaHTTP ? [{ key: 'baselineCompression', label: 'Baseline prepared-body compression', value: () => baseline.java_http_compression || 'Unavailable' }] : [])] : [])] : [])]}>
      {panel.kind === 'line' && <ResponsiveContainer width="100%" height={240}><LineChart data={rows} onClick={state => { const index = Number(state.activeTooltipIndex); if (inspectable && state.activeTooltipIndex != null && Number.isInteger(index) && rows[index]?.count) select(result.current.run_id, rows[index]) }}><XAxis dataKey="offset_seconds" /><YAxis /><Tooltip /><Line name={`Current (${unit})`} dataKey="value" stroke="var(--chart-5)" connectNulls={false} dot={false} isAnimationActive={false} />{baseline && <Line name={`Baseline (${unit})`} dataKey="baseline" stroke="var(--chart-3)" connectNulls={false} dot={false} isAnimationActive={false} />}</LineChart></ResponsiveContainer>}
    </ExportableChart>
    <details><summary>Inspect time buckets ({rows.length})</summary><table><thead><tr><th>Elapsed seconds</th><th>Current ({unit})</th>{baseline && <th>Baseline ({unit})</th>}<th>{measured ? 'Measured observations' : documents ? 'Final documents' : messages ? 'Received messages' : 'Raw observations'}</th>{measured && baseline && <th>Baseline measured observations</th>}</tr></thead><tbody>{rows.map((row, i) => <tr key={row.from}><td>{row.offset_seconds}</td><td>{row.value ?? missingLabel(row)}</td>{baseline && <td>{row.baseline ?? (measured ? missingLabel({ reason: row.baselineReason }) : 'Missing')}</td>}<td>{!inspectable ? row.count : <><button disabled={!row.count} onClick={() => select(result.current.run_id, row)}>Inspect current bucket {row.offset_seconds}s ({row.count})</button>{baseline && <button disabled={!baseline.buckets[i]?.count} onClick={() => select(baseline.run_id, baseline.buckets[i])}>Inspect baseline bucket {row.offset_seconds}s ({baseline.buckets[i]?.count || 0})</button>}</>}</td>{measured && baseline && <td>{row.baselineCount ?? 'Unavailable'}</td>}</tr>)}</tbody></table></details>
    {selection && inspectable && panel.query && <TransactionBucketInspector key={`${selection.run}-${selection.bucket.from}`} query={panel.query} selection={{ run: selection.run, from: selection.bucket.from, to: selection.bucket.to }} onClose={() => setSelection(null)} />}
  </section>
}
