import { useEffect, useState } from 'react'
import { useCapacityApi } from '../hooks/api/useCapacityApi'
import type { CapacityReservation, CapacityTimeslot } from '../types'
import { listAllCapacityTimeslots } from './schedulingCatalogModel'
import { settingsHref, workspaceHref } from './settingsNavigation'

export function CapacityCommitments({ reservations, projectId, projects, pools = [] }: { reservations: CapacityReservation[]; projectId?: string; projects: { id: string; name: string }[]; pools?: { id: string; name: string }[] }) {
  const { listTimeslots } = useCapacityApi()
  const [slots, setSlots] = useState<CapacityTimeslot[]>([])
  const [error, setError] = useState(''), [loading, setLoading] = useState(true)
  const [poolId, setPoolId] = useState('')
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    let active = true
    setLoading(true); setSlots([]); setError('')
    void listAllCapacityTimeslots(listTimeslots, projectId || '').then(result => { if (active) setSlots(result) }).catch(() => { if (active) setError('Scheduled bookings could not be loaded. The overview is incomplete.') }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [listTimeslots, projectId, refresh])
  const rows = [
    ...reservations.filter(item => !projectId || item.project_id === projectId).map((item, index) => ({ id: `standing-${index}`, kind: 'Standing allocation', project: item.project_id, pool: item.pool_id, start: item.starts_at, end: item.ends_at, amount: `${item.reserved_generators} generators`, status: 'Saved policy' })),
    ...slots.map(item => ({ id: item.id, kind: 'Scheduled booking', project: item.project_id, pool: item.requested_capacity?.pool_id, start: item.start_at, end: item.end_at, amount: `${item.requested_capacity?.generators || 0} generators / ${item.requested_capacity?.vusers || 0} VUsers`, status: item.approval_state })),
  ].filter(item => !poolId || (item.pool || 'global') === poolId)
  return <section className="settings-subsection settings-commitments" aria-label="Capacity commitments"><h3>Capacity commitments</h3><p>Saved standing allocations and scheduled bookings for {projects.find(item => item.id === projectId)?.name || 'all authorized projects'}. These have different admission rules and are not added together. Unsaved allocation edits appear in the editor below.</p>
    <label>Capacity pool<select value={poolId} onChange={e => setPoolId(e.target.value)}><option value="">All pools</option><option value="global">Global envelope</option>{pools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}</select></label>
    <div className="settings-inline-actions"><button type="button" className="btn btn-secondary" onClick={() => setRefresh(value => value + 1)}>Refresh bookings</button> <a href={workspaceHref('schedules', window.location.hash, projectId)}>Manage scheduled bookings</a></div>
    {loading && <p role="status">Loading bookings…</p>}{error && <p role="alert">{error}</p>}
    {rows.length > 0 && <div className="access-table-wrap"><table className="access-data-table"><thead><tr>{['Type', 'Project', 'Pool', 'Window (local time)', 'Capacity', 'Status'].map(label => <th scope="col" key={label}>{label}</th>)}</tr></thead><tbody>{rows.map(row => <tr key={row.id}><td>{row.kind}</td><td>{projects.find(item => item.id === row.project)?.name || row.project}</td><td>{row.pool ? <a href={settingsHref('pools', window.location.hash, row.pool)}>{pools.find(item => item.id === row.pool)?.name || row.pool}</a> : 'Global envelope'}</td><td>{row.start ? `${new Date(row.start).toLocaleString()} – ${row.end ? new Date(row.end).toLocaleString() : 'No end'}` : 'Always active'}</td><td>{row.amount}</td><td>{row.status}</td></tr>)}</tbody></table></div>}
    {!loading && !error && !rows.length && <p>No commitments match this scope.</p>}
  </section>
}
