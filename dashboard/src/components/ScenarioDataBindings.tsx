import { useEffect, useMemo, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { ScenarioDataPoolBinding, SharedDataPool, SharedDataPoolRow, StoredScript } from '../types'
import type { DataPoolImportResult } from '../hooks/api/useScriptApi'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import { dataBindingErrors, scenarioDataRequirements } from './scenarioDataBindingModel'
import ScenarioScriptData from './ScenarioScriptData'

function PoolPreview({ binding, required, canManage }: { binding: ScenarioDataPoolBinding; required: string[]; canManage: boolean }) {
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
  const columns = [...new Set([...required, ...rows.flatMap(row => Object.keys(row.data))])]
  return <details onToggle={event => setOpen(event.currentTarget.open)}><summary>Preview rows and import CSV</summary>
    {open && <>
      <p>{total === null ? 'Row count not verified' : `${total} rows in shared pool`}. Showing at most 5 rows; preview values may contain sensitive test data.</p>
      {total === 0 && <p role="status">This pool is empty. Add rows before configuring a run.</p>}
      {required.length > 0 && <p>Required columns: {required.join(', ')}. {rows.some(row => required.some(column => !(column in row.data))) ? 'Some preview rows are missing required columns.' : 'Validate the full pool before execution; the preview is only a sample.'}</p>}
      {!!rows.length && <div className="data-table"><table aria-label={`Rows for ${binding.alias}`}><thead><tr>{columns.map(column => <th key={column}>{column}</th>)}</tr></thead><tbody>{rows.map(row => <tr key={row.id}>{columns.map(column => <td key={column}>{row.data[column] ?? 'Missing'}</td>)}</tr>)}</tbody></table></div>}
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
export default function ScenarioDataBindings({ bindings, pools, scripts, onChange, canManage }: {
  bindings: ScenarioDataPoolBinding[]; pools: SharedDataPool[]; scripts: StoredScript[];
  onChange: (bindings: ScenarioDataPoolBinding[]) => void; canManage: boolean
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
  return <div className="scenario-data-editor">
    {!currentDetails ? <p role="status">Checking script data references…</p> : currentDetails.unavailable > 0 && <p role="status">Data requirements could not be inspected for {currentDetails.unavailable} scripts. Review them in Scripts before running.</p>}
    <p>Bindings are shared across scenario groups. Reference a column with <code>{'{{data.customers.email}}'}</code>. Each alias identifies one pool.</p>
    {(currentDetails?.scripts || []).flatMap(script => [...scenarioDataRequirements([script]).keys()]
      .filter(alias => !bindings.some(binding => binding.alias === alias) && !script.script?.data_sources?.some(source => source.name === alias))
      .map(alias => <p key={`${script.id}:${alias}`} role="status">{script.name} references <code>{alias}</code>, but no script source or scenario pool supplies it.</p>))}
    {dataBindingErrors(bindings).map(error => <p key={error} role="alert" className="settings-error-inline">{error}</p>)}
    {bindings.length === 0 && <label className="scenario-field"><span>Data Pool</span><select value="" onChange={event => { if (event.target.value) onChange([{ pool_id: event.target.value, alias: pools.find(pool => pool.id === event.target.value)?.name.toLowerCase().replace(/\W+/g, '_') || 'data', access_mode: 'read_only', selection_strategy: 'sequential' }]) }}><option value="">No data pool</option>{pools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}</select></label>}
    {bindings.map((binding, index) => <fieldset key={index}><legend>Data binding {index + 1}</legend><div className="settings-grid">
      <label><span>Data Pool</span><select value={binding.pool_id} onChange={event => event.target.value ? update(index, { pool_id: event.target.value }) : onChange(bindings.filter((_, i) => i !== index))}><option value="">Select a pool</option>{!pools.some(pool => pool.id === binding.pool_id) && binding.pool_id && <option value={binding.pool_id}>Unavailable pool — choose a replacement</option>}{pools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}</select></label>
      <label><span>Pool Alias</span><input value={binding.alias} onChange={event => update(index, { alias: event.target.value })} placeholder="customers" /></label>
      <label><span>Access Mode</span><select value="read_only" disabled><option value="read_only">Read-only run snapshot</option></select></label>
      <label><span>Selection Strategy</span><select value={binding.selection_strategy} onChange={event => update(index, { selection_strategy: event.target.value as ScenarioDataPoolBinding['selection_strategy'] })}><option value="sequential">Sequential rows</option><option value="random">Random rows</option><option value="unique">Unique rows</option></select></label>
      </div><p>{binding.selection_strategy === 'unique' ? 'Unique selection assigns a stable row per VUser, reused across its iterations. The runtime wraps when rows are insufficient; scenario validation blocks known unique-row shortages. Validate the pool against peak VUsers before running.' : 'Sequential and random selection can reuse rows. Use unique selection when identities must not be reused.'} Rows are snapshotted when the run is created.</p>
      <button type="button" className="btn btn-ghost" onClick={() => onChange(bindings.filter((_, i) => i !== index))}>Remove binding {index + 1}</button>
      {binding.pool_id && <PoolPreview canManage={canManage} key={binding.pool_id} binding={binding} required={[...(requirements.get(binding.alias) || [])]} />}
    </fieldset>)}
    <button type="button" className="btn btn-secondary" onClick={() => onChange([...bindings, { pool_id: '', alias: '', access_mode: 'read_only', selection_strategy: 'sequential' }])}>Add data pool binding</button>
    {!bindings.some(binding => binding.pool_id) && <ScenarioScriptData scripts={scripts} details={currentDetails?.scripts || []} loading={!currentDetails} unavailable={currentDetails?.unavailable || 0} onRetry={() => { setScriptDetails(null); setRetry(value => value + 1) }} />}
  </div>
}
