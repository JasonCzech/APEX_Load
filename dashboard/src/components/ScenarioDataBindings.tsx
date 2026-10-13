import { useEffect, useMemo, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { ScenarioDataPoolBinding, SharedDataPool, SharedDataPoolRow, StoredScript } from '../types'
import type { DataPoolImportResult } from '../hooks/api/useScriptApi'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import { aliasForPool, dataPoolColumnNames, isUntouchedDataBinding, scenarioDataRequirements, touchedDataBindingErrors, DEFAULT_LIVE_TABLE_OPTIONS } from './scenarioDataBindingModel'
import DataRowGrid from './ux/DataRowGrid'
import { orderedGridColumns } from './ux/dataRowGridModel'
import { settingsHref } from './settingsNavigation'
import ScenarioScriptData from './ScenarioScriptData'
import LiveTableBindingOptions from './LiveTableBindingOptions'
import ProjectLiveTableBinding from './ProjectLiveTableBinding'

function PoolPreview({ binding, required, canManage, canEditPool, poolColumns }: { binding: ScenarioDataPoolBinding; required: string[]; canManage: boolean; canEditPool: boolean; poolColumns: string[] }) {
  const api = useApi()
  const [open, setOpen] = useState(false)
  const [rows, setRows] = useState<SharedDataPoolRow[]>([])
  const [total, setTotal] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [inspection, setInspection] = useState<DataPoolImportResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const [notice, setNotice] = useState('')
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  useEffect(() => {
    if (!open || !binding.pool_id) return
    let cancelled = false
    setRows([]); setTotal(null); setError('')
    api.getDataPool(binding.pool_id, { limit: 5, offset: 0 }).then(result => {
      if (cancelled) return
      setRows(result.rows || []); setTotal(result.total ?? result.count ?? null)
    }).catch(cause => { if (!cancelled) setError(getErrorMessage(cause, 'Unable to preview data.')) })
    return () => { cancelled = true }
  }, [api, binding.pool_id, open, revision])
  const importRows = async (dryRun: boolean) => {
    if (!file || busy || !canManage) return
    setBusy(true); setError(''); setNotice('')
    try {
      const result = await api.importDataPoolRows(binding.pool_id, file, { mode: 'append', delimiter: 'auto', header: true, dryRun })
      if (!mounted.current) return
      if (dryRun) setInspection(result)
      else { setInspection(null); setFile(null); setRevision(value => value + 1); setNotice(`Imported ${result.imported ?? result.row_count ?? 0} rows.`) }
    } catch (cause) { if (mounted.current) setError(`${getErrorMessage(cause, 'Unable to import rows.')} After a network error, check the pool rows before retrying to avoid duplicate imports.`) }
    finally { if (mounted.current) setBusy(false) }
  }
  const columns = [...new Set([...required, ...orderedGridColumns(poolColumns, rows.map(row => row.data))])]
  return <details onToggle={event => setOpen(event.currentTarget.open)}><summary>Preview rows and import CSV</summary>
    {open && <>
      <p>{total === null ? 'Row count not verified' : `${total} rows in shared pool`}. Showing at most 5 rows; preview values may contain sensitive test data.</p>
      {total === 0 && <p role="status">{binding.access_mode === 'live' ? 'This pool is empty. The live table starts empty if no rows are added before launch; producers can add runtime rows.' : 'This pool is empty. Add rows before configuring a run.'}</p>}
      {required.length > 0 && <p>Required columns: {required.join(', ')}. {rows.some(row => required.some(column => !(column in row.data))) ? 'Some preview rows are missing required columns.' : 'Validate the full pool before execution; the preview is only a sample.'}</p>}
      {!!rows.length && <DataRowGrid label={`Rows for ${binding.alias}`} columns={columns} rows={rows.map(row => ({ id: row.id, values: row.data }))} />}
      {canEditPool && <p><a href={settingsHref('test-data', undefined, binding.pool_id)}>Edit this pool's rows in Shared test data</a></p>}
      <fieldset disabled={busy || !canManage}><legend>Append rows from CSV</legend>{!canManage && <p>Importing rows requires data-pool management permission.</p>}<p>Use a header row with column names. Preview the import before adding rows to the shared pool.</p>
        <ImportDropzone dropHint="Drop a CSV with a header row" accept=".csv,.tsv" file={file} onFile={next => { setFile(next); setInspection(null); setNotice('') }} onError={setError} disabled={busy} />
        {file && <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void importRows(true)}>Preview CSV import</button>}
        {inspection && <><p>{inspection.row_count ?? 0} rows · Columns: {inspection.columns.join(', ')}</p>
          {inspection.warnings?.map(warning => <p key={warning}>{warning}</p>)}
          {inspection.append_warning && <p role="alert">{inspection.append_warning}</p>}
          {required.some(column => !inspection.columns.includes(column)) && <p role="alert">CSV is missing required columns: {required.filter(column => !inspection.columns.includes(column)).join(', ')}</p>}
          <div className="data-table"><table aria-label="CSV data preview"><thead><tr>{inspection.columns.map(column => <th key={column}>{column}</th>)}</tr></thead><tbody>{inspection.sample_rows?.slice(0, 5).map((row, index) => <tr key={index}>{inspection.columns.map(column => <td key={column}>{row[column] ?? 'Missing'}</td>)}</tr>)}</tbody></table></div>
          <button type="button" className="btn btn-primary" disabled={busy || !!inspection.append_warning || required.some(column => !inspection.columns.includes(column))} onClick={() => void importRows(false)}>Import rows into shared pool</button></>}
      </fieldset>
      {notice && <p role="status">{notice}</p>}{error && <p role="alert" className="settings-error-inline">{error}</p>}
    </>}
  </details>
}
export default function ScenarioDataBindings({ bindings, pools, scripts, onChange, canManage, canManageLiveOutput = false, projectId }: {
  projectId?: string
  bindings: ScenarioDataPoolBinding[]; pools: SharedDataPool[]; scripts: StoredScript[];
  onChange: (bindings: ScenarioDataPoolBinding[]) => void; canManage: boolean; canManageLiveOutput?: boolean
}) {
  const api = useApi()
  const scriptScope = JSON.stringify(scripts.map(script => [script.id, script.version]))
  const scriptRefs = useRef(scripts)
  scriptRefs.current = scripts
  const [scriptDetails, setScriptDetails] = useState<{ scope: string; scripts: StoredScript[]; unavailable: number } | null>(null)
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    let cancelled = false
    void (async () => {
      const details: StoredScript[] = []; let unavailable = 0
      for (const script of scriptRefs.current) {
        if (cancelled) return
        try { const detail = script.script ? script : await api.getScript(script.id); if (detail.script) details.push(detail); else unavailable++ } catch { unavailable++ }
      }
      if (!cancelled) setScriptDetails({ scope: scriptScope, scripts: details, unavailable })
    })()
    return () => { cancelled = true }
  }, [api, scriptScope, retry])
  const currentDetails = scriptDetails?.scope === scriptScope ? scriptDetails : null
  const requirements = useMemo(() => scenarioDataRequirements(currentDetails?.scripts || []), [currentDetails])
  const update = (index: number, patch: Partial<ScenarioDataPoolBinding>) => onChange(bindings.map((binding, i) => i === index ? { ...binding, ...patch } : binding))
  // Picking a pool names the binding after it, unless the alias was typed.
  const selectPool = (index: number, poolId: string) => {
    const binding = bindings[index]
    const others = bindings.flatMap((candidate, i) => i === index ? [] : [candidate.alias])
    const previous = pools.find(pool => pool.id === binding.pool_id)
    const automatic = !binding.alias || (previous !== undefined && binding.alias === aliasForPool(previous.name, others))
    const pool = pools.find(candidate => candidate.id === poolId)
    update(index, automatic && pool ? { pool_id: poolId, alias: aliasForPool(pool.name, others) } : { pool_id: poolId })
  }
  return <div className="scenario-data-editor">
    {!currentDetails ? <p role="status">Checking script data references…</p> : currentDetails.unavailable > 0 && <p role="status">Data requirements could not be inspected for {currentDetails.unavailable} scripts. Review them in Scripts before running.</p>}
    <p>Bindings are shared across scenario groups. Reference a column with <code>{'{{data.customers.email}}'}</code>. Each alias identifies one data binding.</p>
    {(currentDetails?.scripts || []).flatMap(script => [...scenarioDataRequirements([script]).keys()]
      .filter(alias => !bindings.some(binding => binding.alias === alias) && !script.script?.data_sources?.some(source => source.name === alias))
      .map(alias => <p key={`${script.id}:${alias}`} role="status">{script.name} references <code>{alias}</code>, but no script source or scenario pool supplies it.</p>))}
    {touchedDataBindingErrors(bindings).map(error => <p key={error} role="alert" className="settings-error-inline">{error}</p>)}
    {bindings.length === 0 && <p>No data bindings. Scripts use their own data sources unless a binding supplies the alias they reference.</p>}
    {bindings.map((binding, index) => <fieldset key={index}><legend>Data binding {index + 1}</legend><div className="settings-grid">
      {binding.scope !== 'project' && <label><span>Data pool</span><select disabled={binding.access_mode === 'live' && ['empty', 'original_seed'].includes(binding.live_options?.seed_policy || '')} value={binding.pool_id} onChange={event => event.target.value ? selectPool(index, event.target.value) : onChange(bindings.filter((_, i) => i !== index))}><option value="">Select a pool</option>{!pools.some(pool => pool.id === binding.pool_id) && binding.pool_id && <option value={binding.pool_id}>Unavailable pool — choose a replacement</option>}{pools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}</select></label>}
      <label><span>Alias</span><input value={binding.alias} onChange={event => update(index, { alias: event.target.value })} placeholder="customers" /></label>
      <label><span>Access mode</span><select value={binding.access_mode === 'live' ? 'live' : 'read_only'} onChange={event => update(index, event.target.value === 'live'
        ? { access_mode: 'live', selection_strategy: 'manual', live_options: { ...DEFAULT_LIVE_TABLE_OPTIONS, columns: dataPoolColumnNames(pools.find(pool => pool.id === binding.pool_id)) } }
        : { access_mode: 'read_only', scope: undefined, project_table_id: undefined, selection_strategy: 'sequential', live_options: undefined })}><option value="read_only">Read-only run snapshot</option><option value="live">Shared live table</option></select></label>
      {binding.access_mode === 'live' && <label>Table scope<select value={binding.scope || 'run'} onChange={event => update(index, event.target.value === 'project' ? { scope: 'project', project_table_id: '', pool_id: '', selection_strategy: 'manual', live_options: { empty_policy: 'error', operation_timeout_ms: 120000 } } : { scope: 'run', project_table_id: undefined, selection_strategy: 'manual', live_options: { ...DEFAULT_LIVE_TABLE_OPTIONS, seed_policy: 'empty', columns: [] } })}><option value="run">This run only</option><option value="project">Shared across project runs</option></select></label>}
      {binding.access_mode !== 'live' && <label><span>Selection strategy</span><select value={binding.selection_strategy} onChange={event => update(index, { selection_strategy: event.target.value as ScenarioDataPoolBinding['selection_strategy'] })}><option value="sequential">Sequential rows</option><option value="random">Random rows</option><option value="unique">Unique rows</option></select></label>}
      </div>{isUntouchedDataBinding(binding) && <p className="new-test-inline-note">Choose a data pool, or set Access mode to a shared live table.</p>}{binding.access_mode === 'live' ? binding.scope === 'project' ? <ProjectLiveTableBinding projectId={projectId} binding={binding} onChange={patch => update(index, patch)} /> : <LiveTableBindingOptions binding={binding} canManage={canManageLiveOutput} onChange={patch => update(index, patch)} /> : <p>{binding.selection_strategy === 'unique' ? 'Unique selection assigns a stable row per VUser, reused across its iterations. The runtime wraps when rows are insufficient; scenario validation blocks known unique-row shortages. Validate the pool against peak VUsers before running.' : 'Sequential and random selection can reuse rows. Use unique selection when identities must not be reused.'} Rows are snapshotted when the run is created.</p>}
      <button type="button" className="btn btn-ghost" onClick={() => onChange(bindings.filter((_, i) => i !== index))}>Remove binding {index + 1}</button>
      {binding.pool_id && !(binding.access_mode === 'live' && ['empty', 'original_seed'].includes(binding.live_options?.seed_policy || '')) && <PoolPreview canManage={canManage} canEditPool={canManageLiveOutput} key={binding.pool_id} binding={binding} poolColumns={dataPoolColumnNames(pools.find(pool => pool.id === binding.pool_id))} required={[...(requirements.get(binding.alias) || [])]} />}
    </fieldset>)}
    <button type="button" className="btn btn-secondary" onClick={() => onChange([...bindings, { pool_id: '', alias: '', access_mode: 'read_only', selection_strategy: 'sequential' }])}>Add data binding</button>
    {!bindings.some(binding => binding.pool_id || binding.project_table_id) && <ScenarioScriptData scripts={scripts} details={currentDetails?.scripts || []} loading={!currentDetails} unavailable={currentDetails?.unavailable || 0} onRetry={() => { setScriptDetails(null); setRetry(value => value + 1) }} />}
  </div>
}
