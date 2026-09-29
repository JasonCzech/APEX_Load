import { useCallback, useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import { settingsHref } from './settingsNavigation'
import { thresholdAlertMetricLabel } from '../thresholdAlerts'

import { incidentHealth, type Incident, type IncidentResponse, type IncidentHealth } from './thresholdIncidentModel'

export default function ThresholdIncidentPanel({ projectId, canManage, onSelectTest, onHealth }: { projectId?: string; canManage: boolean; onSelectTest?: (id: string) => void; onHealth: (health: IncidentHealth) => void }) {
 const [data, setData] = useState<IncidentResponse | null>(null)
 const [error, setError] = useState<string | null>(null)
 const [busy, setBusy] = useState(false)
 const [ackBusy, setAckBusy] = useState<string | null>(null)
 const [ackError, setAckError] = useState<string | null>(null)
 const [status, setStatus] = useState('')
 const [severity, setSeverity] = useState('')
 const [query, setQuery] = useState('')
 const [search, setSearch] = useState('')
 const [sinceHours, setSinceHours] = useState('')
 const [offset, setOffset] = useState(0)
 const generation = useRef(0)
 const pending = useRef(false)
 const invalidate = useCallback(() => { generation.current++; pending.current = false }, [])
 const load = useCallback(async () => {
  if (!projectId || pending.current) return
  const current = generation.current
  pending.current = true; setBusy(true)
  const params = new URLSearchParams({ project_id: projectId, limit: '50', offset: String(offset) })
  if (status) params.set('status', status)
  if (severity) params.set('severity', severity)
  if (query) params.set('q', query)
  if (sinceHours) params.set('since', new Date(Date.now() - Number(sinceHours) * 3600_000).toISOString())
  try {
   const result = await request<IncidentResponse>(`/monitoring/threshold-incidents?${params}`, { timeoutMs: 4000 })
   if (current === generation.current) { setData(result); setError(null) }
  } catch (err) { if (current === generation.current) setError(err instanceof Error ? err.message : 'Unable to retrieve alert history.') }
  finally { if (current === generation.current) { pending.current = false; setBusy(false) } }
 }, [projectId, status, severity, query, offset, sinceHours])
 useEffect(() => {
  generation.current++; pending.current = false; setData(null); setError(null); setAckError(null); setAckBusy(null)
  void load()
  const refresh = () => { if (document.visibilityState === 'visible') void load() }
  const timer = window.setInterval(refresh, 5000)
  document.addEventListener('visibilitychange', refresh)
  return () => { invalidate(); window.clearInterval(timer); document.removeEventListener('visibilitychange', refresh) }
 }, [load, invalidate])
 useEffect(() => { const timer = window.setTimeout(() => { setOffset(0); setQuery(search.trim()) }, 300); return () => window.clearTimeout(timer) }, [search])
 useEffect(() => { onHealth(incidentHealth(data, error)) }, [data, error, onHealth])
 const health = incidentHealth(data, error)
 const acknowledge = async (incident: Incident) => {
  let current = generation.current; setAckBusy(incident.id); setAckError(null)
  try {
   const saved = await request<Incident>(`/monitoring/threshold-incidents/${encodeURIComponent(incident.id)}/ack`, { method: 'PATCH', body: JSON.stringify({ acknowledged: !incident.acknowledged_at }) })
   if (generation.current !== current) return
   invalidate(); current = generation.current
   setData(value => value ? { ...value, incidents: value.incidents.map(item => item.id === saved.id ? saved : item) } : value)
   await load()
  } catch (err) { if (generation.current === current) setAckError(err instanceof Error ? err.message : 'Unable to acknowledge alert.') }
  finally { if (generation.current === current) setAckBusy(null) }
 }
 return <div className="monitor-body">
  <strong role="status">{projectId ? health.label : 'Select a project'}</strong>
  <p className="monitor-status-note">{health.summary}</p>
  {data && <p className="monitor-status-note">{data.global_policy_count} global policies enabled · Last evaluation: {data.last_evaluated_at ? new Date(data.last_evaluated_at).toLocaleString() : 'None received'}. History is shared across operators and retained across reloads.</p>}
  {error && <p role="alert">{error} Previously loaded incidents may be stale.</p>}
  {ackError && <p role="alert">{ackError}</p>}
  <div className="monitor-workflow-actions">
   {canManage && <a className="btn btn-secondary btn-sm" href={settingsHref('thresholds')}>Configure policies</a>}
   <button type="button" className="btn btn-secondary btn-sm" disabled={busy || !projectId} onClick={() => void load()}>{busy ? 'Refreshing history…' : 'Refresh history'}</button>
  </div>
  <section className="monitor-incident-history" aria-labelledby="shared-sla-incident-history">
   <h3 id="shared-sla-incident-history">Shared SLA incident history</h3>
   <div className="monitor-workflow-actions">
    <label>Search incidents<input type="search" value={search} maxLength={200} onChange={event => setSearch(event.target.value)} placeholder="Test, transaction, or metric" /></label>
    <label>Incident status<select value={status} onChange={event => { setOffset(0); setStatus(event.target.value) }}><option value="">All statuses</option><option value="active">Active</option><option value="unacknowledged">Needs acknowledgement</option><option value="acknowledged">Acknowledged</option><option value="resolved">Resolved</option></select></label>
    <label>Severity<select value={severity} onChange={event => { setOffset(0); setSeverity(event.target.value) }}><option value="">All severities</option><option value="danger">Danger</option><option value="warning">Warning</option></select></label>
    <label>Incident timeframe<select value={sinceHours} onChange={event => { setOffset(0); setSinceHours(event.target.value) }}><option value="">All history</option><option value="24">Last 24 hours</option><option value="168">Last 7 days</option><option value="720">Last 30 days</option></select></label>
   </div>
   <div className="monitor-alert-history">{data?.incidents.map(incident => <article key={incident.id}>
    <strong>{incident.test_name || incident.test_id} · {thresholdAlertMetricLabel(incident.payload.metric || '')}</strong>
    <p>{incident.severity} · {incident.active ? 'Active breach' : 'Resolved'}{incident.payload.action === 'stop_requested' ? ' · Auto-stop requested' : ''} · {incident.payload.value} / threshold {incident.payload.threshold}{incident.payload.transaction ? ` · ${incident.payload.transaction}` : ''}</p>
    <p className="monitor-status-note">First observed {new Date(incident.first_seen_at).toLocaleString()} · Last observed {new Date(incident.last_seen_at).toLocaleString()}</p>
    {incident.acknowledged_at && <p className="monitor-status-note">Acknowledged by {incident.acknowledged_by} at {new Date(incident.acknowledged_at).toLocaleString()}</p>}
    <div className="monitor-workflow-actions">{onSelectTest && <button type="button" className="btn btn-secondary btn-sm" onClick={() => onSelectTest(incident.test_id)}>Open test</button>}{canManage && <button type="button" className="btn btn-secondary btn-sm" disabled={!!ackBusy} onClick={() => void acknowledge(incident)}>{ackBusy === incident.id ? 'Saving…' : incident.acknowledged_at ? 'Clear acknowledgement' : 'Acknowledge'}</button>}</div>
   </article>)}</div>
   {data?.incidents.length === 0 && <p>No persisted incidents match these filters.</p>}
   <div className="monitor-workflow-actions"><button type="button" className="btn btn-secondary btn-sm" disabled={busy || offset === 0} onClick={() => setOffset(value => Math.max(0, value - 50))}>Newer incidents</button><span>Page {offset / 50 + 1}</span><button type="button" className="btn btn-secondary btn-sm" disabled={busy || !data || data.incidents.length < 50} onClick={() => setOffset(value => value + 50)}>Older incidents</button></div>
  </section>
 </div>
}
