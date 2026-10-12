import { useEffect, useMemo, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import type { SharedDataPool } from '../types/dataPools'
import type { ProjectLiveTableCreate, ProjectLiveTableLifecycle, ProjectLiveTableMutation, ProjectLiveTableRow, ProjectLiveTableRows, ProjectLiveTableView } from '../types/projectLiveTables'
import { getErrorMessage } from '../utils/errorUtils'
import RepositorySectionTabs from './RepositorySectionTabs'
import { decimalInteger, projectTableCreateErrors, projectTableLifecycleBlock } from './projectLiveTableModel'
import './ProjectLiveTablesPage.css'

const profileLabel = (profile: string) => profile === 'native_queue' ? 'Native work queue' : 'Shared columns'

export default function ProjectLiveTablesPage({ projectId, projectName, canCreate = false }: { projectId?: string; projectName?: string; canCreate?: boolean }) {
  const api = useApi()
  const [tables, setTables] = useState<ProjectLiveTableView[]>([])
  const [pools, setPools] = useState<SharedDataPool[]>([])
  const [selected, setSelected] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [refresh, setRefresh] = useState(0)
  const [creating, setCreating] = useState(false)
  const [create, setCreate] = useState<ProjectLiveTableCreate>({ name: '', profile: 'native_queue', columns: [], limits: { max_rows: 10000, max_bytes: 8388608 } })
  const [attempted, setAttempted] = useState(false)
  const contribution = useMemo(() => ({ id: 'project-live-tables', title: 'Live tables', controls: <RepositorySectionTabs current="live-tables" />, priority: 10 }), [])
  useTopbarContribution(contribution)
  useEffect(() => {
    const focus = () => {
      const query = new URLSearchParams(window.location.hash.split('?')[1] || '')
      const id = query.get('resourceId')
      if (id && query.get('setting') === 'live-tables' && query.get('projectId') === projectId) setSelected(id)
    }
    focus(); window.addEventListener('hashchange', focus)
    return () => window.removeEventListener('hashchange', focus)
  }, [projectId])
  useEffect(() => {
    const abort = new AbortController()
    setLoading(true); setError('')
    if (!projectId) { setTables([]); setLoading(false); return () => abort.abort() }
    api.listProjectLiveTables(projectId, { signal: abort.signal }).then(response => {
      if (!abort.signal.aborted) { setTables(response.tables); setLoading(false) }
    }).catch(cause => { if (!abort.signal.aborted) { setTables([]); setLoading(false); setError(getErrorMessage(cause, 'Unable to load project live tables.')) } })
    return () => abort.abort()
  }, [api, projectId, refresh])
  useEffect(() => {
    let active = true
    if (projectId && canCreate) void api.listDataPools(projectId).then(response => { if (active) setPools(response.data_pools || []) }).catch(() => { if (active) setPools([]) })
    return () => { active = false }
  }, [api, projectId, canCreate])
  const submitCreate = async () => {
    setAttempted(true)
    if (!projectId || creating || projectTableCreateErrors(create).length) return
    setCreating(true); setError('')
    try {
      const result = await api.createProjectLiveTable(projectId, { ...create, name: create.name.trim(), seed_pool_id: create.seed_pool_id || undefined })
      setSelected(result.id); setRefresh(value => value + 1); setCreate({ ...create, name: '', seed_pool_id: undefined }); setAttempted(false)
    } catch (cause) { setError(getErrorMessage(cause, 'Unable to create the table.')) }
    finally { setCreating(false) }
  }
  return <main className="page project-live-tables-page">
    <p>Project tables share committed data across concurrent runs in {projectName || 'the selected project'}. They remain available after a run finishes. Existing shared data pools and run tables keep their own lifecycle.</p>
    {!projectId ? <p role="status">Select a project to inspect its live tables.</p> : <>
      <button type="button" className="btn btn-secondary" onClick={() => setRefresh(value => value + 1)}>Refresh project tables</button>
      {error && <p role="alert">{error}</p>}
      {loading ? <p role="status">Loading project live tables…</p> : tables.length === 0 && !error ? <p>No project live tables yet. Create one to share runtime data between runs.</p> : <label>Project live table<select value={selected} onChange={event => setSelected(event.target.value)}><option value="">Select a table</option>{tables.map(table => <option key={table.id} value={table.id}>{table.name} · {profileLabel(table.profile)} · {table.state}</option>)}</select></label>}
      {canCreate && <details><summary>Create project live table</summary><fieldset disabled={creating}><legend>Starting definition</legend>
        <p>The source pool is read once at creation. Recovery uses durable encrypted state. Leaving the source empty creates an empty table with the declared columns.</p>
        <div className="settings-grid">
          <label>Name<input value={create.name} maxLength={200} onChange={event => setCreate({ ...create, name: event.target.value })} /></label>
          <label>Table behavior<select value={create.profile} onChange={event => setCreate({ ...create, profile: event.target.value as ProjectLiveTableCreate['profile'] })}><option value="native_queue">Native work queue</option><option value="vts_columns">Shared columns</option></select></label>
          <label>Columns<input value={create.columns.join(',')} placeholder="id,email" onChange={event => setCreate({ ...create, columns: event.target.value.split(',').map(column => column.trim()) })} /></label>
          <label>Seed from pool<select value={create.seed_pool_id || ''} onChange={event => setCreate({ ...create, seed_pool_id: event.target.value, ...(event.target.value && !create.columns.length ? { columns: Object.keys(pools.find(pool => pool.id === event.target.value)?.schema || {}) } : {}) })}><option value="">Start empty</option>{pools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}</select></label>
          <label>Maximum rows<input type="number" min={1} max={100000} value={create.limits.max_rows} onChange={event => setCreate({ ...create, limits: { ...create.limits, max_rows: Number(event.target.value) } })} /></label>
          <label>Maximum table size (bytes)<input type="number" min={1024} max={8388608} value={create.limits.max_bytes} onChange={event => setCreate({ ...create, limits: { ...create.limits, max_bytes: Number(event.target.value) } })} /></label>
        </div>
        {attempted && projectTableCreateErrors(create).map(issue => <p role="alert" key={issue}>{issue}</p>)}
        <button type="button" className="btn btn-primary" onClick={() => void submitCreate()}>{creating ? 'Creating…' : 'Create table'}</button>
      </fieldset></details>}
      {selected && <ProjectTableDetail key={`${projectId}:${selected}`} projectId={projectId} tableId={selected} onChanged={() => setRefresh(value => value + 1)} />}
    </>}
  </main>
}

function ProjectTableDetail({ projectId, tableId, onChanged }: { projectId: string; tableId: string; onChanged: () => void }) {
  const api = useApi()
  const [table, setTable] = useState<ProjectLiveTableView | null>(null)
  const [rows, setRows] = useState<ProjectLiveTableRows | null>(null)
  const [offset, setOffset] = useState(0)
  const [pageOffsets, setPageOffsets] = useState<number[]>([0])
  const [error, setError] = useState('')
  const [metadataError, setMetadataError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const [edit, setEdit] = useState<(ProjectLiveTableRow & { position: number; revision: string; instance: string }) | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  const [operation, setOperation] = useState<ProjectLiveTableMutation['operation']>('append')
  const [columns, setColumns] = useState<string[]>([])
  const [delta, setDelta] = useState('1')
  const [lifecycle, setLifecycle] = useState<ProjectLiveTableLifecycle['action']>('quiesce')
  const [reason, setReason] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const [pending, setPending] = useState<ProjectLiveTableMutation | null>(null)
  const [pendingExpired, setPendingExpired] = useState(false)
  const [reconciliationReviewed, setReconciliationReviewed] = useState(false)
  const mounted = useRef(true)
  const currentTable = useRef<ProjectLiveTableView | null>(null)
  const previewEpoch = useRef(0)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; currentTable.current = null } }, [])
  useEffect(() => {
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    const read = async () => {
      try {
        const result = await api.getProjectLiveTable(projectId, tableId, { signal: abort.signal })
        if (abort.signal.aborted) return
        const prior = currentTable.current
        currentTable.current = result
        setTable(result)
        setMetadataError('')
        if (prior?.instance_id !== result.instance_id || prior?.stats?.revision !== result.stats?.revision || !result.permissions.can_view) setReconciliationReviewed(false)
        setRows(previous => prior?.instance_id === result.instance_id && previous?.revision === result.stats?.revision && result.permissions.can_view ? previous : null)
        if (!result.permissions.can_view || (prior && prior.instance_id !== result.instance_id)) { setEdit(null); setValues({}); setOperation('append') }
      } catch (cause) { if (!abort.signal.aborted) { currentTable.current = null; setTable(null); setRows(null); setReconciliationReviewed(false); if (cause instanceof ApiError && [401, 403, 404].includes(cause.status)) { setEdit(null); setValues({}) } setMetadataError(getErrorMessage(cause, 'Unable to refresh the table.')) } }
      finally { if (!abort.signal.aborted) timer = setTimeout(() => void read(), 5000) }
    }
    void read()
    return () => { abort.abort(); clearTimeout(timer) }
  }, [api, projectId, tableId, refresh])
  const loadRows = async (nextOffset = 0, direction?: 'next' | 'previous') => {
    if (!table?.stats?.revision || busy) return
    setBusy(true); setError(''); setReconciliationReviewed(false)
    const epoch = ++previewEpoch.current
    try {
      const result = await api.getProjectLiveTableRows(projectId, tableId, table.instance_id, table.stats.revision, nextOffset)
      if (mounted.current && previewEpoch.current === epoch && currentTable.current?.instance_id === table.instance_id && currentTable.current?.stats?.revision === result.revision && currentTable.current?.permissions.can_view) { setReconciliationReviewed(false); setRows(result); setOffset(nextOffset); setPageOffsets(previous => direction === 'next' ? [...previous, nextOffset] : direction === 'previous' ? previous.slice(0, -1) : [nextOffset]) }
    } catch (cause) { if (mounted.current) { setRows(null); setError(getErrorMessage(cause, 'Unable to preview rows. Refresh the table and try again.')); setRefresh(value => value + 1) } }
    finally { if (mounted.current) setBusy(false) }
  }
  const mutate = async (retry?: ProjectLiveTableMutation) => {
    if (!table?.stats?.revision || busy) return
    const command = retry || { idempotency_key: crypto.randomUUID(), issued_at: new Date().toISOString(), expected_instance_id: edit?.instance || table.instance_id, expected_revision: edit?.revision || table.stats.revision, operation, ...(edit ? table.profile === 'native_queue' ? { record_id: edit.id, expected_version: edit.version } : { position: edit.position } : {}), ...(operation === 'append' || operation === 'update' ? { values } : { columns }), ...(operation === 'increment' ? { delta } : {}) }
    setBusy(true); setError(''); setNotice(''); setReconciliationReviewed(false)
    try {
      const response = await api.mutateProjectLiveTable(projectId, tableId, command)
      if (!mounted.current) return
      if (response.result.status !== 'ok') {
        setPending(null); setPendingExpired(false); setRows(null); setRefresh(value => value + 1)
        setError(`The command did not change the table (${response.result.status.replace(/_/g, ' ')}). Your draft is preserved; preview the current row before trying again.`)
        return
      }
      setPending(null); setPendingExpired(false); setNotice(`${response.replayed ? 'Original outcome recovered' : 'Command completed'}: ${response.result.status}. Revision ${response.result.revision}.`)
      setEdit(null); setValues({}); setOperation('append'); setRows(null); setRefresh(value => value + 1); onChanged()
    } catch (cause) {
      if (!mounted.current) return
      // A retry rejection cannot disprove a prior commit. Initial auth failures
      // can also follow a commit because the server rechecks access afterward.
      const unknown = !!retry || !(cause instanceof ApiError) || ![400, 409, 422].includes(cause.status)
      const expired = cause instanceof ApiError && cause.status === 410
      setPending(unknown ? command : null); setRows(null); setRefresh(value => value + 1)
      setPendingExpired(unknown && expired)
      setError(`${getErrorMessage(cause, 'The command outcome is unknown.')} ${expired ? 'The original outcome can no longer be recovered. Preview and review the table before clearing this pending command.' : unknown ? 'Retain and retry the same command when access and capacity allow; its original outcome is still unknown.' : 'Your draft is preserved. Refresh and review the current row before applying it again.'}`)
    } finally { if (mounted.current) setBusy(false) }
  }
  const changeLifecycle = async () => {
    if (!table?.stats?.revision || busy || !reason.trim() || !confirmed) return
    setBusy(true); setError('')
    try {
      await api.changeProjectLiveTableLifecycle(projectId, tableId, { action: lifecycle, expected_config_revision: table.config_revision, expected_instance_id: table.instance_id, expected_revision: table.stats.revision, reason: reason.trim() })
      if (mounted.current) { setRows(null); setEdit(null); setValues({}); setOperation('append'); setReason(''); setConfirmed(false); setRefresh(value => value + 1); onChanged() }
    } catch (cause) { if (mounted.current) { setRows(null); setError(`${getErrorMessage(cause, 'Unable to change the lifecycle.')} Refresh before retrying.`); setRefresh(value => value + 1) } }
    finally { if (mounted.current) setBusy(false) }
  }
  const exportRows = async () => {
    if (!table?.stats?.revision || busy) return
    setBusy(true); setError('')
    try {
      const data = await api.exportProjectLiveTable(projectId, tableId, table.instance_id, table.stats.revision)
      if (!mounted.current) return
      const current = currentTable.current
      if (!current?.permissions.can_view || current.instance_id !== table.instance_id || current.stats?.revision !== table.stats.revision) {
        setError('Table access or revision changed while exporting. Refresh and review the table before exporting again.')
        return
      }
      const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }))
      const link = document.createElement('a'); link.href = url; link.download = `live-table-${table.id}.json`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (cause) { if (mounted.current) setError(getErrorMessage(cause, 'Unable to export the table.')) }
    finally { if (mounted.current) setBusy(false) }
  }
  const lifecycleBlock = table && (lifecycle === 'reset' || lifecycle === 'archive') ? projectTableLifecycleBlock(table, lifecycle) : null
  const reviewedRow = edit && rows && rows.revision === table?.stats?.revision ? table?.profile === 'native_queue' ? rows.rows.find(row => row.id === edit.id) : rows.rows[edit.position - offset - 1] : undefined
  return <section className="settings-subsection" aria-label="Project live table details">
    {error && <p role="alert">{error}</p>}{metadataError && <p role="alert">{metadataError}</p>}{notice && <p role="status">{notice}</p>}
    {!table ? !error && !metadataError && <p role="status">Loading table details…</p> : <>
      <h2>{table.name}</h2><p>Project scope · {profileLabel(table.profile)} · {table.state} · Configuration revision {table.config_revision} · Data revision {table.stats?.revision || 'Unavailable'}</p>
      <p>Instance <code>{table.instance_id}</code>. Finishing a run detaches it; it does not clear or close this table.</p>
      <p>Available: {table.stats?.counts?.available ?? 'Unknown'} · Leased: {table.stats?.counts?.leased ?? 'Unknown'} · Size: {table.stats?.data_bytes ?? 'Unknown'} bytes · Capacity: {table.stats?.capacity?.max_rows ?? 'Unknown'} rows, {table.stats?.capacity?.max_bytes ?? 'Unknown'} bytes.</p>
      <p>Command retries retain the original outcome for {table.stats?.capacity?.retry_window_ms ? `${table.stats.capacity.retry_window_ms / 1000} seconds` : 'the configured retry window'}. After that window, inspect the table before starting a new command.</p>
      <h3>Attached runs</h3>{!table.attachments?.length ? <p>No runs attached.</p> : <ul>{table.attachments.map((attachment, index) => <li key={attachment.id || index}><a href={`#/test/${encodeURIComponent(attachment.run_id)}?projectId=${encodeURIComponent(projectId)}`}>{attachment.run_id}</a> · {attachment.group_name} · {attachment.alias} · {attachment.state}</li>)}</ul>}
      <p>Preview and export reveal test data and are audited. Each preview is bound to the instance and revision shown above.</p>
      <div className="project-live-table-actions"><button type="button" className="btn btn-secondary" disabled={busy || !table.permissions.can_view || !table.stats?.revision} onClick={() => void loadRows()}>Preview up to 100 rows</button><button type="button" className="btn btn-secondary" disabled={busy || !table.permissions.can_view || !table.stats?.revision} onClick={() => void exportRows()}>Export table</button><button type="button" className="btn btn-ghost" onClick={() => { previewEpoch.current++; setRows(null); setReconciliationReviewed(false); setEdit(null); setValues({}); setOperation('append') }}>Hide row values</button><button type="button" className="btn btn-ghost" onClick={() => setRefresh(value => value + 1)}>Refresh table</button></div>
      {rows && <><p>Preview revision {rows.revision} · {rows.total ? offset + 1 : 0}–{Math.min(offset + rows.rows.length, rows.total)} of {rows.total} rows.</p><div className="data-table"><table aria-label="Project live table rows"><thead><tr>{rows.columns.map(column => <th key={column}>{column}</th>)}<th>State</th><th>Actions</th></tr></thead><tbody>{rows.rows.map((row, index) => <tr key={row.id || index}>{rows.columns.map(column => <td key={column}>{Object.prototype.hasOwnProperty.call(row.values, column) ? row.values[column] : <em>Absent</em>}</td>)}<td>{row.state || 'Available'}</td><td><button type="button" className="btn btn-ghost" disabled={!table.permissions.can_mutate || table.state !== 'active' || row.state === 'leased' || busy || !!pending} onClick={() => { setEdit({ ...row, position: offset + index + 1, revision: rows.revision, instance: table.instance_id }); setValues({ ...row.values }); setOperation('update'); setColumns([]) }}>Edit row</button></td></tr>)}</tbody></table></div><button type="button" disabled={busy || offset === 0} onClick={() => void loadRows(pageOffsets[pageOffsets.length - 2] ?? 0, 'previous')}>Previous rows</button><button type="button" disabled={busy || offset + rows.rows.length >= rows.total} onClick={() => void loadRows(offset + rows.rows.length, 'next')}>Next rows</button></>}
      {table.permissions.can_mutate && table.state === 'quiesced' && <p role="status">This table is quiesced. Resume it before applying new row changes.</p>}
      {table.permissions.can_mutate && table.state !== 'archived' && <fieldset disabled={busy || !!pending || table.state !== 'active'}><legend>{edit ? `Edit row ${edit.id} at version ${edit.version}` : 'Append a row'}</legend>
        <p>{edit ? 'Edits require the row version to match and reject leased rows. A conflict keeps your draft for review.' : 'Select the cells to include. An unchecked cell remains absent; a checked empty field is an empty string.'}</p>
        {edit && reviewedRow && edit.revision !== rows?.revision && <button type="button" className="btn btn-secondary" onClick={() => setEdit({ ...reviewedRow, position: edit.position, revision: rows!.revision, instance: table.instance_id })}>Use reviewed row version and keep my draft</button>}
        {edit && <label>Operation<select value={operation} onChange={event => setOperation(event.target.value as ProjectLiveTableMutation['operation'])}><option value="update">Conditional update</option><option value="clear">Clear selected cells</option><option value="increment">Increment a cell</option></select></label>}
        <div className="settings-grid">{table.columns.map(column => <label key={column}><span><input type="checkbox" aria-label={`${operation === 'clear' || operation === 'increment' ? 'Select' : 'Include'} ${column}`} checked={operation === 'clear' || operation === 'increment' ? columns.includes(column) : Object.prototype.hasOwnProperty.call(values, column)} onChange={event => {
          if (operation === 'clear' || operation === 'increment') setColumns(event.target.checked ? operation === 'increment' ? [column] : [...columns, column] : columns.filter(value => value !== column))
          else setValues(previous => { const next = { ...previous }; if (event.target.checked) next[column] = ''; else delete next[column]; return next })
        }} />{column}</span>{operation !== 'clear' && operation !== 'increment' && <input aria-label={`Value for ${column}`} disabled={!Object.prototype.hasOwnProperty.call(values, column)} value={values[column] ?? ''} onChange={event => setValues({ ...values, [column]: event.target.value })} />}</label>)}</div>
        {operation === 'increment' && <label>Integer delta<input value={delta} inputMode="numeric" maxLength={20} onChange={event => setDelta(event.target.value)} aria-invalid={!decimalInteger(delta)} /></label>}
        <button type="button" className="btn btn-primary" disabled={!table.stats?.revision || ((operation === 'append' || operation === 'update') && !Object.keys(values).length) || ((operation === 'clear' || operation === 'increment') && !columns.length) || (operation === 'increment' && !decimalInteger(delta))} onClick={() => void mutate()}>{edit ? 'Apply row change' : 'Append row'}</button>
        {edit && <button type="button" className="btn btn-ghost" onClick={() => { setEdit(null); setValues({}); setOperation('append') }}>Cancel row edit</button>}
      </fieldset>}
      {pending && <div><p role="status">A command needs reconciliation. Its original key and payload are retained. <button type="button" className="btn btn-secondary" disabled={busy || !table.permissions.can_mutate || pendingExpired} onClick={() => void mutate(pending)}>Retry same command</button></p>
        {!table.permissions.can_mutate && <p>Current data-management permission is required to recover the outcome.</p>}
        {pendingExpired && <><p>The retry window expired. The earlier command may have completed. Review a current preview and retained evidence before clearing its identity; clearing it sends no new command.</p><label><input type="checkbox" aria-label="Confirm reconciliation review" disabled={busy || !rows || rows.revision !== table.stats?.revision} checked={reconciliationReviewed} onChange={event => setReconciliationReviewed(event.target.checked)} />I reviewed the table and understand that the earlier command may have completed.</label><button type="button" className="btn btn-secondary" disabled={busy || !table.permissions.can_mutate || !reconciliationReviewed || !rows || rows.revision !== table.stats?.revision} onClick={() => { setPending(null); setPendingExpired(false); setReconciliationReviewed(false); setEdit(null); setValues({}); setColumns([]); setOperation('append'); setError(''); setNotice('Pending command cleared after review. No new command was sent.') }}>Clear pending command after review</button></>}
      </div>}
      {table.permissions.can_admin && <details><summary>Table lifecycle</summary><fieldset disabled={busy || !!pending}><legend>Governed lifecycle action</legend><label>Action<select value={lifecycle} onChange={event => { setLifecycle(event.target.value as ProjectLiveTableLifecycle['action']); setConfirmed(false) }}><option value="quiesce">Quiesce — stop new attachments</option><option value="resume">Resume — allow new attachments</option><option value="reset">Reset — create a fresh instance</option><option value="archive">Archive table</option></select></label><p>Reset and archive require quiescence, all attachment retry windows to finish and no leases. Reset changes the instance identity; old capabilities cannot be reused.</p>{lifecycleBlock && <p role="status">{lifecycleBlock}</p>}<label>Reason<textarea value={reason} maxLength={1024} onChange={event => setReason(event.target.value)} /></label><label><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />I confirm this lifecycle change for {table.name}.</label><button type="button" className="btn btn-secondary" disabled={!!lifecycleBlock || !confirmed || !reason.trim() || !table.stats?.revision} onClick={() => void changeLifecycle()}>Apply lifecycle change</button></fieldset></details>}
    </>}
  </section>
}
