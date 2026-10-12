import { useEffect, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { ScenarioDataPoolBinding } from '../types/dataPools'
import type { ProjectLiveTableView } from '../types/projectLiveTables'
import { getErrorMessage } from '../utils/errorUtils'

export default function ProjectLiveTableBinding({ projectId, binding, onChange }: { projectId?: string; binding: ScenarioDataPoolBinding; onChange: (patch: Partial<ScenarioDataPoolBinding>) => void }) {
  const api = useApi()
  const [state, setState] = useState<{ projectId?: string; tables: ProjectLiveTableView[]; loading: boolean; error?: string }>({ tables: [], loading: true })
  useEffect(() => {
    const abort = new AbortController()
    setState({ projectId, tables: [], loading: !!projectId })
    if (projectId) void api.listProjectLiveTables(projectId, { signal: abort.signal }).then(response => { if (!abort.signal.aborted) setState({ projectId, tables: response.tables, loading: false }) }).catch(cause => { if (!abort.signal.aborted) setState({ projectId, tables: [], loading: false, error: getErrorMessage(cause, 'Unable to load project live tables.') }) })
    return () => abort.abort()
  }, [api, projectId])
  const tables = state.projectId === projectId ? state.tables : []
  const selected = tables.find(table => table.id === binding.project_table_id)
  const options = binding.live_options || {}
  return <section aria-label="Project live-table binding">
    <p>This table is shared with other runs in the project and keeps its data when this run finishes. Its definition controls schema, capacity and recovery.</p>
    {!projectId && <p role="status">Select a project to choose a shared live table.</p>}
    {state.loading && <p role="status">Loading project tables…</p>}{state.error && <p role="alert">{state.error}</p>}
    <div className="settings-grid">
      <label>Project live table<select value={binding.project_table_id || ''} disabled={!projectId || state.loading} onChange={event => { const table = tables.find(item => item.id === event.target.value); onChange({ project_table_id: event.target.value, ...(table?.profile === 'vts_columns' && binding.selection_strategy === 'lease' ? { selection_strategy: 'manual' } : {}) }) }}><option value="">Select a project table</option>{binding.project_table_id && !selected && <option value={binding.project_table_id}>Unavailable table — choose a replacement</option>}{tables.map(table => <option key={table.id} value={table.id} disabled={table.state !== 'active' || !table.permissions.can_view}>{table.name} · {table.profile === 'native_queue' ? 'Native work queue' : 'Shared columns'} · {table.state}</option>)}</select></label>
      <label>At each iteration<select value={binding.selection_strategy} onChange={event => onChange({ selection_strategy: event.target.value as ScenarioDataPoolBinding['selection_strategy'] })}><option value="manual">Use explicit script actions</option><option value="next">Read next row</option><option value="pop">Remove next row</option>{selected?.profile !== 'vts_columns' && <option value="lease">Lease a work item</option>}</select></label>
      <label>When no row is available<select value={options.empty_policy || 'error'} onChange={event => onChange({ live_options: { ...options, empty_policy: event.target.value as 'error' | 'wait' | 'skip', ...(event.target.value === 'wait' ? { wait_ms: options.wait_ms || 10000 } : { wait_ms: undefined }) } })}><option value="error">End the VUser with an error</option><option value="wait">Wait for a row, then error on timeout</option><option value="skip">Skip the iteration</option></select></label>
      {options.empty_policy === 'wait' && <label>Maximum wait (ms)<input type="number" min={1} max={60000} value={options.wait_ms ?? 10000} onChange={event => onChange({ live_options: { ...options, wait_ms: Number(event.target.value) } })} /></label>}
      <label>Operation timeout (ms)<input type="number" min={65000} max={120000} value={options.operation_timeout_ms ?? 120000} onChange={event => onChange({ live_options: { ...options, operation_timeout_ms: Number(event.target.value) } })} /></label>
    </div>
    {selected && <p>Columns: {selected.columns.join(', ')}. Current instance: <code>{selected.instance_id}</code>. New attachments require an active table; launch rechecks access and lifecycle.</p>}
  </section>
}
