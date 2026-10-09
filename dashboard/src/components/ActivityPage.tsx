import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ObjectActivityResponse } from '../types/objectActivity'
import { getErrorMessage } from '../utils/errorUtils'
import { ActivityPager, ActivityTable } from './ObjectHistory'
import { ACTIVITY_ACTIONS, activityActionLabel, activityQueryString, parseActivityQuery } from './objectActivityModel'
import './ObjectActivity.css'
import Notice from './ux/Notice'

const PAGE_SIZE = 50

export default function ActivityPage({ projectId, projectName, query, onQueryChange }: { projectId?: string; projectName?: string; query?: string; onQueryChange?: (query: string) => void }) {
  const [initial] = useState(() => parseActivityQuery(query))
  const [draft, setDraft] = useState(initial.filter)
  const [filter, setFilter] = useState(initial.filter)
  const [page, setPage] = useState(initial.page)
  const [refresh, setRefresh] = useState(0)
  const [data, setData] = useState<ObjectActivityResponse | null>(null)
  const [error, setError] = useState('')
  const invalidDates = Boolean(draft.after && draft.before && draft.after > draft.before)
  const lastProjectId = useRef(projectId)
  useEffect(() => {
    // Only a project switch resets paging; the first render keeps the URL's page.
    if (lastProjectId.current === projectId) return
    lastProjectId.current = projectId
    setPage(0)
  }, [projectId])
  const onQueryChangeRef = useRef(onQueryChange); onQueryChangeRef.current = onQueryChange
  const currentQuery = activityQueryString(filter, page)
  const currentQueryRef = useRef(currentQuery); currentQueryRef.current = currentQuery
  useEffect(() => { onQueryChangeRef.current?.(currentQuery) }, [currentQuery])
  useEffect(() => {
    // A link or edited address on this page replaces the filters in place.
    if (!onQueryChangeRef.current || (query || '') === currentQueryRef.current) return
    const next = parseActivityQuery(query || '')
    setDraft(next.filter); setFilter(next.filter); setPage(next.page)
  }, [query])
  useEffect(() => {
    if (!projectId) return
    let cancelled = false
    setData(null); setError('')
    const query = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(page * PAGE_SIZE) })
    if (filter.search) query.set('q', filter.search)
    if (filter.actor) query.set('actor_subject', filter.actor)
    if (filter.kind) query.set('resource_type', filter.kind)
    if (filter.action) query.set('action', filter.action)
    if (filter.after) query.set('created_after', new Date(filter.after).toISOString())
    if (filter.before) query.set('created_before', new Date(filter.before).toISOString())
    void request<ObjectActivityResponse>(`/projects/${encodeURIComponent(projectId)}/activity?${query}`).then(result => { if (!cancelled) setData(result) }).catch(error => { if (!cancelled) setError(getErrorMessage(error, 'Unable to load activity')) })
    return () => { cancelled = true }
  }, [projectId, filter, page, refresh])
  return <div className="page object-activity-page">{/* The top bar already titles this page "Activity". */}<p>Object history for {projectName || 'this project'}, including deleted objects.</p>{!projectId ? <p>Select a project to view its activity.</p> : <><form className="object-activity-filters" onSubmit={event => { event.preventDefault(); setFilter(draft); setPage(0); setRefresh(value => value + 1) }}><label>Object name<input className="settings-input" value={draft.search} onChange={event => setDraft(value => ({ ...value, search: event.target.value }))} /></label><label>Actor identity<input className="settings-input" value={draft.actor} onChange={event => setDraft(value => ({ ...value, actor: event.target.value }))} /></label><label>Object type<select className="settings-input" value={draft.kind} onChange={event => setDraft(value => ({ ...value, kind: event.target.value }))}><option value="">All objects</option><option value="script">Scripts</option><option value="scenario">Scenarios</option><option value="test">Runs and results</option><option value="external_result_import">Imported results</option></select></label><label>Action<select className="settings-input" value={draft.action} onChange={event => setDraft(value => ({ ...value, action: event.target.value }))}><option value="">All actions</option>{ACTIVITY_ACTIONS.map(action => <option key={action} value={action}>{activityActionLabel(action)}</option>)}</select></label><label>From (local time)<input className="settings-input" type="datetime-local" value={draft.after} onChange={event => setDraft(value => ({ ...value, after: event.target.value }))} /></label><label>Through (local time)<input className="settings-input" type="datetime-local" value={draft.before} onChange={event => setDraft(value => ({ ...value, before: event.target.value }))} /></label><button className="btn btn-primary" disabled={invalidDates}>Apply filters</button></form>{invalidDates && <p role="alert">The end date must follow the start date.</p>}{error ? <Notice tone="error" title="Activity could not be loaded" details={error} action={<button className="btn btn-secondary" type="button" onClick={() => setRefresh(value => value + 1)}>Retry activity</button>}>Check your connection or access, then retry.</Notice> : !data ? <p role="status">Loading activity…</p> : <><ActivityTable events={data.events} /><ActivityPager label="events" page={page} pageSize={PAGE_SIZE} shown={data.events.length} hasMore={data.has_more} onPage={setPage} /></>}</>}</div>
}
