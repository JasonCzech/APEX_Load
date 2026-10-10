import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ActivityResourceType, ObjectActivityEvent, ObjectActivityResponse, ObjectActivitySummary } from '../types/objectActivity'
import { getErrorMessage } from '../utils/errorUtils'
import './ObjectActivity.css'
import Notice from './ux/Notice'
import { activityActionLabel } from './objectActivityModel'
import { formatDateTime } from '../utils/dateTime'

export function ActivityAttribution({ activity }: { activity?: ObjectActivitySummary }) {
  const actor = (event?: ObjectActivityEvent) => event?.actor_display_name || event?.actor_subject || 'Unknown'
  return <span className="object-activity-attribution">Created by {actor(activity?.creator)} · Last modified by {actor(activity?.last_modifier)}{activity?.last_archive && <> · {activity.last_archive.action === 'restore' ? 'Restored' : 'Archived'} by {actor(activity.last_archive)}</>}</span>
}

/**
 * The one pager for activity lists: the visible range plus newer/older controls.
 * The API reports whether more events exist, not a total, so no "of N" is shown.
 */
export function ActivityPager({ page, pageSize, shown, hasMore, onPage, label }: { page: number; pageSize: number; shown: number; hasMore: boolean; onPage: (page: number) => void; label: string }) {
  const first = shown ? page * pageSize + 1 : 0
  return <nav className="settings-inline-actions object-activity-pager" aria-label={`${label} pages`}>
    <button className="btn btn-secondary" type="button" disabled={page === 0} onClick={() => onPage(page - 1)}>Newer {label}</button>
    <span role="status">{shown ? `Showing ${first.toLocaleString()}–${(first + shown - 1).toLocaleString()}${hasMore ? ' · more available' : ''}` : 'No events on this page'}</span>
    <button className="btn btn-secondary" type="button" disabled={!hasMore} onClick={() => onPage(page + 1)}>Older {label}</button>
  </nav>
}

export function ActivityTable({ events }: { events: ObjectActivityEvent[] }) {
  return <div className="object-activity-table"><table data-table-pagination="off"><caption className="sr-only">Object activity history</caption><thead><tr>{['Time', 'Actor', 'Action', 'Object', 'Outcome', 'Changes'].map(value => <th key={value} scope="col">{value}</th>)}</tr></thead><tbody>{events.map(event => {
    const scope = `projectId=${encodeURIComponent(event.project_id)}`
    const link = event.resource_type === 'script' ? `#/scripts?mode=edit&scriptId=${encodeURIComponent(event.resource_id)}&${scope}` : event.resource_type === 'test' ? `#/test/${encodeURIComponent(event.resource_id)}?${scope}` : event.resource_type === 'scenario' ? `#/new-test?scenarioId=${encodeURIComponent(event.resource_id)}&${scope}` : `#/results?${scope}`
    return <tr key={event.id}><td><time dateTime={event.created_at}>{formatDateTime(event.created_at, { seconds: true })}</time></td><td>{event.actor_display_name || event.actor_subject || 'Unknown'}<small>{event.actor_type}{event.initiated_by ? ` · Initiated by ${event.initiated_by}` : ''}</small></td><td>{activityActionLabel(event.action)}</td><td>{event.deleted ? <span>{event.resource_name || event.resource_id} (deleted)</span> : <a href={link}>{event.resource_name || event.resource_id}</a>}<small>{event.resource_type} · {event.resource_id}</small></td><td>{event.status}</td><td>{event.changed_fields?.join(', ') || '—'}</td></tr>
  })}</tbody></table>{!events.length && <p>No retained activity found. Historical attribution may be unknown.</p>}</div>
}

/** compact: dense tables show only the History disclosure; who created and changed the object is inside it. */
export default function ObjectHistory({ projectId, resourceType, resourceId, activity, revision = 0, compact = false }: { projectId?: string; resourceType: ActivityResourceType; resourceId: string; activity?: ObjectActivitySummary; revision?: number; compact?: boolean }) {
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState(0)
  const [data, setData] = useState<ObjectActivityResponse | null>(null)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  useEffect(() => { setPage(0) }, [projectId, resourceType, resourceId])
  useEffect(() => { const refreshHistory = () => setRefresh(value => value + 1); window.addEventListener('apexload:activity-changed', refreshHistory); return () => window.removeEventListener('apexload:activity-changed', refreshHistory) }, [])
  useEffect(() => {
    setData(null)
    if (!open || !projectId) return
    let cancelled = false
    const query = new URLSearchParams({ resource_type: resourceType, resource_id: resourceId, limit: '25', offset: String(page * 25) })
    setData(null); setError('')
    void request<ObjectActivityResponse>(`/projects/${encodeURIComponent(projectId)}/activity?${query}`).then(value => { if (!cancelled) setData(value) }).catch(error => { if (!cancelled) setError(getErrorMessage(error, 'Unable to load history')) })
    return () => { cancelled = true }
  }, [open, projectId, resourceType, resourceId, page, revision, refresh])
  return <section className={`object-history${compact ? ' compact' : ''}`} aria-label="Object history">{!compact && <ActivityAttribution activity={data?.activity || activity} />}<details open={open} onToggle={event => setOpen(event.currentTarget.open)}><summary>History</summary>{compact && <ActivityAttribution activity={data?.activity || activity} />}{!projectId ? <p>Select a project to view history.</p> : error ? <Notice tone="error" title="History could not be loaded" details={error} action={<button className="btn btn-secondary" type="button" onClick={() => setRefresh(value => value + 1)}>Retry history</button>} /> : !data ? (open ? <p role="status">Loading history…</p> : null) : <><ActivityTable events={data.events} /><ActivityPager label="history" page={page} pageSize={25} shown={data.events.length} hasMore={data.has_more} onPage={setPage} /><button className="btn btn-secondary" type="button" onClick={() => setRefresh(value => value + 1)}>Refresh history</button></>}</details></section>
}
