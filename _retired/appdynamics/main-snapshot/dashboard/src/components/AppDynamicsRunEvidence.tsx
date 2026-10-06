import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { AppDynamicsEvidence, RunMonitorJob } from '../types/appdynamics'
import AppDynamicsEvidenceTable from './AppDynamicsEvidenceTable'
import { monitorJobState } from './appDynamicsConnectionModel'

interface EvidencePage { evidence: AppDynamicsEvidence[]; has_more: boolean; after_key: string }

export default function AppDynamicsRunEvidence({ testId, jobs, sourceId }: { testId: string; jobs: RunMonitorJob[]; sourceId?: string }) {
  const selected = jobs.filter(job => ['appdynamics', 'dynatrace'].includes(job.source.pack) && (!sourceId || job.source.id === sourceId))
  const provider = selected.some(job => job.source.pack === 'dynatrace') ? selected.some(job => job.source.pack === 'appdynamics') ? 'Monitoring' : 'Dynatrace' : 'AppDynamics'
  const [page, setPage] = useState<EvidencePage>()
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [cursor, setCursor] = useState('')
  const [kind, setKind] = useState('')
  const sourceKey = selected.map(job => job.source.id).join(',')
  const collecting = selected.some(job => ['collecting', 'reconciling', 'draining'].includes(job.state))
  useEffect(() => { setCursor(''); setPage(undefined) }, [testId, sourceId, kind])
  useEffect(() => {
    if (!collecting) return
    const timer = setInterval(() => setRevision(value => value + 1), 15000)
    return () => clearInterval(timer)
  }, [collecting, testId, sourceKey])
  useEffect(() => {
    if (!sourceKey) return
    const controller = new AbortController()
    const query = new URLSearchParams({ after_key: cursor, limit: '100' })
    if (sourceId) query.set('source_id', sourceId)
    if (kind) query.set('kind', kind)
    void request<EvidencePage>(`/tests/${encodeURIComponent(testId)}/monitor-evidence?${query}`, { signal: controller.signal }).then(result => { if (!controller.signal.aborted) { setPage(result); setError('') } }).catch(err => { if (!controller.signal.aborted) setError(String(err)) })
    return () => controller.abort()
  }, [testId, sourceId, sourceKey, kind, cursor, revision])
  if (!selected.length) return null
  return <section aria-label={`${provider} run evidence`}><h4>{provider} capture evidence</h4>
    {selected.map(job => <div key={job.id}><p role="status"><strong>{job.source.name}</strong>: {monitorJobState(job)}{job.coverage_reason ? ` · ${job.coverage_reason}` : ''}</p><p>Last successful collection: {job.last_success_at ? new Date(job.last_success_at).toLocaleString() : 'Not recorded'}. Source watermark: {job.query_watermark ? new Date(job.query_watermark).toLocaleString() : 'Not recorded'}{['draining', 'reconciling'].includes(job.state) && job.drain_deadline ? `. Final collection deadline: ${new Date(job.drain_deadline).toLocaleString()}` : ''}.</p>{job.query_watermark && <p>Source lag: {Math.max(0, Math.round((Date.now() - Date.parse(job.query_watermark)) / 1000))} seconds. Queries retry within the collection deadline; load execution continues.</p>}{job.coverage && <ul>{Object.entries(job.coverage).map(([capability, health]) => <li key={capability}>{capability}: {health.status}{health.truncated ? ' · truncated coverage' : ''}{health.message ? ` · ${health.message}` : ''}</li>)}</ul>}</div>)}
    <p>Bounded, redacted diagnostic summaries. Selection by time and entity includes production activity; an empty result does not prove the absence of errors. Required incomplete capture makes the test inconclusive.</p>
    {error && <p role="alert">Monitoring evidence unavailable: {error}</p>}
    <label>{provider} evidence type<select value={kind} onChange={event => setKind(event.target.value)}><option value="">All retained diagnostics</option>{(provider === 'AppDynamics' ? ['event', 'violation', 'snapshot', 'log', 'transaction'] : provider === 'Dynatrace' ? ['problem', 'event', 'log', 'span', 'trace', 'topology'] : ['event', 'violation', 'snapshot', 'problem', 'log', 'transaction', 'span', 'trace', 'topology']).map(value => <option key={value} value={value}>{value}</option>)}</select></label>
    <button type="button" onClick={() => { setCursor(''); setRevision(value => value + 1) }}>Refresh {provider} evidence</button>
    {page && <AppDynamicsEvidenceTable evidence={page.evidence.map(record => ({ ...record, provider: record.provider || (selected.find(job => job.source.id === record.source_id)?.source.pack === 'dynatrace' ? 'dynatrace' : provider === 'Dynatrace' ? 'dynatrace' : 'appdynamics') }))} provider={provider} />}
    {page?.has_more && <button type="button" onClick={() => setCursor(page.after_key)}>Next evidence page</button>}
  </section>
}
