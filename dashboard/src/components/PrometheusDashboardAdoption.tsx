import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import { usePrometheusConnectionsApi } from '../hooks/api/usePrometheusConnectionsApi'
import type { ImportedDashboard } from '../types'
import type { PrometheusConnection, PrometheusDashboardDraft, PrometheusQuery } from '../types/prometheus'
import { prometheusQueryValid } from './prometheusConnectionModel'
import { PrometheusQueryFields } from './PrometheusSourceFields'

export default function PrometheusDashboardAdoption({ dashboard, projectId }: { dashboard: ImportedDashboard; projectId: string }) {
  const api = usePrometheusConnectionsApi()
  const [open, setOpen] = useState(false)
  const [queries, setQueries] = useState<PrometheusQuery[]>([])
  const [connections, setConnections] = useState<PrometheusConnection[]>([])
  const [connectionID, setConnectionID] = useState('')
  const [draft, setDraft] = useState<PrometheusDashboardDraft>()
  const [reviewed, setReviewed] = useState(false)
  const [name, setName] = useState(`${dashboard.name} test monitoring`)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    void api.listPrometheusConnections(projectId, { signal: controller.signal }).then(value => {
      if (!controller.signal.aborted) setConnections(value.connections.filter(item => item.enabled && item.verified_revision === item.revision))
    }).catch(err => { if (!controller.signal.aborted) setError(String(err)) })
    return () => controller.abort()
  }, [open, api, projectId])
  useEffect(() => { setOpen(false); setQueries([]); setDraft(undefined); setReviewed(false); setNotice(''); setError(''); setConnectionID('') }, [projectId, dashboard.id])
  return <div>
    <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => { setNotice(''); setError(''); setOpen(true) }}>Create Prometheus monitor source</button>
    {notice && <p role="status">{notice}</p>}{error && <p role="alert">{error}</p>}
    {open && <fieldset disabled={busy} aria-label="Adopt dashboard for test monitoring"><legend>Review target-system monitor source</legend>
      <p>Choose an approved project connection. The server resolves committed variable bindings and time macros into an editable draft. This creates a disabled source after review. No performance criteria are created automatically.</p>
      <label>Adopted source name<input value={name} onChange={event => setName(event.target.value)} /></label>
      <label>Adoption Prometheus connection<select value={connectionID} onChange={event => { setConnectionID(event.target.value); setDraft(undefined); setQueries([]); setReviewed(false) }}><option value="">Select a verified connection</option>{connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
      <button type="button" className="btn btn-secondary btn-sm" disabled={!connectionID} onClick={async () => {
        setBusy(true); setError(''); setReviewed(false)
        try { const value = await api.adoptPrometheusDashboard(projectId, connectionID, dashboard.id); setDraft(value); setQueries(value.selection.queries) }
        catch (err) { setError(String(err)) } finally { setBusy(false) }
      }}>Resolve dashboard query draft</button>
      {draft && <div aria-label="Resolved dashboard query provenance">
        <p>Dashboard source: {draft.dashboard_sha256 || 'Unknown'} · connection revision {draft.connection_revision} · macro lookback {draft.range_seconds}s.</p>
        <p>Original datasource identities: {draft.datasource_uids.join(', ') || 'Default datasource'}. All adopted expressions use the selected project connection.</p>
        {draft.resolved_bindings && <p>Resolved variables: {Object.entries(draft.resolved_bindings).map(([key, value]) => `${key}=${value}`).join(', ') || 'None'}.</p>}
        <ul>{draft.warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul>
        {draft.truncated && <p role="status">Draft truncated at the source query limit.</p>}
        {!queries.length && <p>No reusable supported expressions were resolved. Define the target queries explicitly in Monitoring → Telemetry.</p>}
        <PrometheusQueryFields queries={queries} onChange={next => { setQueries(next); setReviewed(false) }} />
        <label><input type="checkbox" checked={reviewed} onChange={event => setReviewed(event.target.checked)} />I reviewed the target scope, units, semantics and datasource mapping</label>
      </div>}
      <button type="button" className="btn btn-primary btn-sm" disabled={!name.trim() || !connectionID || !draft || draft.connection_id !== connectionID || !reviewed || !queries.length || queries.some(query => !prometheusQueryValid(query))} onClick={async () => {
        const connection = connections.find(item => item.id === connectionID)
        if (!connection || !draft) return
        setBusy(true); setError('')
        try {
          await request(`/projects/${encodeURIComponent(projectId)}/monitor-sources`, { method: 'POST', body: JSON.stringify({ name, revision: 0, pack: 'prometheus-query', executor_mode: connection.collection_mode, collector_id: connection.collector_agent_id || '', connection_id: connection.id, enabled: false, interval_seconds: 30, prometheus: { ...draft.selection, project_id: projectId, queries } }) })
          setOpen(false); setNotice('Disabled monitor source created. Review and enable it in Monitoring → Telemetry, then attach it explicitly to a test or scenario.')
        } catch (err) { setError(String(err)) } finally { setBusy(false) }
      }}>Save adopted source</button>
      <button type="button" className="btn btn-ghost btn-sm" onClick={() => setOpen(false)}>Cancel source adoption</button>
    </fieldset>}
  </div>
}
