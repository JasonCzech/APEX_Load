import type { ScriptMessageDetailConfig } from '../types/scripts'

type Props = { value?: ScriptMessageDetailConfig; onChange: (value: ScriptMessageDetailConfig | undefined) => void }
export function MessageDetailFields({ value, onChange }: Props) {
 const patch = (next: Partial<ScriptMessageDetailConfig>) => onChange({ mode: 'errors', ...value, ...next })
 return <fieldset><legend>Replay and load-test diagnostics</legend>
  <label>Exchange capture<select className="settings-input" value={value?.mode || ''} onChange={event => onChange(event.target.value ? { ...value, mode: event.target.value as ScriptMessageDetailConfig['mode'], ...(event.target.value === 'sampled' ? { sample_rate: value?.sample_rate || 1 } : {}) } : undefined)}>
   <option value="">Default / inherit</option><option value="off">Off</option><option value="errors">Errors only</option><option value="sampled">Sampled exchanges</option><option value="adaptive">Adaptive</option>
  </select></label>
  {value && <>
   <label>Include fetched resources<select className="settings-input" value={value.include_resources === undefined ? '' : String(value.include_resources)} onChange={event => patch({ include_resources: event.target.value === '' ? undefined : event.target.value === 'true' })}><option value="">Default (disabled)</option><option value="true">Enabled</option><option value="false">Disabled</option></select></label>
   {(value.mode === 'sampled' || value.mode === 'adaptive') && <label>Capture sample rate<input type="number" min={0} max={1} step={0.01} value={value.sample_rate ?? ''} onChange={event => patch({ sample_rate: event.target.value === '' ? undefined : Number(event.target.value) })} /></label>}
   {([['max_items', 'Retained exchange limit', 128, 4096], ['max_item_bytes', 'Bytes per exchange', 1048576, 16777216], ['max_total_bytes', 'Total capture bytes', 16777216, 1073741824]] as const).map(([key, label, fallback, max]) => <label key={key}>{label}<input type="number" min={0} max={max} step={1} placeholder={String(fallback)} value={value[key] ?? ''} onChange={event => patch({ [key]: event.target.value === '' ? undefined : Number(event.target.value) })} /></label>)}
  </>}
  <p className="form-hint">A sample rate of 1 requests every exchange within retention limits. Resources share the parent budget and are captured only when already fetched. Operator limits may reduce retention. Snapshot on error remains a separate setting.</p>
 </fieldset>
}
