import { useCallback, useEffect, useState } from 'react'

import { request } from '../../hooks/apiClient'
import type { MonitorSource } from '../../types/runMonitoring'
import type { BusinessKeyLookupResponse, BusinessKeyMapping, BusinessKeyMappingDefinition } from '../../types/businessKeys'
import type { ProjectAnalysisSettingsResponse } from '../../types/rca'
import { getErrorMessage } from '../../utils/errorUtils'
import { parseBusinessKeyValues } from './businessKeyModel'
import { formatMs } from './runReportModel'
import './BusinessKeyLookup.css'

const MAX_VALUES = 20
const MAX_VALUE_BYTES = 256

interface Props {
  testId: string
  projectId: string
  canLookup: boolean
  canApprove: boolean
  // settingsHref opens the project's analysis settings, where business keys
  // are turned on, for those who may change them.
  settingsHref?: string
}

const mappingsPath = (projectId: string) => `/analysis/business-key-mappings?project_id=${encodeURIComponent(projectId)}`

const mappingLabel = (m: BusinessKeyMapping) => m.label || m.extraction

const formatTime = (value: string) => {
  const at = Date.parse(value)
  return Number.isFinite(at) ? new Date(at).toISOString().replace('T', ' ').replace(/\.\d+Z$|Z$/, ' UTC') : 'Not recorded'
}

const LOG_STATUS: Record<string, string> = {
  found: 'Found in the logs',
  not_found: 'Not found in the logs',
  error: 'The log search failed',
  unavailable: 'Logs not searched',
}

function ApprovedMappings({ projectId, mappings, onChanged }: { projectId: string; mappings: BusinessKeyMappingDefinition[]; onChanged: () => void }) {
  const [sources, setSources] = useState<MonitorSource[]>([])
  const [draft, setDraft] = useState<BusinessKeyMapping>({ extraction: '', source_id: '', field: '', label: '' })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    let cancelled = false
    request<MonitorSource[]>(`/projects/${encodeURIComponent(projectId)}/monitor-sources`)
      .then(list => { if (!cancelled) setSources((Array.isArray(list) ? list : []).filter(source => source.pack === 'elastic-query')) })
      .catch(() => { if (!cancelled) setSources([]) })
    return () => { cancelled = true }
  }, [projectId])
  const approve = async () => {
    setSaving(true)
    setError('')
    try {
      await request(mappingsPath(projectId), { method: 'POST', body: JSON.stringify({ name: draft.label || draft.extraction, definition: { ...draft, label: draft.label || undefined } }) })
      setDraft({ extraction: '', source_id: '', field: '', label: '' })
      onChanged()
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to approve the mapping'))
    } finally {
      setSaving(false)
    }
  }
  const remove = async (m: BusinessKeyMappingDefinition) => {
    setError('')
    try {
      await request(`/analysis/business-key-mappings/${encodeURIComponent(m.id)}?project_id=${encodeURIComponent(projectId)}&revision=${m.revision}`, { method: 'DELETE' })
      onChanged()
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to remove the mapping'))
    }
  }
  const sourceName = (id: string) => sources.find(source => source.id === id)?.name || id
  return (
    <details className="business-key-mappings">
      <summary>Approved business keys ({mappings.length})</summary>
      {mappings.length > 0 && <ul>{mappings.map(m => (
        <li key={m.id}>
          <strong>{mappingLabel(m.definition)}</strong>: extraction <code>{m.definition.extraction}</code> is <code>{m.definition.field}</code> in {sourceName(m.definition.source_id)}
          <button type="button" className="btn btn-sm btn-ghost" onClick={() => void remove(m)}>Remove</button>
        </li>
      ))}</ul>}
      <form className="business-key-mapping-form" onSubmit={e => { e.preventDefault(); void approve() }}>
        <label>Label <input value={draft.label} maxLength={128} onChange={e => setDraft({ ...draft, label: e.target.value })} placeholder="Order ID" /></label>
        <label>Extraction <input value={draft.extraction} required maxLength={128} onChange={e => setDraft({ ...draft, extraction: e.target.value })} placeholder="order_id" /></label>
        <label>Log source <select value={draft.source_id} required onChange={e => setDraft({ ...draft, source_id: e.target.value })}>
          <option value="">Choose an Elasticsearch or OpenSearch source</option>
          {sources.map(source => <option key={source.id} value={source.id}>{source.name}</option>)}
        </select></label>
        <label>Log field <input value={draft.field} required maxLength={256} onChange={e => setDraft({ ...draft, field: e.target.value })} placeholder="order.id" /></label>
        <button type="submit" className="btn btn-sm btn-secondary" disabled={saving}>{saving ? 'Approving…' : 'Approve mapping'}</button>
      </form>
      {error && <p className="run-report-error" role="alert">{error}</p>}
    </details>
  )
}

// BusinessKeyLookup finds a run's requests and the mapped source's logs for
// business-key values the operator types in. The values stay in this
// component: they are sent once with the lookup and never stored, put in the
// URL or kept in the browser.
export default function BusinessKeyLookup({ testId, projectId, canLookup, canApprove, settingsHref }: Props) {
  const [mappings, setMappings] = useState<BusinessKeyMappingDefinition[] | null>(null)
  const [enabled, setEnabled] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [mappingId, setMappingId] = useState('')
  const [text, setText] = useState('')
  const [sent, setSent] = useState<string[]>([])
  const [result, setResult] = useState<BusinessKeyLookupResponse | null>(null)
  const [looking, setLooking] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)

  // The project's gates say whether business keys are on, so a project
  // without them is not asked for mappings it refuses; the mappings' own
  // refusal still covers a gate that could not be read. The gate is read once
  // per project, not again after each mapping change.
  // The gate is kept with the project it was read for, so a project switch
  // never acts on the previous project's answer.
  const [gateRead, setGateRead] = useState<{ projectId: string; enabled: boolean } | null>(null)
  const gate = gateRead?.projectId === projectId ? gateRead.enabled : null
  useEffect(() => {
    let cancelled = false
    request<ProjectAnalysisSettingsResponse>(`/projects/${encodeURIComponent(projectId)}/analysis-settings`)
      .then(settings => { if (!cancelled) setGateRead({ projectId, enabled: settings?.effective?.business_keys?.enabled !== false }) }, () => { if (!cancelled) setGateRead({ projectId, enabled: true }) })
    return () => { cancelled = true }
  }, [projectId])

  useEffect(() => {
    if (gate === null) return undefined
    if (!gate) {
      setEnabled(false)
      return undefined
    }
    let cancelled = false
    setLoadError('')
    request<BusinessKeyMappingDefinition[]>(mappingsPath(projectId))
      .then(list => {
        if (cancelled) return
        const live = (Array.isArray(list) ? list : []).filter(m => !m.deleted)
        setMappings(live)
        setEnabled(true)
        setMappingId(current => live.some(m => m.id === current) ? current : live[0]?.id || '')
      })
      .catch(err => {
        if (cancelled) return
        // 409: the project does not use business keys.
        if ((err as { status?: unknown } | null)?.status === 409) setEnabled(false)
        else setLoadError(getErrorMessage(err, 'Unable to load business key mappings'))
      })
    return () => { cancelled = true }
  }, [gate, projectId, reload])

  // Another run never sees this run's values or results.
  useEffect(() => { setText(''); setSent([]); setResult(null); setError('') }, [testId])

  const values = parseBusinessKeyValues(text)
  const tooMany = values.length > MAX_VALUES
  const tooLong = values.some(value => new TextEncoder().encode(value).length > MAX_VALUE_BYTES)
  const lookUp = useCallback(async () => {
    setLooking(true)
    setError('')
    try {
      const response = await request<BusinessKeyLookupResponse>(`/tests/${encodeURIComponent(testId)}/archive/business-keys/lookup`, { method: 'POST', body: JSON.stringify({ mapping_id: mappingId, values }) })
      setSent(values)
      setResult(response)
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to look up the business keys'))
    } finally {
      setLooking(false)
    }
  }, [mappingId, testId, values])

  // Someone who could use it learns the capability exists; others see nothing.
  if (!enabled) return canLookup
    ? <p className="run-report-muted" role="note">Business key lookup is not enabled for this project. {settingsHref
      ? <>Turn it on in <a href={settingsHref}>Settings › Workspaces › Project analysis</a>.</>
      : 'A project administrator turns it on in Settings › Workspaces › Project analysis.'}</p>
    : null
  return (
    <section className="run-report-panel business-key-lookup" aria-label="Business key lookup">
      <div className="run-report-section-heading">
        <div>
          <h2>Business key lookup</h2>
          <p className="run-report-muted">Find this run's requests and the matching log entries for order IDs, account numbers or other approved business keys. Values are sent once and never stored.</p>
        </div>
      </div>
      {loadError && <p className="run-report-error" role="alert">{loadError}</p>}
      {mappings && mappings.length === 0 && <p>No business key is approved for this project yet.{canApprove ? ' Approve one below.' : ' A project admin approves them.'}</p>}
      {mappings && mappings.length > 0 && canLookup && (
        <form className="business-key-form" onSubmit={e => { e.preventDefault(); void lookUp() }}>
          <label>Business key <select value={mappingId} onChange={e => setMappingId(e.target.value)}>
            {mappings.map(m => <option key={m.id} value={m.id}>{mappingLabel(m.definition)}</option>)}
          </select></label>
          <label>Values, one per line (up to {MAX_VALUES})
            <textarea value={text} rows={3} autoComplete="off" spellCheck={false} onChange={e => setText(e.target.value)} />
          </label>
          {tooMany && <p className="run-report-error" role="alert">Look up at most {MAX_VALUES} values at a time.</p>}
          {tooLong && <p className="run-report-error" role="alert">Each value can be at most {MAX_VALUE_BYTES} bytes.</p>}
          <div className="run-report-toolbar-actions">
            <button type="submit" className="btn btn-sm btn-secondary" disabled={looking || !mappingId || values.length === 0 || tooMany || tooLong}>{looking ? 'Looking up…' : 'Look up'}</button>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => { setText(''); setSent([]); setResult(null) }}>Clear</button>
          </div>
        </form>
      )}
      {mappings && mappings.length > 0 && !canLookup && <p className="run-report-muted">Editors can look business keys up.</p>}
      {error && <p className="run-report-error" role="alert">{error}</p>}
      {result && <div className="business-key-results">
        {result.limitations.map((limitation, i) => <p key={i} className="run-report-muted" role="note">{limitation}</p>)}
        {result.results.map(r => (
          <section key={r.index} className="business-key-result" aria-label={`Result for ${sent[r.index] ?? `value ${r.index + 1}`}`}>
            <h3><code>{sent[r.index] ?? `Value ${r.index + 1}`}</code></h3>
            <p><strong>{r.requests.total}</strong> archived {r.requests.total === 1 ? 'request' : 'requests'} · {LOG_STATUS[r.logs.status] || r.logs.status}{r.logs.total > 0 ? ` (${r.logs.total})` : ''}{r.logs.detail ? `: ${r.logs.detail}` : ''}</p>
            {r.requests.matches.length > 0 && <table className="business-key-table">
              <thead><tr><th scope="col">Time</th><th scope="col">Request</th><th scope="col">Duration</th><th scope="col">Result</th><th scope="col">Trace</th></tr></thead>
              <tbody>{r.requests.matches.map((m, i) => <tr key={i}>
                <td>{formatTime(m.timestamp)}</td><td>{m.group ? `${m.group} · ` : ''}{m.name}</td><td>{formatMs(m.duration_ms)}</td>
                <td>{m.success ? 'Success' : `Failed${m.status_code ? ` (${m.status_code})` : ''}`}</td><td>{m.trace_id ? <code>{m.trace_id}</code> : 'None'}</td>
              </tr>)}</tbody>
            </table>}
            {r.requests.total > r.requests.matches.length && <p className="run-report-muted">Showing the first {r.requests.matches.length}.</p>}
            {r.logs.entries.length > 0 && <table className="business-key-table">
              <thead><tr><th scope="col">Log time</th><th scope="col">Trace</th><th scope="col">Message</th></tr></thead>
              <tbody>{r.logs.entries.map((entry, i) => <tr key={i}><td>{formatTime(entry.timestamp)}</td><td>{entry.trace_id ? <code>{entry.trace_id}</code> : 'None'}</td><td>{entry.message || ''}</td></tr>)}</tbody>
            </table>}
          </section>
        ))}
      </div>}
      {canApprove && mappings && <ApprovedMappings projectId={projectId} mappings={mappings} onChanged={() => setReload(n => n + 1)} />}
    </section>
  )
}
