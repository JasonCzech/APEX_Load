import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ActivityResourceType, ObjectActivityEvent, ObjectActivityResponse, ObjectActivitySummary } from '../types/objectActivity'
import { getErrorMessage } from '../utils/errorUtils'
import './ObjectActivity.css'

export function ActivityAttribution({ activity }: { activity?: ObjectActivitySummary }) {
  const actor = (event?: ObjectActivityEvent) => event?.actor_display_name || event?.actor_subject || 'Unknown'
  return <span className="object-activity-attribution">Created by {actor(activity?.creator)} · Last modified by {actor(activity?.last_modifier)}{activity?.last_archive && <> · {activity.last_archive.action === 'restore' ? 'Restored' : 'Archived'} by {actor(activity.last_archive)}</>}</span>
}

export function ActivityTable({ events }: { events: ObjectActivityEvent[] }) {
  return <div className="object-activity-table"><table data-table-pagination="off"><caption className="sr-only">Object activity history</caption><thead><tr>{['Time', 'Actor', 'Action', 'Object', 'Outcome', 'Changes'].map(value => <th key={value} scope="col">{value}</th>)}</tr></thead><tbody>{events.map(event => {
    const scope = `projectId=${encodeURIComponent(event.project_id)}`
    const link = event.resource_type === 'script' ? `#/scripts?mode=edit&scriptId=${encodeURIComponent(event.resource_id)}&${scope}` : event.resource_type === 'test' ? `#/test/${encodeURIComponent(event.resource_id)}?${scope}` : event.resource_type === 'scenario' ? `#/new-test?scenarioId=${encodeURIComponent(event.resource_id)}&${scope}` : `#/results?${scope}`
    return <tr key={event.id}><td><time dateTime={event.created_at}>{new Date(event.created_at).toLocaleString()}</time></td><td>{event.actor_display_name || event.actor_subject || 'Unknown'}<small>{event.actor_type}{event.initiated_by ? ` · Initiated by ${event.initiated_by}` : ''}</small></td><td>{event.action === 'prune' ? 'Delete result artifacts' : event.action}</td><td>{event.deleted ? <span>{event.resource_name || event.resource_id} (deleted)</span> : <a href={link}>{event.resource_name || event.resource_id}</a>}<small>{event.resource_type} · {event.resource_id}</small></td><td>{event.status}</td><td>{event.changed_fields?.join(', ') || '—'}</td></tr>
  })}</tbody></table>{!events.length && <p>No retained activity found. Historical attribution may be unknown.</p>}</div>
}

export default function ObjectHistory({ projectId, resourceType, resourceId, activity, revision = 0 }: { projectId?: string; resourceType: ActivityResourceType; resourceId: string; activity?: ObjectActivitySummary; revision?: number }) {
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
  return <section className="object-history" aria-label="Object history"><ActivityAttribution activity={data?.activity || activity} /><details open={open} onToggle={event => setOpen(event.currentTarget.open)}><summary>History</summary>{!projectId ? <p>Select a project to view history.</p> : error ? <div role="alert">{error}<button className="btn btn-secondary" onClick={() => setRefresh(value => value + 1)}>Retry history</button></div> : !data ? <p role="status">Loading history…</p> : <><ActivityTable events={data.events} /><div className="settings-inline-actions"><button className="btn btn-secondary" disabled={page === 0} onClick={() => setPage(value => value - 1)}>Previous history</button><span>Page {page + 1}</span><button className="btn btn-secondary" disabled={!data.has_more} onClick={() => setPage(value => value + 1)}>Next history</button><button className="btn btn-secondary" onClick={() => setRefresh(value => value + 1)}>Refresh history</button></div></>}</details></section>
}
