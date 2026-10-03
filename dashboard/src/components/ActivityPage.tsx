import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ObjectActivityResponse } from '../types/objectActivity'
import { getErrorMessage } from '../utils/errorUtils'
import { ActivityTable } from './ObjectHistory'
import './ObjectActivity.css'

const empty = { search: '', actor: '', kind: '', action: '', after: '', before: '' }
export default function ActivityPage({ projectId, projectName }: { projectId?: string; projectName?: string }) {
  const [draft, setDraft] = useState(empty)
  const [filter, setFilter] = useState(empty)
  const [page, setPage] = useState(0)
  const [refresh, setRefresh] = useState(0)
  const [data, setData] = useState<ObjectActivityResponse | null>(null)
  const [error, setError] = useState('')
  const invalidDates = Boolean(draft.after && draft.before && draft.after > draft.before)
  useEffect(() => { setPage(0) }, [projectId])
  useEffect(() => {
    if (!projectId) return
    let cancelled = false
    setData(null); setError('')
    const query = new URLSearchParams({ limit: '50', offset: String(page * 50) })
    if (filter.search) query.set('q', filter.search)
    if (filter.actor) query.set('actor_subject', filter.actor)
    if (filter.kind) query.set('resource_type', filter.kind)
    if (filter.action) query.set('action', filter.action)
    if (filter.after) query.set('created_after', new Date(filter.after).toISOString())
    if (filter.before) query.set('created_before', new Date(filter.before).toISOString())
    void request<ObjectActivityResponse>(`/projects/${encodeURIComponent(projectId)}/activity?${query}`).then(result => { if (!cancelled) setData(result) }).catch(error => { if (!cancelled) setError(getErrorMessage(error, 'Unable to load activity')) })
    return () => { cancelled = true }
  }, [projectId, filter, page, refresh])
  return <div className="page object-activity-page"><div className="page-header"><div><h1>Activity</h1></div></div><p>Object history for {projectName || 'this project'}, including deleted objects.</p>{!projectId ? <p>Select a project to view its activity.</p> : <><form className="object-activity-filters" onSubmit={event => { event.preventDefault(); setFilter(draft); setPage(0); setRefresh(value => value + 1) }}><label>Object name<input className="settings-input" value={draft.search} onChange={event => setDraft(value => ({ ...value, search: event.target.value }))} /></label><label>Actor identity<input className="settings-input" value={draft.actor} onChange={event => setDraft(value => ({ ...value, actor: event.target.value }))} /></label><label>Object type<select className="settings-input" value={draft.kind} onChange={event => setDraft(value => ({ ...value, kind: event.target.value }))}><option value="">All objects</option><option value="script">Scripts</option><option value="scenario">Scenarios</option><option value="test">Runs and results</option><option value="external_result_import">Imported results</option></select></label><label>Action<select className="settings-input" value={draft.action} onChange={event => setDraft(value => ({ ...value, action: event.target.value }))}><option value="">All actions</option>{['create', 'modify', 'delete', 'move', 'archive', 'restore', 'start', 'stop', 'abort', 'pause', 'resume', 'prune'].map(action => <option key={action} value={action}>{action === 'prune' ? 'Delete result artifacts' : action}</option>)}</select></label><label>From (local time)<input className="settings-input" type="datetime-local" value={draft.after} onChange={event => setDraft(value => ({ ...value, after: event.target.value }))} /></label><label>Through (local time)<input className="settings-input" type="datetime-local" value={draft.before} onChange={event => setDraft(value => ({ ...value, before: event.target.value }))} /></label><button className="btn btn-primary" disabled={invalidDates}>Apply filters</button></form>{invalidDates && <p role="alert">The end date must follow the start date.</p>}{error ? <div role="alert">{error}<button className="btn btn-secondary" onClick={() => setRefresh(value => value + 1)}>Retry activity</button></div> : !data ? <p role="status">Loading activity…</p> : <><ActivityTable events={data.events} /><div className="settings-inline-actions"><button className="btn btn-secondary" disabled={page === 0} onClick={() => setPage(value => value - 1)}>Previous events</button><span>Page {page + 1}</span><button className="btn btn-secondary" disabled={!data.has_more} onClick={() => setPage(value => value + 1)}>Next events</button></div></>}</>}</div>
}
