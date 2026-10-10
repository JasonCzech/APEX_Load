import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { RunMonitorJob } from '../types/runMonitoring'
import type { ElasticRecord } from '../types/elastic'
import ElasticEvidenceTable, { ElasticTraceWaterfall } from './ElasticEvidenceTable'
import { elasticEvidenceTimeError } from './elasticEvidenceModel'
import { formatDateTime } from '../utils/dateTime'

interface EvidencePage { evidence: ElasticRecord[]; has_more: boolean; after_key: string }
export default function ElasticRunEvidence({ testId, jobs, sourceId }: { testId: string; jobs: RunMonitorJob[]; sourceId?: string }) {
  const selected = jobs.filter(job => job.source.pack === 'elastic' && (!sourceId || job.source.id === sourceId))
  const sourceKey = selected.map(job => job.source.id).join(',')
  const [page, setPage] = useState<EvidencePage>()
  const [error, setError] = useState('')
  const [cursor, setCursor] = useState('')
  const [evidenceSource, setEvidenceSource] = useState('')
  const evidenceSourceId = sourceId || selected.find(job => job.source.id === evidenceSource)?.source.id || selected[0]?.source.id || ''
  const [filters, setFilters] = useState({ kind: '', service: '', severity: '', trace_id: '', from: '', until: '' })
  const [applied, setApplied] = useState(filters)
  const [refresh, setRefresh] = useState(0)
  const collecting = selected.some(job => ['collecting', 'reconciling', 'draining'].includes(job.state))
  useEffect(() => { setPage(undefined); setCursor(''); setError('') }, [testId, evidenceSourceId, applied])
  useEffect(() => { if (!collecting) return; const timer = setInterval(() => setRefresh(value => value + 1), 15000); return () => clearInterval(timer) }, [collecting, sourceKey, testId])
  useEffect(() => {
    if (!sourceKey) return
    const controller = new AbortController()
    const query = new URLSearchParams({ after_key: cursor, limit: '100' })
    query.set('source_id', evidenceSourceId)
    for (const [name, value] of Object.entries(applied)) if (value.trim()) query.set(name, value.trim())
    void request<EvidencePage>(`/tests/${encodeURIComponent(testId)}/monitor-evidence?${query}`, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) { setPage(result); setError('') } }).catch(err => { if (!controller.signal.aborted) setError(String(err)) })
    return () => controller.abort()
  }, [testId, evidenceSourceId, sourceKey, applied, cursor, refresh])
  if (!selected.length) return null
  const evidence = (page?.evidence || []).filter(record => record.provider === 'elasticsearch' || record.provider === 'opensearch' || selected.some(job => job.source.id === record.source_id))
  return <section className="elastic-evidence" aria-label="Elastic run evidence"><h4>Elasticsearch / OpenSearch evidence</h4>
    <p>Retained records include the full approved scalar projection within quotas. Time or service correlation can include unrelated activity. Missing evidence is not zero activity.</p>
    {!sourceId && <label>Elastic evidence source<select value={evidenceSourceId} onChange={event => setEvidenceSource(event.target.value)}>{selected.map(job => <option value={job.source.id} key={job.id}>{job.source.name}</option>)}</select></label>}
    {selected.map(job => <div key={job.id}><p role="status"><strong>{job.source.name}</strong> · {job.state} · source revision {job.source.revision} · {job.source.executor_mode || 'direct'}{job.coverage_reason ? ` · ${job.coverage_reason}` : ''}</p><p>Last successful collection: {job.last_success_at ? formatDateTime(job.last_success_at, { seconds: true }) : 'Not recorded'} · Watermark: {job.query_watermark || 'Not recorded'}{job.query_watermark ? ` · lag ${Math.max(0, Math.round((Date.now() - Date.parse(job.query_watermark)) / 1000))}s` : ''}{job.drain_deadline ? ` · final reconciliation deadline ${job.drain_deadline}` : ''}</p><p>Captured query definitions: {(job.source.elastic?.queries || []).map(query => `${query.name} (${query.language}/${query.kind})`).join(', ') || 'Mapped signals and metrics'}.</p>{job.coverage && <ul>{Object.entries(job.coverage).map(([name, capability]) => <li key={name}>{name}: {capability.status}{capability.truncated ? ' · truncated' : ''}{capability.message ? ` · ${capability.message}` : ''}{'reason' in capability && capability.reason ? ` · ${capability.reason}` : ''}</li>)}</ul>}</div>)}
    <fieldset><legend>Retained record filters</legend><label>Elastic evidence kind<select value={filters.kind} onChange={event => setFilters({ ...filters, kind: event.target.value })}><option value="">All retained records</option>{['log', 'error', 'span', 'transaction', 'health'].map(kind => <option key={kind}>{kind}</option>)}</select></label>{(['service', 'severity', 'trace_id', 'from', 'until'] as const).map(field => <label key={field}>Elastic evidence {field.replace(/_/g, ' ')}<input value={filters[field]} placeholder={field === 'from' || field === 'until' ? 'UTC RFC3339 timestamp' : undefined} onChange={event => setFilters({ ...filters, [field]: event.target.value })} /></label>)}<button type="button" onClick={() => { const invalidTime = elasticEvidenceTimeError(filters.from.trim(), filters.until.trim()); if (invalidTime) { setError(invalidTime); return } setCursor(''); setApplied({ ...filters }); setRefresh(value => value + 1) }}>Refresh Elastic evidence</button></fieldset>
    {error && <p role="alert">Elastic evidence unavailable: {error}</p>}
    <ElasticEvidenceTable evidence={evidence} /><ElasticTraceWaterfall evidence={evidence} />
    {page?.has_more && <button type="button" disabled={!page.after_key || page.after_key === cursor} onClick={() => setCursor(page.after_key)}>Next Elastic evidence page</button>}
    {cursor && <button type="button" onClick={() => setCursor('')}>First Elastic evidence page</button>}
  </section>
}
