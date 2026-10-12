import ProjectLiveTableReferences from './ProjectLiveTableReferences'
import { LiveTableFinalizationOutcome, LiveTableOperationSummary } from './LiveTableReportSummary'
import { useEffect, useRef, useState } from 'react'
import { apiFetch, request } from '../hooks/apiClient'
import { liveTablesPath, liveTableStateLabel, useLiveTablesSnapshot, type LiveTableSummary } from '../hooks/useLiveTablesSnapshot'
import type { DashboardAuthEffectiveAccess } from '../types'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { downloadBlob } from '../utils/fileUtils'
import LoadingState from './ux/LoadingState'
import EmptyState from './ux/EmptyState'
import './LiveTables.css'

export type { LiveTableSummary }
interface LiveTableRows { revision: number; columns?: string[]; rows?: { id?: string; version: number; values: Record<string, string> }[]; count?: number }

// A revision change remounts this component so a preview never pretends to be
// the current table. Values are fetched only after an explicit disclosure action.
function LiveTablePreview({ testId, table }: { testId: string; table: LiveTableSummary }) {
  const [rows, setRows] = useState<LiveTableRows | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const controller = useRef<AbortController | null>(null)
  useEffect(() => () => controller.current?.abort(), [])
  const peek = async () => {
    controller.current?.abort()
    const pending = new AbortController(); controller.current = pending
    setBusy(true); setRows(null); setError('')
    try {
      const result = await request<LiveTableRows>(`${liveTablesPath(testId)}/${encodeURIComponent(table.id)}/rows?offset=0&limit=100&revision=${table.revision ?? 0}`, { signal: pending.signal })
      if (pending.signal.aborted) return
      if (table.revision !== undefined && result.revision !== table.revision) throw new Error('Revision changed')
      setRows(result)
    } catch { if (!pending.signal.aborted) setError('The row preview is unavailable or the table changed. Refresh the table and try again.') }
    finally { if (!pending.signal.aborted) setBusy(false) }
  }
  const exportRows = async () => {
    controller.current?.abort()
    const pending = new AbortController(); controller.current = pending
    setBusy(true); setError('')
    try {
      const response = await apiFetch(`${liveTablesPath(testId)}/${encodeURIComponent(table.id)}/export${table.revision === undefined ? '' : `?revision=${table.revision}`}`, { signal: pending.signal })
      if (!response.ok) throw new Error('Export failed')
      const blob = await response.blob()
      if (!pending.signal.aborted) await downloadBlob(blob, `live-table-${table.alias.replace(/[^A-Za-z0-9_-]/g, '_')}-revision-${table.revision ?? 'current'}.json`)
    } catch { if (!pending.signal.aborted) setError('The export is unavailable or the table changed. Refresh the table and try again.') }
    finally { if (!pending.signal.aborted) setBusy(false) }
  }
  const columns = rows?.columns || []
  return <div>
    <p>Preview and export reveal test data. A preview is a bounded snapshot; read again after the revision changes.</p>
    <div className="button-row">
      <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void peek()}>Preview up to 100 rows</button>
      <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void exportRows()}>Export table</button>
      {rows && <button type="button" className="btn btn-ghost" onClick={() => setRows(null)}>Hide row values</button>}
    </div>
    {busy && <LoadingState what="live table data" inline />}
    {error && <p role="alert">{error}</p>}
    {rows && <><p role="status">Preview revision {rows.revision} · {rows.rows?.length || 0} of {rows.count ?? rows.rows?.length ?? 0} rows</p>
      {!rows.rows?.length ? <EmptyState compact title="No live rows yet." hint="Producers can add rows while the run is active." /> : <div className="data-table"><table aria-label={`Live rows for ${table.alias}`}>
        <thead><tr>{columns.map(column => <th key={column} scope="col">{column}</th>)}</tr></thead>
        <tbody>{rows.rows.map((row, index) => <tr key={row.id || index}>{columns.map(column => <td key={column}>{Object.prototype.hasOwnProperty.call(row.values, column) ? row.values[column] === '' ? <span aria-label="Empty value">(empty)</span> : row.values[column] : <span aria-label="Absent cell">(absent)</span>}</td>)}</tr>)}</tbody>
      </table></div>}
    </>}
  </div>
}

function LiveTableFinalization({ testId, projectId, table, onSaved }: { testId: string; projectId?: string; table: LiveTableSummary; onSaved: () => void }) {
  const [allowed, setAllowed] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const pending = useRef<AbortController | null>(null)
  useEffect(() => {
    const controller = new AbortController(); pending.current = controller
    setAllowed(false)
    if (projectId) void request<DashboardAuthEffectiveAccess>(`/auth/effective-access?project_id=${encodeURIComponent(projectId)}`, { signal: controller.signal }).then(access => {
      if (!controller.signal.aborted) setAllowed(isEffectiveOperationAllowed(access.resources, 'manage_data_pools'))
    }).catch(() => { /* Access is denied until verified. */ })
    return () => controller.abort()
  }, [projectId])
  const save = async () => {
    if (!allowed || busy || !name.trim()) return
    setBusy(true); setNotice('')
    try {
      await request(`${liveTablesPath(testId)}/${encodeURIComponent(table.id)}/finalize`, { method: 'POST', body: JSON.stringify({ output_pool_name: name.trim() }), signal: pending.current?.signal })
      if (!pending.current?.signal.aborted) { setNotice('Final output saved as a new data pool.'); onSaved() }
    } catch { if (!pending.current?.signal.aborted) setNotice('Unable to save the final output. Refresh to check its status before retrying.') }
    finally { if (!pending.current?.signal.aborted) setBusy(false) }
  }
  return <div><p>Final output is awaiting reconciliation. You can preserve it in a separate pool.</p>
    {allowed ? <><label>Final output pool name<input value={name} maxLength={256} onChange={event => setName(event.target.value)} /></label><button type="button" className="btn btn-secondary" disabled={busy || !name.trim()} onClick={() => void save()}>Save as a new data pool</button></> : <p>Saving the final output requires data-pool management permission.</p>}
    {notice && <p role="status">{notice}</p>}
  </div>
}

export default function LiveTablesPanel({ testId, projectId }: { testId: string; projectId?: string }) {
  const { current, error, connectionState, retry } = useLiveTablesSnapshot(testId)
  return <section className="live-tables-panel" aria-label="Live data tables">
    <div className="section-header"><h2>Live data tables</h2><button type="button" className="btn btn-secondary" onClick={retry}>Refresh tables</button></div>
    <p>Run tables share runtime data within this run. Project table references identify data shared across runs. Live notifications refresh counts, with a 2-second refresh to recover missed updates.</p>
    <p role="status">{connectionState === 'connected' ? 'Live updates connected.' : connectionState === 'connecting' ? 'Connecting live updates. Counts may be stale until refreshed.' : 'Reconnecting live updates. Counts may be stale; previous row previews have been cleared.'}{current?.updatedAt && <> Last sampled: <time dateTime={current.updatedAt}>{new Date(current.updatedAt).toLocaleTimeString()}</time>.</>}</p>
    {error && <p role="alert">{error}</p>}
    {!current && !error && <LoadingState what="live tables" inline />}
    {current?.tables.length === 0 && current.projectTables.length === 0 && <EmptyState compact title="No live tables yet." hint="Tables become available when the run starts." />}
    <ProjectLiveTableReferences tables={current?.projectTables} projectId={projectId} />
    {current?.tables.map(table => <article key={table.id} aria-label={`Live table ${table.alias}`}>
      <h3>{table.alias}</h3><p>{table.profile === 'vts_columns' ? 'Shared columns' : 'Native work queue'} · {table.durability === 'durable' ? 'Durable recovery' : 'Memory only'} · {liveTableStateLabel(table)} · Revision {table.revision ?? 'Unknown'}</p>
      <dl className="live-table-stats">{(['available', 'leased', 'acknowledged', 'popped', 'quarantined', 'discarded', 'delivery_attempts'] as const).map(key => <div key={key}><dt>{({ available: 'Available', leased: 'Leased', acknowledged: 'Acknowledged', popped: 'Removed', quarantined: 'Quarantined', discarded: 'Discarded', delivery_attempts: 'Delivery attempts' })[key]}</dt><dd>{table.counts?.[key] ?? 'Unknown'}</dd></div>)}</dl>
      {table.op_count !== undefined && <LiveTableOperationSummary stats={table} />}
      {table.finalization?.state && <p>Finalization: {table.finalization.state}.<LiveTableFinalizationOutcome finalization={table.finalization} /></p>}
      {(table.finalization?.state === 'conflict' || (table.state === 'closed' && table.finalization?.state === 'pending' && table.finalization.reason !== 'output_limits')) && <LiveTableFinalization key={`${testId}:${table.id}:finalization`} testId={testId} projectId={projectId} table={table} onSaved={retry} />}
      {table.state !== 'unavailable' && table.state !== 'recovering' && <LiveTablePreview key={`${testId}:${table.id}:${table.revision}:${connectionState}`} testId={testId} table={table} />}
    </article>)}
  </section>
}
