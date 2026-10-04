import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { AppDynamicsEvidence, RunMonitorJob } from '../types/appdynamics'

function diagnosticSeenTime(item: AppDynamicsEvidence, field: string): string | undefined {
  const value = item.properties?.[field]
  return value && value.length <= 64 && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value) && Number.isFinite(Date.parse(value)) ? value : undefined
}
export function PrometheusDiagnosticTable({ evidence }: { evidence: AppDynamicsEvidence[] }) {
  const hasSeenTimes = evidence.some(item => diagnosticSeenTime(item, 'first_observed_utc') || diagnosticSeenTime(item, 'last_observed_utc'))
  return <table aria-label="Prometheus health evidence"><caption>States are sampled observations. No longer observed means absent from a later successful scoped snapshot; it does not establish historical resolution.</caption><thead><tr><th>Time</th><th>Kind</th><th>State</th>{hasSeenTimes && <><th>First seen</th><th>Last seen</th></>}<th>Summary</th><th>Correlation</th></tr></thead><tbody>{evidence.map((item, index) => <tr key={`${item.source_id}:${item.id || index}`}><td>{item.timestamp_utc || item.timestamp || '—'}</td><td>{item.kind || item.type || '—'}</td><td>{item.status || item.severity || '—'}</td>{hasSeenTimes && <><td>{diagnosticSeenTime(item, 'first_observed_utc') || '—'}</td><td>{diagnosticSeenTime(item, 'last_observed_utc') || '—'}</td></>}<td>{item.summary || item.message || item.title || '—'}</td><td>{item.correlation || item.correlation_strength || 'entity_time'}</td></tr>)}</tbody></table>
}
export default function PrometheusRunEvidence({ jobs, sourceId, testId }: { jobs: RunMonitorJob[]; sourceId?: string; testId?: string }) {
  const selected = jobs.filter(job => job.source.pack === 'prometheus-query' && (!sourceId || job.source.id === sourceId))
  const sourceKey = selected.map(job => job.source.id).join(',')
  const [page, setPage] = useState<{ evidence: AppDynamicsEvidence[]; has_more: boolean; after_key?: string }>()
  const [cursor, setCursor] = useState('')
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState('')
  useEffect(() => { setPage(undefined); setCursor(''); setError('') }, [testId, sourceId])
  const collecting = selected.some(job => ['collecting', 'draining', 'reconciling'].includes(job.state))
  useEffect(() => { if (!collecting) return; const timer = setInterval(() => setRevision(value => value + 1), 15000); return () => clearInterval(timer) }, [collecting])
  useEffect(() => {
    if (!testId || !sourceKey) return
    const controller = new AbortController()
    const query = new URLSearchParams({ limit: '100', after_key: cursor })
    if (sourceId) query.set('source_id', sourceId)
    void request<{ evidence: AppDynamicsEvidence[]; has_more: boolean; after_key?: string }>(`/tests/${encodeURIComponent(testId)}/monitor-evidence?${query}`, { signal: controller.signal }).then(value => { if (!controller.signal.aborted) { setPage(value); setError('') } }).catch(err => { if (!controller.signal.aborted) setError(String(err)) })
    return () => controller.abort()
  }, [testId, sourceKey, sourceId, cursor, revision])
  if (!selected.length) return null
  const evidence = page?.evidence.filter(item => !item.source_id || selected.some(job => job.source.id === item.source_id)) || []
  return <section aria-label="Prometheus run evidence"><h4>Prometheus target-system evidence</h4>{selected.map(job => <div key={job.id}><p role="status"><strong>{job.source.name}</strong>: {['draining', 'reconciling'].includes(job.state) ? 'Load finished; collecting delayed Prometheus data' : job.state === 'stopped' ? 'Collection finished' : job.state === 'incomplete' ? 'Collection incomplete' : job.state}{job.coverage_reason ? ` · ${job.coverage_reason}` : ''}</p><p>Last successful collection: {job.last_success_at || 'Not recorded'}. Query watermark: {job.query_watermark || 'Not recorded'}{job.drain_deadline ? `. Final collection deadline: ${job.drain_deadline}` : ''}.</p>{job.coverage && <ul>{Object.entries(job.coverage).map(([query, health]) => <li key={query}>{query}: {health.status}{health.truncated ? ' · truncated' : ''}{health.freshness ? ` · source freshness ${health.freshness}` : ''}{health.expected_points !== undefined ? ` · ${health.points || 0}/${health.expected_points} evaluation slots` : ''}{health.message ? ` · ${health.message}` : ''}{health.warnings?.map((warning, index) => <p key={`w${index}`}>Upstream warning: {warning}</p>)}{health.infos?.map((info, index) => <p key={`i${index}`}>Upstream information: {info}</p>)}</li>)}</ul>}</div>)}<p>Evaluation timestamps align with the test window. API success and point presence do not prove target freshness. Labels and time select all activity in that scope; correlation does not establish causation.</p>{error && <p role="alert">Monitoring evidence unavailable: {error}</p>}{evidence.length > 0 && <PrometheusDiagnosticTable evidence={evidence} />}{page?.has_more && page.after_key && <button type="button" onClick={() => setCursor(page.after_key || '')}>Next Prometheus evidence page</button>}</section>
}
