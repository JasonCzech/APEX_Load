import type { LiveTableOptions, ScenarioDataPoolBinding } from '../types/dataPools'
import { DEFAULT_LIVE_TABLE_OPTIONS } from './scenarioDataBindingModel'
import './LiveTables.css'

export default function LiveTableBindingOptions({ binding, canManage, onChange }: {
  binding: ScenarioDataPoolBinding
  canManage: boolean
  onChange: (patch: Partial<ScenarioDataPoolBinding>) => void
}) {
  const options = { ...DEFAULT_LIVE_TABLE_OPTIONS, ...binding.live_options }
  const update = (patch: Partial<LiveTableOptions>) => onChange({ live_options: { ...options, ...patch } })
  const native = options.profile === 'native_queue'
  return <section className="live-table-options" aria-label={`Live table options for ${binding.alias || 'new binding'}`}>
    <p>Every group and load generator in this run shares one live table. Separate runs get separate tables. Read again to refresh a VUser’s variables.</p>
    <div className="settings-grid">
      <label><span>Table behavior</span><select value={options.profile} onChange={event => onChange({
        ...(event.target.value === 'vts_columns' && binding.selection_strategy === 'lease' ? { selection_strategy: 'manual' } : {}),
        live_options: { ...options, profile: event.target.value as LiveTableOptions['profile'], ...(event.target.value === 'vts_columns' && options.end_mode === 'write_back' ? { end_mode: 'discard' } : {}) },
      })}><option value="native_queue">Native work queue</option><option value="vts_columns">Shared columns</option></select></label>
      <label><span>Starting data</span><select value={options.seed_policy} onChange={event => update({ seed_policy: event.target.value as LiveTableOptions['seed_policy'], seed_run_id: undefined, ...(event.target.value === 'empty' && options.end_mode === 'write_back' ? { end_mode: 'discard' } : {}) })}><option value="current_at_start">Current pool when the run starts</option><option value="empty">Empty table for producers</option><option value="original_seed">Original seed from a prior run</option></select></label>
      {options.seed_policy === 'original_seed' && <label><span>Source run ID</span><input value={options.seed_run_id || ''} maxLength={128} onChange={event => update({ seed_run_id: event.target.value.trim() })} /></label>}
      <label><span>Columns</span><input value={(options.columns || []).join(',')} placeholder="customer_id,email" onChange={event => update({ columns: event.target.value.split(',').map(column => column.trim()) })} onBlur={() => update({ columns: (options.columns || []).filter(Boolean) })} /></label>
      <label><span>At each iteration</span><select value={binding.selection_strategy} onChange={event => onChange({ selection_strategy: event.target.value as ScenarioDataPoolBinding['selection_strategy'] })}><option value="manual">Use explicit script actions</option><option value="next">Read next row</option><option value="pop">Remove next row</option>{native && <option value="lease">Lease a work item</option>}</select></label>
      <label><span>When no row is available</span><select value={options.empty_policy} onChange={event => update({ empty_policy: event.target.value as LiveTableOptions['empty_policy'], ...(event.target.value === 'wait' ? { wait_ms: options.wait_ms || 10000, operation_timeout_ms: Math.max(options.operation_timeout_ms || (options.durability === 'durable' ? 120000 : 10000), options.wait_ms || 10000) } : { wait_ms: 0 }) })}><option value="error">End the VUser with an error</option><option value="wait">Wait for a row, then error on timeout</option><option value="skip">Skip the iteration</option></select></label>
      {options.empty_policy === 'wait' && <label><span>Maximum wait (ms)</span><input type="number" min={1} max={60000} value={options.wait_ms ?? 10000} onChange={event => update({ wait_ms: Number(event.target.value) })} /></label>}
      <label><span>When the run ends</span><select value={options.end_mode} onChange={event => update({ end_mode: event.target.value as LiveTableOptions['end_mode'], ...(event.target.value !== 'save_as_new' ? { output_pool_name: undefined } : {}) })}><option value="discard">Discard runtime changes</option><option value="save_as_new" disabled={!canManage}>Save as a new data pool</option>{native && <option value="write_back" disabled={!canManage || !binding.pool_id || options.seed_policy === 'empty'}>Write back if the source pool is unchanged</option>}</select></label>
      {options.end_mode === 'save_as_new' && <label><span>New pool name</span><input value={options.output_pool_name || ''} onChange={event => update({ output_pool_name: event.target.value })} /></label>}
    </div>
    {options.seed_policy === 'original_seed' && <p>Use the same alias as the prior run. Its retained encrypted seed is copied into a fresh table; final mutated rows are not reused. Start validation checks project access and seed availability.</p>}
    {options.seed_policy === 'empty' && <p>Declare the columns above. Producers can add rows after the run starts.</p>}
    {!native && <p>Shared columns use positional cells and preserve absent versus empty values. Native work-item leases are unavailable.</p>}
    {binding.selection_strategy === 'pop' && <p>Removing a row is permanent for this run. A load generator failure can leave its external processing outcome unknown.</p>}
    {binding.selection_strategy === 'lease' && <p>A work item is acknowledged after a successful iteration; failed iterations release it. Expired leases can be delivered again. Use a target-supported idempotency key for external side effects.</p>}
    {options.end_mode === 'write_back' && <p>Write-back is a separate finalization step. A changed, archived, or deleted source pool prevents replacement; the final output remains available for review.</p>}
    {!canManage && <p>Saving runtime changes to a data pool requires data-pool management permission.</p>}
    <details><summary>Recovery and capacity limits</summary><div className="settings-grid">
      <label><span>Recovery</span><select value={options.durability} onChange={event => update({ durability: event.target.value as LiveTableOptions['durability'], ...(event.target.value === 'durable' ? { operation_timeout_ms: Math.max(options.operation_timeout_ms || 120000, 65000) } : {}) })}><option value="memory">Memory only</option><option value="durable">Durable recovery</option></select></label>
      <label><span>Maximum rows</span><input type="number" min={1} max={100000} value={options.max_rows ?? 10000} onChange={event => update({ max_rows: Number(event.target.value) })} /></label>
      <label><span>Maximum table size (bytes)</span><input type="number" min={1024} max={8388608} value={options.max_bytes ?? 8388608} onChange={event => update({ max_bytes: Number(event.target.value) })} /></label>
      <label><span>Operation timeout (ms)</span><input type="number" min={options.durability === 'durable' ? 65000 : 1} max={120000} value={options.operation_timeout_ms ?? (options.durability === 'durable' ? 120000 : 10000)} onChange={event => update({ operation_timeout_ms: Number(event.target.value) })} /></label>
      {binding.selection_strategy === 'lease' && <><label><span>Lease duration (ms)</span><input type="number" min={1000} max={300000} value={options.lease_ttl_ms ?? 30000} onChange={event => update({ lease_ttl_ms: Number(event.target.value) })} /></label><label><span>Maximum delivery attempts</span><input type="number" min={1} max={100} value={options.max_delivery_attempts ?? 5} onChange={event => update({ max_delivery_attempts: Number(event.target.value) })} /></label></>}
    </div><p>{options.durability === 'memory' ? 'Controller loss fails a memory-only live table run and can lose its runtime data.' : 'Durable mode requires configured recovery storage and supported execution ownership. Start validation checks availability.'}</p></details>
  </section>
}
